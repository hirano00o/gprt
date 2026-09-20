# Keybindings

Reference for gprt's default keybindings. This is the source for the in-app `?` help screen.

## Status

Every binding below is **Planned**: no key handling, router, or keymap exists yet (see `docs/REQUIREMENTS.md`, requirement F11, milestone M1a). Bindings are implemented milestone by milestone alongside the feature they control — for example, `diff.*` actions ship with M2, `pr.pending`/`pr.submit` with M3b/M4. Consult `docs/REQUIREMENTS.md` for which milestone delivers which action.

## Remapping

`keys:` in `config.yaml` remaps a binding by **Action ID**, using vim notation for the key sequence (`<C-w>o`, `gt`, `]c`, `<Esc>`, `<Enter>`). Any action not listed in `keys:` keeps its default sequence. Example:

```yaml
keys:
  list.toggle: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

Two remapping errors are rejected at startup: an unknown action ID, and a key sequence that duplicates or is a prefix of another sequence already bound in the same context.

The vim editor's own keys (listed at the bottom of this document) are **fixed** and cannot be remapped through `keys:`.

## Default bindings

| Key | Context | Action | Action ID |
|-----|---------|--------|-----------|
| `j` | list / diff / tree | move down | `list.down` |
| `k` | list / diff / tree | move up | `list.up` |
| `gg` | list / diff / tree | move to top | `list.top` |
| `G` | list / diff / tree | move to bottom | `list.bottom` |
| `Ctrl-d` | list / diff / tree | move down half a page | `list.half_down` |
| `Ctrl-u` | list / diff / tree | move up half a page | `list.half_up` |
| `Ctrl-w h` | global | focus previous column (list ⇄ detail; in Files: tree ⇄ diff) | `global.focus_left` |
| `Ctrl-w l` | global | focus next column (list ⇄ detail; in Files: tree ⇄ diff) | `global.focus_right` |
| `Ctrl-w j` | composer open | focus down into the composer | `global.focus_down`¹ |
| `Ctrl-w k` | composer open | focus up out of the composer, back to diff/detail | `global.focus_up`¹ |
| `Ctrl-l`, `gt` | detail | switch to the next tab (PR → Files) | `detail.tab_next` |
| `Ctrl-h`, `gT` | detail | switch to the previous tab (Files → PR); `Ctrl-h` equals Backspace on legacy terminals, so it is only bound outside text input | `detail.tab_prev` |
| `Ctrl-w o` | global | toggle the PR list column | `global.toggle_list` |
| `Ctrl-w t` | Files | toggle the file tree | `files.toggle_tree` |
| `R` | global | reload the list and the current PR, ignoring cache | `global.reload` |
| `/` | list | open the filter input (`Esc` clears) | `list.filter` |
| `Enter`, `l` | list | open the selected PR and focus the detail pane; in the tree: open the file and focus the diff | `list.open` |
| `V` | diff | start visual line selection for a range comment; `Esc` cancels | `diff.visual` |
| `c` | diff | new comment on the current line or visual selection | `diff.comment` |
| `c` | PR tab | new general comment on the PR | `diff.comment`² |
| `c` | on a thread | reply (same as `r`) | `thread.reply`² |
| `C` | diff | file-level comment on the current file | `diff.comment_file` |
| `r` | thread | reply | `thread.reply` |
| `e` | own comment / PR body | edit | `comment.edit` |
| `d` | own comment / PR body | delete (with confirmation) | `comment.delete` |
| `x` | thread (not pending) | toggle resolved | `thread.toggle_resolved` |
| `a` | comment / review / PR body | open the reaction picker (toggles a reaction) | `comment.react` |
| `p` | PR open | pending review comments + drafts list (edit / delete / discard review) | `pr.pending` |
| `S` | PR open | open the submit review dialog | `pr.submit` |
| `n` | list | create a PR | `list.new_pr` |
| `E` | PR | edit PR meta (title / base / labels / reviewers / draft) | `pr.edit` |
| `o` | any | open in browser | `global.open_browser` |
| `za` | diff | fold the thread under the cursor | `diff.fold` |
| `zR` | diff | unfold all threads | `diff.unfold_all` |
| `zM` | diff | fold all threads | `diff.fold_all` |
| `zh` | diff | scroll left | `diff.scroll_left` |
| `zl` | diff | scroll right | `diff.scroll_right` |
| `]c` | diff | jump to the next thread | `diff.next_thread` |
| `[c` | diff | jump to the previous thread | `diff.prev_thread` |
| `]f` | diff | jump to the next file | `diff.next_file` |
| `[f` | diff | jump to the previous file | `diff.prev_file` |
| `?` | global | help | `global.help` |
| `q` | global | close the topmost dialog/composer, otherwise quit | `global.quit` |
| `Ctrl-c` | global | quit; asks for confirmation only while a mutation is in flight | `global.quit`³ |
| `:` | global (outside the editor) | open the command line | `global.command` |

¹ `global.focus_down`/`global.focus_up` extend the `global.focus_left`/`global.focus_right` naming scheme for the composer's vertical focus toggle; unlike the other rows, these two action IDs are not yet in the plan's confirmed action ID set and should be treated as provisional until M3a implements the composer.
² `c` is one physical key whose action depends on where the cursor is: on a diff line or the PR tab it starts a new comment (`diff.comment`); on an existing thread it behaves like `r` (`thread.reply`). It does not introduce a third action ID.
³ `Ctrl-c` shares `global.quit`'s intent (quit the application) but is handled separately by the router because of its in-flight-mutation confirmation; see `docs/DESIGN.md`, concurrency rule 9.

## `:` commands

Entered via `global.command` (`:`), these exist only as typed commands, never as single-key bindings, specifically to avoid an accidental keypress next to `n` triggering something destructive:

| Command | Effect |
|---------|--------|
| `:merge` | Open the merge dialog (method picker sourced from the repository's settings, commit headline editor), then a confirmation dialog |
| `:close` | Close the current PR, after a confirmation dialog |
| `:reopen` | Reopen the current PR, after a confirmation dialog |
| `:messages` | Show the ring buffer of recent log messages (see `internal/logging`) |
| `:help` | Show the help screen (same as `?`) |
| `:q` | Quit |

An unrecognized command shows an error toast instead of doing nothing silently.

## Vim editor keys (composer, fixed — not remappable)

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
