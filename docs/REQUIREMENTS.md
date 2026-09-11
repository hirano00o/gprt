# Requirements

Status values: `Done`, `In progress`, `Planned`. Milestones refer to the branches in the [Milestones](#milestones) section of `docs/DESIGN.md` (`M1a`, `M1b`, `M2`, `M3a`, `M3b`, `M4`, `M5`). This table is updated in the same commit as any change to implementation status.

## Functional requirements

| # | Requirement | Decision | Status | Milestone |
|---|-------------|----------|--------|-----------|
| F1 | PR list | Cross-repository via GitHub search. Built-in sections: direct review requests, team review requests, my PRs, other involvement; config adds custom search sections. Default `state:open`; filter can switch to closed / merged / all (re-search). Search client, store, and TUI (`widget.ListView`, section headers, sort, colour-coding, `/` filter, `R` reload, auto-refresh) are all done. | Done | M1a |
| F2 | PR create | Repository and branches chosen inside the TUI (no cwd git): repo picker, head branch (pushed), base (default branch preselected), title, body (vim editor, PR template pre-filled), reviewers (users + teams, autocomplete), draft flag. After creation the PR opens and its checks show/refresh. | Planned | M5 |
| F3 | Files changed | Left file tree (diffview.nvim style, collapsible) + right diff. Unified diff with `+`/`-` colours immediately, language syntax highlighting (chroma) applied asynchronously. Comments on a line, a `V` range, or a whole file; replies; resolve/unresolve; inline foldable thread blocks (resolved folded by default). The "Files" detail tab exists as a placeholder ("Files view arrives in M2"). | Planned | M2, M3b |
| F4 | Review submit | Two paths like GitHub: single comment published immediately, or pending review accumulating comments and submitted with Approve / Request changes / Comment + body. Pending comments editable/deletable before submit. | Planned | M3b, M4 |
| F5 | Mentions | `@` in insert mode opens an autocomplete popup (repo `mentionableUsers`, fuzzy). | Planned | M3a, M4 |
| F6 | Reactions | Toggle the 8 GitHub reactions on PR body, issue comments, review comments, reviews. | Planned | M4 |
| F7 | Editing | Hybrid: in-app vim-like modal editor with `@` completion; `:e` suspends the TUI and opens `$EDITOR` on the buffer. | Planned | M3a |
| F8 | Edit PR/comments | Edit PR title/body/base, reviewers, labels, Draft⇄Ready; edit/delete own comments (issue + review comments). | Planned | M3a, M3b, M5 |
| F9 | Extra actions | Open PR/comment/check in browser (`o`) — done, including surfacing failures: an `Open` error toasts and reaches `:messages`, and a launched browser process exiting non-zero later (`browser.Opener.OnExit`, a background goroutine) is reported the same way via `App.ShowError`. Merge (method choice) / Close / Reopen via `:merge` / `:close` / `:reopen` commands (no single-key binding) each followed by a confirmation dialog — planned for M5. Local checkout is out of scope. | In progress | M1a, M5 |
| F10 | CI | Show check-runs/statuses, refreshed with the PR; open a check in the browser. No rerun, no log viewing. The list's rollup icon (from the search query's `statusCheckRollup`) is done; the per-check list itself needs the PR detail query and is still planned. | In progress | M1a, M1b |
| F11 | Key remapping | Every navigation/action binding is remappable in config (`keys:`); the vim editor's own keys are fixed. `internal/ui/keys` (normalisation, vim-notation parsing, the full default keymap, `Merge` validation, the multi-key/count `Sequencer`) and the router are done. | Done | M1a |

## Screen layout

| Requirement | Decision | Status | Milestone |
|-------------|----------|--------|-----------|
| Layout | Two columns: left PR list (collapsible via `Ctrl-w o`), right detail with tabs **[PR]** and **[Files]**. The tabs are placeholders in M1a ("PR" shows the previewed pull request's title/repo/author/state/URL; "Files" says "Files view arrives in M2"); the full description/checks/conversation view arrives in M1b. | Done (list column); placeholder (detail tabs) | M1a, M1b, M2 |
| List ordering | Sorted: direct review requests → team review requests → my PRs → involved → custom sections; within a section by `updatedAt` desc; colour-coded repo, author, reviewers (reviewer colour by latest review state; requested-but-not-reviewed dimmed; the viewer's own login bold). | Done | M1a |
| Filter | `/` filters the list (qualifiers `repo:`/`author:`/`reviewer:`/`label:`/`state:` plus free text; `Esc` clears, `Enter` keeps it). | Done | M1a |
| Icons | Unicode by default, Nerd Font optional via config (`internal/ui/theme.Icons`, `IconsFor`). | Done | M1a |

## Non-functional requirements

| Requirement | Decision | Status | Milestone |
|-------------|----------|--------|-----------|
| Lazy loading | List page 1 → detail → files pages → highlight, loaded on demand rather than up front. The list's page 1 loads eagerly on start; reaching the last loaded row of a section with more pages triggers `Store.LoadMore`. Detail/files/highlight lazy loading is planned for M1b/M2. | In progress | M1a–M2 |
| Cache | Disk cache with ETag revalidation for REST and stale-while-revalidate for GraphQL. The on-disk `Entry{Body, ETag, FetchedAt}` store (`internal/cache`) with atomic writes and PR-scoped invalidation exists; `internal/store` caches search results per section (stale-while-revalidate: a cached page-1 result is shown immediately, marked stale, while a network refetch runs) and the viewer lookup, now wired end-to-end into the TUI (stale rows render dimmed). REST ETag revalidation (for file patches, M2) is not yet done. | Done (list); planned (files, M2) | M1a–M3b |
| Refresh | 5-minute auto refresh (configurable via `refresh_interval`) for the list and the current PR. `internal/store.StartAutoRefresh` is started by `App.Run` for the list; wiring it to the current PR arrives with M1b's PR detail. `R` (`global.reload`) forces an immediate cache-skipping reload. | Done (list); planned (current PR, M1b) | M1a |
| Drafts | Drafts persisted on every change and restored across PR switches/reloads/restarts. | Planned | M3a |
| Auth | Reused from `gh` (`GH_TOKEN`/`GITHUB_TOKEN` override; GHES via gh host config). Implemented by `gh.New` via go-gh's `auth.DefaultHost`/`auth.TokenForHost`. | Done | M1a |
| Language | All docs, comments, commits, and UI strings in English. | Done | (ongoing convention) |
| Stack | Go 1.26, tview + tcell only (no BubbleTea), no mouse support. `go.mod` pins `github.com/rivo/tview v0.42.0` and `github.com/gdamore/tcell/v2 v2.13.10` as direct dependencies; the TUI never calls any mouse-related tcell/tview API. | Done | M1a |

## Foundation packages implemented so far

These are not independently numbered requirements; they are the building blocks the functional requirements above depend on.

| Package | Provides | Status |
|---------|----------|--------|
| `internal/config` | YAML config loading (strict, unknown keys rejected), defaults, validation, config/cache/state directory resolution with `GPRT_CONFIG_DIR` > `XDG_CONFIG_HOME` > `~/.config` precedence | Done |
| `internal/logging` | `slog`-based file logger (enabled by `--debug`), ring buffer of recent errors for `:messages` | Done |
| `internal/model` | Domain types: `PullRequest`, `ReviewThread`, `ReviewComment`, `IssueComment`, `Check`, `ChangedFile`, `Section`, `RepoRef`/`PRRef`, reactions, timeline | Done |
| `internal/cache` | Disk+memory `Store` keyed by host/login/repo/PR, atomic writes, `InvalidatePR`/`InvalidateSearch` | Done |
| `internal/browser` | URL opener resolving `GPRT_BROWSER` → config `browser` → `$BROWSER` → OS default, http(s)-only | Done |
| `internal/gh` | GitHub client: `go-gh` auth resolution, embedded GraphQL queries, `Viewer`, `SearchPullRequests`, `BuildSearchQuery`, classified `Error{Kind}`. PR detail, files, and mutations are not yet implemented. | In progress |
| `internal/store` | UI-goroutine-owned state for the PR list: sections (built-in + custom), cache-first load with stale-while-revalidate, generation tokens, in-flight dedup, pagination, filter (`repo:`/`author:`/`reviewer:`/`label:`/`state:` + free text), auto-refresh ticker, dedupe/sort in `Rows`. PR detail, files, drafts, and mutations are not yet implemented. | In progress |
| `internal/ui/keys` | Terminal-protocol-independent key normalisation (`Normalize`, unifying legacy vs. CSI-u reporting of Backspace/Ctrl-H/Tab/Ctrl-I/Enter/Ctrl-M/Esc/Ctrl-\[), vim-notation parsing (`Parse`), the full `Action`/`Context` set and default keymap (`Defaults`) reproducing `docs/KEYBINDINGS.md`, config-override merging with duplicate/prefix-conflict validation (`Merge`), and the multi-key/count state machine (`Sequencer`) | Done |
| `internal/ui/theme` | Dark colour theme applied to `tview.Styles` (`Apply`), named `tcell.Style`s (`Base`, `Header`, `Cursor`, `Stale`, `Muted`, `Accent`, `Error`, `Success`, `Warning`, `Pending`), a stable FNV-hash colour per repository/author (`ColorFor`), review/rollup-state styles (`ReviewerStyle`, `RollupStyle`), and the Unicode/Nerd Font icon sets (`Icons`, `Unicode`, `Nerd`, `IconsFor`) | Done |
| `internal/ui/widget` | `Span`/`DrawSpans`/`SpanWidth` (grapheme-aware drawing and truncation, `uniseg` widths, never `len`), and `ListView` — a scrollable, cursor-driven list primitive (section headers plus 1–2-line items, cursor-by-ID preservation across `SetRows`, half-page/top/bottom movement, changed/selected callbacks) that never handles key input itself | Done |
| `internal/ui` | The tview application: root layout (PR list column with a hideable `/` filter, a detail column with a **[PR]**/**[Files]** tab bar over placeholder tabs, a status bar/command-line row), the single `SetInputCapture` router (Sequencer-driven action dispatch, text-entry passthrough for the filter and command line, an overlay layer for `?` help and `:messages`), the PR list's row rendering and 300 ms preview debounce, the loading spinner and error-toast status bar, and `App.Dispatch`/`App.Run` wiring the Store's events into it | Done (M1a scope); PR detail/files/composer panes are placeholders until M1b–M3a |
| `cmd/gprt` | The CLI entry point: `--config`/`--debug`/`--version` flags, config → logging → theme → cache → `gh` client → browser → keymap → store → app wiring, with the Store/App circular dependency resolved via a forward-declared `*ui.App` variable bound after `ui.New` returns | Done |

## Open items to confirm against the live API

- `reviewThreads` includes the viewer's PENDING threads (M3b). If not, merge the `reviews(states:[PENDING]).comments` connection into the thread model.
- Mixed-side ranges (`startSide: LEFT`, `side: RIGHT`) accepted or rejected (M3b).
- Negated qualifiers (`-user-review-requested:@me`) accepted by search (M1a); otherwise rely on dedupe only.
- Key events on the user's terminal: `internal/ui/keys.Normalize` and the router are implemented and unit-tested for both legacy (`KeyBackspace`) and CSI-u (`KeyCtrlH`) reporting of Ctrl-H; validating against the user's actual terminal is still open (`go run ./cmd/gprt`).

## Known limitations (v1)

- Multi-line range comments are single-side only (all deletion lines or all addition/context lines, not a mix).
- No mouse support.
- No local checkout of a pull request's branch.
- No CI rerun and no check-run log viewing; checks can only be viewed and opened in the browser.
- The vim editor's own keys are fixed and cannot be remapped through config.
- Actions bound by the default keymap but not implemented until a later milestone (`list.new_pr`, `pr.*`, `diff.*`, `thread.*`, `comment.*`, `files.toggle_tree`) are silently ignored by the M1a router rather than showing a "not implemented" toast for every exploratory keypress; `?`/`:help` lists every binding regardless.
- The **[PR]** and **[Files]** detail tabs are placeholders in M1a: **[PR]** shows only the previewed pull request's title, repository, author, state, and URL; **[Files]** shows a static "arrives in M2" message.
