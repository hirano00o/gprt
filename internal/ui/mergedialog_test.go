// mergedialog_test.go covers the ":merge" command: an open, loaded pull
// request is required; repository settings are loaded (EnsureRepositoryMetadata)
// with a "loading…" placeholder shown until they resolve (or already
// available immediately); the method chooser is restricted to the
// repository's allowed methods with a single allowed one preselected; the
// commit headline defaults to "<title> (#N)"; Submit confirms then calls
// Store.Merge, refusing locally when the pull request is no longer OPEN.
package ui

import (
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func TestCmdMergeRequiresOpenPullRequest(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendCommand(app, "merge")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
	if app.overlay != "" {
		t.Fatal("no pull request open: the merge dialog must not open")
	}
}

func TestCmdMergeShowsLoadingPlaceholderThenForm(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	openDetailForComposer(t, app, fake, pr)

	// Blocked so the fetch stays "in flight" long enough to observe the
	// loading placeholder before it resolves — otherwise an unblocked fake
	// answering an unarmed RepositoryInfo (the zero value, which allows no
	// merge method at all) can resolve and close the dialog again before
	// the test's poll ever catches the brief "merge" overlay state.
	block := make(chan struct{})
	fake.SetRepositoryBlock(block)
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{
		MergeCommitAllowed: true, SquashMergeAllowed: true, RebaseMergeAllowed: true,
	})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.overlay == "merge" })
	if got := confirmText(app, screen); !containsSubstring(got, "loading repository settings") {
		t.Fatalf("merge dialog text = %q, want a loading placeholder", got)
	}

	close(block)

	waitFor(t, app.app, func() bool { return app.mergeForm != nil })
	if got := confirmText(app, screen); !containsSubstring(got, "Add widget support (#1)") {
		t.Fatalf("merge form headline default missing; screen = %q", got)
	}
}

func TestCmdMergeOpensFormImmediatelyWhenMetadataAlreadyAvailable(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	pr.Title = "Add widgets"
	openDetailForComposer(t, app, fake, pr)

	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})
	act(app.app, func() { app.deps.Store.EnsureRepositoryMetadata(ref.Repo) })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(ref.Repo); return ok })

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })
	if got := query(app.app, func() int { return len(app.mergeMethods) }); got != 1 {
		t.Fatalf("allowed method count = %d, want 1 (only squash allowed)", got)
	}
	if got := query(app.app, func() model.MergeMethod { return app.mergeMethods[0] }); got != model.MergeMethodSquash {
		t.Fatalf("preselected method = %v, want Squash", got)
	}
}

func TestCmdMergeMethodChooserRestrictedToAllowedMethods(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{MergeCommitAllowed: true, RebaseMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	got := query(app.app, func() []model.MergeMethod { return app.mergeMethods })
	want := []model.MergeMethod{model.MergeMethodMerge, model.MergeMethodRebase}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("allowed methods = %v, want %v (squash not allowed)", got, want)
	}
}

func TestCmdMergeNoAllowedMethodsRefuses(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{}) // no method allowed

	sendCommand(app, "merge")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no merge methods are allowed") })
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestCmdMergeLoadingFailureClosesDialog(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	fake.SetRepositoryError(ref.Repo, errors.New("network down"))
	// Blocked so the failure cannot resolve (and close the dialog) before
	// the test observes the "merge" overlay first — the same race
	// TestCmdMergeShowsLoadingPlaceholderThenForm's own block guards
	// against, in the other direction.
	block := make(chan struct{})
	fake.SetRepositoryBlock(block)

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.overlay == "merge" })

	close(block)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "network down") })
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestMergeDialogSubmitConfirmsThenCallsStoreMerge(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	pr.HeadOID = "deadbeef"
	openDetailForComposer(t, app, fake, pr)
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	act(app.app, func() { app.submitMergeForm() })
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if got := confirmText(app, screen); !containsSubstring(got, "Merge #1 with Squash?") {
		t.Fatalf("confirm dialog text = %q, want it to contain %q", got, "Merge #1 with Squash?")
	}
	confirmYes(app.app)

	waitFor(t, app.app, func() bool { return len(fake.MergePullRequestCalls()) == 1 })
	call := fake.MergePullRequestCalls()[0]
	if call.method != model.MergeMethodSquash || call.expectedHeadOID != "deadbeef" {
		t.Fatalf("MergePullRequest call = %+v, want method=SQUASH expectedHeadOID=deadbeef", call)
	}
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "merged" })
}

func TestMergeDialogRefusesSubmitWhenPullRequestNotOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	openDetailForComposer(t, app, fake, pr)
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })

	// The pull request closes (an unrelated background event) while the
	// merge form is still open.
	closedPR := pr
	closedPR.State = model.PRStateClosed
	fake.SetPRResult(ref, gh.DetailResult{PR: closedPR})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.State == model.PRStateClosed
	})

	act(app.app, func() { app.submitMergeForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not open") })
	if len(fake.MergePullRequestCalls()) != 0 {
		t.Fatal("submitting merge on a no-longer-open pull request must not call the store")
	}
}

// TestMergeDialogSummaryLineRefreshesOnPRUpdate covers refreshMergeSummaryIfOpen:
// mergeSummaryLine is computed once when the form opens, but the same pull
// request updating afterward (a background refresh, an unrelated
// mutation's own refetch, …) while the dialog stays open must not leave
// the summary line frozen at its original values.
func TestMergeDialogSummaryLineRefreshesOnPRUpdate(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	pr.MergeStateStatus = model.MergeStateStatusClean
	pr.ReviewDecision = ""
	openDetailForComposer(t, app, fake, pr)
	fake.SetRepositoryInfo(ref.Repo, model.RepositoryInfo{SquashMergeAllowed: true})

	sendCommand(app, "merge")
	waitFor(t, app.app, func() bool { return app.mergeForm != nil })
	if got := query(app.app, func() string { return app.mergeSummaryView.GetText(true) }); !containsSubstring(got, "CLEAN") {
		t.Fatalf("initial summary = %q, want it to mention CLEAN", got)
	}

	updated := pr
	updated.MergeStateStatus = model.MergeStateStatusDirty
	updated.ReviewDecision = model.ReviewDecisionApproved
	fake.SetPRResult(ref, gh.DetailResult{PR: updated})
	act(app.app, func() { app.deps.Store.ReloadPR() })

	// cond runs on the UI goroutine already (waitFor's own QueueUpdate) —
	// reading mergeSummaryView directly here, not through a nested query()
	// call (which would itself QueueUpdate from within a QueueUpdate
	// callback and deadlock the whole event loop).
	waitFor(t, app.app, func() bool { return containsSubstring(app.mergeSummaryView.GetText(true), "DIRTY") })
	if got := query(app.app, func() string { return app.mergeSummaryView.GetText(true) }); !containsSubstring(got, "APPROVED") {
		t.Fatalf("refreshed summary = %q, want it to mention the new review decision APPROVED", got)
	}
}
