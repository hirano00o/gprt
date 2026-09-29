// dialogs_test.go covers showConfirm's own shared behavior — a single
// regression test here protects every caller (delete-comment, discard-
// changes, :merge/:close/:reopen, Ctrl-C-while-mutating) at once, rather
// than duplicating the same assertion in each of their own test files.
package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// TestShowConfirmDefaultsFocusToCancelNotConfirmLabel is a regression test
// for a real bug the first M5 review round found: tview's own Form (which
// Modal wraps its two buttons in) defaults focusIndex to 0 — the *first*
// AddButtons argument, confirmLabel, the destructive/executing choice —
// so a bare Enter right after a confirm dialog opens, with no navigation
// at all, must never run onConfirm. Exercised here via the delete-comment
// confirm (any showConfirm caller would do), since showConfirm itself
// carries the fix (modal.SetFocus(1)) all of them share.
func TestShowConfirmDefaultsFocusToCancelNotConfirmLabel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	// A bare Enter, with no navigation, lands on "Cancel" (the default
	// focus): the dialog closes, exactly like actually choosing Cancel
	// would, but onConfirm must never run.
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if len(fake.DeleteCommentIDs()) != 0 {
		t.Fatal("a bare Enter landed on the default \"Cancel\" focus and must not have run onConfirm (DeleteComment)")
	}

	// Re-open it and confirm the counter-case: Tab then Enter reaches the
	// confirm button and does run onConfirm.
	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)
	waitFor(t, app.app, func() bool { return len(fake.DeleteCommentIDs()) == 1 })
}

// TestCtrlCWhileMutatingOverMergeDialogStacksAndQuitStillWorks covers
// showConfirm stacking over the merge dialog: Ctrl-C while a mutation is
// in flight shows the confirm on top of the still-open merge form (its own
// page stays mounted), "Cancel" returns focus to it, and "Quit" still
// quits the app despite the merge dialog being stacked underneath.
func TestCtrlCWhileMutatingOverMergeDialogStacksAndQuitStillWorks(t *testing.T) {
	app, done, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if !query(app.app, func() bool { return app.root.HasPage("merge") }) {
		t.Fatal("the merge dialog's own page must stay mounted underneath the confirm")
	}

	sendSpecial(app.app, tcell.KeyEsc) // "Cancel" is the Modal's default focus
	waitFor(t, app.app, func() bool { return app.overlay == "merge" })
	if !query(app.app, func() bool { return app.mergeForm.HasFocus() }) {
		t.Fatal("focus must return to the merge form after cancelling")
	}

	// "Quit" still quits, even with the merge dialog stacked underneath.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	close(block)
	confirmYes(app.app)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("app did not quit after confirming \"Quit\" with the merge dialog stacked underneath")
	}
}

// TestCtrlCWhileMutatingOverPendingListReturnsOnCancel covers showConfirm
// stacking over the pending list (App.pendingListView, not App.overlay,
// is what closePendingList and the store-event rebuild checks now key on
// — see closePendingList's own doc comment).
func TestCtrlCWhileMutatingOverPendingListReturnsOnCancel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if !query(app.app, func() bool { return app.root.HasPage("pending") }) {
		t.Fatal("the pending list's own page must stay mounted underneath the confirm")
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	if !query(app.app, func() bool { return app.pendingListView.HasFocus() }) {
		t.Fatal("focus must return to the pending list after cancelling")
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestCtrlCWhileMutatingOverThreadListReturnsOnCancel mirrors
// TestCtrlCWhileMutatingOverPendingListReturnsOnCancel for the "t" review-
// threads list.
func TestCtrlCWhileMutatingOverThreadListReturnsOnCancel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if !query(app.app, func() bool { return app.root.HasPage("threads") }) {
		t.Fatal("the thread list's own page must stay mounted underneath the confirm")
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })
	if !query(app.app, func() bool { return app.threadListView.HasFocus() }) {
		t.Fatal("focus must return to the thread list after cancelling")
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestCtrlCWhileMutatingOverEditLabelsReturnsOnCancel covers the
// editlabels overlay stacked over the edit-PR form: Cancel must return
// focus to editlabels itself (not fall through to the form underneath it),
// and the edit form's own page must still be mounted below both.
func TestCtrlCWhileMutatingOverEditLabelsReturnsOnCancel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditLabelsOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editlabels" })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if !query(app.app, func() bool { return app.root.HasPage("editlabels") }) {
		t.Fatal("the editlabels overlay's own page must stay mounted underneath the confirm")
	}
	if !query(app.app, func() bool { return app.root.HasPage("editform") }) {
		t.Fatal("the edit form's own page must still be mounted underneath editlabels")
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "editlabels" })
	if !query(app.app, func() bool { return app.editLabelsView.HasFocus() }) {
		t.Fatal("focus must return to the editlabels overlay, not fall through to the edit form")
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestCtrlCWhileMutatingOverPendingDeleteConfirmStacksThenBothStillWork
// covers Ctrl-C-while-mutating stacking its own "Quit anyway?" confirm on
// top of the pending list's own delete/discard confirm (pendinglist.go's
// showConfirm call, now that showConfirm nests instead of treating an
// already-open confirm as a no-op): Cancel on the quit confirm returns to
// the discard confirm underneath, still focused on the button it had, and
// choosing "Delete" on it afterwards still discards the pending review and
// returns to the pending list.
func TestCtrlCWhileMutatingOverPendingDeleteConfirmStacksThenBothStillWork(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_1"}
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	sendRune(app.app, 'D')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	discardConfirmFocus := query(app.app, func() tview.Primitive { return app.app.GetFocus() })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return len(app.confirms) == 2 })
	if got := query(app.app, func() string { return app.overlay }); got != "confirm" {
		t.Fatalf("overlay = %q, want \"confirm\" while the quit confirm is stacked on top", got)
	}

	sendSpecial(app.app, tcell.KeyEsc) // Cancel the quit confirm
	waitFor(t, app.app, func() bool { return len(app.confirms) == 1 })
	if got := query(app.app, func() tview.Primitive { return app.app.GetFocus() }); got != discardConfirmFocus {
		t.Fatalf("focus after cancelling the quit confirm = %T, want the discard confirm's own Modal back (%T)", got, discardConfirmFocus)
	}

	confirmYes(app.app) // "Delete" on the discard confirm, still intact underneath
	if got := query(app.app, func() string { return app.overlay }); got != "pending" {
		t.Fatalf("overlay = %q after confirming discard, want back on the pending list", got)
	}

	// DiscardPendingReview was only just enqueued behind the still-blocked
	// AddComment mutation (see enqueuePreparedMutation's own doc comment) —
	// it cannot have run yet.
	close(block)
	waitFor(t, app.app, func() bool { return len(fake.DeletePendingReviewCalls()) == 1 })
}

// TestCtrlCWhileMutatingOverPlainConfirmStacksAndQuitStillWorks mirrors
// TestCtrlCWhileMutatingOverPendingDeleteConfirmStacksThenBothStillWork for
// a plain showConfirm caller (the delete-comment confirm) rather than the
// pending list's own: the quit confirm stacks on top the same way, and
// "Quit" still quits despite the delete-comment confirm sitting underneath,
// unresolved.
func TestCtrlCWhileMutatingOverPlainConfirmStacksAndQuitStillWorks(t *testing.T) {
	app, done, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return len(app.confirms) == 2 })

	close(block)
	confirmYes(app.app) // the quit confirm is the top of the stack; "Quit" wins
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("app did not quit after confirming \"Quit\" while stacked over the delete-comment confirm")
	}
}

// TestSecondCtrlCWhileQuitConfirmShownDoesNotStackAnother covers
// showConfirm's own duplicate-message guard: a second Ctrl-C while the
// "Quit anyway?" confirm is already the top of App.confirms must not push
// a second copy of it.
func TestSecondCtrlCWhileQuitConfirmShownDoesNotStackAnother(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if got := query(app.app, func() int { return len(app.confirms) }); got != 1 {
		t.Fatalf("confirms depth = %d after the first Ctrl-C, want 1", got)
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	if got := query(app.app, func() int { return len(app.confirms) }); got != 1 {
		t.Fatalf("confirms depth = %d after a second Ctrl-C while the quit confirm was already on top, want it to stay a no-op (1)", got)
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestConfirmStackedOverMergeDialogStaysOnTopWhenPRSwitchCloses covers a
// store event (EventPRChanged, via closeMergeDialogIfWrongPR) closing a
// stacked overlay out from under the confirm dialog while it is still
// showing: the merge dialog must still actually close (its own page
// removed, its fields cleared) even though a.overlay currently reads
// "confirm" rather than "merge" — and, once the confirm itself closes
// (Cancel), a.overlay must be "" with focus on a real pane, not left
// pointing at the now-gone merge dialog.
func TestConfirmStackedOverMergeDialogStaysOnTopWhenPRSwitchCloses(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref1))
	fake.SetRepositoryInfo(ref1.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: closablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })

	waitFor(t, app.app, func() bool { return app.mergeForm == nil })
	if got := query(app.app, func() string { return app.overlay }); got != "confirm" {
		t.Fatalf("overlay = %q, want the confirm dialog to still be the active overlay while it is showing", got)
	}
	if onScreen := query(app.app, func() bool { return app.root.HasPage("merge") }); onScreen {
		t.Fatal("the merge dialog's own page must have been removed by the PR switch")
	}

	close(block)
	sendSpecial(app.app, tcell.KeyEsc) // Cancel
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	// openDetailForComposer leaves the PR tab focused, and cmdMerge's own
	// savedFocus capture (consumed by showConfirm's own restoreFocus()
	// fallback once it notices the merge dialog is gone) points back to
	// it — see showConfirm's own doc comment for why.
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.prView }); !got {
		t.Fatal("focus must land back on the PR tab (a real pane), not linger on the now-gone merge dialog")
	}
}

// TestConfirmStackedOverPendingListStillAppliesStoreRefresh covers a
// store event that only refreshes a stacked overlay (rebuildPendingList,
// via EventMutationChanged) still applying while the confirm dialog is on
// top of it — not silently skipped merely because a.overlay currently
// reads "confirm" rather than "pending".
func TestConfirmStackedOverPendingListStillAppliesStoreRefresh(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	if got := query(app.app, func() int { return len(app.pendingListEntries) }); got != 0 {
		t.Fatalf("pending list entries = %d, want 0 before any draft exists", got)
	}

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	draftKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:pkg/example.go"}
	if err := app.deps.Drafts.Save(draftKey, "a new thought"); err != nil {
		t.Fatalf("Drafts.Save: %v", err)
	}

	// Resolving the in-flight mutation fires EventMutationChanged, whose
	// handler rebuilds the pending list (storeevents.go) while the confirm
	// dialog is still the active, focused overlay.
	close(block)
	waitFor(t, app.app, func() bool { return len(app.pendingListEntries) == 1 })
	if got := query(app.app, func() string { return app.overlay }); got != "confirm" {
		t.Fatalf("overlay = %q, want the confirm dialog to still be showing while the refresh applied", got)
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
}

// TestConfirmOverComposerStaysOnTopWhenPRSwitchClosesComposer covers the
// composer (a pane, not an overlay) being closed by a PR switch while a
// Ctrl-C confirm opened from it is showing: the confirm keeps focus, and
// Cancel afterwards lands on a real pane rather than the composer's
// now-detached text area.
func TestConfirmOverComposerStaysOnTopWhenPRSwitchClosesComposer(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	modalFocus := query(app.app, func() tview.Primitive { return app.app.GetFocus() })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: fixtureDetailPR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	if got := query(app.app, func() tview.Primitive { return app.app.GetFocus() }); got != modalFocus {
		t.Fatalf("focus after the composer closed under the confirm = %T, want it to stay on the confirm (%T)", got, modalFocus)
	}

	close(block)
	sendSpecial(app.app, tcell.KeyEsc) // Cancel
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.prView }); !got {
		t.Fatal("focus after Cancel must land on the PR tab, not the closed composer")
	}
}

// TestConfirmStackedOverMergeLoadingStaysOnTopWhenFormResolves covers the
// merge dialog replacing its loading placeholder with the real form while
// a Ctrl-C confirm is on top: the confirm stays the focused top page, and
// Cancel then returns to the new form rather than the discarded
// placeholder.
func TestConfirmStackedOverMergeLoadingStaysOnTopWhenFormResolves(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))

	repoBlock := make(chan struct{})
	fake.SetRepositoryBlock(repoBlock)
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})
	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.overlay == "merge" && app.mergeLoading })

	commentBlock := make(chan struct{})
	fake.SetAddCommentBlock(commentBlock)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	modalFocus := query(app.app, func() tview.Primitive { return app.app.GetFocus() })

	close(repoBlock)
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })
	if got := query(app.app, func() string { return app.overlay }); got != "confirm" {
		t.Fatalf("overlay after the merge form resolved = %q, want the confirm to stay active", got)
	}
	if got := query(app.app, func() tview.Primitive { return app.app.GetFocus() }); got != modalFocus {
		t.Fatalf("focus after the merge form resolved = %T, want it to stay on the confirm (%T)", got, modalFocus)
	}
	if front := query(app.app, func() string { name, _ := app.root.GetFrontPage(); return name }); front != "confirm" {
		t.Fatalf("front page = %q, want the confirm drawn on top of the merge form", front)
	}

	close(commentBlock)
	sendSpecial(app.app, tcell.KeyEsc) // Cancel
	waitFor(t, app.app, func() bool { return app.overlay == "merge" })
	if got := query(app.app, func() bool { return app.mergeForm.HasFocus() }); !got {
		t.Fatal("focus after Cancel must be on the merge form, not the discarded loading placeholder")
	}
}
