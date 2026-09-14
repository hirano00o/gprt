// pendinglist.go implements the "p" (pr.pending) dialog: every pending
// review comment for the current pull request (Store.PendingComments())
// followed by every local draft for it (internal/drafts.Store.List), with
// j/k/Enter/d/D/q/Esc routed through router.go's routePendingKey — the
// same "overlay" mechanism help/messages/confirm use (see App.overlay).
package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// pendingListEntry is one row of the "p" list: either a pending review
// comment or a local draft for the current pull request, in that fixed
// order (see rebuildPendingList).
type pendingListEntry struct {
	isDraft bool
	comment model.ReviewComment
	draft   drafts.Draft
}

// openPendingList opens the "p" dialog (pr.pending): a no-op toast when no
// pull request is open, or when another overlay is already showing.
func (a *App) openPendingList() {
	if a.overlay != "" {
		return
	}
	if _, ok := a.deps.Store.CurrentRef(); !ok {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "pending"
	a.pendingListView = tview.NewList().ShowSecondaryText(false)
	a.pendingListView.SetBorder(true)
	a.rebuildPendingList()

	a.root.AddPage("pending", a.pendingListView, true, true)
	a.app.SetFocus(a.pendingListView)
}

// closePendingList closes the "p" dialog and restores focus.
func (a *App) closePendingList() {
	if a.overlay != "pending" {
		return
	}
	a.root.RemovePage("pending")
	a.overlay = ""
	a.pendingListView = nil
	a.pendingListEntries = nil
	a.restoreFocus()
}

// rebuildPendingList re-renders the "p" dialog's contents from the current
// Store/Drafts state, preserving the cursor position where possible. Called
// on open and, while open, on every EventPRChanged/EventMutationChanged
// (see storeevents.go) — a pending comment's own edit/delete/discard, or
// any other change to the pull request, must not leave the list showing
// stale entries.
func (a *App) rebuildPendingList() {
	if a.pendingListView == nil {
		return
	}
	// By entry identity, not raw index: an entry earlier in the list
	// disappearing (its comment was deleted, its draft resolved) must not
	// silently land the cursor on some other, unrelated entry that now
	// happens to sit at the same index.
	var curID string
	if cur := a.pendingListView.GetCurrentItem(); cur >= 0 && cur < len(a.pendingListEntries) {
		curID = pendingListEntryID(a.pendingListEntries[cur])
	}

	var entries []pendingListEntry
	for _, c := range a.deps.Store.PendingComments() {
		entries = append(entries, pendingListEntry{comment: c})
	}
	if ref, ok := a.deps.Store.CurrentRef(); ok && a.deps.Drafts != nil {
		// List's own returned list is used regardless of err: a single
		// corrupt draft file must not hide every other, perfectly valid
		// draft from this list (List itself already tolerates and
		// aggregates per-file errors — see its own doc comment — rather
		// than failing the whole call for that reason). The error is
		// still surfaced, once, the same "warn once, reset once resolved"
		// shape files.go's checkFilesWarnings uses for its own warnings.
		list, err := a.deps.Drafts.List(ref.Key())
		for _, d := range list {
			entries = append(entries, pendingListEntry{isDraft: true, draft: d})
		}
		switch {
		case err != nil && !a.pendingListDraftsWarned:
			a.pendingListDraftsWarned = true
			a.deps.Logger.Warn("draft list for pending list failed", "err", err)
			a.showToast("some drafts could not be read: "+err.Error(), theme.Warning)
		case err == nil:
			a.pendingListDraftsWarned = false
		}
	}
	a.pendingListEntries = entries

	a.pendingListView.Clear()
	for _, e := range entries {
		a.pendingListView.AddItem(a.pendingListEntryText(e), "", 0, nil)
	}
	if len(entries) == 0 {
		a.pendingListView.AddItem("(nothing pending)", "", 0, nil)
	}

	newIdx := 0
	for i, e := range entries {
		if pendingListEntryID(e) == curID {
			newIdx = i
			break
		}
	}
	if newIdx < a.pendingListView.GetItemCount() {
		a.pendingListView.SetCurrentItem(newIdx)
	}

	title := " Pending "
	if rev := a.deps.Store.PendingReview(); rev != nil && rev.Body != "" {
		title = " Pending: " + firstLine(rev.Body) + " "
	}
	a.pendingListView.SetTitle(title)
}

// pendingListEntryID returns a stable identity for e — a pending
// comment's own ID, or a draft's own Key.String() — used by
// rebuildPendingList to restore the cursor by identity rather than by a
// raw index that shifts whenever an earlier entry disappears.
func pendingListEntryID(e pendingListEntry) string {
	if e.isDraft {
		return "draft:" + e.draft.Key.String()
	}
	return "comment:" + e.comment.ID
}

// pendingListEntryText renders one entry's display line.
func (a *App) pendingListEntryText(e pendingListEntry) string {
	if e.isDraft {
		return draftEntryText(e.draft)
	}
	path, line, _ := a.pendingCommentLocation(e.comment.ID)
	return fmt.Sprintf("%s:%d @%s: %s  PENDING", path, line, e.comment.Author.Login, firstLine(e.comment.Body))
}

// pendingCommentLocation finds the path/line a pending review comment
// (identified by id) is anchored to, by looking up its owning thread —
// model.ReviewComment itself carries no thread reference of its own.
func (a *App) pendingCommentLocation(id string) (path string, line int, ok bool) {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return "", 0, false
	}
	for _, t := range pr.ReviewThreads {
		for _, c := range t.Comments {
			if c.ID == id {
				return t.Path, t.Line, true
			}
		}
	}
	return "", 0, false
}

// draftEntryText renders one drafts.Draft's display line, per its Kind.
func draftEntryText(d drafts.Draft) string {
	switch d.Key.Kind {
	case drafts.KindComment:
		if path, side, start, line, ok := parseLineAnchor(d.Key.Anchor); ok {
			if start != 0 && start != line {
				return fmt.Sprintf("%s:%d-%d (%s): %s  DRAFT", path, start, line, side, firstLine(d.Text))
			}
			return fmt.Sprintf("%s:%d (%s): %s  DRAFT", path, line, side, firstLine(d.Text))
		}
	case drafts.KindFile:
		return fmt.Sprintf("%s: %s  DRAFT", strings.TrimPrefix(d.Key.Anchor, "file:"), firstLine(d.Text))
	case drafts.KindReply:
		return fmt.Sprintf("reply to %s: %s  DRAFT", d.Key.Anchor, firstLine(d.Text))
	case drafts.KindEdit:
		return fmt.Sprintf("edit %s: %s  DRAFT", d.Key.Anchor, firstLine(d.Text))
	}
	return fmt.Sprintf("%s: %s  DRAFT", d.Key.Anchor, firstLine(d.Text))
}

// routePendingKey handles the "p" dialog: j/k move, Enter opens the current
// item, d deletes it (after a confirm), D discards the whole pending
// review (after a confirm), q/Esc close.
//
// Closing (Esc/q) and opening an entry (Enter, which itself closes the
// list on success) both return immediately instead of continuing the
// loop, for the same reason routeChoiceKey does — see its own doc
// comment: a raw event can normalize into more than one Key, and feeding
// a later one to moveListSelection/etc. against a *tview.List the close
// already nilled out would panic.
func (a *App) routePendingKey(_ *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k), isPlainRune(k, 'q'):
			a.closePendingList()
			return nil
		case isEnterKey(k):
			a.openPendingListEntry()
			return nil
		case isPlainRune(k, 'j'):
			moveListSelection(a.pendingListView, 1)
		case isPlainRune(k, 'k'):
			moveListSelection(a.pendingListView, -1)
		case isPlainRune(k, 'd'):
			a.deletePendingListEntry()
		case isPlainRune(k, 'D'):
			a.discardPendingReviewFromList()
		}
	}
	return nil
}

// confirmWithinPendingList shows a confirm modal stacked on top of the
// still-open "p" dialog (a plain showConfirm would close it first, since
// both share the single App.overlay slot — see showConfirm's own doc
// comment): the router treats "pendingConfirm" exactly like "confirm"
// (return the event unchanged, letting the Modal's own InputHandler run),
// and the done func pops back to "pending" and refocuses the list either
// way, running onConfirm only when the user picked "Delete". "Cancel"
// (button index 1) defaults focus, mirroring showConfirm's own
// SetFocus(1) — this modal is built directly, not through showConfirm, so
// it needs the identical fix applied independently (see showConfirm's own
// doc comment for why a bare Enter must never default to the destructive
// choice).
func (a *App) confirmWithinPendingList(message string, onConfirm func()) {
	a.overlay = "pendingConfirm"
	modal := tview.NewModal().SetText(message).AddButtons([]string{"Delete", "Cancel"})
	modal.SetFocus(1)
	modal.SetDoneFunc(func(_ int, label string) {
		a.root.RemovePage("pendingConfirm")
		a.overlay = "pending"
		a.app.SetFocus(a.pendingListView)
		if label == "Delete" {
			onConfirm()
		}
	})
	a.root.AddPage("pendingConfirm", modal, true, true)
	a.app.SetFocus(modal)
}

// openPendingListEntry implements Enter: a pending comment opens its edit
// composer; a draft opens the composer for its own target, restoring its
// text (openComposer's own Drafts.Load does that automatically once the
// right target/draftKey is given) and, for a line/range/file draft, also
// opens its file in the Files tab and arranges for the diff cursor to land
// on its anchor once that file's data is available (widget.DiffView's own
// JumpToLine — see its doc comment for why the target survives a rebuild
// that runs before the data has arrived).
func (a *App) openPendingListEntry() {
	idx := a.pendingListView.GetCurrentItem()
	if idx < 0 || idx >= len(a.pendingListEntries) {
		return
	}
	entry := a.pendingListEntries[idx]

	if !entry.isDraft {
		a.closePendingList()
		a.openReviewCommentEditComposer(entry.comment)
		return
	}
	// Only close the list once the draft's own target actually opens —
	// a draft this dialog cannot resolve (an edit whose comment ID no
	// longer matches anything, or a Kind this milestone's UI never opens
	// from here, such as a pending review's own body or an in-progress PR
	// body edit) toasts instead, leaving the list open rather than
	// silently doing nothing.
	if !a.openDraftFromList(entry.draft) {
		a.showToast("cannot open this draft here", theme.Warning)
		return
	}
	a.closePendingList()
}

// openDraftFromList dispatches to the composer opener matching d.Key.Kind,
// reporting whether it actually found one to open.
func (a *App) openDraftFromList(d drafts.Draft) bool {
	switch d.Key.Kind {
	case drafts.KindComment:
		if path, side, start, line, ok := parseLineAnchor(d.Key.Anchor); ok {
			a.openFilesTabAndFile(path)
			a.diffView.JumpToLine(side, line)
			a.openLineOrRangeComposer(path, diff.Range{StartSide: side, Side: side, StartLine: start, Line: line})
			return true
		}
		// Not a line/range anchor: a general PR comment draft (Anchor ==
		// "issue", see openGeneralCommentComposer's own draftKey).
		a.switchTab("pr", 0)
		a.openGeneralCommentComposer()
		return true
	case drafts.KindFile:
		path := strings.TrimPrefix(d.Key.Anchor, "file:")
		a.openFilesTabAndFile(path)
		a.openFileCommentComposer(path)
		return true
	case drafts.KindReply:
		a.openReplyComposer(d.Key.Anchor, a.threadAuthorFor(d.Key.Anchor))
		return true
	case drafts.KindEdit:
		return a.openEditDraftFromList(d.Key.Anchor)
	default:
		// KindReview (a pending review's own body) and KindPRBody have no
		// composer this milestone's UI opens from the pending list.
		return false
	}
}

// openEditDraftFromList reopens the edit composer for a KindEdit draft
// (anchored by comment ID): an issue comment (PR tab) or a review comment
// (diff), whichever the ID actually belongs to on the current pull
// request — a KindEdit draft's anchor alone does not say which. Reports
// false when commentID matches neither (the comment it was drafted for
// was deleted, or the pull request's own data has not loaded that far).
func (a *App) openEditDraftFromList(commentID string) bool {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return false
	}
	for _, item := range pr.Timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == commentID {
			ref, ok := a.deps.Store.CurrentRef()
			if !ok {
				return false
			}
			a.openComposer(composerTarget{
				kind:      composerKindEdit,
				ref:       ref,
				commentID: commentID,
				title:     "Edit comment by @" + item.IssueComment.Author.Login,
				draftKey:  drafts.Key{PR: ref.Key(), Kind: drafts.KindEdit, Anchor: commentID},
			}, item.IssueComment.Body)
			return true
		}
	}
	for _, t := range pr.ReviewThreads {
		for _, c := range t.Comments {
			if c.ID == commentID {
				a.openReviewCommentEditComposer(c)
				return true
			}
		}
	}
	return false
}

// threadAuthorFor returns threadID's first comment's author login, or
// "unknown" when the thread cannot be found (should not normally happen: a
// saved reply draft's thread ID came from an existing thread).
func (a *App) threadAuthorFor(threadID string) string {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return "unknown"
	}
	for _, t := range pr.ReviewThreads {
		if t.ID == threadID && len(t.Comments) > 0 && t.Comments[0].Author.Login != "" {
			return t.Comments[0].Author.Login
		}
	}
	return "unknown"
}

// openFilesTabAndFile switches to the Files tab (if not already showing)
// and opens path there.
func (a *App) openFilesTabAndFile(path string) {
	if a.currentTab != "files" {
		a.switchTab("files", 1)
	}
	a.openFile(path)
}

// deletePendingListEntry implements "d": deletes the current item after a
// confirm — a pending comment via Store.DeleteReviewComment (its own
// EventPRChanged, once the mutation lands, refreshes this list — see
// storeevents.go), a draft via drafts.Store.Delete (local and synchronous,
// so this refreshes the list itself).
func (a *App) deletePendingListEntry() {
	idx := a.pendingListView.GetCurrentItem()
	if idx < 0 || idx >= len(a.pendingListEntries) {
		return
	}
	entry := a.pendingListEntries[idx]

	if entry.isDraft {
		key := entry.draft.Key
		a.confirmWithinPendingList("Delete this draft?", func() {
			if a.deps.Drafts != nil {
				if err := a.deps.Drafts.Delete(key); err != nil {
					a.showErrorToast("draft delete failed: " + err.Error())
				} else if path, _, _, _, ok := parseLineAnchor(key.Anchor); key.Kind == drafts.KindComment && ok {
					// A line/range comment draft's own gutter marker
					// (widget.DiffFile.DraftLines) must disappear the
					// moment its draft is deleted from here too, not only
					// via the composer's own onComposerChange/close path.
					a.refreshDraftGutterForPath(path)
				}
			}
			a.rebuildPendingList()
			a.renderStatusBar(a.spinnerFrame)
		})
		return
	}

	id := entry.comment.ID
	a.confirmWithinPendingList("Delete this pending comment?", func() {
		a.deps.Store.DeleteReviewComment(id)
	})
}

// discardPendingReviewFromList implements "D": discards the whole pending
// review after a confirm, disabled with a toast when there is none.
func (a *App) discardPendingReviewFromList() {
	if a.deps.Store.PendingReview() == nil {
		a.showToast("no pending review to discard", theme.Warning)
		return
	}
	a.confirmWithinPendingList("Discard the entire pending review?", func() {
		a.deps.Store.DiscardPendingReview()
	})
}
