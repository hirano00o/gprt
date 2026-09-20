// threadactions.go implements the diff-focused review-thread and
// review-comment actions (diff.comment/diff.comment_file/thread.reply/
// thread.toggle_resolved/comment.edit/comment.delete while the diff has
// focus), reached from router.go's dispatch. Unlike the PR tab's own
// issue-comment equivalents (composer.go's editCurrentComment/
// deleteCurrentComment), these read the diff's own cursor state
// (widget.DiffView.CursorLine/CursorThread/InVisual/Selection) rather than
// a block ID.
package ui

import (
	"strings"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// diffCommentOrReply implements "c" while the diff has focus
// (diff.comment): a reply when the cursor is on a thread row (matching "r"
// exactly, per docs/KEYBINDINGS.md footnote 2 — this is not a second
// action ID, just this one key's own cursor-dependent behaviour), otherwise
// a new line or range comment from the cursor's current line or active
// visual selection.
func (a *App) diffCommentOrReply() {
	if _, ok := a.diffView.CursorThread(); ok {
		a.replyToCurrentThread()
		return
	}
	a.openLineOrRangeCommentFromCursor()
}

// openLineOrRangeCommentFromCursor opens the line/range comment composer
// for the diff's current cursor line, or its active visual selection if
// one is open. A mixed-side selection (diff.RangeAnchor's ErrMixedSides) is
// refused with a toast and the selection is left exactly as it was — the
// user can adjust it and try again — matching GitHub's own single-sided
// range restriction (docs/DESIGN.md).
func (a *App) openLineOrRangeCommentFromCursor() {
	path := a.currentFilePath
	if path == "" {
		a.showToast("no file open", theme.Warning)
		return
	}

	if a.diffView.InVisual() {
		lines, ok := a.diffView.Selection()
		if !ok {
			return
		}
		if len(lines) == 1 {
			// A visual selection that never moved off its anchor line is a
			// plain single-line comment, not a one-line "range" — avoids
			// RangeAnchor's own "requires at least 2 lines" error for what
			// is, in effect, the same thing as pressing "c" without "V" at
			// all.
			side, num := diff.Anchor(lines[0])
			a.diffView.EndVisual()
			a.openLineOrRangeComposer(path, diff.Range{StartSide: side, Side: side, Line: num})
			return
		}
		r, err := diff.RangeAnchor(lines)
		if err != nil {
			a.showToast("select lines on one side only", theme.Warning)
			return
		}
		a.diffView.EndVisual()
		a.openLineOrRangeComposer(path, r)
		return
	}

	line, ok := a.diffView.CursorLine()
	if !ok {
		return
	}
	side, num := diff.Anchor(line)
	a.openLineOrRangeComposer(path, diff.Range{StartSide: side, Side: side, Line: num})
}

// diffCommentFile implements "C" (diff.comment_file): refused with a toast
// when nothing is open or the current file has no patch to anchor a
// comment to.
func (a *App) diffCommentFile() {
	path := a.currentFilePath
	if path == "" {
		a.showToast("no file open", theme.Warning)
		return
	}
	entry, ok := a.deps.Store.FileByPath(path)
	if !ok || !entry.File.HasPatch {
		a.showToast("file has no patch to comment on", theme.Warning)
		return
	}
	if a.diffView.InVisual() {
		a.diffView.EndVisual()
	}
	a.openFileCommentComposer(path)
}

// replyToCurrentThread implements "r" (thread.reply) and "c" on a thread
// row: toasts when the cursor is not actually on a thread.
func (a *App) replyToCurrentThread() {
	th, ok := a.diffView.CursorThread()
	if !ok {
		a.showToast("not on a comment thread", theme.Warning)
		return
	}
	author := "unknown"
	if len(th.Comments) > 0 && th.Comments[0].Author.Login != "" {
		author = th.Comments[0].Author.Login
	}
	a.openReplyComposer(th.ID, author)
}

// toggleCurrentThreadResolved implements "x" (thread.toggle_resolved):
// toasts when the cursor is not on a thread; otherwise the store itself
// refuses (as an EventError, already toasted by subscribeStore) a pending
// thread or a viewer who cannot resolve/unresolve it.
func (a *App) toggleCurrentThreadResolved() {
	th, ok := a.diffView.CursorThread()
	if !ok {
		a.showToast("not on a comment thread", theme.Warning)
		return
	}
	a.deps.Store.SetThreadResolved(th.ID, !th.IsResolved)
}

// editableReviewComments returns th's comments the viewer may edit
// (ViewerCanUpdate).
func editableReviewComments(th model.ReviewThread) []model.ReviewComment {
	var out []model.ReviewComment
	for _, c := range th.Comments {
		if c.ViewerCanUpdate {
			out = append(out, c)
		}
	}
	return out
}

// deletableReviewComments returns th's comments the viewer may delete
// (ViewerCanDelete).
func deletableReviewComments(th model.ReviewThread) []model.ReviewComment {
	var out []model.ReviewComment
	for _, c := range th.Comments {
		if c.ViewerCanDelete {
			out = append(out, c)
		}
	}
	return out
}

// editCurrentThreadComment implements "e" (comment.edit) with the diff's
// cursor on a thread row: none of the thread's comments editable by the
// viewer toasts; exactly one opens its edit composer directly; several
// show the choice menu (dialogs.go) so the user picks which one. Off a
// thread row entirely, it toasts the same "not on a comment thread" as
// "r"/"x" do, rather than doing nothing at all.
func (a *App) editCurrentThreadComment() {
	th, ok := a.diffView.CursorThread()
	if !ok {
		a.showToast("not on a comment thread", theme.Warning)
		return
	}
	candidates := editableReviewComments(th)
	switch len(candidates) {
	case 0:
		a.showToast("you can only edit your own comments", theme.Warning)
	case 1:
		a.openReviewCommentEditComposer(candidates[0])
	default:
		a.showReviewCommentChoiceMenu("Edit which comment?", candidates, a.openReviewCommentEditComposer)
	}
}

// deleteCurrentThreadComment implements "d" (comment.delete) with the
// diff's cursor on a thread row: same none/one/several shape as
// editCurrentThreadComment (including the same off-thread toast),
// confirming (the existing showConfirm dialog) before calling
// Store.DeleteReviewComment.
func (a *App) deleteCurrentThreadComment() {
	th, ok := a.diffView.CursorThread()
	if !ok {
		a.showToast("not on a comment thread", theme.Warning)
		return
	}
	candidates := deletableReviewComments(th)
	switch len(candidates) {
	case 0:
		a.showToast("you can only delete your own comments", theme.Warning)
	case 1:
		a.confirmDeleteReviewComment(candidates[0].ID)
	default:
		a.showReviewCommentChoiceMenu("Delete which comment?", candidates, func(c model.ReviewComment) {
			a.confirmDeleteReviewComment(c.ID)
		})
	}
}

// confirmDeleteReviewComment shows the standard confirm dialog before
// calling Store.DeleteReviewComment(id).
func (a *App) confirmDeleteReviewComment(id string) {
	a.showConfirm("Delete this comment?", "Delete", func() {
		a.deps.Store.DeleteReviewComment(id)
	})
}

// showReviewCommentChoiceMenu shows a choice menu (dialogs.go) listing
// comments as "@author: first line of body", calling onChoose with the
// picked one.
func (a *App) showReviewCommentChoiceMenu(title string, comments []model.ReviewComment, onChoose func(model.ReviewComment)) {
	labels := make([]string, len(comments))
	for i, c := range comments {
		labels[i] = "@" + c.Author.Login + ": " + firstLine(c.Body)
	}
	a.showChoiceMenu(title, labels, func(i int) {
		onChoose(comments[i])
	})
}

// firstLine returns body's first line, trimmed of surrounding whitespace —
// used wherever a comment/draft needs a short, one-line preview (the
// review-comment choice menu, the "p" pending list).
func firstLine(body string) string {
	line := body
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		line = body[:idx]
	}
	return strings.TrimSpace(line)
}
