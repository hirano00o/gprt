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
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
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

// TestCreateFormBodyComposerRoundTrip covers "Edit body": it mounts inside
// the create-PR form's own body slot (not a.detailColumn — the composer
// used to live there behind a removed "createform" page, before this test
// was updated for the redesign), Ctrl-s hands the typed text back to the
// form (App.createFormBody) instead of calling the store, the read-only
// body view shows that text once the composer closes, and the create-PR
// form's own page is visible again.
func TestCreateFormBodyComposerRoundTrip(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") }) // "Edit body" needs a chosen repository

	act(app.app, func() { app.openCreatePRBodyComposer() })
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got := query(app.app, func() bool { return flexContains(app.createFormBodySlot, app.composerFlex) }); !got {
		t.Fatal("the body composer must be mounted inside the create-PR form's own body slot")
	}
	if got := query(app.app, func() bool { return flexContains(app.detailColumn, app.composerFlex) }); got {
		t.Fatal("the body composer must not be mounted in the detail column")
	}
	if got := query(app.app, func() bool { return app.root.HasPage("createform") }); !got {
		t.Fatal("the create-PR form's own root page must stay mounted while its body composer is open")
	}

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
	if got := query(app.app, func() string { return app.createFormBodyView.GetText(true) }); got != "This PR does X" {
		t.Errorf("body view text = %q, want %q", got, "This PR does X")
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

// flexContains reports whether item is one of flex's own children.
func flexContains(flex *tview.Flex, item tview.Primitive) bool {
	for i := 0; i < flex.GetItemCount(); i++ {
		if flex.GetItem(i) == item {
			return true
		}
	}
	return false
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

// openCreateFormDismissingRepoAutocomplete opens the create-PR form (like
// sendRune(app.app, 'n') alone) and sends the one Esc needed to close the
// Repository field's own autocomplete drop-down: SetAutocompleteFunc's own
// eager Autocomplete() call (buildCreateForm) already populates it — with
// every viewer repository, or the "(loading repositories…)" placeholder
// while that list is still nil — the moment the field gets focus, before
// anything is even typed (see TestCreateFormRepoAutocompleteEscClosesListNotForm,
// which covers that first Esc's own "close the drop-down, not the form"
// behavior directly). Every test below that sends its own, separate Esc
// afterward needs this first so that one actually reaches
// routeCreateFormKey's cancel path, not the drop-down's own close-only Esc.
func openCreateFormDismissingRepoAutocomplete(t *testing.T, app *App) {
	t.Helper()
	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "createform" })
}

func TestCreateFormEscWithNoChangesClosesImmediately(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	openCreateFormDismissingRepoAutocomplete(t, app)

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

	openCreateFormDismissingRepoAutocomplete(t, app)
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

	openCreateFormDismissingRepoAutocomplete(t, app)
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

	openCreateFormDismissingRepoAutocomplete(t, app)

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

	openCreateFormDismissingRepoAutocomplete(t, app)
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

// TestCreateFormArrowKeysMoveBetweenTitleAndDraft covers rewriteFormNavKey
// (formnav.go) applied by routeCreateFormKey: Up/Down move between form
// items exactly like Tab/Backtab already do, for two plain fields with no
// autocomplete of their own (Title/Draft) — so suggesting is always false
// here and every Up/Down is simply rewritten to Backtab/Tab.
func TestCreateFormArrowKeysMoveBetweenTitleAndDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	openCreateFormDismissingRepoAutocomplete(t, app)
	// Walk to Title via real Tab presses (not Application.SetFocus
	// directly on the field): the latter would move tview's own focus
	// without telling the Form itself, leaving its internal
	// focusedElement — which the very Tab/Backtab this test exercises
	// relies on — stale at the Repository field's own index.
	sendSpecial(app.app, tcell.KeyTab) // repo -> head
	sendSpecial(app.app, tcell.KeyTab) // head -> base
	sendSpecial(app.app, tcell.KeyTab) // base -> title
	if item, _ := formFocusIndex(app.app, app.createForm); item != 3 {
		t.Fatalf("focused form item after walking to Title = %d, want 3", item)
	}

	sendSpecial(app.app, tcell.KeyDown)
	if item, _ := formFocusIndex(app.app, app.createForm); item != 4 {
		t.Fatalf("focused form item after Down from Title = %d, want 4 (Draft)", item)
	}

	sendSpecial(app.app, tcell.KeyUp)
	if item, _ := formFocusIndex(app.app, app.createForm); item != 3 {
		t.Fatalf("focused form item after Up from Draft = %d, want 3 (Title)", item)
	}
}

// TestCreateFormArrowKeysReachButtonsAndBack covers the same rewriting at
// the form's own item/button boundary: Down from the last field (Draft)
// reaches the first button, and Up from a button returns to the field
// above.
func TestCreateFormArrowKeysReachButtonsAndBack(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	openCreateFormDismissingRepoAutocomplete(t, app)
	sendSpecial(app.app, tcell.KeyTab) // repo -> head
	sendSpecial(app.app, tcell.KeyTab) // head -> base
	sendSpecial(app.app, tcell.KeyTab) // base -> title
	sendSpecial(app.app, tcell.KeyTab) // title -> draft
	if item, _ := formFocusIndex(app.app, app.createForm); item != 4 {
		t.Fatalf("focused form item after walking to Draft = %d, want 4", item)
	}

	sendSpecial(app.app, tcell.KeyDown)
	if item, button := formFocusIndex(app.app, app.createForm); item != -1 || button != 0 {
		t.Fatalf("focus after Down from Draft = (item %d, button %d), want (-1, 0) (\"Edit body\")", item, button)
	}

	sendSpecial(app.app, tcell.KeyUp)
	if item, _ := formFocusIndex(app.app, app.createForm); item != 4 {
		t.Fatalf("focused form item after Up from the first button = %d, want 4 (Draft)", item)
	}
}

// TestCreateFormRepoAutocompleteTabNavigatesCandidatesEnterSelects covers
// the other half of rewriteFormNavKey: while the Repository field's own
// autocomplete drop-down is shown, Tab navigates its candidates instead of
// moving focus, and Enter selects the highlighted one — filling the field
// and leaving focus right where it was — after which the drop-down is
// closed, so a following Down moves to the next field like normal.
func TestCreateFormRepoAutocompleteTabNavigatesCandidatesEnterSelects(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories([]model.RepositorySummary{
		{Ref: model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}, ID: "R_1", DefaultBranch: "main"},
		{Ref: model.RepoRef{Host: "github.com", Owner: "acme", Name: "gadgets"}, ID: "R_2", DefaultBranch: "main"},
	})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	waitFor(t, app.app, func() bool { return app.deps.Store.ViewerRepositories() != nil })
	// The drop-down shown when the field first got focus was still built
	// from the "(loading repositories…)" placeholder (ViewerRepositories()
	// was still nil at that point); refresh it now that the real list has
	// resolved, exactly like a real keystroke would (InputField.InputHandler
	// calls Autocomplete() itself whenever the text actually changes).
	act(app.app, func() { app.createFormRepoField.Autocomplete() })

	sendSpecial(app.app, tcell.KeyTab) // navigates the candidate list, not the form
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormRepoField }); !got {
		t.Fatal("focus after Tab with candidates shown must stay on the Repository field")
	}
	if got := query(app.app, func() string { return app.overlay }); got != "createform" {
		t.Fatalf("overlay after Tab with candidates shown = %q, want still %q", got, "createform")
	}
	if got := query(app.app, func() string { return app.createFormRepoField.GetText() }); got != "" {
		t.Fatalf("repo field text after Tab = %q, want the typed query (empty) kept while navigating", got)
	}

	sendSpecial(app.app, tcell.KeyEnter) // selects the highlighted (second) candidate
	if got := query(app.app, func() string { return app.createFormRepoField.GetText() }); got != "acme/gadgets" {
		t.Fatalf("repo field text after Tab, Enter = %q, want the second candidate %q", got, "acme/gadgets")
	}
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormRepoField }); !got {
		t.Fatal("focus after Enter's selection must stay on the Repository field")
	}

	sendSpecial(app.app, tcell.KeyDown) // the drop-down is now closed: Down moves to the next field
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormHeadField }); !got {
		t.Fatal("Down after selecting a candidate must move focus to the Head branch field")
	}
}

// TestCreateFormRepoAutocompleteEscClosesListNotForm covers
// routeCreateFormKey's own "Esc while suggesting" branch directly: the
// Repository field's own autocomplete drop-down is already shown the
// moment the field gets focus (SetAutocompleteFunc's own eager
// Autocomplete() call, buildCreateForm) — even with no viewer repository
// list resolved yet, the "(loading repositories…)" placeholder alone is
// enough — so the very first Esc must only close it, leaving the form
// (and focus) untouched; only a second Esc, now that nothing is shown,
// reaches the real cancel path.
func TestCreateFormRepoAutocompleteEscClosesListNotForm(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetViewerRepositories(nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	sendSpecial(app.app, tcell.KeyEsc)
	if got := query(app.app, func() string { return app.overlay }); got != "createform" {
		t.Fatalf("overlay after Esc closing the suggestion drop-down = %q, want still %q", got, "createform")
	}
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormRepoField }); !got {
		t.Fatal("Esc closing the drop-down must not move focus away from the Repository field")
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestCreateFormPreviewShowsIncompleteHintInitially covers
// scheduleCreateFormPreview's own immediate (non-debounced) placeholder
// while nothing has been chosen yet.
func TestCreateFormPreviewShowsIncompleteHintInitially(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	got := query(app.app, func() string { return app.createFormPreview.GetText(true) })
	if got != createFormPreviewIncompleteHint {
		t.Errorf("preview text = %q, want %q", got, createFormPreviewIncompleteHint)
	}
}

// TestCreateFormPreviewRendersFileHeaderAndPatchLines covers
// scheduleCreateFormPreview's own debounced Store.CompareBranches call,
// fired once the repository, head, and base are all set, and
// renderCompareResult's own file-header/patch-line rendering of its result.
func TestCreateFormPreviewRendersFileHeaderAndPatchLines(t *testing.T) {
	old := createFormPreviewDebounce
	createFormPreviewDebounce = 5 * time.Millisecond
	t.Cleanup(func() { createFormPreviewDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCompareResult(gh.CompareResult{Files: []model.ChangedFile{
		{
			Path: "internal/a.go", Status: model.FileStatusModified,
			Additions: 3, Deletions: 1, HasPatch: true,
			Patch: "@@ -1,3 +1,4 @@\n+foo\n context",
		},
	}})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	// Wait for repository metadata (and its own Base-field prefill) to
	// resolve before typing Head/Base, exactly like the submit-validation
	// tests above do: otherwise its own later, asynchronous
	// applyCreateFormRepositoryInfoIfResolved could still overwrite
	// whatever Base is set to below (createFormBaseApplied is per-repository,
	// not per-field-value), racing scheduleCreateFormPreview's own debounce.
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormBaseField.SetText("main")
	})

	waitFor(t, app.app, func() bool {
		return containsSubstring(app.createFormPreview.GetText(true), "internal/a.go")
	})
	got := query(app.app, func() string { return app.createFormPreview.GetText(true) })
	for _, want := range []string{"internal/a.go", "+3", "-1", "+foo", "context"} {
		if !containsSubstring(got, want) {
			t.Errorf("preview text = %q, want it to contain %q", got, want)
		}
	}
	if calls := fake.CompareCalls(); len(calls) != 1 || calls[0].repo != repo || calls[0].base != "main" || calls[0].head != "feature" {
		t.Errorf("CompareFiles calls = %+v, want exactly one for (repo=%+v, base=main, head=feature)", calls, repo)
	}
}

// TestRenderCompareResult is a pure-function, table-driven test of
// renderCompareResult/compareFileHeader — no *App, no store, no timers.
// Expected "+"/"-"/"@@" colour tags are computed via styleColorTag itself
// (the same helper renderCompareResult uses) rather than hardcoded, so this
// stays correct if theme.Success/Error/Info's own colours ever change.
func TestRenderCompareResult(t *testing.T) {
	addTag := styleColorTag(theme.Success)
	delTag := styleColorTag(theme.Error)

	tests := []struct {
		name string
		res  gh.CompareResult
		want string
	}{
		{
			name: "no files",
			res:  gh.CompareResult{},
			want: "(no differences)",
		},
		{
			name: "binary or too-large file has no patch lines",
			res: gh.CompareResult{Files: []model.ChangedFile{
				{Path: "assets/logo.png", Status: model.FileStatusModified, Additions: 2, Deletions: 1, HasPatch: false},
			}},
			want: fmt.Sprintf("assets/logo.png  [%s]+2[-] [%s]-1[-]\n(binary or too large to display)\n", addTag, delTag),
		},
		{
			name: "renamed file spells out both paths",
			res: gh.CompareResult{Files: []model.ChangedFile{
				{PreviousPath: "old.go", Path: "new.go", Status: model.FileStatusRenamed, Additions: 1, Deletions: 1, HasPatch: false},
			}},
			want: fmt.Sprintf("old.go -> new.go (renamed)  [%s]+1[-] [%s]-1[-]\n(binary or too large to display)\n", addTag, delTag),
		},
		{
			name: "removed file is marked as such",
			res: gh.CompareResult{Files: []model.ChangedFile{
				{Path: "deleted.go", Status: model.FileStatusRemoved, Additions: 0, Deletions: 9, HasPatch: false},
			}},
			want: fmt.Sprintf("deleted.go (removed)  [%s]+0[-] [%s]-9[-]\n(binary or too large to display)\n", addTag, delTag),
		},
		{
			name: "truncated note is appended after every file's own patch",
			res: gh.CompareResult{
				Files:     []model.ChangedFile{{Path: "a.go", Status: model.FileStatusModified, Additions: 1, HasPatch: false}},
				Truncated: true,
			},
			want: fmt.Sprintf(
				"a.go  [%s]+1[-] [%s]-0[-]\n(binary or too large to display)\n\n(more files changed than shown; GitHub's own compare endpoint truncated this response)\n",
				addTag, delTag,
			),
		},
		{
			name: "a context patch line containing a literal '[red]' is escaped, not left as a real colour tag",
			res: gh.CompareResult{Files: []model.ChangedFile{
				{Path: "a.go", Status: model.FileStatusModified, HasPatch: true, Patch: "unchanged [red] middle"},
			}},
			want: fmt.Sprintf("a.go  [%s]+0[-] [%s]-0[-]\nunchanged [red[] middle\n", addTag, delTag),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderCompareResult(tc.res); got != tc.want {
				t.Errorf("renderCompareResult() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreateFormPreviewShowsErrorOnFailure covers applyCreateFormPreview's
// own error rendering.
func TestCreateFormPreviewShowsErrorOnFailure(t *testing.T) {
	old := createFormPreviewDebounce
	createFormPreviewDebounce = 5 * time.Millisecond
	t.Cleanup(func() { createFormPreviewDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	repo := model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}
	fake.SetViewerRepositories([]model.RepositorySummary{{Ref: repo, ID: "R_1", DefaultBranch: "main"}})
	fake.SetRepositoryInfo(repo, model.RepositoryInfo{ID: "R_1", Ref: repo, DefaultBranch: "main"})
	fake.SetCompareError(errors.New("boom"))

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormRepoField.SetText("acme/widgets") })
	waitFor(t, app.app, func() bool { _, ok := app.deps.Store.RepositoryInfo(repo); return ok })
	act(app.app, func() {
		app.createFormHeadField.SetText("feature")
		app.createFormBaseField.SetText("main")
	})

	waitFor(t, app.app, func() bool {
		return containsSubstring(app.createFormPreview.GetText(true), "boom")
	})
}

// TestCreateFormCtrlWLFocusesPreviewHReturnsToForm covers Ctrl-w l/h moving
// focus between the form and its own diff preview.
func TestCreateFormCtrlWLFocusesPreviewHReturnsToForm(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'l')
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormPreview }); !got {
		t.Fatal("Ctrl-w l must focus the diff preview")
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'h')
	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormRepoField }); !got {
		t.Fatal("Ctrl-w h must return focus to the form")
	}
}

// TestCreateFormEscOnPreviewReturnsFocusInsteadOfCancelling covers Esc's
// different meaning while the preview has focus: it must return focus to
// the form instead of triggering cancelCreateForm's own discard confirm.
func TestCreateFormEscOnPreviewReturnsFocusInsteadOfCancelling(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.createForm != nil })
	act(app.app, func() { app.createFormTitleField.SetText("dirty") }) // would otherwise trigger a discard confirm

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.createFormPreview })

	sendSpecial(app.app, tcell.KeyEsc)

	if got := query(app.app, func() bool { return app.app.GetFocus() == app.createFormRepoField }); !got {
		t.Fatal("Esc on the preview must return focus to the form, not cancel it")
	}
	if app.createForm == nil {
		t.Fatal("Esc on the preview must not close the create-PR form")
	}
}
