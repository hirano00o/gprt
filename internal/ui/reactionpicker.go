// reactionpicker.go implements the "a" (comment.react) reaction picker: an
// overlay listing GitHub's eight known reactions for one subject (the
// current pull request itself, an issue comment, a review, or a review
// comment), each row showing its current count and a marker when the
// viewer has already reacted with it. j/k move, Enter/Space toggles via
// Store.ToggleReaction (the picker stays open so several reactions can be
// toggled in a row), q/Esc closes — the same "overlay" mechanism and
// j/k/Enter/q/Esc router shape pendinglist.go's "p" dialog already uses.
package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// reactionContentOrder lists the eight reactions the picker always shows,
// in this fixed order (matching model.ReactionContent's own declaration
// order).
var reactionContentOrder = []model.ReactionContent{
	model.ReactionThumbsUp, model.ReactionThumbsDown, model.ReactionLaugh,
	model.ReactionHooray, model.ReactionConfused, model.ReactionHeart,
	model.ReactionRocket, model.ReactionEyes,
}

// reactToCurrentPRTabBlock implements "a" while the PR tab has focus:
// toasts "no pull request open" when nothing has loaded yet (the PR tab
// can have focus before that, exactly like the diff can — see
// TestDiffCWithNoFileOpenToasts). The description block (the pull
// request's own body) reacts to the pull request itself; an issue-comment
// or review timeline block reacts to that comment/review. Every other
// block (the header, a check, a commit, a non-comment event) is not a
// reactable subject, so this is silently a no-op on one, matching
// editCurrentComment's own off-comment behaviour.
func (a *App) reactToCurrentPRTabBlock() {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	id := a.prView.CurrentID()
	if id == "description" {
		a.openReactionPicker(pr.ID)
		return
	}
	var idx int
	if n, err := fmt.Sscanf(id, "timeline:%d", &idx); n != 1 || err != nil || idx < 0 || idx >= len(pr.Timeline) {
		return
	}
	switch item := pr.Timeline[idx]; item.Kind {
	case model.TimelineKindIssueComment:
		if item.IssueComment != nil {
			a.openReactionPicker(item.IssueComment.ID)
		}
	case model.TimelineKindReview:
		if item.Review != nil {
			a.openReactionPicker(item.Review.ID)
		}
	}
}

// reactToCurrentThreadComment implements "a" while the diff has focus: the
// cursor must be on a thread row (toasting "not on a comment thread"
// otherwise, matching r/x/e/d's own off-thread toast); its comment(s) are
// all reactable regardless of authorship (unlike comment.edit/delete's own
// ViewerCanUpdate/ViewerCanDelete filtering) — none opens the picker
// directly, several show the choice menu threadactions.go's edit/delete
// actions already use to pick one.
func (a *App) reactToCurrentThreadComment() {
	th, ok := a.diffView.CursorThread()
	if !ok {
		a.showToast("not on a comment thread", theme.Warning)
		return
	}
	switch len(th.Comments) {
	case 0:
		a.showToast("no comments to react to", theme.Warning)
	case 1:
		a.openReactionPicker(th.Comments[0].ID)
	default:
		a.showReviewCommentChoiceMenu("React to which comment?", th.Comments, func(c model.ReviewComment) {
			a.openReactionPicker(c.ID)
		})
	}
}

// openReactionPicker opens the picker for subjectID: a no-op toast when no
// pull request is open, ViewerCanReact is false, or another overlay is
// already showing (choice menus/pending list never stack, and neither does
// this).
func (a *App) openReactionPicker(subjectID string) {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	if !pr.ViewerCanReact {
		a.showToast("you cannot react to this pull request", theme.Warning)
		return
	}
	if a.overlay != "" {
		a.showToast("another dialog is already open", theme.Warning)
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "reaction"
	a.reactionSubjectID = subjectID

	a.reactionPickerView = tview.NewList().ShowSecondaryText(false)
	a.reactionPickerView.SetBorder(true).SetTitle(" React ")
	a.rebuildReactionPicker()

	a.root.AddPage("reaction", a.reactionPickerView, true, true)
	a.app.SetFocus(a.reactionPickerView)
}

// closeReactionPicker closes the picker and restores focus.
//
// The guard is a.reactionPickerView == nil, not a.overlay != "reaction": a
// Ctrl-C-while-mutating confirm can be stacked on top (a.overlay ==
// "confirm") when rebuildReactionPicker's own auto-close (its subject can
// no longer be found — an EventPRChanged handler, see its doc comment)
// runs while that confirm is showing, mirroring closeEditForm's own
// a.editForm-based guard. When that happens, a.overlay/focus are left
// alone (the confirm stays the visible, focused overlay) — showConfirm's
// own done func notices via overlayStillOpen("reaction") and falls back
// once it closes.
func (a *App) closeReactionPicker() {
	if a.reactionPickerView == nil {
		return
	}
	a.root.RemovePage("reaction")
	a.reactionPickerView = nil
	a.reactionSubjectID = ""
	if a.overlay != "confirm" {
		a.overlay = ""
		a.restoreFocus()
	}
}

// reactionGroupsForSubject returns subjectID's current ReactionGroups on
// pr, wherever it appears — the pull request itself, an issue comment or a
// review in the conversation timeline, a review in LatestReviews, or a
// review comment in a review thread — mirroring internal/store's own
// (unexported) reactionGroupsFor: the store keeps that one private since
// it also drives mutation decisions there, but the picker needs the same
// read here purely to render current counts/markers, and re-implementing
// this small a read is simpler than exporting store internals for it.
func reactionGroupsForSubject(pr *model.PullRequest, subjectID string) ([]model.ReactionGroup, bool) {
	if pr == nil {
		return nil, false
	}
	if pr.ID == subjectID {
		return pr.ReactionGroups, true
	}
	for _, item := range pr.Timeline {
		switch item.Kind {
		case model.TimelineKindIssueComment:
			if item.IssueComment != nil && item.IssueComment.ID == subjectID {
				return item.IssueComment.ReactionGroups, true
			}
		case model.TimelineKindReview:
			if item.Review != nil && item.Review.ID == subjectID {
				return item.Review.ReactionGroups, true
			}
		}
	}
	for _, r := range pr.LatestReviews {
		if r.ID == subjectID {
			return r.ReactionGroups, true
		}
	}
	for _, t := range pr.ReviewThreads {
		for _, c := range t.Comments {
			if c.ID == subjectID {
				return c.ReactionGroups, true
			}
		}
	}
	return nil, false
}

// rebuildReactionPicker re-renders the picker's eight rows from the
// current pull request's data, preserving the cursor position. Called on
// open and, while open, on every EventPRChanged (see storeevents.go), so a
// toggle's own optimistic apply — or an unrelated background refresh —
// updates counts/markers live. Closes the picker itself (via
// closeReactionPicker) if reactionSubjectID can no longer be found at all
// (its comment was deleted, or a different pull request became current).
func (a *App) rebuildReactionPicker() {
	if a.reactionPickerView == nil {
		return
	}
	groups, found := reactionGroupsForSubject(a.deps.Store.CurrentPR(), a.reactionSubjectID)
	if !found {
		a.closeReactionPicker()
		return
	}

	byContent := make(map[model.ReactionContent]model.ReactionGroup, len(groups))
	for _, g := range groups {
		byContent[g.Content] = g
	}

	cur := a.reactionPickerView.GetCurrentItem()
	a.reactionPickerView.Clear()
	for _, content := range reactionContentOrder {
		g := byContent[content]
		marker := ""
		if g.ViewerHasReacted {
			marker = "  YOU"
		}
		text := fmt.Sprintf("%s  %d%s", content.Emoji(), g.Count, marker)
		a.reactionPickerView.AddItem(text, "", 0, nil)
	}
	if cur >= 0 && cur < a.reactionPickerView.GetItemCount() {
		a.reactionPickerView.SetCurrentItem(cur)
	}
}

// toggleCurrentReaction implements Enter/Space on the picker: refused with
// a toast while Store.Mutating(), matching every other mutation trigger in
// gprt. The picker is left open either way — see the package doc comment.
func (a *App) toggleCurrentReaction() {
	if a.deps.Store.Mutating() {
		a.showToast("a mutation is already in progress; try again shortly", theme.Warning)
		return
	}
	idx := a.reactionPickerView.GetCurrentItem()
	if idx < 0 || idx >= len(reactionContentOrder) {
		return
	}
	a.deps.Store.ToggleReaction(a.reactionSubjectID, reactionContentOrder[idx])
}

// routeReactionKey handles the "reaction" overlay: j/k move, Enter/Space
// toggles, q/Esc close. A close returns immediately instead of continuing
// the loop, for the same reason routeChoiceKey/routePendingKey do (see
// their own doc comments): keys.Normalize can expand one raw event into
// more than one Key (Alt+rune -> [Esc, rune]), and feeding a later one to
// moveListSelection/GetCurrentItem against a *tview.List closeReactionPicker
// already nilled out would panic.
func (a *App) routeReactionKey(_ *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k), isPlainRune(k, 'q'):
			a.closeReactionPicker()
			return nil
		case isEnterKey(k), isPlainRune(k, ' '):
			a.toggleCurrentReaction()
		case isPlainRune(k, 'j'):
			moveListSelection(a.reactionPickerView, 1)
		case isPlainRune(k, 'k'):
			moveListSelection(a.reactionPickerView, -1)
		}
	}
	return nil
}
