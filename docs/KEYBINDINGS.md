# Keybindings

Reference for gprt's default keybindings. This is the source for the in-app `?` help screen.

## Status

`internal/ui/keys` (normalisation, vim-notation parsing, the full default keymap below, `Merge` validation, and the `Sequencer` multi-key/count state machine) and the router (`internal/ui`) are implemented (M1a). The **Status** column below marks each binding `Done (M1a)` once the *action itself* has an effect — reaching it through the Sequencer and dispatch table is not enough on its own if the pane or feature it controls does not exist yet. Every binding is present in the keymap and listed by `?`/`:help` regardless of its status, since remapping validation (`keys:` in config) does not depend on whether an action is implemented yet — including bindings in `thread`/`comment`/`pr`/`composer`, contexts `keys.Context` already defines but `App.currentContexts` never actually resolves a keypress against today (no focused pane maps to any of them yet; only `global`/`list`/`detail`/`files`/`diff` do), so those bindings are listed but unreachable until the panes/features that would give them focus arrive with M3a/M3b.

The `?` help and `:messages` overlays are scrollable `TextView`s: `q` or `Esc` closes either one (pressing `?` again also closes the help overlay specifically), and every other key — including `j`/`k`/`gg`/`G`/`PgUp`/`PgDn` — is left to the `TextView`'s own native scrolling rather than being swallowed by the router. `list.down`/`up`/`top`/`bottom`/`half_down`/`half_up` are bound in the `files`/`detail`/`diff` contexts too and move whichever movable pane currently has focus — the PR list, the PR tab, the Files tab's tree, or its diff. For the tree specifically, these actions are routed through a `treeMovablePane` adapter that synthesises `tview.TreeView`'s own native `j`/`k`/PgUp/PgDn keys (repeating `k`/`j` for top/bottom, since `tview.TreeView`'s native `g`/`G` only scroll the viewport and never move the selection itself) rather than reimplementing tree traversal by hand, so the visible movement is unchanged from the tree's native behaviour. `K`/`J` (parent/child navigation) remain `tview.TreeView`'s own, unmapped by gprt's keymap. `h` is added on top of the tree's native handling via its own `SetInputCapture` (collapse-or-parent); `l` is the remappable `list.open` action (`Enter`'s row below), routed to `treeOpen` when the tree has focus rather than the list's own `Select`. A key genuinely bound in no context still falls through unchanged.

## Remapping

`keys:` in `config.yaml` remaps a binding by **Action ID**, using vim notation for the key sequence (`<C-w>o`, `gt`, `]c`, `<Esc>`, `<Enter>`). Any action not listed in `keys:` keeps its default sequence. Example:

```yaml
keys:
  global.toggle_list: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

Two remapping errors are rejected at startup: an unknown action ID, and a key sequence that duplicates or is a prefix of another sequence already bound in the same context.

The vim editor's own keys (listed at the bottom of this document) are **fixed** and cannot be remapped through `keys:`.

## Default bindings

| Key | Context | Action | Action ID | Status |
|-----|---------|--------|-----------|--------|
| `j` | list / files / diff / detail | move down; in the tree, its own native movement reached via `treeMovablePane` (see the Status section above) | `list.down` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `k` | list / files / diff / detail | move up; in the tree, its own native movement | `list.up` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `gg` | list / files / diff / detail | move to top; in the tree, repeated native `k` presses (its own native `g` only scrolls, see the Status section above) | `list.top` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `G` | list / files / diff / detail | move to bottom; in the tree, repeated native `j` presses | `list.bottom` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `Ctrl-d` | list / files / diff / detail | move down half a page | `list.half_down` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `Ctrl-u` | list / files / diff / detail | move up half a page | `list.half_up` | Done (M1a list; M1b PR tab; M2 diff, tree) |
| `Ctrl-w h` | global | focus previous pane (list ⇄ detail; in Files: list → tree → diff, and back) | `global.focus_left` | Done (M1a; M2 Files tab) |
| `Ctrl-w l` | global | focus next pane (list ⇄ detail; in Files: list → tree → diff, and back) | `global.focus_right` | Done (M1a; M2 Files tab) |
| `Ctrl-w j` | composer open | focus down into the composer | `global.focus_down`¹ | Planned (M3a) |
| `Ctrl-w k` | composer open | focus up out of the composer, back to diff/detail | `global.focus_up`¹ | Planned (M3a) |
| `Ctrl-l`, `gt` | detail / tree / diff | switch to the next tab (PR → Files) | `detail.tab_next` | Done (M1a) |
| `Ctrl-h`, `gT` | detail / tree / diff | switch to the previous tab (Files → PR); `Ctrl-h` equals Backspace on legacy terminals, so it is only bound outside text input | `detail.tab_prev` | Done (M1a) |
| `Ctrl-w o` | global | toggle the PR list column | `global.toggle_list` | Done (M1a) |
| `Ctrl-w t` | Files / diff | toggle the file tree (bound in both, so it stays reachable from the diff once the tree is hidden) | `files.toggle_tree` | Done (M2) |
| `R` | global | reload the list and the current PR, ignoring cache | `global.reload` | Done |
| `/` | list | open the filter input (`Esc` clears) | `list.filter` | Done (M1a) |
| `Enter`, `l` | list / tree | open the selected PR and focus the detail pane; in the tree: expand a directory, or open a file and focus the diff | `list.open` | Done (M1a PR list; M2 tree) |
| `V` | diff | start visual line selection for a range comment; `Esc` cancels | `diff.visual` | Done (M2 selection; comment creation M3b) |
| `c` | diff | new comment on the current line or visual selection | `diff.comment` | Planned (M2, M3b) |
| `c` | PR tab | new general comment on the PR | `diff.comment`² | Planned (M3a) |
| `c` | on a thread | reply (same as `r`) | `thread.reply`² | Planned (M3b) |
| `C` | diff | file-level comment on the current file | `diff.comment_file` | Planned (M2, M3b) |
| `r` | thread | reply | `thread.reply` | Planned (M3b) |
| `e` | own comment / PR body | edit | `comment.edit` | Planned (M3a, M3b) |
| `d` | own comment / PR body | delete (with confirmation) | `comment.delete` | Planned (M3a, M3b) |
| `x` | thread (not pending) | toggle resolved | `thread.toggle_resolved` | Planned (M3b) |
| `a` | comment / review / PR body | open the reaction picker (toggles a reaction) | `comment.react` | Planned (M4) |
| `p` | PR open | pending review comments + drafts list (edit / delete / discard review) | `pr.pending` | Planned (M3b) |
| `S` | PR open | open the submit review dialog | `pr.submit` | Planned (M4) |
| `n` | list | create a PR | `list.new_pr` | Planned (M5) |
| `E` | PR | edit PR meta (title / base / labels / reviewers / draft) | `pr.edit` | Planned (M5) |
| `o` | any | open in browser: the list cursor's PR on the list, the selected block's own URL on the PR tab (falling back to the pull request's URL when the block has none of its own, for example a commit or event row), the thread comment's URL on the diff when the cursor is on a thread (otherwise the pull request's own `/files` URL, also used on the tree), otherwise the currently open pull request | `global.open_browser` | Done |
| `za` | diff | fold the thread under the cursor | `diff.fold` | Done (M2) |
| `zR` | diff | unfold all threads | `diff.unfold_all` | Done (M2) |
| `zM` | diff | fold all threads | `diff.fold_all` | Done (M2) |
| `zh` | diff | scroll left | `diff.scroll_left` | Done (M2) |
| `zl` | diff | scroll right | `diff.scroll_right` | Done (M2) |
| `]c` | diff | jump to the next thread | `diff.next_thread` | Done (M2) |
| `[c` | diff | jump to the previous thread | `diff.prev_thread` | Done (M2) |
| `]f` | diff | jump to the next file | `diff.next_file` | Done (M2) |
| `[f` | diff | jump to the previous file | `diff.prev_file` | Done (M2) |
| `?` | global | help (pressing it again while help is open closes it) | `global.help` | Done (M1a) |
| `q` | global | close the topmost dialog/composer, otherwise quit | `global.quit` | Done (M1a, list/detail/overlay) |
| `Ctrl-c` | global | quit; asks for confirmation only while a mutation is in flight | `global.quit`³ | Done (M1a; the confirmation itself arrives with M3a's mutation queue) |
| `:` | global (outside the editor) | open the command line | `global.command` | Done (M1a) |

¹ `global.focus_down`/`global.focus_up` extend the `global.focus_left`/`global.focus_right` naming scheme for the composer's vertical focus toggle; unlike the other rows, these two action IDs are not yet in the plan's confirmed action ID set and should be treated as provisional until M3a implements the composer.

² `c` is one physical key whose action depends on where the cursor is: on a diff line or the PR tab it starts a new comment (`diff.comment`); on an existing thread it behaves like `r` (`thread.reply`). It does not introduce a third action ID.

³ `Ctrl-c` shares `global.quit`'s intent (quit the application) but is handled separately by the router because of its in-flight-mutation confirmation; see `docs/DESIGN.md`, concurrency rule 9.

## `:` commands

Entered via `global.command` (`:`), these exist only as typed commands, never as single-key bindings, specifically to avoid an accidental keypress next to `n` triggering something destructive:

| Command | Effect | Status |
|---------|--------|--------|
| `:merge` | Open the merge dialog (method picker sourced from the repository's settings, commit headline editor), then a confirmation dialog | Planned (M5) |
| `:close` | Close the current PR, after a confirmation dialog | Planned (M5) |
| `:reopen` | Reopen the current PR, after a confirmation dialog | Planned (M5) |
| `:messages` | Show the ring buffer of recent log messages (see `internal/logging`) | Done (M1a) |
| `:help` | Show the help screen (same as `?`) | Done (M1a) |
| `:q` | Quit | Done (M1a) |
| `:quit` | Quit (alias of `:q`) | Done (M1a) |
| `:reload` | Discard cached data and re-fetch every section (same as `R`) | Done (M1a) |

An unrecognized command shows an error toast instead of doing nothing silently.

## Vim editor keys (composer, fixed — not remappable)

Planned (M3a). None of the following exist yet: the composer pane, the `editor.Vim` modal layer, or the `editor.Editor` primitive.

| Key(s) | Effect |
|--------|--------|
| `Esc` | Leave insert/visual mode |
| `i a I A o O` | Enter insert mode (insert before/after cursor, start/end of line, open line below/above) |
| `h j k l w b e 0 ^ $ gg G` (with optional counts) | Motions |
| `x X dd dw de d$ D cc cw c$ C yy yw p P` | Operators and shortcuts (delete, change, yank, paste) |
| `J` | Join lines |
| `u` | Undo |
| `Ctrl-r` | Redo |
| `v V` + `d y c` | Visual / visual-line selection combined with an operator |
| `:w`, `Ctrl-s` | Send (opens the "Add single comment" / "Add to review" menu when no pending review exists yet) |
| `:q` | Close the composer, keeping the draft |
| `:q!` | Close the composer, discarding the draft |
| `:e` | Suspend the TUI and edit the buffer in `$EDITOR` (or the configured `editor`) |
| `Ctrl-n`, `Ctrl-p`, `Up`, `Down` (mention popup) | Move the selection in the `@`-mention popup |
| `Tab`, `Enter` (mention popup) | Accept the selected mention |
| `Esc` (mention popup) | Close the popup |
