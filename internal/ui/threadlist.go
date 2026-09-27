// threadlist.go implements the "t" (pr.threads) dialog: every review thread
// on the current pull request, sorted by path then line, with j/k/Enter/q/
// Esc routed through router.go's routeThreadListKey — the same "overlay"
// mechanism pendinglist.go's own "p" dialog uses (see App.overlay). Unlike
// the pending list, this is read-only: Enter jumps to the thread in the
// Files tab instead of opening a composer, and there is no d/D mutation
// path (editing/deleting/reacting to a thread is the diff's own job, once
// Enter has landed the cursor on it there).
package ui

import (
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// openThreadList opens the "t" dialog (pr.threads): a no-op toast when no
// pull request is open, or when another overlay is already showing.
func (a *App) openThreadList() {
	if a.overlay != "" {
		return
	}
	if _, ok := a.deps.Store.CurrentRef(); !ok {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "threads"
	a.threadListView = tview.NewList().ShowSecondaryText(false)
	a.threadListView.SetBorder(true)
	a.threadListView.SetTitle(" Review threads ")
	a.rebuildThreadList()

	a.root.AddPage("threads", a.threadListView, true, true)
	a.app.SetFocus(a.threadListView)
}

// closeThreadList closes the "t" dialog and restores focus.
func (a *App) closeThreadList() {
	if a.overlay != "threads" {
		return
	}
	a.root.RemovePage("threads")
	a.overlay = ""
	a.threadListView = nil
	a.threadListEntries = nil
	a.restoreFocus()
}

// rebuildThreadList re-renders the "t" dialog's contents from the current
// pull request's own ReviewThreads, sorted by path, then line (a file-level
// thread's zero Line value leads its own path's other entries), then ID (a
// stable tiebreaker) — preserving the cursor by thread ID where possible.
// Called on open and, while open, on every EventPRChanged/
// EventMutationChanged (see storeevents.go), mirroring rebuildPendingList.
func (a *App) rebuildThreadList() {
	if a.threadListView == nil {
		return
	}
	var curID string
	if cur := a.threadListView.GetCurrentItem(); cur >= 0 && cur < len(a.threadListEntries) {
		curID = a.threadListEntries[cur].ID
	}

	pr := a.deps.Store.CurrentPR()
	var entries []model.ReviewThread
	if pr != nil {
		entries = append(entries, pr.ReviewThreads...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		if entries[i].Line != entries[j].Line {
			return entries[i].Line < entries[j].Line
		}
		return entries[i].ID < entries[j].ID
	})
	a.threadListEntries = entries

	a.threadListView.Clear()
	for _, t := range entries {
		a.threadListView.AddItem(threadListEntryText(t), "", 0, nil)
	}
	if len(entries) == 0 {
		a.threadListView.AddItem("(no review threads)", "", 0, nil)
	}

	newIdx := 0
	for i, t := range entries {
		if t.ID == curID {
			newIdx = i
			break
		}
	}
	if newIdx < a.threadListView.GetItemCount() {
		a.threadListView.SetCurrentItem(newIdx)
	}
}

// threadListEntryText renders one entry's display line: "<path>:<line>
// <flags>@<author>: <first line of the first comment>" for a line/range
// thread, or "<path> (file)  <flags>@<author>: <first line>" for a
// file-level one (model.ThreadSubjectFile). flags is "[resolved] " and/or
// "[outdated] ", each omitted when false; " PENDING" is appended when any
// of the thread's own comments is not yet published.
func threadListEntryText(t model.ReviewThread) string {
	loc := fmt.Sprintf("%s:%d", t.Path, t.Line)
	if t.SubjectType == model.ThreadSubjectFile {
		loc = t.Path + " (file)"
	}

	var flags string
	if t.IsResolved {
		flags += "[resolved] "
	}
	if t.IsOutdated {
		flags += "[outdated] "
	}

	author := "unknown"
	var body string
	pending := false
	if len(t.Comments) > 0 {
		if t.Comments[0].Author.Login != "" {
			author = t.Comments[0].Author.Login
		}
		body = firstLine(t.Comments[0].Body)
	}
	for _, c := range t.Comments {
		if c.State == model.ReviewCommentStatePending {
			pending = true
			break
		}
	}

	text := fmt.Sprintf("%s  %s@%s: %s", loc, flags, author, body)
	if pending {
		text += "  PENDING"
	}
	return text
}

// routeThreadListKey handles the "t" dialog: j/k move, Enter jumps to the
// entry under the cursor, q/Esc close.
//
// Closing (Esc/q) and opening an entry (Enter, which itself closes the
// list) both return immediately instead of continuing the loop, for the
// same reason routePendingKey does — see its own doc comment: a raw event
// can normalize into more than one Key (Alt+rune -> [Esc, rune]), and
// feeding a later one to moveListSelection against a *tview.List a close
// already nilled out would panic.
func (a *App) routeThreadListKey(_ *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k), isPlainRune(k, 'q'):
			a.closeThreadList()
			return nil
		case isEnterKey(k):
			a.openThreadListEntry()
			return nil
		case isPlainRune(k, 'j'):
			moveListSelection(a.threadListView, 1)
		case isPlainRune(k, 'k'):
			moveListSelection(a.threadListView, -1)
		}
	}
	return nil
}

// openThreadListEntry implements Enter: closes the dialog, opens the
// entry's own file in the Files tab, syncs the tree's own selection to it
// (the same findTreeNodeByPath/SetCurrentNode pattern stepFile uses),
// arranges for the diff's cursor to land on the thread once that file's
// data has loaded (widget.DiffView.JumpToThread), and finally focuses the
// diff — mirroring treeOpen's own SetFocus(diffView) once it opens a file —
// so r/x/c work on the landed thread immediately.
func (a *App) openThreadListEntry() {
	idx := a.threadListView.GetCurrentItem()
	if idx < 0 || idx >= len(a.threadListEntries) {
		return
	}
	t := a.threadListEntries[idx]

	a.closeThreadList()
	a.openFilesTabAndFile(t.Path)
	if node := a.findTreeNodeByPath(t.Path); node != nil {
		a.treeView.SetCurrentNode(node)
	}
	a.diffView.JumpToThread(t.ID)
	a.app.SetFocus(a.diffView)
}
