# Design

Condensed architecture reference for `gprt`. See `docs/REQUIREMENTS.md` for feature status and `docs/KEYBINDINGS.md` for the key reference.

## Module layout and dependency rules

```
go.mod                         module github.com/hirano00o/gprt (go 1.26)
cmd/gprt/main.go               flags (--debug, --config), config load, wiring, app.Run(); returns immediately after Run
internal/config/               YAML config (strict), defaults, gh-style dir resolution
internal/logging/              slog file logger, ring buffer of recent errors (shown by :messages)
internal/model/                domain types (PullRequest, ReviewThread, ReviewComment, Check, ChangedFile, Section…)
internal/gh/                   GitHub client: auth via go-gh, embedded GraphQL queries, REST files with ETag, mapping, errors
internal/cache/                disk+memory store: Entry{Body, ETag, FetchedAt}; namespaced keys; goroutine-safe
internal/diff/                 hunk parser → Hunk/Line with old/new numbers; anchors; thread location
internal/highlight/            chroma tokenising → per-line []Token{Text, Kind}; no tcell dependency
internal/drafts/               draft store keyed by target; atomic JSON files under the state dir
internal/browser/              URL resolver + cli/browser
internal/store/                UI-goroutine-owned state + service layer: sections, current PR, files, pending review,
                                drafts index, mutation queue, refresh scheduling; talks to gh via its own interface
internal/ui/                   tview composition: root, router, panes, dialogs, status bar
internal/ui/keys/              key normalisation + Sequencer (multi-key/count state machine) + keymap tables (pure)
internal/ui/widget/            ListView, DiffView, Span{Text, Style} + span drawing helpers, popup list
internal/ui/editor/            Vim (modal layer, pure) + Editor primitive (TextArea + status line) + mention popup + $EDITOR bridge
internal/ui/theme/             tview.Styles setup, palette-by-hash colours, token → tcell.Style mapping, icon sets
docs/REQUIREMENTS.md           requirement list with implementation status (updated every milestone)
docs/DESIGN.md                 this file
docs/KEYBINDINGS.md            keymap reference (source for the `?` help)
```

**Dependency direction:** `ui → store → {gh, cache, drafts, diff, highlight} → model`.

- `editor` depends only on tcell/tview and receives a `CandidateFunc func(prefix string) []Candidate`.
- `highlight` returns token kinds; `theme` maps them to `tcell.Style`.
- `widget.Span` is the single span type used across widgets.
- `store` defines `type GitHub interface {…}` (grown per milestone); its tests use a fake in the same package.
- `store` never imports tview: it receives `Dispatch func(func())`, wired to `app.QueueUpdateDraw`.

## Domain types (`internal/model`) — implemented

- `RepoRef{Host, Owner, Name}` with `NameWithOwner()`; `PRRef{Repo, Number}` with `Key()` (`host/owner/repo#n`).
- `PullRequest`: ID, Ref, RepositoryID, Title, Body, Author, State, IsDraft, ReviewDecision, Mergeable (`MergeableState`: MERGEABLE|CONFLICTING|UNKNOWN), MergeStateStatus (`MergeStateStatus`: BEHIND|BLOCKED|CLEAN|DIRTY|DRAFT|HAS_HOOKS|UNKNOWN|UNSTABLE), BaseRefName, HeadRefName, HeadOID, Additions/Deletions/ChangedFiles, Labels, ReviewRequests (`Reviewer{Login, Kind: User|Team|Other, AsCodeOwner}`), LatestReviews (`Review{ID, Author, State, Body, SubmittedAt, ReactionGroups}`), Checks (`Check{Name, Status, Conclusion, URL, Workflow, IsRequired}`), Timeline (`[]TimelineItem`: IssueComment | Review | Commit | Event), ReviewThreads, PendingReview (`*Review`), ReactionGroups, viewer flags (`ViewerCanUpdate`, `ViewerDidAuthor`, `ViewerCanClose`, `ViewerCanReopen`, `ViewerCanReact`), CreatedAt, UpdatedAt, URL.
- `ReviewThread{ID, Path, Line, StartLine, Side, StartSide, SubjectType, IsResolved, IsOutdated, ViewerCanReply, ViewerCanResolve, ViewerCanUnresolve, Comments}`.
- `ReviewComment{ID, ReviewID, Author, Body, CreatedAt, State (PENDING|SUBMITTED), ReactionGroups, ViewerCanUpdate, ViewerCanDelete, URL}`; `IssueComment` similar (no `ReviewID`/`State`).
- `ChangedFile{Path, PreviousPath, Status, Additions, Deletions, Patch, HasPatch, SHA}`; `Status` is a `FileStatus` (`added|removed|modified|renamed|copied|changed|unchanged`, lowercase to match the REST "list pull request files" response).
- `Check` / `ChecksSummary` / `ChecksState`: `Summarize(checks) ChecksSummary` tallies outcomes like `gh pr checks` does — a non-`COMPLETED` status (`QUEUED`, `IN_PROGRESS`, `PENDING`, `WAITING`, `REQUESTED`, `EXPECTED`) is pending; a completed check is success on `SUCCESS`, skipped on `SKIPPED`/`NEUTRAL`, and failure on every other conclusion, including an empty or unrecognised one — a completed check is never dropped from the tally, so an unknown outcome (or one this package does not know about yet) reads as a failure rather than silently as green. `ChecksSummary.State()` derives the rollup icon state (failure beats pending beats success; no checks → `none`).
- `Section{Name, Query, Kind}` (`Kind` ∈ DirectReview, TeamReview, Mine, Involved, Custom), `ListItem{PR *PullRequest, Section}`; `RateLimit{Remaining, ResetAt}` returned with every result.
- `ReactionContent` (8 known values) with `Emoji()`; `ReactionGroup{Content, Count, ViewerHasReacted}`.

All types are pure data: no network or UI dependencies, methods are simple derivations of fields already present.

## GitHub client (`internal/gh`) — planned (M1a onward)

- `New(cfg) (*Client, error)`: host/token resolved via go-gh `auth`; `ClientOptions{Timeout: 30s, Headers, Log when debug}`; one GraphQL client + one raw HTTP client. Stateless; safe for concurrent use.
- **Reads:** `Viewer`, `SearchPullRequests(query, cursor)`, `PullRequest(ref)` (one query; nested connections paged with `after` while `hasNextPage`), `ChangedFiles(ref, page)` (REST + ETag via `cache`), `MentionableUsers(repo, q)`, `Teams(org, q)`, `Branches(repo, q)`, `Labels(repo)`, `PullRequestTemplates(repo)`, `Repository(nameWithOwner)`.
- **Mutations:** `CreatePendingReview`, `AddReviewThread`, `AddThreadReply`, `SubmitReview`, `AddReviewNow` (`addPullRequestReview` with event+threads), `DeletePendingReview`, `UpdateReviewComment`, `DeleteReviewComment`, `AddIssueComment`, `UpdateIssueComment`, `DeleteIssueComment`, `ResolveThread`, `UnresolveThread`, `AddReaction`, `RemoveReaction`, `UpdatePullRequest`, `RequestReviewers(union)`, `MarkReady`, `ConvertToDraft`, `Merge`, `Close`, `Reopen`, `CreatePullRequest`. Each returns the payload fields needed for optimistic apply.
- Queries live in `internal/gh/queries/*.graphql` (embedded), decoded into private structs and mapped into `model`.
- Errors: `*gh.Error{Kind: Auth|RateLimited|NotFound|Validation|Network, Message, RetryAfter}`.
- **GraphQL is primary** (search, PR detail with `reviewThreads`/`comments`/`reviews`/`statusCheckRollup`, all mutations). **REST** is used only for `/pulls/{n}/files?per_page=100` (patches, ETag; ≤3000 files; `patch` absent for binary/huge files). GraphQL has no ETag; REST 304s are free. go-gh's built-in cache stays **off** (it is TTL-only and caches 404/422 and GraphQL POSTs, which is wrong for gprt's needs).
- Viewer's pending review: `reviews(first: 1, author: $login, states: [PENDING])`. Only one pending review per user per PR; while it exists, every new comment/reply must attach to it (`pullRequestReviewId`), matching GitHub's own UI, which hides "single comment" in that state.
- `addPullRequestReview` without `event` creates a PENDING review; with `event: COMMENT` + `threads:` publishes immediately. `DraftPullRequestReviewThread` has no `subjectType`, so file-level comments always go through `addPullRequestReviewThread(subjectType: FILE)` on a pending review.
- Comments must target lines inside a hunk; file-level comments omit `line`; multi-line comments use `startLine`/`startSide`.
- `requestReviews(union: false)` replaces the reviewer set. Draft toggle: `markPullRequestReadyForReview` / `convertPullRequestToDraft`. Reactions: `addReaction`/`removeReaction(subjectId, content)`.
- Mention candidates: `repository.mentionableUsers(query:, first: 100)`; teams: `organization.teams(query:)`; templates: `repository.pullRequestTemplates`; branches: `repository.refs(refPrefix: "refs/heads/", query:)`.
- Search: 30 req/min, 1000 results max; qualifiers `type:pr archived:false sort:updated-desc` plus a section qualifier; multiple `repo:` in advanced search must be grouped `(repo:a OR repo:b)`. GraphQL budget is 5000 points/hour; every query selects `rateLimit { remaining resetAt }`.
- Section queries de-overlap with negations: `user-review-requested:@me`; `review-requested:@me -user-review-requested:@me`; `author:@me -review-requested:@me`; `involves:@me -author:@me -review-requested:@me`; dedupe by ID stays as a safety net regardless. Custom sections are appended verbatim (the `(repo:a OR repo:b)` grouping rule is documented for users, not enforced).

## Cache (`internal/cache`) — store implemented, ETag/SWR wiring planned

- Layout: `<cache dir>/v1/<host>/<login>/repos/<owner>/<repo>/pr-<n>/<sha256(rest)>.json` and `v1/<host>/<login>/search/<sha256>.json`. `Entry{Body, ETag, FetchedAt}`; path segments outside `[A-Za-z0-9._-]` (including `""`, `.`, `..`) are replaced with `"=" + hex(segment)` — the `=` prefix is outside the safe character class, so an escaped segment can never collide with a literal one, and an empty segment can never disappear via `filepath.Join` and collide two different keys onto the same path. The fixed `repos` segment keeps the PR tree disjoint from the `search` tree at the same level: without it, a repository literally owned by an account named "search" would land under `.../<login>/search/...`, and `InvalidateSearch`'s `os.RemoveAll` would delete that owner's PR caches along with the search cache.
- `Store` serialises `Get`, `Put`, and `invalidateDir` (used by `InvalidatePR`/`InvalidateSearch`) behind a single mutex held across each call's entire body, including its disk I/O, not just the in-memory map: `Store.Get(k) (Entry, bool, error)` checks memory then falls back to disk, and `Store.Put(k, e) error` — after rejecting `e` if its `Body` is nil or its `FetchedAt` is zero (the same two conditions `readDiskEntry`, below, treats as corruption, so `[]byte{}` is a valid non-nil body but a zero-value `Entry` is not) — marshals `e`, then takes the lock and updates memory and writes to disk together. This closes two races: a `Get` reading a file `invalidateDir` is mid-way through deleting could otherwise write the stale result back into memory, and a `Put` in flight during an invalidation could otherwise write its file after the invalidation's `RemoveAll` and resurrect the directory it just deleted (`writeAtomic`'s temp-file-then-rename means a concurrent `Get` can never observe a half-written file at the destination path, so that is not a separate risk). Coarser than a per-key scheme, an accepted trade-off given gprt is a single-user CLI touching small JSON files.
- A missing file is `(Entry{}, false, nil)`. A corrupt file — invalid JSON, or JSON that decodes but has a nil body or a zero fetch time (as a truncated write might produce) — is also `(Entry{}, false, nil)` and is deleted so it does not keep failing on every lookup; if the deletion itself fails, that error is returned instead. Any other read error (for example a permission error) is returned as-is. Disk writes are atomic (temp file + rename). Directories are created mode `0700`, files mode `0600`; no request headers are persisted.
- `InvalidatePR(host, login, ref) error` removes the PR's on-disk directory and the matching memory entries; `InvalidateSearch(host, login) error` does the same for the search namespace. Neither touches unrelated hosts/logins/PRs. Both return the `os.RemoveAll` error instead of swallowing it, since a failed removal would otherwise let a stale disk entry resurface on a later `Get`. Both delegate to shared `prDir`/`searchDir` helpers so the on-disk layout is defined in one place, alongside `Store.path`.
- `Key` should be built with `PRKey(host, login, ref, rest) (Key, error)` or `SearchKey(host, login, rest) (Key, error)` rather than a struct literal: both validate the fields that become on-disk path segments (non-empty host/login; for `PRKey`, also a non-empty repo owner/name and a positive PR number).
- Planned once `internal/gh` exists: REST reads send `If-None-Match`; a `304` keeps the cached body, a `200` replaces it. GraphQL has no validators, so it uses stale-while-revalidate: show the cached entry immediately, always refetch on view; after any mutation on a PR, `InvalidatePR` then refetch. Pressing `R` skips the cached entry and refetches directly.

**Open items:** the in-memory map and on-disk tree are unbounded (no LRU eviction, no pruning of old entries at startup). Deferred to M2, when REST file patches start entering the cache and unbounded growth becomes worth paying for.

## Store / service (`internal/store`) — planned

- All fields are owned by the UI goroutine; no mutex. Goroutines receive `ctx` plus snapshot arguments (`PRRef`, page, generation token) and never touch the store directly. Results come back only through `dispatch`.
- Fetch pattern: register an in-flight key → `go func(){ defer recover→log+toast; res := …; dispatch(func(){ if gen == current { apply(res) }; unregister }) }()`. The generation token increments on every PR switch and cancels the previous context.
- Refresh: a `time.Ticker` (`refresh_interval`, default 5m) calls `dispatch(store.RefreshAll)`; the list preview refreshes after a 300 ms `time.AfterFunc` debounce (stopped on re-selection); the callback only dispatches.
- Files: REST pages fetched sequentially (page count derived from `changedFiles`); each page is parsed, published as rows, and its lines enqueued as highlight jobs on a pool of 2 workers, cancelled by context on PR switch. All pages are refetched when `HeadOID` changes.
- Pending review: discovered on PR load; `EnsurePendingReview` creates one lazily. Pending comments come from `reviewThreads.comments` with `State == PENDING` (to verify against the first real PR in M3b); the review query supplies the review's ID and body.
- Mutations: a single-flight FIFO queue; the composer's send action is disabled while one is in flight. Optimistic apply of the payload, then `InvalidatePR` and refetch; state-changing mutations (merge/close/reopen/draft/title/labels/reviewers/submit) also refetch the list. If `addPullRequestReview` fails with "already has a pending review", the store rediscovers the pending review and retries as add-to-review.
- Drafts: keyed by `drafts.Key(prKey, kind, anchor)`. An editor change triggers `drafts.Save` synchronously (atomic temp+rename, no debounce); drafts are cleared on send/discard. Anchor sets are precomputed for gutter markers; drafts that no longer resolve to a line (for example after a force-push) surface in the `p` list to open or discard.

## Diff (`internal/diff`) — planned (M2)

- `Parse(patch string) ([]Hunk, error)`: parses `@@ -a[,b] +c[,d] @@ [section]` headers and lines prefixed ` `/`+`/`-`; `\ No newline at end of file` is ignored for line numbering; tabs are expanded before highlighting (`tab_width`, default 4).
- `Line{Kind Context|Add|Del, OldNo, NewNo, Text}`. `Anchor(line)`: `Del` → `LEFT`/`OldNo`, otherwise `RIGHT`/`NewNo`.
- `RangeAnchor(lines)`: requires the same hunk, contiguous lines, at least 2 lines, and a **single side**: only `Del` lines → `LEFT`/`OldNo`; no `Del` lines → `RIGHT`/`NewNo`; a mix is an error ("select lines on one side"). Mixed-side ranges are recorded as unsupported in v1; M3b runs one exploratory API call and lifts the restriction if the API accepts it (see "Open items" in `docs/REQUIREMENTS.md`).
- `LocateThread(hunks, thread)`: `LEFT` → the line with `OldNo == Line` (`Del`|`Context`); `RIGHT` → `NewNo == Line` (`Add`|`Context`); a thread that cannot be located, or whose `IsOutdated` is true, falls into the file's "outdated" block.

## UI (`internal/ui`) — planned

- **Router** (the only `app.SetInputCapture`): `keys.Normalize` → build a context (dialog open?, command line open?, composer mode, focused pane) → in composer insert mode, forward everything except `Esc`/`Ctrl-s`/popup keys straight to the editor, bypassing the Sequencer → `keys.Sequencer` (pending prefixes derived from the merged keymap, plus a count; `Esc` clears) → keymap lookup by `(context, sequence)` → dispatch the action to its target → return `nil`. The original event is returned only for keys deliberately left to tview built-ins (`InputField`, `DropDown`, `TreeView`, `Modal`). `Ctrl-c` is handled here.
- **Keymap** (`keys` package): `Action` IDs with default sequences per context; `Merge(defaults, cfg.Keys)` parses vim notation (`<C-w>o`, `<Esc>`, `<Enter>`, `<Tab>`, `<Space>`, `gt`, `]c`), then validates unknown action IDs, duplicate sequences within a context, and prefix conflicts (a sequence that is itself a prefix of another sequence in the same context); all three are reported as config errors at startup with the offending key. The editor's own vim keys are not in this table.
- **Command line** (`:`, outside the editor): a one-line `InputField` in the status bar row with history. Commands: `merge` (method picker from repo settings + commit headline editor → confirm), `close`, `reopen` (confirm), `messages`, `help`, `q`; an unknown command shows a toast. Destructive PR actions exist only as commands, never as single-key bindings.
- **Root:** `Pages{"main": Flex[list | detail], overlays}` plus a status bar. Detail = tab bar + `Pages{"pr","files"}`; the composer pane (≈40% height) is added to the detail column only while composing.
- **PR list** (`widget.ListView`, 2 rows per PR): `<state/checks icon> #123 Title` then `  owner/repo · author · ⇢ reviewers`. Repo/author colours by hash, reviewer colour by state, `@me` bold, section headers. `/` opens an `InputField` under the list; the filter matches title/number/repo/author and understands qualifiers `repo:`, `author:`, `reviewer:`, `label:`, `state:` (`state:` re-runs the search).
- **PR tab** (`widget.ListView` of blocks): header (title, state, base←head, author, reviewers, labels, checks summary), body (light Markdown styling), checks, timeline (comments/reviews/events). Cursor on a block enables `e`/`d`/`a`/`r`/`o`.
- **Files tab:** `Flex[TreeView (30 cols) | DiffView]`; the tree is grouped by directory with a status glyph, `+n -m`, and comment/draft markers.
- **DiffView** (`widget.DiffView`): rows are file headers, hunk headers, diff lines (old/new gutters, marker, spans), inline thread blocks (author, relative time, wrapped body via cached `tview.WordWrap`, badges, reactions), and fold placeholders. The cursor is a logical position (file, hunk, line | thread ID), re-resolved after every rebuild. Diff lines are truncated (no wrap), scrolled horizontally with `zh`/`zl`; drawing is per grapheme using `uniseg` widths; control characters and ANSI are shown as `^M`/`?`; highlighting is skipped for lines over 4 KB or files over 1 MB. Supports visual selection, folds, `]c`/`[c`/`]f`/`[f`, and a `✎` gutter marker for drafts.
- **Composer:** the title names the target (`Comment on src/a.go:12-15 (RIGHT)`, `Reply to @user`, `Review body`, …); body is `editor.Editor` plus a status line with hints. Send (`Ctrl-s`/`:w`): if a pending review exists, comments/replies attach to it without asking; otherwise a two-item menu appears ("Add single comment" / "Add to review"), mirroring GitHub's own two buttons. General PR comments and review bodies send directly.
- **Dialogs** (`Pages` overlays): submit review, reaction picker, confirm, merge dialog (opened by `:merge`), edit-PR form, create-PR form, reviewer picker, label picker, pending list (`p`), help (`?`), `:messages`, error toast.
- **Status bar:** focus/tab, key hints, spinner + "loading files 3/12", rate limit, last refresh, draft count, errors.

## Vim editor (`internal/ui/editor`) — planned (M3a)

- `editor.Vim` (pure): a modal state machine over `Text interface { Text() string; Cursor() int; SetCursor(int); Replace(start, end int, s string); Select(start, end int) }`. Tests use a string-backed fake that panics on an offset not aligned to a grapheme-cluster boundary; production wraps `*tview.TextArea` (`Cursor()` = the start of `GetSelection()` when nothing is selected; the adapter works before the first `Draw`, so it is unit-testable). Modes: Normal / Insert / Visual / VisualLine / Command; state: count, pending operator, unnamed register (with a linewise flag), visual anchor. `Handle(key) Action` returns `Consumed` | `ForwardToTextArea` | `Command(":w"…)` | `MentionQuery(prefix)`.
- **Undo/redo live in the layer, not in `TextArea`** — `TextArea` groups insert-mode keystrokes per word and does not export its own undo. Every command maps to one `Replace(start, end, new)`; the layer records `{start, old, new}`; `u` = `Replace(start, start+len(new), old)`, `Ctrl-r` is the inverse. Commands that enter insert mode (`i a I A o O cc cw c$ C s`) snapshot `Text()` on entry and, on `Esc`, push a single entry built from the common prefix/suffix diff, so one insert session is one undo unit, matching vim. `SetText` (`:e` return, draft restore) pushes one entry so `u` returns to the previous text. `Ctrl-z`/`y`/`l`/`d`/`k`/`q`/`x`/`v` are never forwarded to `TextArea`.
- Motions compute byte offsets from `Text()` (called once per keystroke) using `uniseg` grapheme boundaries (`Select` rounds up to a cluster boundary, so implementing `h` as "-1 byte" would not move on CJK text); operate on logical lines; keep the desired column in display cells; `j`/`k` skip whole wrapped paragraphs like vim (`gj`/`gk` is out of scope); word classes are unicode-aware. After `Replace` the cursor sits at the end of the replacement, so every command sets the cursor explicitly (`dd` → next line start, `O` → new line start, `p` → last pasted character, `x` → same column). Leaving insert mode moves the cursor left one column, matching vim.
- Insert mode forwards a whitelist to `TextArea`: runes, Enter, Tab (when the popup is closed), Backspace, Delete, arrows, Home/End, Ctrl-w, Ctrl-u. `Esc`/`Ctrl-s`/popup keys are intercepted first; everything else is dropped. Forwarding is a single path: the router calls `textArea.InputHandler()(ev, app.SetFocus)` and returns `nil`. Paste (`PasteHandler`) is accepted in any mode and recorded as one undo entry.
- `Editor` primitive (`Flex`: `TextArea` + status line): `Focus` delegates to `TextArea`; `Draw` runs `Flex.Draw` then the mention popup (`*tview.List`), placed at the cursor's screen position from `GetCursor`/`GetOffset`/`GetInnerRect` (opens upward when it does not fit, like `InputField`); cursor style is set via `screen.SetCursorStyle` (steady block in normal/visual, steady bar in insert; reset on blur/exit). Mention detection runs in the `changed` handler (`@[\w-]*` before the cursor); accepting a candidate is one `Replace(atPos, cursor, "@login ")`; candidates come from the injected `CandidateFunc` (fuzzy match; `MatchedIndexes` are byte offsets, converted to cells with `uniseg` for highlighting).
- The composer is removed from the `Flex` (`RemoveItem`) when hidden, never resized to 0 — a `TextArea` drawn at width 0 stops extending its lines.
- External editor: `:e` runs `app.Suspend(func(){ temp file; exec config editor > $EDITOR > vim })`, then `SetText` (plus an undo entry), then `app.Sync()`.

## Configuration (`internal/config`) — implemented

Config file: `~/.config/gprt/config.yaml` (or the resolved `ConfigDir()`), strict YAML, every key optional.

```yaml
host: github.com          # default: gh's default host
refresh_interval: 5m
icons: unicode            # unicode | nerd
editor: ""                # overrides $EDITOR for :e
browser: ""               # overrides $BROWSER
highlight_style: github-dark
tab_width: 4
list:
  state: open             # open | closed | merged | all
  sections:
    - name: Backend
      query: "org:acme label:backend"
keys:                     # action ID → key sequence (vim notation); unspecified actions keep defaults
  list.toggle: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

- `ConfigDir()`, `CacheDir()`, `StateDir()` follow gh's own precedence rather than `os.UserConfigDir`/`adrg/xdg`, which resolve to `~/Library/…` on macOS and would diverge from `~/.config/gh`, which gprt piggybacks on for authentication:
  - Config: `GPRT_CONFIG_DIR` > `$XDG_CONFIG_HOME/gprt` > `~/.config/gprt`
  - Cache: `$XDG_CACHE_HOME/gprt` > `~/.cache/gprt`
  - State: `$XDG_STATE_HOME/gprt` > `~/.local/state/gprt`
  - A non-absolute `$XDG_CONFIG_HOME`/`$XDG_CACHE_HOME`/`$XDG_STATE_HOME` is invalid per the XDG Base Directory specification and is silently treated as unset. `$GPRT_CONFIG_DIR` is gprt's own variable, not an XDG one: if it is set but not absolute, `ConfigDir()` (and therefore `DefaultPath()`) returns an error naming the variable and its value instead of silently falling back to `$XDG_CONFIG_HOME`.
- `Load(path)` returns `Default()` unchanged if the file does not exist; a path that exists as a symlink whose target does not (a broken symlink) is instead reported as an error, since treating it as "no config" would silently hide the misconfiguration. An empty document (zero bytes, blank lines, comments only, or just a `---` marker) also returns `Default()` unchanged: goccy/go-yaml's `Decoder.Decode` overwrites its destination with a zero value before reporting `io.EOF` for an empty document, so `Load` uses `yaml.NewDecoder(...).Decode` specifically to detect that `io.EOF` and discards the now-zeroed struct in favour of a fresh `Default()`, rather than the naive `yaml.UnmarshalWithOptions` (which would silently return the zeroed config with no error at all). A file with more than one YAML document (separated by `---`) is rejected as `*config.ParseError`: without this check the decoder would silently parse only the first document and ignore the rest. A lone trailing `---` with nothing meaningful after it is not a second document (decoding it also reports `io.EOF`, same as an empty file) and is accepted. Otherwise `Load` unmarshals starting from `Default()` (so omitted keys keep their default) with `yaml.Strict()` (unknown keys are a config error), then calls `Validate()`. A parse/strict-decode failure, or a rejected extra document, is returned as `*config.ParseError{Path, Err, Formatted}`: `Error()` returns `yaml.FormatError`'s human-readable text (what a user sees) or the plain "multiple YAML documents" message, `Unwrap()` returns the underlying yaml error (so callers can still `errors.Is`/`errors.As` against the cause).
- `Validate()` collects every problem via `errors.Join` instead of stopping at the first one: `icons` ∈ {unicode, nerd}; `list.state` ∈ {open, closed, merged, all}; `refresh_interval` ≥ 30s; `tab_width` ∈ [1, 16]; each `list.sections[i]` needs a non-empty `name` and `query`; each `keys[action]` needs a non-empty sequence. A non-integral `tab_width` (`2.5`) is silently truncated by the YAML decoder before `Validate` ever sees it (`tab_width: 2.5` becomes `2`, no error) — a YAML-decoder quirk, not a validation gap.

## Mutation sequences

PR = pull request node ID, PRV = viewer's pending review ID, T = thread ID, C = comment ID. After every mutation: apply the payload → `InvalidatePR` → refetch the PR (state-changing mutations also refetch the list).

| Action | No pending review | Pending review exists |
|--------|-------------------|-----------------------|
| Line comment, single | `addPullRequestReview(PR, event: COMMENT, threads: [{path, line, side, body}])` | not offered → add to review |
| Line comment, add to review | `addPullRequestReview(PR)` → PRV → `addPullRequestReviewThread(pullRequestReviewId: PRV, path, line, side, body)` | `addPullRequestReviewThread(PRV, …)` |
| Range comment (single side) | as above + `startLine`, `startSide` | same |
| File comment, single | `addPullRequestReview(PR)` → `addPullRequestReviewThread(PRV, path, subjectType: FILE)` → `submitPullRequestReview(PRV, event: COMMENT)`; a failure mid-way leaves a pending review that shows up in `p` for discard | `addPullRequestReviewThread(PRV, subjectType: FILE)` |
| File comment, add to review | `addPullRequestReview(PR)` → `addPullRequestReviewThread(PRV, subjectType: FILE)` | same as right column above |
| Reply, single | `addPullRequestReviewThreadReply(T, body)` | not offered → `addPullRequestReviewThreadReply(T, body, pullRequestReviewId: PRV)` |
| Reply, add to review | ensure pending → `addPullRequestReviewThreadReply(T, body, PRV)` | same |
| General PR comment | `addComment(subjectId: PR, body)` (always immediate) | same |
| Edit own review comment | `updatePullRequestReviewComment(pullRequestReviewCommentId: C, body)` | same |
| Delete own review comment | `deletePullRequestReviewComment(id: C)`; deleting the last pending comment prompts "discard review?" | same |
| Issue comment edit / delete | `updateIssueComment(id, body)` / `deleteIssueComment(id)` | same |
| Resolve / unresolve | `resolveReviewThread(threadId: T)` / `unresolveReviewThread(threadId: T)` (disabled on pending threads) | same |
| Submit (`S`) | `addPullRequestReview(PR, event, body)` | `submitPullRequestReview(PRV, event, body)`; `COMMENT` with empty body and zero comments is rejected in the UI |
| Discard pending review | — | `deletePullRequestReview(PRV)` |
| Reaction | `addReaction` / `removeReaction(subjectId, content)` on PR, IssueComment, PullRequestReviewComment, PullRequestReview | same |
| Edit PR | `updatePullRequest(PR, title?, body?, baseRefName?, labelIds?)`; reviewers `requestReviews(PR, userIds, teamIds, union: false)`; draft `markPullRequestReadyForReview` / `convertPullRequestToDraft` | same |
| Merge / close / reopen | `mergePullRequest(PR, mergeMethod, commitHeadline?, commitBody?, expectedHeadOid: HeadOID)` / `closePullRequest` / `reopenPullRequest` | same |
| Create | `createPullRequest(repositoryId, baseRefName, headRefName, title, body, draft)` → `requestReviews(newPR, union: true)` | — |

## Concurrency rules

Binding for every implementer; violating one of these is a defect regardless of test coverage.

1. `store` fields are UI-goroutine only; no mutex. Goroutines get ctx + snapshot values, never the store itself.
2. `dispatch` is called only from goroutines the store started; never from key handlers or dispatch callbacks. Synchronous results return by value. Tests drive `Dispatch` through a channel-based fake event loop (a fake that calls `f()` synchronously is forbidden because it would hide deadlocks).
3. Every async result carries a generation token; `apply` drops results whose token != current (ctx cancel alone is not enough).
4. Timer callbacks (`AfterFunc` debounce, refresh ticker) only `dispatch(func(){ store.X() })`.
5. In-flight dedup map is registered/unregistered on the UI goroutine.
6. `cache.Store` (mutex) and `gh.Client` (stateless) are goroutine-safe; rate-limit info travels with results.
7. Mutations run through a single-flight FIFO queue; no re-send while in flight.
8. Highlight pool (2 workers) is ctx-cancelled on PR switch; check chroma lexer concurrency in M2 (else one lexer per worker).
9. Every goroutine has `defer recover` → log + toast. Never wait for goroutines after `app.Stop()` (tview #1163); `main` returns as soon as `Run()` returns.
10. `app.Suspend`/`app.Sync` may be called from key handlers; never `app.Draw()` from the event loop; use `QueueUpdateDraw` everywhere. SimulationScreen tests run `Run()` in a goroutine and synchronise assertions through `app.QueueUpdate`.

## Libraries

Versions as verified against the module proxy on 2026-09-10.

| Purpose | Choice | Notes |
|---------|--------|-------|
| TUI | `rivo/tview v0.42.0`, `gdamore/tcell/v2 v2.13.10` | Pin tcell explicitly; never `tcell/v3`. tcell ≥ v2.10 changed control-key constants (`KeyCtrlH (72) != KeyBackspace (8)`), so one key-normalisation layer unifies legacy vs. CSI-u terminal reporting. |
| GitHub | `cli/go-gh/v2 v2.16.0` | Provides auth token resolution, a GraphQL client, and a raw HTTP client for REST + custom ETag handling. |
| Highlight | `alecthomas/chroma/v2 v2.27.0` | Not the v3 alpha. `lexers.Match` can return nil, so fall back to `lexers.Fallback`; cache the lexer per file extension; use `chroma.Coalesce`; tokenise a whole hunk then `chroma.SplitTokensIntoLines` (never per line); build the style with a cleared, `NoInherit` background entry and use foreground colour only. |
| Diff parsing | own parser (`internal/diff`, ~80 lines) | GitHub's patches are header-less `@@` hunks; `go-gitdiff` rejects them; `go-git` only encodes diffs, it does not parse GitHub's format; `sourcegraph/go-diff`'s `ParseHunks` is the fallback if edge cases pile up. |
| YAML | `goccy/go-yaml v1.19.2` | `gopkg.in/yaml.v3` was archived 2025-04. Uses `yaml.NewDecoder(r, yaml.Strict()).Decode(&cfg)` (not `yaml.UnmarshalWithOptions`, which cannot distinguish an empty document from a genuinely empty config) and `yaml.FormatError` for readable error messages. |
| Fuzzy matching | `sahilm/fuzzy v0.1.3` | `MatchedIndexes` are byte offsets, not grapheme/cell offsets — convert before using them for highlighting. |
| Browser | `cli/browser v1.3.0` + `google/shlex` + own resolver | `cli/browser` does not read `$BROWSER`, so gprt resolves `GPRT_BROWSER` → config `browser` → `$BROWSER` (each trimmed of whitespace, so a value like a lone space is treated as unset) → `browser.OpenURL` itself; only http/https URLs are accepted. A configured launcher is split shell-style with `shlex.Split` (so a quoted path with an embedded space survives as one argument; a parse error is returned from `Open`) and run with `Start` (never `Run`), so `Open` does not block; `cmd.WaitDelay` is set to 5s so a launcher that forks a long-lived child (common for GUI browsers that fork-and-detach) does not delay the exit report until that child closes. `Wait` happens in a background goroutine and, on a non-zero exit, calls `Opener.OnExit(err)` with the launcher's trimmed, 4 KiB-capped stderr folded into `err`; `New(configBrowser string, onExit func(error)) *Opener` takes `OnExit` as an explicit constructor argument rather than an optional field set after the fact, so a caller has to consciously pass `nil` to discard launcher exit failures. Both the launcher's stderr and the fallback's stderr are captured through a small bounded writer (`boundedWriter`, 4 KiB) rather than an unbounded `bytes.Buffer`, since a long-lived or chatty process could otherwise grow gprt's memory for as long as it keeps writing. `cli/browser.Stdout`/`Stderr` are process-global variables, not per-call options: `New` points them at `io.Discard` and a private bounded buffer so the fallback launcher never writes to gprt's own stdout/stderr (which would corrupt the TUI) and a fallback failure's captured stderr is included in the returned error; constructing a second `Opener` repoints these globals at its own buffer, so an older `Opener`'s `Fallback` (if it still runs afterward) silently captures nothing rather than "interleaving" with the newer one — gprt only ever constructs one `Opener`, so this is accepted. |
| Directories | own (~30 lines, gh's precedence) | `os.UserConfigDir()`/`adrg/xdg` resolve to `~/Library/…` on macOS, which would diverge from gh's own `~/.config/gh`. |
| HTTP cache | own minimal ETag store | Generic HTTP caches honour `max-age=60` (which would hide fresh data for a minute) and can persist the `Authorization` header via `Vary`, which gprt does not want. Files `0600`, directories `0700`. |
| Vim editing | own modal layer over `tview.TextArea` | No reusable modal-editing library exists for tcell/tview; `kopecmaciej/vi-sql` is the closest precedent. |
| Logging | `log/slog` to `<state dir>/gprt.log` | Enabled by `--debug` / `GPRT_DEBUG`; stdout is reserved for the TUI. The JSON file handler is built with `&slog.HandlerOptions{Level: slog.LevelDebug}` (the zero-value default is `LevelInfo`, which would otherwise silently drop every Debug record `--debug` exists to show). `logging.Recorder` wraps the real handler (or a discard handler when `--debug` is off) and always keeps a ring buffer of the last 50 `slog.LevelError`+ entries for `:messages`. Each `Entry{Time, Level, Message, Attrs}` carries the record's own attrs plus any bound via `WithAttrs`, keys qualified by their `WithGroup` prefix (`"component.err"`); `Entry.String()` renders `"message key=value ..."`. If the wrapped handler's `Handle` returns an error (for example the log file becomes unwritable), that error is always propagated, and — once per `Recorder`, via `sync.Once` — a `"debug log write failed: …"` entry is pushed into the ring so the failure is visible even though it could not reach the file. |
| Tests | `net/http/httptest`, `tcell.NewSimulationScreen("")` | Standard library plus tcell's simulation screen for UI smoke tests. |

## Milestones

Each milestone is its own branch, PR, and user validation step. Sizes are impl+test LOC estimates.

| Milestone | Branch | Scope | Exit criteria |
|---|--------|-------|---------------|
| M1a (~3k) | `feat/m1a-foundation-list` | go.mod, config + dirs (incl. `keys:` merge/validation), logging, model, `gh` (auth, viewer, search), cache store, `keys` (normalise, sequencer, keymap table), `widget.ListView`, theme, root layout + router + `:` command line (`q`, `help`, `messages`), PR list with sections/sort/colours/filter, status bar, refresh ticker, `R o ? q`. Docs: README, docs/REQUIREMENTS.md, docs/DESIGN.md, docs/KEYBINDINGS.md | `go run ./cmd/gprt` lists real PRs; j/k/gg/G, `/`, `R`, auto refresh, `o`; a remapped key from config works |
| M1b (~2.8k) | `feat/m1b-pr-detail` | PR detail query (body, meta, checks, timeline, threads read-only), PR tab, tab bar, `Ctrl-h/l`, `gt/gT`, `Ctrl-w h/l/o`, list preview debounce | Selecting a PR shows description/checks/conversation; 5-min refresh updates it |
| M2 (~3.8k) | `feat/m2-files-diff` | REST files + ETag, `diff` parser, TreeView, DiffView (rows, cursor, visual selection, folds, inline threads read-only, jumps, `zh/zl`), lazy pages, highlight worker, `Ctrl-w t` | A 100+ file PR opens without blocking; raw diff first, highlight later; threads inline. May be split into M2a/M2b if the DiffView takes longer |
| M3a (~2.8k) | `feat/m3a-editor-drafts` | `editor.Vim` + `Editor`, composer pane, drafts persistence + `✎` gutter, external editor, mention popup with a static candidate source, general PR comment (`c` on PR tab), edit/delete own issue comments | Drafts survive PR switch/reload/restart; first end-to-end mutation works |
| M3b (~2k) | `feat/m3b-review-comments` | Pending review discovery/ensure/delete, line/range/file comments (single + review), replies, resolve/unresolve, edit/delete own review comments, `p` list, exploratory mixed-side range check | Comments land on GitHub at the right lines; pending comments visible and editable |
| M4 (~1.8k) | `feat/m4-submit-reactions-mentions` | `S` submit dialog (both paths), reaction picker, mention candidates from `mentionableUsers` (cached per repo, 1h) | Approve/request-changes with body; reactions toggle; `@` completion with real users |
| M5 (~3.5k) | `feat/m5-pr-create-edit` | Create-PR form (repo picker, base/head, title, body with template, reviewers, draft), edit-PR form, body edit, `:merge` / `:close` / `:reopen` commands with confirmation | A PR created from the TUI shows checks; edits reflected on GitHub. May be split (create vs. edit/merge) |

Process per milestone: branch → single-feature TDD implementation → review loop until zero findings → commit/push/PR → user validation. `docs/REQUIREMENTS.md` status is updated in the same commits.
