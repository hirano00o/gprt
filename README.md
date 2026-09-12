# gprt
GitHub PR TUI application

`gprt` is a terminal user interface for working with GitHub pull requests without leaving the terminal. It lists pull requests across repositories, shows descriptions, checks, and diffs, and supports vim-style commenting, reviewing, reacting, and mentioning, all driven by keyboard only (no mouse). Authentication is reused from the `gh` CLI, and data is cached on disk with periodic background refresh.

## Status

Milestone **M3a (vim editor, composer, drafts, general comments)** is complete, on top of M1a's foundation, PR list, M1b's PR tab, and M2's Files tab: `go run ./cmd/gprt` lists your pull requests, grouped into sections (direct review requests, team review requests, your own, other involvement), sorted, colour-coded, filterable with `/`, and kept fresh by a 5-minute auto-refresh and a manual `R` reload. Selecting a pull request (`Enter`/`l`, or just moving the cursor) opens its **[PR]** tab: title, state, base←head, author, timestamps, labels, review decision and reviewers, diff stats, a lightly-Markdown-styled description, the checks list, and the full conversation timeline (comments, reviews, commits, events) — all navigable with `j`/`k`/`gg`/`G`/`Ctrl-d`/`Ctrl-u`, with `o` opening the selected item in the browser. Switching to the **[Files]** tab (`gt`/`Ctrl-l`) shows a directory tree of changed files (with a status glyph, diff stats, and a thread-count marker) next to a diff view: hunks with gutters and syntax highlighting, inline review threads (foldable with `za`/`zR`/`zM`, jumped between with `]c`/`[c`), `]f`/`[f` between files, `V` for a visual line selection, and `zh`/`zl` to scroll a wide line horizontally — reading is complete; commenting on a diff line lands in M3b. On the **[PR]** tab, `c` opens a vim-style composer to add a general comment; `e`/`d` on one of your own comments opens it for editing or asks to confirm deleting it. The composer supports normal/insert/visual-mode editing with counts, operators, undo/redo, `@`-mention completion (from the PR's own participants for now), and `:e` to edit the buffer in your `$EDITOR`; `Ctrl-s`/`:w` sends, `:q`/`:q!` close it keeping or discarding a draft — drafts persist to disk and survive a PR switch, a reload, or restarting `gprt`, and the status bar shows how many the current pull request has (`✎ N`). Every navigation/action key is remappable via config (the editor's own keys are fixed), and `?` shows the effective bindings. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for the full status by requirement and milestone.

## Features (roadmap)

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

F1 (the PR list), F7 (the vim editor and `:e`), F10 (CI checks, list and PR tab), F11 (key remapping), and the browser-opening half of F9 are done. F3 (Files changed) is in progress — the read-only tree, diff, threads, folds, and visual selection are done, with commenting/replying/resolving on a diff line landing in M3b. F5 (mentions) and F8 (edit/delete comments) are in progress — general (issue) comments are done end to end (add/edit/delete, with a composer trigger and confirmation), review comments and PR metadata editing remain planned. Every other feature above is still planned. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for per-item status.

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
editor: ""                # overrides $EDITOR for :e
browser: ""               # overrides $BROWSER
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

Keybindings are vim-style and remappable through the `keys:` config section (the composer's own vim keys, below, are fixed). `j`/`k`/`gg`/`G`/`Ctrl-d`/`Ctrl-u` move the cursor in the list, the **[PR]** tab, the file tree, or the diff; `/` filters the list, `Enter`/`l` opens a PR (or, in the tree, a directory/file), `o` opens the list's current PR (the PR tab's selected block, a check's own details URL, or a diff thread's/file's URL) in the browser, `R` reloads, `Ctrl-w h`/`Ctrl-w l` cycle focus through the list and whichever detail pane(s) the active tab has (list ⇄ PR tab, or list → tree → diff), `Ctrl-w o` toggles the list column, `Ctrl-w t` toggles the file tree, `gt`/`gT`/`Ctrl-l`/`Ctrl-h` switch the **[PR]**/**[Files]** tabs, and on the diff `V` starts a visual line selection (`Esc` cancels), `za`/`zR`/`zM` fold/unfold threads, `zh`/`zl` scroll horizontally, and `]c`/`[c`/`]f`/`[f` jump between threads/files. On the **[PR]** tab, `c` opens a composer for a new general comment, and `e`/`d` on one of your own comments edit or (after confirming) delete it — the composer stays open with `Ctrl-w j`/`Ctrl-w k` toggling focus between it and the pane it was opened over. Inside the composer: `i a I A o O` enter insert mode, `h j k l w b e 0 ^ $ gg G` move (with counts), `x X D C s S dd dw de d$ cc cw c$ yy yw p P` operate, `J` joins lines, `u`/`Ctrl-r` undo/redo, `v`/`V` start a (line) visual selection, `@` opens a mention popup (`Ctrl-n`/`Ctrl-p`/arrows to move, `Tab`/`Enter` to accept, `Esc` to close just the popup), `Ctrl-s`/`:w` sends, `:q`/`:q!` close it keeping/discarding the draft, and `:e` suspends `gprt` to edit the buffer in your configured `editor`/`$EDITOR`. `?` shows help, `:` opens a command line (`:q`, `:help`, `:messages`, `:reload`), and `q`/`Ctrl-c` quit (`Ctrl-c` asks for confirmation while a comment is still being sent, edited, or deleted). See [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md) for the full table, action IDs, and which bindings are implemented yet.

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
golangci-lint fmt
```

## License

MIT. See [LICENSE](LICENSE).
