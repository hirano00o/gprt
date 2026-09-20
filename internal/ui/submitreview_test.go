// submitreview_test.go covers the "S" (pr.submit) submit-review dialog: a
// choice menu ("Approve"/"Request changes"/"Comment") opening a composer
// for the review's own body, its local empty-body validation, sending
// through Store.SubmitReview (or GitHub.AddReviewNowWithEvent when no
// pending review exists yet), prefilling from an existing pending
// review's body, and refreshing the PR list on success.
package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// TestSubmitReviewChoiceOpensComposerWithTitleAndDraftKey covers each of
// the three choice-menu items opening a composer whose title names the
// chosen event and whose draft key uses the fixed "review" anchor.
func TestSubmitReviewChoiceOpensComposerWithTitleAndDraftKey(t *testing.T) {
	tests := []struct {
		name      string
		itemIndex int
		wantTitle string
	}{
		{"approve", 0, "Approve: review body"},
		{"request changes", 1, "Request changes: review body"},
		{"comment", 2, "Comment: review body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, _, fake, _ := newTestApp(t, nil)
			ref := fixtureRef(1)
			openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

			sendRune(app.app, 'S')
			waitFor(t, app.app, func() bool { return app.overlay == "choice" })
			if got := query(app.app, func() int { return app.choiceMenu.GetItemCount() }); got != 3 {
				t.Fatalf("choice menu item count = %d, want 3", got)
			}

			act(app.app, func() { app.choiceMenu.SetCurrentItem(tc.itemIndex) })
			sendSpecial(app.app, tcell.KeyEnter)
			waitFor(t, app.app, func() bool { return composerOpen(app) })

			if got := query(app.app, func() string { return app.composerTitle.GetText(false) }); got != tc.wantTitle {
				t.Fatalf("composer title = %q, want %q", got, tc.wantTitle)
			}
			wantKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindReview, Anchor: "review"}
			if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantKey {
				t.Fatalf("composer draft key = %+v, want %+v", got, wantKey)
			}
		})
	}
}

// TestSubmitReviewSendCallsAddReviewNowWithEventWhenNoPendingReview covers
// Send going through GitHub.AddReviewNowWithEvent (via Store.SubmitReview)
// with the chosen event and typed body when the pull request has no
// pending review yet.
func TestSubmitReviewSendCallsAddReviewNowWithEventWhenNoPendingReview(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(1) }) // Request changes
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	typeAndSend(app, "please fix this")

	waitFor(t, app.app, func() bool { return len(fake.AddReviewNowWithEventCalls()) == 1 })
	got := fake.AddReviewNowWithEventCalls()[0]
	if got.event != model.ReviewEventRequestChanges || got.body != "please fix this" {
		t.Fatalf("AddReviewNowWithEvent call = %+v, want event=REQUEST_CHANGES body=%q", got, "please fix this")
	}
	if len(fake.SubmitReviewCalls()) != 0 {
		t.Fatal("no pending review exists: SubmitReview must not be called")
	}
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "review submitted" })
}

// TestSubmitReviewSendCallsSubmitReviewWhenPendingReviewExists covers Send
// going through GitHub.SubmitReview (an existing pending review) instead,
// with an empty body allowed for APPROVE.
func TestSubmitReviewSendCallsSubmitReviewWhenPendingReviewExists(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_existing"}
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(0) }) // Approve
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.SubmitReviewCalls()) == 1 })
	got := fake.SubmitReviewCalls()[0]
	if got.reviewID != "PVR_existing" || got.event != model.ReviewEventApprove || got.body != "" {
		t.Fatalf("SubmitReview call = %+v, want reviewID=PVR_existing event=APPROVE body=\"\"", got)
	}
	if len(fake.AddReviewNowWithEventCalls()) != 0 {
		t.Fatal("a pending review already exists: AddReviewNowWithEvent must not be called")
	}
}

// TestSubmitReviewEmptyCommentReviewRefused covers a COMMENT review with a
// blank body and no pending comments being refused locally, leaving the
// composer open with nothing sent.
func TestSubmitReviewEmptyCommentReviewRefused(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(2) }) // Comment
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "needs a body or pending comments") })
	if !composerOpen(app) {
		t.Fatal("a refused empty comment-review send must leave the composer open")
	}
	if len(fake.AddReviewNowWithEventCalls())+len(fake.SubmitReviewCalls()) != 0 {
		t.Fatal("a refused send must not call the store")
	}
}

// TestSubmitReviewEmptyRequestChangesRefused mirrors
// TestSubmitReviewEmptyCommentReviewRefused for REQUEST_CHANGES.
func TestSubmitReviewEmptyRequestChangesRefused(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(1) }) // Request changes
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "needs a body or pending comments") })
	if !composerOpen(app) {
		t.Fatal("a refused empty request-changes send must leave the composer open")
	}
}

// TestSubmitReviewApproveWithEmptyBodyIsAllowed covers APPROVE never
// requiring a body.
func TestSubmitReviewApproveWithEmptyBodyIsAllowed(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(0) }) // Approve
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.AddReviewNowWithEventCalls()) == 1 })
	if got := fake.AddReviewNowWithEventCalls()[0]; got.event != model.ReviewEventApprove || got.body != "" {
		t.Fatalf("AddReviewNowWithEvent call = %+v, want event=APPROVE body=\"\"", got)
	}
}

// TestSubmitReviewEmptyCommentAllowedWithPendingComments covers a COMMENT
// review with a blank body being allowed once pending review comments
// already exist (reviewThreadsPR's t-pending thread has one).
func TestSubmitReviewEmptyCommentAllowedWithPendingComments(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reviewThreadsPR(ref))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(2) }) // Comment
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.AddReviewNowWithEventCalls()) == 1 })
	if got := fake.AddReviewNowWithEventCalls()[0]; got.event != model.ReviewEventComment || got.body != "" {
		t.Fatalf("AddReviewNowWithEvent call = %+v, want event=COMMENT body=\"\"", got)
	}
}

// TestSubmitReviewPrefillsFromPendingReviewBody covers the composer
// prefilling from PendingReview().Body when no draft exists yet.
func TestSubmitReviewPrefillsFromPendingReviewBody(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_existing", Body: "overall looks fine"}
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(2) }) // Comment
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got, want := query(app.app, func() string { return composerText(app) }), "overall looks fine"; got != want {
		t.Fatalf("composer text = %q, want the pending review's own body %q", got, want)
	}
}

// TestSubmitReviewRefreshesListOnSuccess covers a successful submit
// refreshing the PR list exactly once — driven by Store's own
// mutation.refreshList (finishMutation calling Refresh, the same
// non-forced entry point the auto-refresh ticker uses), not additionally
// by the UI itself: composer.go used to also call Store.Refresh() after a
// successful review-body send, which — since finishMutation's own call
// and composer's onMutationChanged handler both run synchronously inside
// the same Event emission — started a second, redundant list generation
// on top of the store's own, doubling every section's SearchPullRequests
// call for no benefit.
func TestSubmitReviewRefreshesListOnSuccess(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))
	sectionCount := query(app.app, func() int { return len(app.deps.Store.Sections()) })
	before := query(app.app, func() int { return fake.SearchCalls() })

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(0) }) // Approve
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.AddReviewNowWithEventCalls()) == 1 })
	waitFor(t, app.app, func() bool { return fake.SearchCalls()-before >= sectionCount })

	// Let a second, unwanted refresh (the regression this test guards
	// against) have time to happen before asserting an exact count: both
	// refreshes, when the bug is present, are triggered synchronously
	// within the same Event emission, so their fetch goroutines start
	// within microseconds of each other — a short, generous settle window
	// is enough to distinguish "one refresh" from "two" reliably against
	// this in-memory fake (no real network latency involved).
	time.Sleep(100 * time.Millisecond)
	if got := fake.SearchCalls() - before; got != sectionCount {
		t.Fatalf("SearchPullRequests calls after a successful submit = %d, want exactly %d (a single list refresh)", got, sectionCount)
	}
}

// TestSubmitReviewChoiceCallbackRefusesWhenTargetPRIsNoLongerCurrent
// covers the choice menu's own deferred callback re-checking
// Store.CurrentRef() (mirroring performReviewSend's own re-check) before
// opening a composer: the callback fires well after the menu was shown, so
// a different pull request may have become current in the meantime. The
// menu itself is not closed by that alone (closeComposerIfWrongPR only
// acts once a composer is actually open, and none is yet here), so it is
// still there to pick from — but doing so must refuse, not open a
// composer for the now-stale ref.
func TestSubmitReviewChoiceCallbackRefusesWhenTargetPRIsNoLongerCurrent(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref1))

	sendRune(app.app, 'S')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: fixtureDetailPR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref2
	})

	act(app.app, func() { app.choiceMenu.SetCurrentItem(0) })
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "different pull request") })
	if composerOpen(app) {
		t.Fatal("choosing an event after the target pull request changed must not open a composer")
	}
}
