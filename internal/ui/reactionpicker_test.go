// reactionpicker_test.go covers the "a" (comment.react) reaction picker:
// opening it for the right subject from the PR tab (PR body, an issue
// comment, a review) or the diff (a thread's own comment, with the
// none/one/several choice-menu pattern threadactions.go's edit/delete
// actions already use), toggling a reaction while it stays open, its own
// j/k/Enter/Space/q/Esc key router, re-rendering on EventPRChanged, closing
// once its subject disappears, and respecting PullRequest.ViewerCanReact.
package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// reactablePR returns fixtureDetailPR(ref) with ViewerCanReact set, so the
// reaction picker's own gate does not block every test in this file that
// is not specifically about that gate.
func reactablePR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.ViewerCanReact = true
	return pr
}

// reactionThreadsPR extends reviewThreadsPR (reviewui_test.go) with
// ViewerCanReact set.
func reactionThreadsPR(ref model.PRRef) model.PullRequest {
	pr := reviewThreadsPR(ref)
	pr.ViewerCanReact = true
	return pr
}

// openFilesTabWithReactionThreads mirrors openFilesTabWithReviewThreads
// (reviewui_test.go) but with a ViewerCanReact-enabled pull request.
func openFilesTabWithReactionThreads(t *testing.T) (*App, *fakeGitHub, model.PRRef) {
	t.Helper()
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: reactionThreadsPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	return app, fake, ref
}

// focusPRBlock moves the PR tab's cursor onto the block with id.
func focusPRBlock(app *App, id string) {
	act(app.app, func() {
		idx := detailSelectableIndex(app.prView.Rows(), id)
		app.prView.SetCursor(idx)
	})
}

// TestReactionPickerOnPRBodyOpensForPRSubject covers "a" on the
// description block opening the picker for the pull request's own ID, with
// all eight reactions listed and a toggle sending AddReaction for the
// chosen content while leaving the picker open.
func TestReactionPickerOnPRBodyOpensForPRSubject(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := reactablePR(ref)
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	if got := query(app.app, func() int { return app.reactionPickerView.GetItemCount() }); got != 8 {
		t.Fatalf("reaction picker item count = %d, want 8", got)
	}
	if got := query(app.app, func() string { return app.reactionSubjectID }); got != pr.ID {
		t.Fatalf("reaction subject = %q, want the pull request's own ID %q", got, pr.ID)
	}

	act(app.app, func() { app.reactionPickerView.SetCurrentItem(0) }) // ThumbsUp, first in reactionContentOrder
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return len(fake.AddReactionCalls()) == 1 })
	if got := fake.AddReactionCalls()[0]; got.subjectID != pr.ID || got.content != model.ReactionThumbsUp {
		t.Fatalf("AddReaction call = %+v, want subjectID=%q content=%s", got, pr.ID, model.ReactionThumbsUp)
	}
	if got := query(app.app, func() string { return app.overlay }); got != "reaction" {
		t.Fatalf("overlay after toggling = %q, want the picker to stay open", got)
	}
}

// TestReactionPickerOnIssueCommentOpensForItsSubject covers "a" on an
// issue-comment timeline block.
func TestReactionPickerOnIssueCommentOpensForItsSubject(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := reactablePR(ref)
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "timeline:0") // dave's issue comment, IC_1

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })
	if got := query(app.app, func() string { return app.reactionSubjectID }); got != "IC_1" {
		t.Fatalf("reaction subject = %q, want %q", got, "IC_1")
	}
}

// TestReactionPickerOnReviewOpensForItsSubject covers "a" on a review
// timeline block.
func TestReactionPickerOnReviewOpensForItsSubject(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := reactablePR(ref)
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "timeline:1") // carol's review, RV_1

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })
	if got := query(app.app, func() string { return app.reactionSubjectID }); got != "RV_1" {
		t.Fatalf("reaction subject = %q, want %q", got, "RV_1")
	}
}

// TestReactionPickerOnHeaderBlockIsANoOp covers "a" on the header block
// (not a reactable subject): no picker, no toast, no panic.
func TestReactionPickerOnHeaderBlockIsANoOp(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "header")

	sendRune(app.app, 'a')
	if got := query(app.app, func() string { return app.overlay }); got != "" {
		t.Fatalf("overlay = %q, want no picker to open on the header block", got)
	}
}

// TestReactionPickerWithNoPullRequestOpenToasts covers "a" on the PR tab
// with focus there but no pull request loaded yet — reached the same way
// TestDiffCWithNoFileOpenToasts (reviewui_test.go) reaches the diff before
// anything is open: Ctrl-w l moves focus into the detail column
// regardless of whether a pull request has been opened.
func TestReactionPickerWithNoPullRequestOpenToasts(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
	if app.overlay == "reaction" {
		t.Fatal("a with no pull request open must not open the picker")
	}
}

// TestReactionPickerViewerCanReactFalseShowsToast covers
// PullRequest.ViewerCanReact gating the picker across every subject kind.
func TestReactionPickerViewerCanReactFalseShowsToast(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref) // ViewerCanReact defaults false
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot react") })
	if app.overlay == "reaction" {
		t.Fatal("ViewerCanReact = false must not open the picker")
	}
}

// TestReactionPickerOnDiffThreadSingleCommentOpensDirectly covers "a" on a
// diff thread row with exactly one comment.
func TestReactionPickerOnDiffThreadSingleCommentOpensDirectly(t *testing.T) {
	app, _, _ := openFilesTabWithReactionThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })
	if got := query(app.app, func() string { return app.reactionSubjectID }); got != "RC_own" {
		t.Fatalf("reaction subject = %q, want %q", got, "RC_own")
	}
}

// TestReactionPickerOnDiffThreadSeveralCommentsShowsChoiceMenu covers "a"
// on a thread with several comments: a choice menu lists every one of
// them (no ownership filtering — RC_other, not the viewer's own, is
// listed too), and choosing one opens the picker for it.
func TestReactionPickerOnDiffThreadSeveralCommentsShowsChoiceMenu(t *testing.T) {
	app, _, _ := openFilesTabWithReactionThreads(t)
	moveDiffCursorToThread(t, app, "t-multi")

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	if got := query(app.app, func() int { return app.choiceMenu.GetItemCount() }); got != 3 {
		t.Fatalf("choice menu item count = %d, want 3 (all comments reactable, no ownership check)", got)
	}

	act(app.app, func() { app.choiceMenu.SetCurrentItem(2) }) // RC_other, "not mine"
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })
	if got := query(app.app, func() string { return app.reactionSubjectID }); got != "RC_other" {
		t.Fatalf("reaction subject = %q, want %q", got, "RC_other")
	}
}

// TestReactionPickerOnDiffOffThreadRowToasts covers "a" off a thread row.
func TestReactionPickerOnDiffOffThreadRowToasts(t *testing.T) {
	app, _, _ := openFilesTabWithReactionThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not on a comment thread") })
	if app.overlay == "reaction" {
		t.Fatal("a off a thread row must not open the picker")
	}
}

// TestReactionPickerEnterCallsRemoveReactionWhenAlreadyReacted covers
// toggling a reaction the viewer has already added.
func TestReactionPickerEnterCallsRemoveReactionWhenAlreadyReacted(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := reactablePR(ref)
	pr.Timeline[0].IssueComment.ReactionGroups = []model.ReactionGroup{
		{Content: model.ReactionThumbsUp, Count: 1, ViewerHasReacted: true},
	}
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "timeline:0")

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	act(app.app, func() { app.reactionPickerView.SetCurrentItem(0) }) // ThumbsUp row
	sendRune(app.app, ' ')
	waitFor(t, app.app, func() bool { return len(fake.RemoveReactionCalls()) == 1 })
	if got := fake.RemoveReactionCalls()[0]; got.subjectID != "IC_1" || got.content != model.ReactionThumbsUp {
		t.Fatalf("RemoveReaction call = %+v, want subjectID=IC_1 content=THUMBS_UP", got)
	}
}

// TestReactionPickerDisabledWhileMutating covers the picker refusing to
// toggle (with a toast, the picker left open) while Store.Mutating().
func TestReactionPickerDisabledWhileMutating(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "already in progress") })
	if len(fake.AddReactionCalls()) != 0 {
		t.Fatal("toggling while mutating must not call AddReaction")
	}
	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestReactionPickerQClosesPicker covers "q" closing the overlay.
func TestReactionPickerQClosesPicker(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestReactionPickerEscClosesPicker covers "Esc" closing the overlay.
func TestReactionPickerEscClosesPicker(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestReactionPickerJKMoveSelection covers the picker's own j/k router.
func TestReactionPickerJKMoveSelection(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	sendRune(app.app, 'j')
	if got := query(app.app, func() int { return app.reactionPickerView.GetCurrentItem() }); got != 1 {
		t.Fatalf("cursor after j = %d, want 1", got)
	}
	sendRune(app.app, 'k')
	if got := query(app.app, func() int { return app.reactionPickerView.GetCurrentItem() }); got != 0 {
		t.Fatalf("cursor after k = %d, want 0", got)
	}
}

// TestReactionPickerRerendersOnEventPRChanged covers the picker's rows
// reflecting an updated reaction count once EventPRChanged fires.
func TestReactionPickerRerendersOnEventPRChanged(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	updated := reactablePR(ref)
	updated.ReactionGroups = []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 5, ViewerHasReacted: true}}
	fake.SetPRResult(ref, gh.DetailResult{PR: updated})
	act(app.app, func() { app.deps.Store.ReloadPR() })

	waitFor(t, app.app, func() bool {
		text, _ := app.reactionPickerView.GetItemText(0)
		return containsSubstring(text, "5")
	})
}

// TestReactionPickerClosesWhenSubjectDisappears covers the picker closing
// itself once its own subject is no longer found on the current pull
// request (for example a background refresh landing without it).
func TestReactionPickerClosesWhenSubjectDisappears(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "timeline:0") // IC_1

	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	updated := reactablePR(ref)
	updated.Timeline = nil
	fake.SetPRResult(ref, gh.DetailResult{PR: updated})
	act(app.app, func() { app.deps.Store.ReloadPR() })

	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestReactionPickerAltKeyDoesNotPanic mirrors
// TestPendingListAltKeyDoesNotPanic (reviewui_test.go) for routeReactionKey.
func TestReactionPickerAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reactablePR(ref))
	focusPRBlock(app, "description")
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool { return app.overlay == "reaction" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}
