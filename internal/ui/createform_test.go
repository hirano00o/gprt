// createform_test.go covers the "n" (list.new_pr) create-pull-request
// form: opening it, the repository field's fuzzy autocomplete and its
// "exact owner/name not in the list" acceptance, choosing a repository
// triggering metadata load / base-branch default / template prefill,
// head/base branch autocomplete with stale-answer dropping, local
// validation, the "Edit body" composer's round trip (including its own
// draft persistence), the reused reviewers overlay, submit success/failure
// (including a partial reviewer-request failure), the Esc dirty confirm,
// Alt-key safety, and the form's own independence from the currently open
// pull request.
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

func TestOpenCreatePRFormOpensFromList(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')

	waitFor(t, app.app, func() bool { return app.createForm != nil })
	if app.overlay != "createform" {
		t.Fatalf("overlay = %q, want %q", app.overlay, "createform")
	}
}

// TestCreateFormRepositoryAutocompleteShowsLoadingPlaceholder covers
// createFormRepoAutocomplete's own "(loading repositories…)" placeholder
// row while Store.ViewerRepositories() has never resolved at all (nil, not
// merely empty — see MentionableUsersOf's own doc comment for why this
// convention is used throughout this package).
func TestCreateFormRepositoryAutocompleteShowsLoadingPlaceholder(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	got := query(app.app, func() []string { return app.createFormRepoAutocomplete("") })
	if len(got) != 1 || got[0] != "(loading repositories…)" {
		t.Errorf("createFormRepoAutocomplete(\"\") before any fetch = %v, want [(loading repositories…)]", got)
	}
}

func TestCreateFormRepositoryAutocompleteFuzzyMatch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories([]model.RepositorySummary{
		{Ref: model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}, ID: "R_1", DefaultBranch: "main"},
		{Ref: model.RepoRef{Host: "github.com", Owner: "acme", Name: "gadgets"}, ID: "R_2", DefaultBranch: "main"},
	})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	waitFor(t, app.app, func() bool { return app.deps.Store.ViewerRepositories() != nil })

	got := query(app.app, func() []string { return app.createFormRepoAutocomplete("wdg") })
	if len(got) != 1 || got[0] != "acme/widgets" {
		t.Errorf(`createFormRepoAutocomplete("wdg") = %v, want [acme/widgets]`, got)
	}
}

// TestCreateFormRepositoryExactEntryNotInListIsAccepted covers typing an
// exact "owner/name" that is not among Store.ViewerRepositories(): it is
// still accepted, built against Store.Host() directly.
func TestCreateFormRepositoryExactEntryNotInListIsAccepted(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	act(app.app, func() { app.createFormRepoField.SetText("other/unlisted") })

	if got := query(app.app, func() bool { return app.createFormRepoChosen }); !got {
		t.Fatal("createFormRepoChosen = false, want true for a syntactically valid exact owner/name")
	}
	want := model.RepoRef{Host: "github.com", Owner: "other", Name: "unlisted"}
	if got := query(app.app, func() model.RepoRef { return app.createFormRepo }); got != want {
		t.Errorf("createFormRepo = %+v, want %+v", got, want)
	}
}

// TestCreateFormChoosingRepositoryPrefillsBaseAndBody covers choosing a
// repository triggering EnsureRepositoryMetadata, preselecting the Base
// branch with the resolved default branch, and prefilling the body from
// the repository's first pull request template.
func TestCreateFormChoosingRepositoryPrefillsBaseAndBody(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetTemplates(repo, []model.PullRequestTemplate{{Body: "## Summary\n"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })

	waitFor(t, app.app, func() bool { return app.createFormBaseApplied })
	if got := query(app.app, func() string { return app.createFormBaseField.GetText() }); got != "main" {
		t.Errorf("base field = %q, want %q", got, "main")
	}
	if got := query(app.app, func() string { return app.createFormBody }); got != "## Summary\n" {
		t.Errorf("createFormBody = %q, want the repository's own template body", got)
	}
}

// TestCreateFormHeadAutocompleteDropsStaleAnswer covers Store.SearchBranches'
// own generation-token dropping (docs/DESIGN.md's search.go bullet):
// blocking an earlier ("stale") call's own network response until well
// after a later ("fresh") one has already resolved and applied must never
// let the stale one overwrite it once it is finally released.
func TestCreateFormHeadAutocompleteDropsStaleAnswer(t *testing.T) {
	old := createFormBranchDebounce
	createFormBranchDebounce = 5 * time.Millisecond
	t.Cleanup(func() { createFormBranchDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetBranches("stale", []model.Branch{{Name: "stale-branch"}})
	fake.SetBranches("fresh", []model.Branch{{Name: "fresh-branch"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })

	block := make(chan struct{})
	fake.SetBranchesBlock(block)
	act(app.app, func() {
		app.createFormHeadField.SetText("stale")
		app.createFormHeadField.Autocomplete()
	})
	// Let the debounce fire so the "stale" call actually starts (and blocks).
	time.Sleep(4 * createFormBranchDebounce)

	fake.SetBranchesBlock(nil) // only the *next* call skips the gate
	act(app.app, func() {
		app.createFormHeadField.SetText("fresh")
		app.createFormHeadField.Autocomplete()
	})
	waitFor(t, app.app, func() bool {
		s := app.createFormHeadSuggestions
		return len(s) == 1 && s[0] == "fresh-branch"
	})

	// Release the stale call — Store.SearchBranches' own generation token
	// must have already dropped it before it ever reaches this callback
	// (see applyBranchSearchResult), so this must never overwrite "fresh"'s
	// own, already-applied result.
	close(block)
	time.Sleep(4 * createFormBranchDebounce)
	if got := query(app.app, func() []string { return app.createFormHeadSuggestions }); len(got) != 1 || got[0] != "fresh-branch" {
		t.Errorf("createFormHeadSuggestions after releasing the stale call = %v, want still [fresh-branch]", got)
	}
}

func TestSubmitCreateFormRefusedWithoutRepositoryChosen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormTitleField.SetText("Add widgets") })

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "choose a repository") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("no repository chosen: CreatePullRequest must not be called")
	}
}

func TestSubmitCreateFormRefusedWhileRepositoryMetadataLoading(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	block := make(chan struct{})
	fake.SetRepositoryBlock(block)
	t.Cleanup(func() { close(block) })

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() {
		app.createFormRepoField.SetText("acme/widgets")
		app.createFormHeadField.SetText("feature")
		app.createFormBaseField.SetText("main")
		app.createFormTitleField.SetText("Add widgets")
	})
	waitFor(t, app.app, func() bool { return fake.RepositoryCalls() == 1 })

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not loaded yet") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("metadata not loaded: CreatePullRequest must not be called")
	}
	// The first fetch is still in flight (blocked): EnsureRepositoryMetadata's
	// own loading dedup must keep Submit's retry from starting a duplicate.
	if got := fake.RepositoryCalls(); got != 1 {
		t.Errorf("Repository calls after Submit while a fetch is still in flight = %d, want still 1 (deduped)", got)
	}
}

// TestSubmitCreateFormRetriesRepositoryMetadataAfterFailure covers a
// repository whose EnsureRepositoryMetadata attempt already failed
// outright (not merely still in flight): Submit's own retry
// (EnsureRepositoryMetadata again) must actually start a fresh fetch —
// found missing in review, since neither pressing Create again nor
// re-typing the identical, already-chosen "owner/name" retried it before.
func TestSubmitCreateFormRetriesRepositoryMetadataAfterFailure(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryError(repo, errors.New("boom"))

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() {
		app.createFormRepoField.SetText("acme/widgets") // the first EnsureRepositoryMetadata call, which fails
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
	})
	waitFor(t, app.app, func() bool { return fake.RepositoryCalls() == 1 })

	act(app.app, func() { app.submitCreateForm() })

	// The retry is what matters here, not the "not loaded yet; retrying"
	// toast: the retried fetch fails again immediately (the fake's error
	// is permanent), and its own EventError toast replaces the retrying
	// one before a poll can reliably observe it.
	waitFor(t, app.app, func() bool { return fake.RepositoryCalls() == 2 })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("metadata still not loaded: CreatePullRequest must not be called")
	}
}

func TestSubmitCreateFormRefusedForArchivedRepository(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main", IsArchived: true}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "archived") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("archived repository: CreatePullRequest must not be called")
	}
}

func TestSubmitCreateFormRefusedWhenHeadEqualsBase(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("main") // the base field is already prefilled to "main"
		app.createFormTitleField.SetText("Add widgets")
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "must differ") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("head == base: CreatePullRequest must not be called")
	}
}

func TestSubmitCreateFormRefusedWithBlankTitle(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() { app.createFormHeadField.SetText("feature") })

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "title is required") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("blank title: CreatePullRequest must not be called")
	}
}

// TestCreateFormBodyComposerRoundTrip covers "Edit body": Ctrl-s hands the
// typed text back to the form (App.createFormBody) instead of calling the
// store, and the create-PR form's own page is visible again once the
// composer closes.
func TestCreateFormBodyComposerRoundTrip(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") }) // "Edit body" needs a chosen repository

	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "This PR does X" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	if got := query(app.app, func() string { return app.createFormBody }); got != "This PR does X" {
		t.Errorf("createFormBody = %q, want %q", got, "This PR does X")
	}
	if app.createForm == nil {
		t.Fatal("the create-PR form must still exist after the body composer closes")
	}
	if got := query(app.app, func() string { return app.overlay }); got != "createform" {
		t.Errorf("overlay = %q, want %q (the create form must be visible again)", got, "createform")
	}
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("sending the body composer must never call the store")
	}
}

// TestCreateFormBodyDraftSurvivesCloseAndReopen covers ":q" (unlike ":q!",
// a close that keeps the draft): the typed text is handed back to
// App.createFormBody immediately — found missing in review, since only
// Send used to do this, leaving ":q"'s own kept draft merely *look* saved
// while Create would still submit the old/empty body — and Submit actually
// sends that text as the pull request's own Body. Reopening the composer
// (from its own on-disk draft, independently of createFormBody) also still
// restores the same text.
func TestCreateFormBodyDraftSurvivesCloseAndReopen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: model.PRRef{Repo: repo, Number: 5}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })

	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "keep me" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	if got := query(app.app, func() string { return app.createFormBody }); got != "keep me" {
		t.Fatalf(`createFormBody after ":q" = %q, want %q (":q" must hand the text back, not just keep it on disk)`, got, "keep me")
	}

	// Reopening (from the same on-disk draft) still restores the text too.
	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "keep me" })
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
	})
	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return len(fake.CreatePullRequestCalls()) == 1 })
	if got := fake.CreatePullRequestCalls()[0].in.Body; got != "keep me" {
		t.Errorf(`CreatePullRequest Body = %q, want %q (Create must send text last left via ":q")`, got, "keep me")
	}
}

// TestOpenCreateReviewersOverlayRequiresRepositoryChosen covers the
// "Reviewers" button's own local refusal before any repository has been
// chosen — there would be nothing to search mentionable users/teams
// against.
func TestOpenCreateReviewersOverlayRequiresRepositoryChosen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	act(app.app, func() { app.openCreateReviewersOverlay() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "choose a repository") })
	if app.overlay != "createform" {
		t.Fatal("no repository chosen: the reviewers overlay must not open")
	}
}

// TestCreateFormReviewersOverlaySelectUserWritesBackAndUpdatesButton
// exercises the edit-PR form's reviewers overlay reused by the create-PR
// form end to end (editreviewers.go's owner-aware generalisation): Space
// then Enter writes the selection back to App.createFormSelectedReviewers
// and updates the "Reviewers (N selected)" button text.
func TestCreateFormReviewersOverlaySelectUserWritesBackAndUpdatesButton(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetMentionableUsers(repo, []model.User{{ID: "U_bob", Login: "bob"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })

	act(app.app, func() { app.openCreateReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })
	waitFor(t, app.app, func() bool { return app.deps.Store.MentionableUsersOf(repo) != nil })

	act(app.app, func() {
		app.editReviewersView.SetCurrentItem(0)
		app.toggleCurrentEditReviewer()
	})
	sendSpecial(app.app, tcell.KeyEnter) // confirm

	waitFor(t, app.app, func() bool { return app.overlay == "createform" })
	if ok := query(app.app, func() bool { _, ok := app.createFormSelectedReviewers["U_bob"]; return ok }); !ok {
		t.Fatalf("createFormSelectedReviewers = %+v, want bob selected", app.createFormSelectedReviewers)
	}
	if got := query(app.app, func() string { return app.createForm.GetButton(createFormReviewersButtonIndex).GetLabel() }); got != "Reviewers (1 selected)" {
		t.Errorf("Reviewers button label = %q, want %q", got, "Reviewers (1 selected)")
	}
}

// TestCreateFormSubmitSplitsSelectedReviewersIntoUserAndTeamIDs covers
// Submit calling Store.CreatePullRequest, whose own mutation splits the
// chosen reviewers into user/team IDs (gh.Client.RequestReviewers, union:
// true — its own first reviewers).
func TestCreateFormSubmitSplitsSelectedReviewersIntoUserAndTeamIDs(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: model.PRRef{Repo: repo, Number: 7}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
		app.createFormSelectedReviewers = map[string]model.Reviewer{
			"U_bob":  {ID: "U_bob", Login: "bob", Kind: model.ReviewerKindUser},
			"T_core": {ID: "T_core", Login: "core", Kind: model.ReviewerKindTeam},
		}
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return len(fake.RequestReviewersCalls()) == 1 })
	call := fake.RequestReviewersCalls()[0]
	if len(call.userIDs) != 1 || call.userIDs[0] != "U_bob" {
		t.Errorf("RequestReviewers userIDs = %v, want [U_bob]", call.userIDs)
	}
	if len(call.teamIDs) != 1 || call.teamIDs[0] != "T_core" {
		t.Errorf("RequestReviewers teamIDs = %v, want [T_core]", call.teamIDs)
	}
	if !call.union {
		t.Error("RequestReviewers union = false, want true (a brand-new pull request's very first reviewers)")
	}
}

// TestCreateFormSubmitCallsCreatePullRequestWithFormValues covers Submit
// building gh.CreatePullRequestInput from the form's own current values.
func TestCreateFormSubmitCallsCreatePullRequestWithFormValues(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: model.PRRef{Repo: repo, Number: 3}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
		app.createFormBody = "the body"
		app.createFormDraftBox.SetChecked(true)
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return len(fake.CreatePullRequestCalls()) == 1 })
	got := fake.CreatePullRequestCalls()[0].in
	want := gh.CreatePullRequestInput{
		RepositoryID: "R_1", BaseRefName: "main", HeadRefName: "feature",
		Title: "Add widgets", Body: "the body", Draft: true,
	}
	if got != want {
		t.Errorf("CreatePullRequest input = %+v, want %+v", got, want)
	}
}

// TestCreateFormSubmitSuccessOpensCreatedPRAndToasts covers a successful
// creation: the form closes, its own body draft is deleted, the new pull
// request opens, and a "created #N" toast shows.
func TestCreateFormSubmitSuccessOpensCreatedPRAndToasts(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	newRef := model.PRRef{Repo: repo, Number: 42}
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: newRef})
	fake.SetPRResult(newRef, gh.DetailResult{PR: fixtureDetailPR(newRef)})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
		app.createFormBody = "the body"
	})

	key := app.createFormBodyDraftKey()
	if err := app.deps.Drafts.Save(key, "leftover draft"); err != nil {
		t.Fatalf("seed body draft: %v", err)
	}

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return app.createForm == nil })
	waitFor(t, app.app, func() bool { ref, ok := app.deps.Store.CurrentRef(); return ok && ref == newRef })
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "created #42") })

	if _, ok, _ := app.deps.Drafts.Load(key); ok {
		t.Error("the body draft must be deleted after a successful creation")
	}
}

func TestCreateFormSubmitFailureKeepsFormOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCreatePullRequestError(errors.New("boom"))

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "boom") })
	if app.overlay != "createform" || app.createForm == nil {
		t.Fatal("a failed creation must leave the form open, its values intact")
	}
	if got := query(app.app, func() string { return app.createFormTitleField.GetText() }); got != "Add widgets" {
		t.Errorf("title field after a failed submit = %q, want the typed value kept", got)
	}
}

// TestCreateFormSubmitPartialReviewerFailureToastsCombinedMessage covers a
// pull request that was created successfully but whose follow-up
// RequestReviewers call then failed: the pull request still opens, and the
// toast reports both outcomes together.
func TestCreateFormSubmitPartialReviewerFailureToastsCombinedMessage(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	newRef := model.PRRef{Repo: repo, Number: 9}
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: newRef})
	fake.SetPRResult(newRef, gh.DetailResult{PR: fixtureDetailPR(newRef)})
	fake.SetRequestReviewersError(errors.New("reviewer boom"))

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
		app.createFormSelectedReviewers = map[string]model.Reviewer{"U_bob": {ID: "U_bob", Login: "bob", Kind: model.ReviewerKindUser}}
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return app.createForm == nil })
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "created #9") && containsSubstring(app.statusBar.toast, "reviewer boom")
	})
}

func TestCreateFormEscWithNoChangesClosesImmediately(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	sendSpecial(app.app, tcell.KeyEsc)

	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestCreateFormEscWithChangesDiscardConfirmDefaultsToCancel covers Esc's
// own dirty check and the shared confirm dialog's "Cancel"-by-default focus
// (dialogs.go's showConfirm, SetFocus(1)): a bare Enter right after the
// dialog appears, with no navigation at all, must not discard — the form's
// own data survives untouched.
func TestCreateFormEscWithChangesDiscardConfirmDefaultsToCancel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormTitleField.SetText("changed") })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	sendSpecial(app.app, tcell.KeyEnter) // bare Enter: "Cancel" is the default focus
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if app.createForm == nil {
		t.Fatal("a bare Enter on the discard confirm must default to Cancel, keeping the form's own data")
	}
	if got := query(app.app, func() string { return app.createFormTitleField.GetText() }); got != "changed" {
		t.Errorf("title field after Cancel = %q, want unchanged (%q)", got, "changed")
	}
}

// TestCreateFormEscWithChangesConfirmDiscardClosesForm covers the same
// dialog's other outcome: explicitly navigating to and choosing "Discard"
// actually closes the form.
func TestCreateFormEscWithChangesConfirmDiscardClosesForm(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormTitleField.SetText("changed") })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	confirmYes(app.app) // Tab then Enter reaches "Discard"
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if stillOpen := query(app.app, func() bool { return app.createForm != nil }); stillOpen {
		t.Fatal("confirming Discard must actually close the form")
	}
}

// TestCreateFormAltKeyDoesNotPanic mirrors TestEditFormAltKeyDoesNotPanic
// for routeCreateFormKey.
func TestCreateFormAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestCreateFormStaysOpenAcrossPullRequestSwitch covers the create-PR
// form's own independence from Store.CurrentRef(): opening (or switching)
// the currently open pull request while the form is open must never close
// it, nor touch its values.
func TestCreateFormStaysOpenAcrossPullRequestSwitch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormTitleField.SetText("still typing") })

	act(app.app, func() { app.deps.Store.OpenPR(ref) })
	waitFor(t, app.app, func() bool { cur, ok := app.deps.Store.CurrentRef(); return ok && cur == ref })

	if app.createForm == nil || app.overlay != "createform" {
		t.Fatal("an unrelated pull request switch must not close the create-PR form")
	}
	if got := query(app.app, func() string { return app.createFormTitleField.GetText() }); got != "still typing" {
		t.Errorf("title field after an unrelated PR switch = %q, want unchanged", got)
	}
}

// TestCreatePRBodyComposerStaysOpenAcrossPullRequestSwitch covers
// closeComposerIfWrongPR's own skip for composerKindNewPRBody.
func TestCreatePRBodyComposerStaysOpenAcrossPullRequestSwitch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") }) // "Edit body" needs a chosen repository
	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	act(app.app, func() { app.deps.Store.OpenPR(ref) })
	waitFor(t, app.app, func() bool { cur, ok := app.deps.Store.CurrentRef(); return ok && cur == ref })

	if !composerOpen(app) {
		t.Fatal("an unrelated pull request switch must not close the create-PR form's own body composer")
	}
}

// TestCreatePRBodyComposerCtrlWHDoesNotOrphanIt is a regression test found
// in review: Ctrl-w h (outside insert mode, the composer's own focus
// chord) used to hand focus to focusPrevPane's own target (whatever the
// create-PR form's now-removed root page had left behind), after which a
// bare Ctrl-s fell through to routeCreateFormKey and the outer form's own
// Submit — with the form's own values still intact, Submit would actually
// succeed, close the form via closeCreateForm, and leave the still-open
// body composer's Flex permanently unreachable underneath it. For
// composerKindNewPRBody, Ctrl-w h/l must instead be a no-op.
func TestCreatePRBodyComposerCtrlWHDoesNotOrphanIt(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets") // valid enough that Submit would otherwise succeed
	})
	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'h')
	if !query(app.app, func() bool { return app.composerEditor != nil && app.composerEditor.HasFocus() }) {
		t.Fatal("Ctrl-w h must leave focus on the create-PR form's own body composer")
	}

	// A Ctrl-s from here must reach the composer's own Send, never the
	// outer form's Submit.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("Ctrl-s from the body composer must never call CreatePullRequest")
	}
	if app.createForm == nil {
		t.Fatal("the create-PR form must still be open after the body composer sends")
	}
}

// TestSubmitCreateFormRefusedWhileBodyComposerOpen and
// TestCancelCreateFormRefusedWhileBodyComposerOpen cover the defensive,
// second-layer guard in submitCreateForm/cancelCreateForm themselves —
// independent of the Ctrl-w h/l fix above, in case some other path ever
// again leaves the composer open without focus.
func TestSubmitCreateFormRefusedWhileBodyComposerOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormTitleField.SetText("Add widgets")
	})
	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "finish editing the body") })
	if len(fake.CreatePullRequestCalls()) != 0 {
		t.Fatal("Submit must not fall through while the body composer is open")
	}
	if !composerOpen(app) {
		t.Fatal("Submit's refusal must not close the body composer")
	}
	if app.createForm == nil {
		t.Fatal("Submit's refusal must not close the create-PR form")
	}
}

func TestCancelCreateFormRefusedWhileBodyComposerOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") }) // "Edit body" needs a chosen repository
	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	act(app.app, func() { app.cancelCreateForm() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "finish editing the body") })
	if !composerOpen(app) {
		t.Fatal("Cancel's refusal must not close the body composer")
	}
	if app.createForm == nil {
		t.Fatal("Cancel's refusal must not close the create-PR form")
	}
}

// TestCreateFormSwitchingRepositoryClearsReviewersHeadBaseAndAutocompleteCache
// is a regression test found in review: switching to a genuinely different
// repository used to leave the previous repository's own reviewer
// selection, typed head/base branch names, and autocomplete caches in
// place, silently carrying them into the new repository's submission.
func TestCreateFormSwitchingRepositoryClearsReviewersHeadBaseAndAutocompleteCache(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repoA := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	repoB := model.RepoRef{Host: "github.com", Owner: "acme", Name: "gadgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{
		{Ref: repoA, ID: "R_A", DefaultBranch: "main"},
		{Ref: repoB, ID: "R_B", DefaultBranch: "develop"},
	})
	fake.SetRepositoryInfo(repoA, model.RepositoryInfo{ID: "R_A", Ref: repoA, DefaultBranch: "main"})
	fake.SetRepositoryInfo(repoB, model.RepositoryInfo{ID: "R_B", Ref: repoB, DefaultBranch: "develop"})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repoA); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature-a")
		app.createFormSelectedReviewers = map[string]model.Reviewer{"U_bob": {ID: "U_bob", Login: "bob", Kind: model.ReviewerKindUser}}
		app.createFormHeadSuggestions = []string{"feature-a"}
		app.createFormHeadLastQuery = "feature"
		app.createFormBaseSuggestions = []string{"main"}
		app.createFormBaseLastQuery = "m"
	})

	act(app.app, func() { app.createFormRepoField.SetText("acme/gadgets") })

	if got := query(app.app, func() string { return app.createFormHeadField.GetText() }); got != "" {
		t.Errorf("head field after switching repository = %q, want cleared", got)
	}
	if got := query(app.app, func() string { return app.createFormBaseField.GetText() }); got != "develop" {
		t.Errorf("base field after switching repository = %q, want the new repository's own default branch %q", got, "develop")
	}
	if got := query(app.app, func() int { return len(app.createFormSelectedReviewers) }); got != 0 {
		t.Errorf("createFormSelectedReviewers after switching repository has %d entries, want 0", got)
	}
	if got := query(app.app, func() int { return len(app.createFormHeadSuggestions) }); got != 0 {
		t.Errorf("createFormHeadSuggestions after switching repository has %d entries, want cleared", got)
	}
	if got := query(app.app, func() int { return len(app.createFormBaseSuggestions) }); got != 0 {
		t.Errorf("createFormBaseSuggestions after switching repository has %d entries, want cleared", got)
	}
	if got := query(app.app, func() string { return app.createForm.GetButton(createFormReviewersButtonIndex).GetLabel() }); got != "Reviewers (0 selected)" {
		t.Errorf("Reviewers button label after switching repository = %q, want %q", got, "Reviewers (0 selected)")
	}
}

// TestCreateFormDiscardDeletesBodyDraft is a regression test found in
// review: a discarded create-PR form used to leave its own body draft
// behind on disk forever — the "p" pending list has no way to surface it
// (KindNewPR drafts are scoped by repository, not by an existing pull
// request), so discarding the form is the draft's only cleanup path.
func TestCreateFormDiscardDeletesBodyDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })

	key := app.createFormBodyDraftKey()
	if err := app.deps.Drafts.Save(key, "leftover"); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	act(app.app, func() { app.createFormTitleField.SetText("changed") }) // make it dirty, so Esc asks to confirm

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	if _, ok, _ := app.deps.Drafts.Load(key); ok {
		t.Error("discarding the create-PR form must delete its own body draft")
	}
}

// TestCreateFormSwitchingRepositoryDeletesPreviousRepositoryBodyDraft is a
// regression test found in review: switching the chosen repository cleared
// every other repository-scoped field but left the previous repository's
// own body draft on disk, where nothing (closeCreateForm only deletes the
// currently chosen repository's draft; the "p" list never shows KindNewPR
// drafts) would ever delete it again.
func TestCreateFormSwitchingRepositoryDeletesPreviousRepositoryBodyDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repoA := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	repoB := model.RepoRef{Host: "github.com", Owner: "acme", Name: "gadgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{
		{Ref: repoA, ID: "R_A", DefaultBranch: "main"},
		{Ref: repoB, ID: "R_B", DefaultBranch: "develop"},
	})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })

	keyA := query(app.app, func() drafts.Key { return app.createFormBodyDraftKey() })
	if err := app.deps.Drafts.Save(keyA, "body typed for widgets"); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	act(app.app, func() { app.createFormRepoField.SetText("acme/gadgets") })

	if _, ok, _ := app.deps.Drafts.Load(keyA); ok {
		t.Error("switching repository must delete the previous repository's body draft")
	}
}

// TestOpenCreatePRBodyComposerRequiresRepositoryChosen mirrors the
// reviewers overlay's own gate: the body draft is keyed by repository, so
// opening the composer before one is chosen would file the draft under a
// zero-value key that no later form could find.
func TestOpenCreatePRBodyComposerRequiresRepositoryChosen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	act(app.app, func() { app.openCreatePRBodyComposer() })

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "choose a repository") })
	if query(app.app, func() bool { return app.composerTarget != nil }) {
		t.Fatal("no repository chosen: the body composer must not open")
	}
	if app.overlay != "createform" {
		t.Fatalf("overlay = %q after the refused Edit body, want the form still open", app.overlay)
	}
}

// TestSubmitCreateFormTrimsHeadBaseTitle is a regression test found in
// review: head/base/title were sent verbatim, so accidental leading/
// trailing whitespace (easy to introduce via autocomplete/copy-paste)
// reached GitHub unchanged.
func TestSubmitCreateFormTrimsHeadBaseTitle(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCreatePullRequestResult(model.PullRequest{ID: "PR_new", Ref: model.PRRef{Repo: repo, Number: 1}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("  feature  ")
		app.createFormBaseField.SetText(" main ") // overwrite the auto-prefilled default with whitespace
		app.createFormTitleField.SetText("  Add widgets  ")
	})

	act(app.app, func() { app.submitCreateForm() })

	waitFor(t, app.app, func() bool { return len(fake.CreatePullRequestCalls()) == 1 })
	got := fake.CreatePullRequestCalls()[0].in
	if got.HeadRefName != "feature" || got.BaseRefName != "main" || got.Title != "Add widgets" {
		t.Errorf("CreatePullRequest input = %+v, want head/base/title trimmed", got)
	}
}
