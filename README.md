# gprt
English | [日本語](README.ja.md)
GitHub PR TUI application

`gprt` is a terminal user interface for working with GitHub pull requests without leaving the terminal. It lists pull requests across repositories, shows descriptions, checks, and diffs, and supports vim-style commenting, reviewing, reacting, and mentioning, all driven by keyboard only (no mouse). Authentication is reused from the `gh` CLI, and data is cached on disk with periodic background refresh.

## Status

Milestone **M4 (submit review, reactions, real mentions)** is complete, on top of M1a's foundation, PR list, M1b's PR tab, M2's Files tab, M3a's vim editor/composer/drafts/general comments, and M3b's review comments on the diff; **M5**'s UI slice — edit-PR form, merge/close/reopen, and the create-PR form — is also done: `go run ./cmd/gprt` lists your pull requests, grouped into sections (direct review requests, team review requests, your own, other involvement), sorted, colour-coded, filterable with `/`, and kept fresh by a 5-minute auto-refresh and a manual `R` reload. Selecting a pull request (`Enter`/`l`, or just moving the cursor) opens its **[PR]** tab: title, state, base←head, author, timestamps, labels, review decision and reviewers, diff stats, a lightly-Markdown-styled description, the checks list, and the full conversation timeline (comments, reviews, commits, events) — all navigable with `j`/`k`/`gg`/`G`/`Ctrl-d`/`Ctrl-u`, with `o` opening the selected item in the browser; `c` opens a composer for a new general comment, `e`/`d` on one of your own comments edit or (after confirming) delete it, and `a` on the description block, an issue comment, or a review opens the reaction picker for it. Switching to the **[Files]** tab (`gt`/`Ctrl-l`) shows a directory tree of changed files (with a status glyph, diff stats, a thread-count marker, and a `✎` marker for a line with a saved draft) next to a diff view: hunks with gutters and syntax highlighting, inline review threads (foldable with `za`/`zR`/`zM`, jumped between with `]c`/`[c`, a `PENDING` badge on an unpublished review's own comments), `]f`/`[f` between files, `V` for a visual line selection, and `zh`/`zl` to scroll a wide line horizontally. `c` opens a composer for a new line/range comment (or, on an existing thread, replies — same as `r`); `C` comments on the whole file; `x` toggles a thread resolved/unresolved; `e`/`d` on a thread edit or delete one of your own comments there (a menu when it has several); `a` on a thread opens the reaction picker for its comment(s) — every comment is reactable, not just your own, with a menu when it has several. Sending a line/range/file comment or a reply offers GitHub's own choice — "Add single comment" now, or "Add to review" to accumulate one, unless a pending review already exists, in which case every new comment joins it automatically. `p` opens a list of every pending review comment and local draft for the open pull request (`Enter` opens one for editing, `d` deletes it, `D` discards the whole pending review). `S` opens the submit-review dialog (Approve / Request changes / Comment, each with its own composer for the review body) and calls `Store.SubmitReview`, refreshing the list on success. The reaction picker lists all eight GitHub reactions with their current counts, toggled with `Enter`/`Space` and staying open for several toggles. On the **[PR]** and diff composers alike: normal/insert/visual-mode editing with counts, operators, undo/redo, `@`-mention completion (the PR's own participants, then the repository's real mentionable users), and `:e` to edit the buffer in your `$EDITOR`; `Ctrl-s`/`:w` sends, `:q`/`:q!` close it keeping or discarding a draft — drafts persist to disk and survive a PR switch, a reload, or restarting `gprt`, and the status bar shows how many the current pull request has (`✎ N`). Every navigation/action key is remappable via config (the editor's own keys are fixed), and `?` shows the effective bindings.

On top of M4, the first half of M5's UI slice adds pull-request editing and lifecycle commands. `E` (`ContextPR`/`ContextFiles`/`ContextDiff`, refused with a toast when the viewer cannot edit the pull request) opens an edit form: title, base branch (autocompleted from the repository's branches as you type), a "Labels" button opening a multi-select list (`Space` toggles), a "Reviewers" button opening a multi-select list combining currently-requested reviewers (removable), the repository's own mentionable users filtered as you type (addable — a brand-new individual user), and a team search (addable), and a Draft checkbox; `Ctrl-s` saves only what actually changed ("nothing to save" otherwise), toasting each part's own success or failure, and `Esc` asks to discard first only if something did. `e` on the PR tab's description block opens a composer for the pull request's own body instead of a general comment; `d` there is a no-op. The `:merge`, `:close`, and `:reopen` commands (never single keys, to avoid an accidental keypress next to `n`) each show a confirmation dialog before calling the matching mutation — `:merge` first loads the repository's allowed merge methods (preselecting the only one, if there is just one) and shows a commit headline/body form.

`n` (list only) opens the create-PR form, the other half of M5's UI slice: a Repository field fuzzy-autocompleted over your own repositories (typing an exact `owner/name` not in that list works too), Head/Base branch fields autocompleted the same way as the edit form's own base field, a Title field, an "Edit body" button opening the same vim composer over the pull request's body-to-be, a "Reviewers" button (the edit form's own overlay, reused), and a Draft checkbox. Choosing a repository preselects Base with its default branch and, if the body is still empty, prefills it from the repository's first pull request template. `Ctrl-s` validates locally (a repository, differing head/base branches, a title) before creating the pull request, then opens it and toasts its number; a failure — including a created pull request whose reviewer request then failed — keeps you informed without losing what you typed. `Esc` asks to discard first only once you have entered something. Unlike every other dialog, an unrelated pull request switch never closes this form or its body composer. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for the full status by requirement and milestone.

## Features

| # | Feature |
|---|---------|
| F1 | PR list across repositories (built-in and custom search sections, state filter) |
| F2 | Create a PR from inside the TUI (repo/branch pickers, template, reviewers, draft) |
| F3 | Files changed: file tree + unified diff with async syntax highlighting and inline comment threads |
| F4 | Submit reviews: single comment or pending review (Approve / Request changes / Comment) |
| F5 | `@` mention autocomplete in the editor |
| F6 | Toggle the 8 GitHub reactions on PR bodies, comments, and reviews |
| F7 | Hybrid editing: in-app vim-like modal editor, or suspend to `$EDITOR` |
| F8 | Edit PR title/body/base/reviewers/labels/draft state; edit/delete own comments |
| F9 | Open in browser; merge/close/reopen via `:` commands with confirmation |
| F10 | CI checks: rollup icon, check list, refreshed with the PR, open a check in the browser |
| F11 | Every navigation/action key is remappable in config; the editor's own keys are fixed |

Every feature above is done. F1 (the PR list), F3 (Files changed), F4 (submit reviews), F5 (mentions), F6 (reactions), F7 (the vim editor and `:e`), F10 (CI checks, list and PR tab), F11 (key remapping), and the browser-opening half of F9 landed first. F8 (edit/delete comments, PR metadata) covers issue comments and review comments end to end (add/edit/delete, with a composer trigger, a choice menu when several apply, and confirmation), the pull request's own body edited the same way (`e` on the description block), and `E` opening a form editing title/base/labels/reviewers/draft. F9's `:merge`/`:close`/`:reopen` commands each sit behind a confirmation dialog, and F2 (creating a pull request from the TUI, `n`) completed the set. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for per-item status.

## Requirements

- Go 1.26
- The [`gh` CLI](https://cli.github.com/) logged in (`gh auth login`), or the `GH_TOKEN` / `GITHUB_TOKEN` environment variable set

## Install

```sh
go install github.com/hirano00o/gprt/cmd/gprt@latest
```

(Once released — no tagged release exists yet. `go build ./cmd/gprt` builds a local `gprt` binary today.)

## Usage

```sh
go run ./cmd/gprt              # or: gprt, once built/installed
go run ./cmd/gprt --debug      # log to <state dir>/gprt.log (see below)
go run ./cmd/gprt --config /path/to/config.yaml
go run ./cmd/gprt --version
```

| Flag | Effect |
|------|--------|
| `--config <path>` | Use this config file instead of the default config directory's `config.yaml`; unlike the default path, a missing file here is a startup error, not a silent fallback to defaults |
| `--debug` | Write JSON debug logs to `<state dir>/gprt.log` (also enabled by `GPRT_DEBUG`) |
| `--version` | Print the version and exit |

## Configuration

`gprt` reads an optional, strict YAML configuration file. Every key is optional; a missing file is not an error.

Configuration directory precedence:

1. `GPRT_CONFIG_DIR` — must be an absolute path if set; a non-absolute value is a startup error, not silently skipped (it is gprt's own variable, so a typo there should not go unnoticed)
2. `XDG_CONFIG_HOME/gprt` — a non-absolute `XDG_CONFIG_HOME` is silently ignored, per the XDG Base Directory specification
3. `~/.config/gprt`

The configuration file is `config.yaml` inside that directory, for example `~/.config/gprt/config.yaml`.

```yaml
host: github.com          # default: gh's default host
refresh_interval: 5m
icons: unicode            # unicode | nerd
editor: ""                # overrides $EDITOR for :e; split with shell quoting, $VAR/~ expand, globs don't
browser: ""               # overrides $BROWSER; split with shell quoting, $VAR/~ expand, globs don't
highlight_style: github-dark  # "" or any name from chroma's styles.Names(); rejected at startup if unknown
tab_width: 4
list:
  state: open             # open | closed (not merged) | merged | all
  sections:
    - name: Backend
      query: "org:acme label:backend"
keys:                     # action ID → key sequence (vim notation); unspecified actions keep defaults
  global.toggle_list: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

Other directories used by `gprt`:

- Cache (ETag-revalidated API responses): `~/.cache/gprt`
- State (drafts and the debug log): `~/.local/state/gprt`

## Keybindings

Keybindings are vim-style and remappable through the `keys:` config section (the composer's own vim keys, below, are fixed). `j`/`k`/`gg`/`G`/`Ctrl-d`/`Ctrl-u` move the cursor in the list, the **[PR]** tab, the file tree, or the diff; `/` filters the list, `Enter`/`l` opens a PR (or, in the tree, a directory/file), `o` opens the list's current PR (the PR tab's selected block, a check's own details URL, or a diff thread's/file's URL) in the browser, `R` reloads, `Ctrl-w h`/`Ctrl-w l` cycle focus through the list and whichever detail pane(s) the active tab has (list ⇄ PR tab, or list → tree → diff), `Ctrl-w o` toggles the list column, `Ctrl-w t` toggles the file tree, `gt`/`gT`/`Ctrl-l`/`Ctrl-h` switch the **[PR]**/**[Files]** tabs, and on the diff `V` starts a visual line selection (`Esc` cancels), `za`/`zR`/`zM` fold/unfold threads, `zh`/`zl` scroll horizontally, and `]c`/`[c`/`]f`/`[f` jump between threads/files. On the **[PR]** tab, `c` opens a composer for a new general comment, `e`/`d` on one of your own comments edit or (after confirming) delete it, and `a` on the description block, an issue comment, or a review opens the reaction picker for it. On the diff, `c` opens a composer for a new line comment (or, with an active `V` selection, a range — a mix of added and removed lines is refused with a toast) or, on an existing thread, replies (same as `r`); `C` comments on the whole file; `x` toggles a thread resolved/unresolved (toasting on a non-thread row); `e`/`d` on a thread edit or (after confirming) delete one of your own comments there — a small menu when several apply; `a` on a thread opens the reaction picker for its comment(s) — every comment is reactable, not just your own, with the same small menu when it has several. Sending a line/range/file comment or a reply shows GitHub's own two-button choice, "Add single comment" or "Add to review", unless a pending review already exists (then it always joins that review, no menu). `p` opens a list of the current pull request's pending review comments and local drafts (`j`/`k` move, `Enter` opens one for editing, `d` deletes it, `D` discards the whole pending review, `q`/`Esc` closes) — the composer stays open (with `Ctrl-w j`/`Ctrl-w k` toggling focus between it and the pane it was opened over) throughout. `S` opens the submit-review dialog: a choice menu (Approve / Request changes / Comment) then a composer for the review's own body; sending calls `Store.SubmitReview` and refreshes the PR list on success — a Comment/Request-changes review with a blank body and no pending comments is refused (Approve never requires a body). The reaction picker (`a`) lists GitHub's eight reactions with their current counts and a `YOU` marker on ones you have added; `j`/`k` move, `Enter`/`Space` toggles the one under the cursor and leaves the picker open for more, `q`/`Esc` close it; opening it is refused with a toast when the pull request does not allow reactions. Inside the composer: `i a I A o O` enter insert mode, `h j k l w b e 0 ^ $ gg G` move (with counts), `x X D C s S dd dw de d$ cc cw c$ yy yw p P` operate, `J` joins lines, `u`/`Ctrl-r` undo/redo, `v`/`V` start a (line) visual selection, `@` opens a mention popup (candidates are the PR's own participants, then the repository's real mentionable users, fuzzy-filtered; `Ctrl-n`/`Ctrl-p`/arrows to move, `Tab`/`Enter` to accept, `Esc` to close just the popup), `Ctrl-s`/`:w` sends, `:q`/`:q!` close it keeping/discarding the draft, and `:e` suspends `gprt` to edit the buffer in your configured `editor`/`$EDITOR`. `?` shows help, `:` opens a command line (`:q`, `:help`, `:messages`, `:reload`), and `q`/`Ctrl-c` quit (`Ctrl-c` asks for confirmation while a comment is still being sent, edited, or deleted). See [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md) for the full table, action IDs, and which bindings are implemented yet.

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
golangci-lint fmt
```

## License

MIT. See [LICENSE](LICENSE).
