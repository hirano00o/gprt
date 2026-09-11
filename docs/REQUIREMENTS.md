# Requirements

Status values: `Done`, `In progress`, `Planned`. Milestones refer to the branches in the [Milestones](#milestones) section of `docs/DESIGN.md` (`M1a`, `M1b`, `M2`, `M3a`, `M3b`, `M4`, `M5`). This table is updated in the same commit as any change to implementation status.

## Functional requirements

| # | Requirement | Decision | Status | Milestone |
|---|-------------|----------|--------|-----------|
| F1 | PR list | Cross-repository via GitHub search. Built-in sections: direct review requests, team review requests, my PRs, other involvement; config adds custom search sections. Default `state:open`; filter can switch to closed / merged / all (re-search). Search client + store done; UI pending. | In progress | M1a |
| F2 | PR create | Repository and branches chosen inside the TUI (no cwd git): repo picker, head branch (pushed), base (default branch preselected), title, body (vim editor, PR template pre-filled), reviewers (users + teams, autocomplete), draft flag. After creation the PR opens and its checks show/refresh. | Planned | M5 |
| F3 | Files changed | Left file tree (diffview.nvim style, collapsible) + right diff. Unified diff with `+`/`-` colours immediately, language syntax highlighting (chroma) applied asynchronously. Comments on a line, a `V` range, or a whole file; replies; resolve/unresolve; inline foldable thread blocks (resolved folded by default). | Planned | M2, M3b |
| F4 | Review submit | Two paths like GitHub: single comment published immediately, or pending review accumulating comments and submitted with Approve / Request changes / Comment + body. Pending comments editable/deletable before submit. | Planned | M3b, M4 |
| F5 | Mentions | `@` in insert mode opens an autocomplete popup (repo `mentionableUsers`, fuzzy). | Planned | M3a, M4 |
| F6 | Reactions | Toggle the 8 GitHub reactions on PR body, issue comments, review comments, reviews. | Planned | M4 |
| F7 | Editing | Hybrid: in-app vim-like modal editor with `@` completion; `:e` suspends the TUI and opens `$EDITOR` on the buffer. | Planned | M3a |
| F8 | Edit PR/comments | Edit PR title/body/base, reviewers, labels, Draft⇄Ready; edit/delete own comments (issue + review comments). | Planned | M3a, M3b, M5 |
| F9 | Extra actions | Open PR/comment/check in browser; Merge (method choice) / Close / Reopen via `:merge` / `:close` / `:reopen` commands (no single-key binding) each followed by a confirmation dialog. Local checkout is out of scope. | Planned | M1a, M5 |
| F10 | CI | Show check-runs/statuses (with rollup icon in the list), refreshed with the PR; open a check in the browser. No rerun, no log viewing. | Planned | M1a, M1b |
| F11 | Key remapping | Every navigation/action binding is remappable in config (`keys:`); the vim editor's own keys are fixed. | Planned | M1a |

## Screen layout

| Requirement | Decision | Status | Milestone |
|-------------|----------|--------|-----------|
| Layout | Two columns: left ⅓ PR list (collapsible), right detail with tabs **[PR]** (description, meta, checks, conversation) and **[Files]** (tree + diff). | Planned | M1a, M1b, M2 |
| List ordering | Sorted: direct review requests → team review requests → my PRs → involved → custom sections; within a section by `updatedAt` desc; colour-coded repo, author, reviewers. | Planned | M1a |
| Filter | `/` filters the list. | Planned | M1a |
| Icons | Unicode by default, Nerd Font optional via config. | Planned | M1a |

## Non-functional requirements

| Requirement | Decision | Status | Milestone |
|-------------|----------|--------|-----------|
| Lazy loading | List page 1 → detail → files pages → highlight, loaded on demand rather than up front. | Planned | M1a–M2 |
| Cache | Disk cache with ETag revalidation for REST and stale-while-revalidate for GraphQL. The on-disk `Entry{Body, ETag, FetchedAt}` store (`internal/cache`) with atomic writes and PR-scoped invalidation exists; `internal/store` now caches search results per section (stale-while-revalidate: a cached page-1 result is shown immediately, marked stale, while a network refetch runs) and the viewer lookup. REST ETag revalidation (for file patches, M2) is not yet done. | In progress | M1a–M3b |
| Refresh | 5-minute auto refresh (configurable via `refresh_interval`) for the list and the current PR. `internal/store.StartAutoRefresh` implements the ticker for the list; wiring it to the UI and to the current PR (M1b) is pending. | In progress | M1a |
| Drafts | Drafts persisted on every change and restored across PR switches/reloads/restarts. | Planned | M3a |
| Auth | Reused from `gh` (`GH_TOKEN`/`GITHUB_TOKEN` override; GHES via gh host config). Implemented by `gh.New` via go-gh's `auth.DefaultHost`/`auth.TokenForHost`. | Done | M1a |
| Language | All docs, comments, commits, and UI strings in English. | Done | (ongoing convention) |
| Stack | Go 1.26, tview + tcell only (no BubbleTea), no mouse support. The `go.mod` module targets Go 1.26; `tview`/`tcell` are not yet added as dependencies. | In progress | M1a |

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

## Open items to confirm against the live API

- `reviewThreads` includes the viewer's PENDING threads (M3b). If not, merge the `reviews(states:[PENDING]).comments` connection into the thread model.
- Mixed-side ranges (`startSide: LEFT`, `side: RIGHT`) accepted or rejected (M3b).
- Negated qualifiers (`-user-review-requested:@me`) accepted by search (M1a); otherwise rely on dedupe only.
- Key events on the user's terminal (dump in a tiny tcell program before M1a's router work).

## Known limitations (v1)

- Multi-line range comments are single-side only (all deletion lines or all addition/context lines, not a mix).
- No mouse support.
- No local checkout of a pull request's branch.
- No CI rerun and no check-run log viewing; checks can only be viewed and opened in the browser.
- The vim editor's own keys are fixed and cannot be remapped through config.
