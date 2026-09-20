# gprt
GitHub PR TUI application

`gprt` is a terminal user interface for working with GitHub pull requests without leaving the terminal. It lists pull requests across repositories, shows descriptions, checks, and diffs, and supports vim-style commenting, reviewing, reacting, and mentioning, all driven by keyboard only (no mouse). Authentication is reused from the `gh` CLI, and data is cached on disk with periodic background refresh.

## Status

This project is under active development and is not yet usable end to end. Milestone **M1a (foundation and PR list)** is in progress: configuration, logging, the domain model, the disk cache store, and the browser launcher are implemented. The GitHub client, PR list UI, PR detail, diff view, editor, and review/mutation flows are still planned. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for the full status by requirement and milestone.

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

Every feature above is planned; none is implemented yet. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for per-item status.

## Requirements

- Go 1.26
- The [`gh` CLI](https://cli.github.com/) logged in (`gh auth login`), or the `GH_TOKEN` / `GITHUB_TOKEN` environment variable set

## Install

```sh
go install github.com/hirano00o/gprt/cmd/gprt@latest
```

(Once released — no `cmd/gprt` binary or tagged release exists yet.)

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

Other directories used by `gprt`:

- Cache (ETag-revalidated API responses): `~/.cache/gprt`
- State (drafts and the debug log): `~/.local/state/gprt`

## Keybindings

Keybindings are vim-style and remappable through the `keys:` config section. See [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md) for the full table and action IDs.

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
golangci-lint fmt
```

## License

MIT. See [LICENSE](LICENSE).
