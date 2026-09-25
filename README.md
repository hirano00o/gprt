# gprt

English | [日本語](README.ja.md)

A terminal UI for GitHub pull requests. Browse, review, comment, and merge without leaving the terminal, using vim-style keys and nothing but the keyboard.

## Features

- **One list for all your pull requests** across repositories: review requests (yours and your teams'), your own PRs, PRs you are involved in, and any custom search you add. Filter with `/`, switch between open, closed, and merged.
- **PR view** with the description, labels, reviewers, CI checks, and the full conversation timeline.
- **Files view** with a file tree and a syntax-highlighted diff. Review threads appear inline and can be folded.
- **Reviewing**: comment on a line, a range, or a whole file; reply; resolve threads; post a comment right away or build up a pending review and submit it as Approve, Request changes, or Comment.
- **Reactions** and `@`-mention completion with the repository's users.
- **Editing and lifecycle**: edit a PR's title, description, base, labels, reviewers, and draft state; create pull requests from the TUI; merge, close, and reopen through `:merge`, `:close`, `:reopen`, each behind a confirmation.
- **Vim-style editor** for every text you write, with `:e` to jump into your `$EDITOR`. Drafts are saved as you type and survive restarts.
- **Fast and quiet**: reuses your `gh` login, caches API responses on disk, refreshes in the background, and lets you remap every navigation key.

## Installation

Requirements:

- The [`gh` CLI](https://cli.github.com/) logged in (`gh auth login`), or a `GH_TOKEN` / `GITHUB_TOKEN` environment variable. GitHub Enterprise Server works through `gh`'s host configuration.
- Go 1.26 or newer to install from source.

Prebuilt binaries for Linux, macOS, and Windows (amd64 and arm64) are attached to each [release](https://github.com/hirano00o/gprt/releases). Download the archive for your platform and put `gprt` on your `PATH`.

With Nix (flakes enabled):

```sh
nix run github:hirano00o/gprt          # try it without installing
nix profile add github:hirano00o/gprt  # install into your profile
```

In a NixOS or Home Manager configuration, add this repository as a flake input and use `inputs.gprt.packages.<system>.default`. Append a tag or commit to pin a version, for example `github:hirano00o/gprt/v0.1.0`.

Or install from source:

```sh
go install github.com/hirano00o/gprt/cmd/gprt@latest
```

Or build from a checkout:

```sh
git clone https://github.com/hirano00o/gprt.git
cd gprt
go build ./cmd/gprt
```

## Usage

```sh
gprt
```

The list on the left shows your pull requests. Move with `j`/`k`, open one with `Enter`, and switch between the **PR** tab and the **Files** tab with `gt`/`gT`. Press `?` at any time for the key reference, `:` for commands, and `q` to quit.

| Flag | Effect |
|------|--------|
| `--config <path>` | Use this file instead of the default `config.yaml` |
| `--debug` | Write a debug log to `~/.local/state/gprt/gprt.log` (also `GPRT_DEBUG=1`) |
| `--version` | Print the version and exit |

## Configuration

Configuration is optional. `gprt` reads `~/.config/gprt/config.yaml` (or `$XDG_CONFIG_HOME/gprt/config.yaml`; `GPRT_CONFIG_DIR` overrides the directory). Every key may be omitted.

```yaml
host: github.com          # GitHub host; defaults to gh's default host
refresh_interval: 5m      # background refresh of the list and the open PR
icons: unicode            # unicode | nerd (Nerd Font glyphs)
editor: ""                # command for :e; defaults to $EDITOR, then vim
browser: ""               # command for o; defaults to $BROWSER, then the OS default
highlight_style: github-dark  # any chroma style name
tab_width: 4
list:
  state: open             # open | closed | merged | all
  sections:               # extra search sections, appended to the built-in ones
    - name: Backend
      query: "org:acme label:backend"
keys:                     # remap any action (vim notation); see docs/KEYBINDINGS.md for the IDs
  global.toggle_list: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

`editor` and `browser` are split with shell quoting; `$VAR` and `~` are expanded, globs are not. `GPRT_BROWSER` takes precedence over the `browser` setting.

Other directories: cached API responses live in `~/.cache/gprt`, drafts and logs in `~/.local/state/gprt`.

## Keybindings

All keys below except the editor's own can be remapped. The full reference, including action IDs, is in [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md).

### General

| Key | Action |
|-----|--------|
| `j` / `k`, `gg` / `G`, `Ctrl-d` / `Ctrl-u`, `PageDown` / `PageUp` (`Ctrl-f` / `Ctrl-b`) | Move down / up, top / bottom, half page, full page |
| `gt` / `gT` (also `Ctrl-l` / `Ctrl-h`) | Next / previous tab |
| `Ctrl-w h` / `Ctrl-w l` | Focus the previous / next pane |
| `Ctrl-w o` | Show or hide the PR list |
| `o` | Open the current item in the browser |
| `R` | Reload, ignoring the cache |
| `:` | Command line: `:merge`, `:close`, `:reopen`, `:reload`, `:messages`, `:help`, `:q` |
| `?` | Help (`/` inside it searches the binding list, `n` / `N` cycle matches) |
| `q`, `Ctrl-c` | Close the open dialog, or quit |

### PR list

| Key | Action |
|-----|--------|
| `Enter`, `l` | Open the pull request |
| `/` | Filter the list (`Esc` clears) |
| `n` | Create a pull request |

### PR tab

| Key | Action |
|-----|--------|
| `c` | New comment |
| `e` / `d` | Edit / delete your own comment; `e` on the description edits it |
| `a` | Add or remove a reaction |
| `E` | Edit title, base branch, labels, reviewers, draft |
| `S` | Submit a review: Approve, Request changes, or Comment |
| `p` | Pending review comments and saved drafts |
| `t` | Review threads, sorted by path and line (`Enter` jumps to it in the Files tab) |

### Files tab

| Key | Action |
|-----|--------|
| `Enter`, `l` | Open the file under the cursor (in the tree) |
| `Ctrl-w t` | Show or hide the file tree |
| `V` | Select lines for a range comment (`Esc` cancels) |
| `c` | Comment on the line or the selection; on a thread, reply |
| `C` | Comment on the whole file |
| `r` | Reply to the thread |
| `x` | Resolve or unresolve the thread |
| `e` / `d` | Edit / delete your own review comment |
| `a` | Add or remove a reaction |
| `za` / `zR` / `zM` | Fold the thread / unfold all / fold all |
| `]c` / `[c` | Next / previous thread, crossing into the next / previous file that has one |
| `]f` / `[f` | Next / previous file |
| `zh` / `zl` | Scroll horizontally |
| `/` | Search the diff across every changed file (regexp, smartcase; `Enter` runs it, `Esc` cancels or clears the highlight) |
| `n` / `N` | Next / previous search match, wrapping with a toast |

`E`, `S`, `p`, and `t` work here too.

### Editor

| Key | Action |
|-----|--------|
| `i` `a` `I` `A` `o` `O` | Enter insert mode |
| `Esc` | Back to normal mode |
| `h` `j` `k` `l` `w` `b` `e` `0` `^` `$` `gg` `G` | Motions, with counts |
| `x` `dd` `dw` `cc` `cw` `yy` `p` `P` `J` … | Delete, change, yank, paste, join |
| `u` / `Ctrl-r` | Undo / redo |
| `v` / `V` | Visual / visual line selection |
| `@` | Mention completion: `Ctrl-n` / `Ctrl-p` to move, `Tab` or `Enter` to accept |
| `:w`, `Ctrl-s` | Send |
| `:q` / `:q!` | Close, keeping / discarding the draft |
| `:e` | Edit the text in your `$EDITOR` |

Sending a line, range, or file comment asks whether to post it as a single comment or add it to a pending review, the same choice GitHub offers. Confirmation dialogs start on **Cancel**; press `Tab` and then `Enter` to confirm.

## Contributing

Bug reports and pull requests are welcome.

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
```

The architecture is described in [docs/DESIGN.md](docs/DESIGN.md), the feature list with its status in [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md), and every key with its action ID in [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md).

## License

MIT. See [LICENSE](LICENSE).
