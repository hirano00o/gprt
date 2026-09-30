// editform_test.go covers the "E" (pr.edit) edit-PR form: it is refused
// with a toast when no pull request is open or ViewerCanUpdate is false;
// prefills Title/Base branch/Draft from the pull request; Save computes
// the diff against the pull request and enqueues only what changed
// (UpdatePullRequestMeta, then SetReviewers, then SetDraft), toasting
// "nothing to save" when nothing did, and toasting each enqueued step's
// own success/failure — closing only once every step succeeded; Esc asks
// to discard only when the computed diff is non-empty.
package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// editablePR returns fixtureDetailPR(ref) with ViewerCanUpdate true.
func editablePR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.ViewerCanUpdate = true
	pr.Labels = []model.Label{{ID: "L_backend", Name: "backend"}, {ID: "L_urgent", Name: "urgent"}}
	pr.ReviewRequests = []model.Reviewer{{ID: "U_bob", Login: "bob", Kind: model.ReviewerKindUser}}
	return pr
}

func TestOpenEditPRFormRequiresOpenPullRequest(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)
	act(app.app, func() { app.focusDetail() }) // "E" (pr.edit) is bound in ContextPR, not ContextList

	sendRune(app.app, 'E')

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
	if app.overlay != "" {
		t.Fatal("no pull request open: the edit form must not open")
	}
}

func TestOpenEditPRFormRefusedWhenViewerCannotUpdate(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.ViewerCanUpdate = false
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'E')

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot edit") })
	if app.overlay != "" {
		t.Fatal("ViewerCanUpdate false: the edit form must not open")
	}
}

func TestOpenEditPRFormPrefillsTitleBaseDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.Title = "Add widgets"
	pr.BaseRefName = "main"
	pr.IsDraft = true
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	if got := query(app.app, func() string { return app.editFormTitleField.GetText() }); got != "Add widgets" {
		t.Fatalf("title field = %q, want %q", got, "Add widgets")
	}
	if got := query(app.app, func() string { return app.editFormBaseField.GetText() }); got != "main" {
		t.Fatalf("base field = %q, want %q", got, "main")
	}
	if got := query(app.app, func() bool { return app.editFormDraftBox.IsChecked() }); !got {
		t.Fatal("draft checkbox = false, want true (pr.IsDraft)")
	}
}

func TestEditFormSaveWithNoChangesToastsNothingToSave(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return app.statusBar.toast == "nothing to save" })
	if len(fake.UpdatePullRequestCalls()) != 0 || len(fake.RequestReviewersCalls()) != 0 {
		t.Fatal("no changes: nothing should have been enqueued")
	}
	if app.overlay != "editform" {
		t.Fatal("a no-op save must leave the form open")
	}
}

func TestEditFormSaveEnqueuesOnlyChangedTitle(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.editFormTitleField.SetText("New title") })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	call := fake.UpdatePullRequestCalls()[0]
	if call.in.Title == nil || *call.in.Title != "New title" {
		t.Fatalf("UpdatePullRequest Title = %v, want \"New title\"", call.in.Title)
	}
	if call.in.BaseRefName != nil || call.in.LabelIDs != nil {
		t.Fatalf("UpdatePullRequest = %+v, want BaseRefName/LabelIDs left nil (unchanged)", call.in)
	}
	if len(fake.RequestReviewersCalls()) != 0 {
		t.Fatal("reviewers unchanged: RequestReviewers must not be called")
	}
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestEditFormSaveTrimsTitleAndBase is a regression test found in review
// (the create-PR form's own equivalent bug): Title/Base were sent
// verbatim, so accidental leading/trailing whitespace reached GitHub
// unchanged.
func TestEditFormSaveTrimsTitleAndBase(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() {
		app.editFormTitleField.SetText("  New title  ")
		app.editFormBaseField.SetText("  develop  ")
	})

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	call := fake.UpdatePullRequestCalls()[0]
	if call.in.Title == nil || *call.in.Title != "New title" {
		t.Errorf("UpdatePullRequest Title = %v, want trimmed %q", call.in.Title, "New title")
	}
	if call.in.BaseRefName == nil || *call.in.BaseRefName != "develop" {
		t.Errorf("UpdatePullRequest BaseRefName = %v, want trimmed %q", call.in.BaseRefName, "develop")
	}
}

// TestEditFormSaveEnqueuesMetaReviewersDraftInOrder covers title+base+
// labels, reviewers, and draft all changing at once: Save enqueues
// UpdatePullRequestMeta, then SetReviewers, then SetDraft, in that fixed
// order — verified here by each call's own presence rather than
// timestamps, since the mutation queue's FIFO guarantee (docs/DESIGN.md)
// is what actually orders them.
func TestEditFormSaveEnqueuesMetaReviewersDraftInOrder(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.IsDraft = true // so unchecking Draft below is a real change
	openDetailForComposer(t, app, fake, pr)

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() {
		app.editFormTitleField.SetText("New title")
		app.editFormBaseField.SetText("develop")
		app.editFormDraftBox.SetChecked(false) // toggled off
		delete(app.editFormSelectedReviewers, "U_bob")
	})

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool {
		return len(fake.UpdatePullRequestCalls()) == 1 && len(fake.RequestReviewersCalls()) == 1 && len(fake.MarkReadyForReviewCalls()) == 1
	})
	meta := fake.UpdatePullRequestCalls()[0]
	if meta.in.Title == nil || *meta.in.Title != "New title" || meta.in.BaseRefName == nil || *meta.in.BaseRefName != "develop" {
		t.Fatalf("UpdatePullRequest call = %+v, want Title=New title BaseRefName=develop", meta.in)
	}
	reviewers := fake.RequestReviewersCalls()[0]
	if len(reviewers.userIDs) != 0 {
		t.Fatalf("RequestReviewers userIDs = %v, want empty (bob removed)", reviewers.userIDs)
	}
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestEditFormLabelToggleDiffsByID(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	openDetailForComposer(t, app, fake, pr)
	fake.SetLabels(ref.Repo, []model.Label{{ID: "L_backend", Name: "backend"}, {ID: "L_urgent", Name: "urgent"}, {ID: "L_docs", Name: "docs"}})

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	act(app.app, func() { app.openEditLabelsOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editlabels" })
	waitFor(t, app.app, func() bool { return app.editLabelsView.GetItemCount() == 3 })
	// backend (index 0) is already selected: toggling it off, and toggling
	// on docs (index 2), leaves {urgent, docs} selected.
	act(app.app, func() {
		app.editLabelsView.SetCurrentItem(0)
		app.toggleCurrentEditLabel()
		app.editLabelsView.SetCurrentItem(2)
		app.toggleCurrentEditLabel()
	})
	sendSpecial(app.app, tcell.KeyEnter) // confirm the labels overlay
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	got := fake.UpdatePullRequestCalls()[0].in.LabelIDs
	if got == nil {
		t.Fatal("LabelIDs = nil, want the new selection")
	}
	want := map[string]bool{"L_urgent": true, "L_docs": true}
	if len(*got) != len(want) {
		t.Fatalf("LabelIDs = %v, want exactly %v", *got, want)
	}
	for _, id := range *got {
		if !want[id] {
			t.Fatalf("LabelIDs = %v, want exactly %v", *got, want)
		}
	}
}

func TestEditFormEscWithNoChangesClosesImmediately(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	sendSpecial(app.app, tcell.KeyEsc)

	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestEditFormDiscardCancelKeepsFormOnScreen mirrors
// TestCreateFormDiscardCancelKeepsFormOnScreen: Cancel on the discard
// confirm returns to the still-shown edit form, typed changes intact.
func TestEditFormDiscardCancelKeepsFormOnScreen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.editFormTitleField.SetText("changed") })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	sendSpecial(app.app, tcell.KeyEnter) // bare Enter: "Cancel" is the default focus
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })
	if onScreen := query(app.app, func() bool { return app.root.HasPage("editform") }); !onScreen {
		t.Fatal("the edit form's page must still be shown after Cancel")
	}
	if got := query(app.app, func() string { return app.editFormTitleField.GetText() }); got != "changed" {
		t.Errorf("title field after Cancel = %q, want unchanged (%q)", got, "changed")
	}
}

func TestEditFormEscWithChangesShowsDiscardConfirm(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.editFormTitleField.SetText("changed") })

	sendSpecial(app.app, tcell.KeyEsc)

	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("Esc alone must never call the store")
	}
	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Discard"
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	// Regression: showConfirm's own SetDoneFunc reset a.overlay to ""
	// *before* calling onConfirm (closeEditForm), so a guard comparing
	// a.overlay against "editform" (rather than a.editForm itself) made
	// this a silent no-op — the form's own page and every editForm*
	// field were left exactly as they were, discarding nothing at all.
	if stillOpen := query(app.app, func() bool { return app.editForm != nil }); stillOpen {
		t.Fatal("confirming \"Discard\" must actually close the form (editForm == nil)")
	}

	// Reopening after a genuine discard must show the pull request's own,
	// unmodified values — not the typed-but-discarded "changed" title, and
	// not some stale, still-half-open form left behind by the bug above.
	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	if got, want := query(app.app, func() string { return app.editFormTitleField.GetText() }), "Add widget support"; got != want {
		t.Fatalf("title field after reopening post-discard = %q, want the original %q", got, want)
	}
}

// TestEditFormClosesWhenPRSwitchesWhileChildOverlayOpen is a regression
// test for closeEditForm's own guard (see its doc comment): a pull
// request switch while the labels/reviewers child overlay is open over
// the edit form must still tear the whole thing down, not merely leave
// the (now orphaned) form behind because a.overlay reads "editlabels", not
// "editform", at that moment.
func TestEditFormClosesWhenPRSwitchesWhileChildOverlayOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref1))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditLabelsOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editlabels" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: editablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })

	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if editFormStillOpen := query(app.app, func() bool { return app.editForm != nil }); editFormStillOpen {
		t.Fatal("a pull request switch while editlabels was open must still close the edit form (editForm == nil)")
	}
}

func TestEditFormSaveFailureKeepsFormOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))
	fake.SetUpdatePullRequestError(errors.New("boom"))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.editFormTitleField.SetText("New title") })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "boom") })
	if app.overlay != "editform" {
		t.Fatal("a failed save must leave the form open, its values intact")
	}
	if got := query(app.app, func() string { return app.editFormTitleField.GetText() }); got != "New title" {
		t.Fatalf("title field after a failed save = %q, want the typed value kept", got)
	}
}

// TestEditFormBaseBranchAutocompleteDebouncesSearchBranches covers the
// base-branch field's autocomplete: the debounce timer (shortened here,
// mirroring toastDuration's own test-only override) calls
// Store.SearchBranches with the typed text, and the result becomes the
// popup's next set of suggestions.
func TestEditFormBaseBranchAutocompleteDebouncesSearchBranches(t *testing.T) {
	old := editFormBranchDebounce
	editFormBranchDebounce = 10 * time.Millisecond
	t.Cleanup(func() { editFormBranchDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))
	fake.SetBranches("dev", []model.Branch{{Name: "develop"}, {Name: "devel-2"}})

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	// SetText alone never invokes the autocomplete callback (only a real
	// keystroke through InputHandler does — see editFormBranchAutocomplete's
	// own doc comment); Autocomplete() invokes it explicitly with the
	// field's own current text, matching what a keystroke would do.
	act(app.app, func() {
		app.editFormBaseField.SetText("dev")
		app.editFormBaseField.Autocomplete()
	})

	waitFor(t, app.app, func() bool {
		calls := fake.BranchesCalls()
		return len(calls) > 0 && calls[len(calls)-1].query == "dev"
	})
	waitFor(t, app.app, func() bool { return len(app.editFormBranchSuggestions) == 2 })
}

// TestEditFormBaseBranchAutocompleteDoesNotReTriggerItsOwnDebounce is a
// regression test for a real bug (not just test flakiness) a -count=2 run
// caught: editFormBranchAutocomplete's own debounced callback calls
// InputField.Autocomplete() to refresh the popup, but Autocomplete()
// unconditionally re-invokes the very same function — with no guard
// (editFormBranchLastQuery), that recursive call would see the same,
// now-current text and arm *another* debounce timer forever, continuously
// re-fetching the same query. Waiting several debounce intervals past the
// point where "dev" first resolves must not grow its own call count any
// further — counting only "dev"'s own calls, not the form's independent,
// expected one-off autocomplete invocation for the pull request's own
// prefilled base branch when the form first opens (base = "" here,
// specifically to keep that one-off call out of this test's own way).
func TestEditFormBaseBranchAutocompleteDoesNotReTriggerItsOwnDebounce(t *testing.T) {
	old := editFormBranchDebounce
	editFormBranchDebounce = 5 * time.Millisecond
	t.Cleanup(func() { editFormBranchDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.BaseRefName = ""
	openDetailForComposer(t, app, fake, pr)
	fake.SetBranches("dev", []model.Branch{{Name: "develop"}})

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() {
		app.editFormBaseField.SetText("dev")
		app.editFormBaseField.Autocomplete()
	})
	waitFor(t, app.app, func() bool { return len(app.editFormBranchSuggestions) == 1 })

	// Several debounce intervals' worth of real wall-clock time: an
	// unguarded re-trigger loop would have fired many more "dev" calls by
	// now.
	time.Sleep(20 * editFormBranchDebounce)

	devCalls := 0
	for _, c := range query(app.app, func() []branchesCall { return fake.BranchesCalls() }) {
		if c.query == "dev" {
			devCalls++
		}
	}
	if devCalls != 1 {
		t.Fatalf(`"dev" Branches call count = %d, want exactly 1 (no self re-triggering debounce loop)`, devCalls)
	}
}

// TestEditFormAltKeyDoesNotPanic mirrors TestReactionPickerAltKeyDoesNotPanic
// for routeEditFormKey: keys.Normalize expands Alt+rune into [Esc, rune],
// so the router must return immediately once Esc has already cancelled
// the form, rather than feeding the trailing rune to state the cancel
// already tore down.
func TestEditFormAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestEditFormArrowKeysMoveBetweenTitleBaseAndDraft covers rewriteFormNavKey
// (formnav.go) applied by routeEditFormKey: Up/Down move between the
// form's three items exactly like Tab/Backtab already do. Unlike the
// create-PR form's own Repository field, Base's own autocomplete
// drop-down starts closed here (editFormBranchAutocomplete's own
// lastQuery guard short-circuits its very first, eager call — text and
// editFormBranchLastQuery are both "" — to the current, still-nil
// suggestions, matching zero entries), so no dismissal is needed first.
func TestEditFormArrowKeysMoveBetweenTitleBaseAndDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	if item, _ := formFocusIndex(app.app, app.editForm); item != 0 {
		t.Fatalf("focused form item when the edit form opens = %d, want 0 (Title)", item)
	}

	sendSpecial(app.app, tcell.KeyDown) // Title -> Base
	if item, _ := formFocusIndex(app.app, app.editForm); item != 1 {
		t.Fatalf("focused form item after Down from Title = %d, want 1 (Base)", item)
	}

	sendSpecial(app.app, tcell.KeyDown) // Base -> Draft
	if item, _ := formFocusIndex(app.app, app.editForm); item != 2 {
		t.Fatalf("focused form item after Down from Base = %d, want 2 (Draft)", item)
	}

	sendSpecial(app.app, tcell.KeyUp) // Draft -> Base
	if item, _ := formFocusIndex(app.app, app.editForm); item != 1 {
		t.Fatalf("focused form item after Up from Draft = %d, want 1 (Base)", item)
	}

	sendSpecial(app.app, tcell.KeyUp) // Base -> Title
	if item, _ := formFocusIndex(app.app, app.editForm); item != 0 {
		t.Fatalf("focused form item after Up from Base = %d, want 0 (Title)", item)
	}
}

// TestEditLabelsAltKeyDoesNotPanic mirrors TestEditFormAltKeyDoesNotPanic
// for routeEditLabelsKey.
func TestEditLabelsAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))
	fake.SetLabels(ref.Repo, []model.Label{{ID: "L_backend", Name: "backend"}})

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditLabelsOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editlabels" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })
}

// TestMergeDialogAltKeyDoesNotPanic mirrors TestEditFormAltKeyDoesNotPanic
// for routeMergeKey while still loading (mergeLoading), which also treats
// a plain "q" as a close — the branch most exposed to the Alt+rune
// expansion pitfall.
func TestMergeDialogAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	block := make(chan struct{})
	fake.SetRepositoryBlock(block)
	t.Cleanup(func() { close(block) })

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.overlay == "merge" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestEditFormClosesOnPullRequestSwitchWithNoStoreCall covers
// closeEditFormIfWrongPR (EventPRChanged): the form is closed the instant
// a different pull request becomes current, and no store call is made as
// a result of the switch itself.
func TestEditFormClosesOnPullRequestSwitchWithNoStoreCall(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref1))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.editFormTitleField.SetText("changed") })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: editablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })

	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("a pull request switch must not itself call the store")
	}
}

// TestMergeDialogClosesOnPullRequestSwitch covers closeMergeDialogIfWrongPR
// (EventPRChanged), mirroring TestEditFormClosesOnPullRequestSwitchWithNoStoreCall.
func TestMergeDialogClosesOnPullRequestSwitch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref1))
	fake.SetRepositoryInfo(ref1.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: closablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })

	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if len(fake.MergePullRequestCalls()) != 0 {
		t.Fatal("a pull request switch must not itself call the store")
	}
}

// openEditFormBodyComposer opens pr's edit form and then its own body
// composer, waiting for both.
func openEditFormBodyComposer(t *testing.T, app *App, fake *fakeGitHub, pr model.PullRequest) {
	t.Helper()
	openDetailForComposer(t, app, fake, pr)
	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditPRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })
}

// typeIntoComposer appends text in insert mode and returns to normal mode.
func typeIntoComposer(app *App, text string) {
	sendRune(app.app, 'A')
	for _, r := range text {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
}

func TestEditFormBodyComposerRoundTripSavesOnlyBody(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.Body = "old body"
	openEditFormBodyComposer(t, app, fake, pr)

	if got := query(app.app, func() bool { return flexContains(app.editFormBodySlot, app.composerFlex) }); !got {
		t.Fatal("the body composer must be mounted inside the edit form's own body slot")
	}
	if got := query(app.app, func() bool { return flexContains(app.detailColumn, app.composerFlex) }); got {
		t.Fatal("the body composer must not be mounted in the detail column")
	}
	if got := query(app.app, func() string { return composerText(app) }); got != "old body" {
		t.Fatalf("composer prefill = %q, want the pull request's own body", got)
	}

	typeIntoComposer(app, " and more")
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	if got := query(app.app, func() string { return app.editFormBodyView.GetText(true) }); got != "old body and more" {
		t.Errorf("body view text = %q, want %q", got, "old body and more")
	}
	if got := query(app.app, func() string { return app.overlay }); got != "editform" {
		t.Errorf("overlay = %q, want %q (the edit form must be visible again)", got, "editform")
	}
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("sending the body composer must not call the store before Save")
	}

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	call := fake.UpdatePullRequestCalls()[0]
	if call.in.Body == nil || *call.in.Body != "old body and more" {
		t.Fatalf("UpdatePullRequest Body = %v, want \"old body and more\"", call.in.Body)
	}
	if call.in.Title != nil || call.in.BaseRefName != nil || call.in.LabelIDs != nil {
		t.Fatalf("UpdatePullRequest = %+v, want every field but Body left nil (unchanged)", call.in)
	}
}

func TestEditFormBodyComposerCloseWithoutChangesHasNothingToSave(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	pr := editablePR(fixtureRef(1))
	pr.Body = "line one\nline two"
	openEditFormBodyComposer(t, app, fake, pr)

	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return app.statusBar.toast == "nothing to save" })
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("an untouched body must not be sent")
	}
}

func TestSaveEditFormRefusedWhileBodyComposerOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openEditFormBodyComposer(t, app, fake, editablePR(fixtureRef(1)))
	act(app.app, func() { app.editFormTitleField.SetText("changed") })

	act(app.app, func() { app.saveEditForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "finish editing the body") })
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("Save must not fall through while the body composer is open")
	}
	if !composerOpen(app) || app.editForm == nil {
		t.Fatal("Save's refusal must leave both the body composer and the form open")
	}
}

func TestCancelEditFormRefusedWhileBodyComposerOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openEditFormBodyComposer(t, app, fake, editablePR(fixtureRef(1)))

	act(app.app, func() { app.cancelEditForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "finish editing the body") })
	if !composerOpen(app) || app.editForm == nil {
		t.Fatal("Cancel's refusal must leave both the body composer and the form open")
	}
}

func TestEditFormDiscardDeletesBodyDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openEditFormBodyComposer(t, app, fake, editablePR(fixtureRef(1)))
	typeIntoComposer(app, "unsaved")
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	key := query(app.app, func() drafts.Key { return app.editFormBodyDraftKey() })
	if _, ok, _ := app.deps.Drafts.Load(key); !ok {
		t.Fatal(`":q" must keep the body draft while the form is open`)
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	if _, ok, _ := app.deps.Drafts.Load(key); ok {
		t.Error("discarding the edit form must delete its own body draft")
	}
}

func TestEditFormBodyComposerClosesWithFormOnPullRequestSwitch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openEditFormBodyComposer(t, app, fake, editablePR(fixtureRef(1)))

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: editablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })

	waitFor(t, app.app, func() bool { return app.editForm == nil })
	if composerOpen(app) {
		t.Fatal("the edit form's body composer must close together with the form")
	}
	if got := query(app.app, func() string { return app.overlay }); got != "" {
		t.Fatalf("overlay = %q, want \"\"", got)
	}
}

// TestEditFormBodyComposerClosesWhenSaveFinishes covers "Edit body"
// reopened while Save's own steps are still in flight: the form closing
// on their success must take the composer with it, not leave its Flex
// orphaned in a slot no longer on screen.
func TestEditFormBodyComposerClosesWhenSaveFinishes(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, editablePR(fixtureRef(1)))
	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	block := make(chan struct{})
	fake.SetUpdatePullRequestBlock(block)
	act(app.app, func() { app.editFormTitleField.SetText("New title") })
	act(app.app, func() { app.saveEditForm() })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })
	act(app.app, func() { app.openEditPRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	close(block)

	waitFor(t, app.app, func() bool { return app.editForm == nil })
	if composerOpen(app) {
		t.Fatal("the edit form's body composer must close together with the form")
	}
}

// TestCtrlCConfirmOverEditFormBodyComposerSurvivesPullRequestSwitch covers
// a Ctrl-C-while-mutating confirm opened from the edit form's own body
// composer, with a pull request switch closing both the composer and the
// form underneath it: cancelling the confirm must not resurrect the gone
// "editform" overlay.
func TestCtrlCConfirmOverEditFormBodyComposerSurvivesPullRequestSwitch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openEditFormBodyComposer(t, app, fake, editablePR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: editablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool { return app.editForm == nil })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return len(app.confirms) == 0 })

	if got := query(app.app, func() string { return app.overlay }); got != "" {
		t.Fatalf("overlay = %q, want \"\" (the edit form is gone)", got)
	}
	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}
