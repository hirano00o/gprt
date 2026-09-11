# Keybindings

Reference for gprt's default keybindings. This is the source for the in-app `?` help screen.

## Status

`internal/ui/keys` (normalisation, vim-notation parsing, the full default keymap below, `Merge` validation, and the `Sequencer` multi-key/count state machine) and the router (`internal/ui`) are implemented (M1a). The **Status** column below marks each binding `Done (M1a)` once the *action itself* has an effect — reaching it through the Sequencer and dispatch table is not enough on its own if the pane or feature it controls does not exist yet. Every binding is present in the keymap and listed by `?`/`:help` regardless of its status, since remapping validation (`keys:` in config) does not depend on whether an action is implemented yet.

The `?` help and `:messages` overlays are scrollable `TextView`s: `q` or `Esc` closes either one (pressing `?` again also closes the help overlay specifically), and every other key — including `j`/`k`/`gg`/`G`/`PgUp`/`PgDn` — is left to the `TextView`'s own native scrolling rather than being swallowed by the router. The same "let it through" rule applies to the **[PR]**/**[Files]** detail tabs for any key gprt does not bind in the `detail` context, so those `TextView`s scroll the same way even before M1b's full detail view exists.

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
| `j` | list / diff / tree | move down | `list.down` | Done (M1a, list only) |
| `k` | list / diff / tree | move up | `list.up` | Done (M1a, list only) |
| `gg` | list / diff / tree | move to top | `list.top` | Done (M1a, list only) |
| `G` | list / diff / tree | move to bottom | `list.bottom` | Done (M1a, list only) |
| `Ctrl-d` | list / diff / tree | move down half a page | `list.half_down` | Done (M1a, list only) |
| `Ctrl-u` | list / diff / tree | move up half a page | `list.half_up` | Done (M1a, list only) |
| `Ctrl-w h` | global | focus previous column (list ⇄ detail; in Files: tree ⇄ diff) | `global.focus_left` | Done (M1a) |
| `Ctrl-w l` | global | focus next column (list ⇄ detail; in Files: tree ⇄ diff) | `global.focus_right` | Done (M1a) |
| `Ctrl-w j` | composer open | focus down into the composer | `global.focus_down`¹ | Planned (M3a) |
| `Ctrl-w k` | composer open | focus up out of the composer, back to diff/detail | `global.focus_up`¹ | Planned (M3a) |
| `Ctrl-l`, `gt` | detail | switch to the next tab (PR → Files) | `detail.tab_next` | Done (M1a) |
| `Ctrl-h`, `gT` | detail | switch to the previous tab (Files → PR); `Ctrl-h` equals Backspace on legacy terminals, so it is only bound outside text input | `detail.tab_prev` | Done (M1a) |
| `Ctrl-w o` | global | toggle the PR list column | `global.toggle_list` | Done (M1a) |
| `Ctrl-w t` | Files | toggle the file tree | `files.toggle_tree` | Planned (M2) |
| `R` | global | reload the list and the current PR, ignoring cache | `global.reload` | Done (M1a, list only; current PR arrives with M1b) |
| `/` | list | open the filter input (`Esc` clears) | `list.filter` | Done (M1a) |
| `Enter`, `l` | list | open the selected PR and focus the detail pane; in the tree: open the file and focus the diff | `list.open` | Done (M1a, list only) |
| `V` | diff | start visual line selection for a range comment; `Esc` cancels | `diff.visual` | Planned (M2) |
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
| `o` | any | open in browser | `global.open_browser` | Done (M1a) |
| `za` | diff | fold the thread under the cursor | `diff.fold` | Planned (M2) |
| `zR` | diff | unfold all threads | `diff.unfold_all` | Planned (M2) |
| `zM` | diff | fold all threads | `diff.fold_all` | Planned (M2) |
| `zh` | diff | scroll left | `diff.scroll_left` | Planned (M2) |
| `zl` | diff | scroll right | `diff.scroll_right` | Planned (M2) |
| `]c` | diff | jump to the next thread | `diff.next_thread` | Planned (M2) |
| `[c` | diff | jump to the previous thread | `diff.prev_thread` | Planned (M2) |
| `]f` | diff | jump to the next file | `diff.next_file` | Planned (M2) |
| `[f` | diff | jump to the previous file | `diff.prev_file` | Planned (M2) |
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
