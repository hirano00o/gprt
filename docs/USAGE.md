# Usage details

Things you type rather than bind: the list filter, `:` commands, and the keys inside dialogs and forms. Keybindings themselves are in [KEYBINDINGS.md](KEYBINDINGS.md). The in-app `?` help shows a short version of the first two sections.

## Filtering the list

Press `/` in the PR list and type. The list narrows as you type; `Enter` keeps the filter and returns to the list, `Esc` clears it.

The filter is split on spaces. Every word must match (AND), and matching ignores case. The qualifier prefixes themselves must be lowercase: `Repo:x` is free text.

| Word | Matches pull requests |
|------|-----------------------|
| `repo:<text>` | whose `owner/name` contains `<text>` (`repo:gprt`, `repo:hirano00o/`) |
| `author:<login>` | opened by `<login>` |
| `reviewer:<login>` | with a review requested from `<login>` (a user, or a team by its slug) |
| `label:<name>` | that have the label `<name>` |
| `state:<state>` | in `open`, `closed`, `merged`, or `all` |
| any other text | whose title, `#number`, `owner/name`, or author login contains it |

- `repo:`, `author:`, `reviewer:`, and `label:` filter the pull requests already loaded. If one is given twice, the last one wins.
- `state:` searches GitHub again. Removing it goes back to `list.state` from the config. An unknown state is reported once; a partly typed one (`state:clo`) is ignored until it is complete.
- There is no OR or negation. For a view you want every time, add a section to the config instead; its `query` is a GitHub search, so OR, `-repo:`, and every other search qualifier work there:

  ```yaml
  list:
    sections:
      - name: gprt
        query: "repo:hirano00o/gprt"
  ```

## Commands

Press `:`, type a command, and press `Enter`. `Up` / `Down` recall earlier commands, and `Esc` cancels. Commands take no arguments.

| Command | Effect |
|---------|--------|
| `:q`, `:quit` | Quit (asks first while a change is still being sent) |
| `:help` | Show the key reference, like `?` |
| `:messages` | Show recent errors and warnings |
| `:reload` | Reload, ignoring the cache, like `R` |
| `:close` | Close the open pull request, after a confirmation |
| `:reopen` | Reopen the open pull request, after a confirmation |
| `:ready` | Mark the open draft ready for review, after a confirmation |
| `:merge` | Open a dialog to choose the merge method and edit the commit message, then confirm |

## Dialogs and forms

These keys are fixed and cannot be remapped.

**Lists** (pending review `p`, review threads `t`, reactions `a`, labels, reviewers, and choice menus):

| Key | Action |
|-----|--------|
| `j` / `k` | Move |
| `Enter` | Choose, open, or confirm |
| `Space` | Toggle (reactions, labels, reviewers) |
| `d` / `D` | In the pending review: delete the entry / discard the whole review |
| `Tab` | In reviewers: switch between the search field and the list |
| `q`, `Esc` | Close without acting (in reviewers, `q` only while the list has focus) |

**Create (`n`) and edit (`E`) pull request forms:**

| Key | Action |
|-----|--------|
| `Tab` / `Backtab`, `Up` / `Down` | Next / previous field (inside a suggestion list, they move through the suggestions) |
| `Enter` | Pick the highlighted suggestion, or press the focused button |
| `Ctrl-s` | Create or save |
| `Esc` | Close a suggestion list; otherwise close the form, asking first if anything was entered or changed |
| `Ctrl-w l` / `Ctrl-w h` | In the create form: focus the diff preview / go back to the form |

The Repository, Head, and Base fields suggest values as you type. In the create form, choosing a repository fills Base with its default branch and, if the body is still empty, fills the body from the repository's first pull request template. "Edit body" opens the same editor as comments.
