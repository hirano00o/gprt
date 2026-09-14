// editreviewers_test.go covers the edit-PR form's "Reviewers" overlay:
// removing a currently-requested reviewer, adding a team found via a
// debounced Store.SearchTeams call, and Esc discarding changes made
// within the overlay only.
package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/model"
)

func TestEditReviewersRemoveExistingReviewer(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref)) // ReviewRequests: [{ID: U_bob, Login: bob}]

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })

	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })
	if got := query(app.app, func() int { return len(app.editReviewersEntries()) }); got != 1 {
		t.Fatalf("entries = %d, want 1 (bob)", got)
	}

	act(app.app, func() {
		app.app.SetFocus(app.editReviewersView)
		app.editReviewersView.SetCurrentItem(0)
		app.toggleCurrentEditReviewer()
	})
	sendSpecial(app.app, tcell.KeyEnter) // confirm
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	if got := query(app.app, func() int { return len(app.editFormSelectedReviewers) }); got != 0 {
		t.Fatalf("selected reviewers = %d, want 0 (bob removed)", got)
	}

	act(app.app, func() { app.saveEditForm() })
	waitFor(t, app.app, func() bool { return len(fake.RequestReviewersCalls()) == 1 })
	call := fake.RequestReviewersCalls()[0]
	if len(call.userIDs) != 0 || len(call.teamIDs) != 0 {
		t.Fatalf("RequestReviewers call = %+v, want no reviewers", call)
	}
}

func TestEditReviewersAddTeamViaDebouncedSearch(t *testing.T) {
	old := editReviewersSearchDebounce
	editReviewersSearchDebounce = 10 * time.Millisecond
	t.Cleanup(func() { editReviewersSearchDebounce = old })

	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))
	fake.SetTeams("plat", []model.Team{{ID: "T_platform", Slug: "platform"}})

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })

	act(app.app, func() { app.editReviewersSearch.SetText("plat") })
	waitFor(t, app.app, func() bool { return len(app.editReviewersTeams) == 1 })

	// The team row (index 1: bob is index 0) is toggled on.
	act(app.app, func() {
		app.app.SetFocus(app.editReviewersView)
		app.editReviewersView.SetCurrentItem(1)
		app.toggleCurrentEditReviewer()
	})
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	act(app.app, func() { app.saveEditForm() })
	waitFor(t, app.app, func() bool { return len(fake.RequestReviewersCalls()) == 1 })
	call := fake.RequestReviewersCalls()[0]
	if len(call.teamIDs) != 1 || call.teamIDs[0] != "T_platform" {
		t.Fatalf("RequestReviewers teamIDs = %v, want [T_platform]", call.teamIDs)
	}
	if len(call.userIDs) != 1 || call.userIDs[0] != "U_bob" {
		t.Fatalf("RequestReviewers userIDs = %v, want [U_bob] (bob untouched)", call.userIDs)
	}
}

// TestEditReviewersAddNewIndividualUser covers the capability
// mentionable_users.graphql's own "id" selection unlocks: a brand-new
// individual user (not already a requested reviewer), found among
// Store.MentionableUsers(), can be added via Space and actually saved —
// unlike a team, this needs no search debounce at all, since the
// candidate list is filtered locally.
func TestEditReviewersAddNewIndividualUser(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetMentionableUsers(ref.Repo, []model.User{{ID: "U_carol", Login: "carol"}})
	openDetailForComposer(t, app, fake, editablePR(ref)) // ReviewRequests: [{ID: U_bob, Login: bob}]
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })

	if got := query(app.app, func() int { return len(app.editReviewersEntries()) }); got != 2 {
		t.Fatalf("entries = %d, want 2 (bob selected, carol addable)", got)
	}

	// carol (index 1: bob, the already-selected reviewer, sorts first) is
	// toggled on.
	act(app.app, func() {
		app.app.SetFocus(app.editReviewersView)
		app.editReviewersView.SetCurrentItem(1)
		app.toggleCurrentEditReviewer()
	})
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	act(app.app, func() { app.saveEditForm() })
	waitFor(t, app.app, func() bool { return len(fake.RequestReviewersCalls()) == 1 })
	call := fake.RequestReviewersCalls()[0]
	if len(call.userIDs) != 2 || !containsString(call.userIDs, "U_bob") || !containsString(call.userIDs, "U_carol") {
		t.Fatalf("RequestReviewers userIDs = %v, want [U_bob U_carol]", call.userIDs)
	}
	if len(call.teamIDs) != 0 {
		t.Fatalf("RequestReviewers teamIDs = %v, want none", call.teamIDs)
	}
}

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestEditReviewersSendsCompleteUserAndTeamSetsWhenMixed covers adding
// both a new individual user and a new team reviewer together, alongside
// an existing user reviewer left untouched: RequestReviewers must receive
// the full, current set for both kinds — union:false replaces each list
// outright, so sending only a partial one would silently clear the other
// (docs/DESIGN.md's mutation-sequence table, "Edit PR" row).
func TestEditReviewersSendsCompleteUserAndTeamSetsWhenMixed(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetMentionableUsers(ref.Repo, []model.User{{ID: "U_carol", Login: "carol"}})
	fake.SetTeams("", []model.Team{{ID: "T_platform", Slug: "platform"}})
	openDetailForComposer(t, app, fake, editablePR(ref)) // ReviewRequests: [{ID: U_bob, Login: bob}]
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })
	// SetChangedFunc's own callback (unlike SetAutocompleteFunc's) is never
	// invoked merely by registering it — an explicit, unfiltered search is
	// triggered here the same way a first keystroke into an empty field
	// followed by backspacing it would.
	act(app.app, func() { app.onEditReviewersSearchChanged("") })
	waitFor(t, app.app, func() bool { return len(app.editReviewersTeams) == 1 })

	// Toggle on carol (a new user) and platform (a new team); bob (an
	// existing reviewer) is left untouched throughout.
	act(app.app, func() {
		app.app.SetFocus(app.editReviewersView)
		entries := app.editReviewersEntries()
		for i, e := range entries {
			if e.reviewer.Login == "carol" || e.reviewer.Login == "platform" {
				app.editReviewersView.SetCurrentItem(i)
				app.toggleCurrentEditReviewer()
			}
		}
	})
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	act(app.app, func() { app.saveEditForm() })
	waitFor(t, app.app, func() bool { return len(fake.RequestReviewersCalls()) == 1 })
	call := fake.RequestReviewersCalls()[0]
	if len(call.userIDs) != 2 || !containsString(call.userIDs, "U_bob") || !containsString(call.userIDs, "U_carol") {
		t.Fatalf("RequestReviewers userIDs = %v, want the complete set [U_bob U_carol]", call.userIDs)
	}
	if len(call.teamIDs) != 1 || call.teamIDs[0] != "T_platform" {
		t.Fatalf("RequestReviewers teamIDs = %v, want [T_platform]", call.teamIDs)
	}
}

func TestEditReviewersEscDiscardsWithinOverlayOnly(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })

	act(app.app, func() {
		app.app.SetFocus(app.editReviewersView)
		app.editReviewersView.SetCurrentItem(0)
		app.toggleCurrentEditReviewer() // removes bob, within this overlay only
	})
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })

	if got := query(app.app, func() int { return len(app.editFormSelectedReviewers) }); got != 1 {
		t.Fatalf("selected reviewers after Esc = %d, want 1 (bob kept; Esc discards the overlay's own change)", got)
	}
}

// TestEditReviewersAltKeyDoesNotPanic mirrors TestEditFormAltKeyDoesNotPanic
// (editform_test.go) for routeEditReviewersKey, with the list (not the
// search field) focused — the branch that calls moveListSelection/
// toggleCurrentEditReviewer against state a leading Esc may have already
// torn down.
func TestEditReviewersAltKeyDoesNotPanic(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })
	act(app.app, func() { app.app.SetFocus(app.editReviewersView) })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "editform" })
}

// lastEditReviewersRowText reads the last row currently rendered in the
// "editreviewers" list — used to check for the trailing "(loading
// users…)" marker. Callers must already be on the UI goroutine (inside a
// waitFor condition) or wrap this in a single, top-level query call.
func lastEditReviewersRowText(app *App) string {
	text, _ := app.editReviewersView.GetItemText(app.editReviewersView.GetItemCount() - 1)
	return text
}

// TestEditReviewersShowsLoadingUntilMentionableUsersResolve covers
// storeevents.go's own EventMentionableChanged handling: opening the
// overlay before the repository's mentionable-users fetch resolves shows
// a trailing "(loading users…)" marker (in addition to bob, already
// selected), and the fetch resolving afterward both adds carol as a
// candidate and rebuilds the list to drop the marker — without this event
// wired up, the overlay would never repaint once the fetch completed after
// it was already open.
func TestEditReviewersShowsLoadingUntilMentionableUsersResolve(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	block := make(chan struct{})
	fake.SetMentionableUsersBlock(block)
	fake.SetMentionableUsers(ref.Repo, []model.User{{ID: "U_carol", Login: "carol"}})
	openDetailForComposer(t, app, fake, editablePR(ref)) // ReviewRequests: [{ID: U_bob, Login: bob}]

	sendRune(app.app, 'E')
	waitFor(t, app.app, func() bool { return app.editForm != nil })
	act(app.app, func() { app.openEditReviewersOverlay() })
	waitFor(t, app.app, func() bool { return app.overlay == "editreviewers" })

	waitFor(t, app.app, func() bool { return containsSubstring(lastEditReviewersRowText(app), "loading") })
	if got := query(app.app, func() int { return len(app.editReviewersEntries()) }); got != 1 {
		t.Fatalf("entries while loading = %d, want 1 (bob only; carol not yet resolved)", got)
	}

	close(block)

	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })
	waitFor(t, app.app, func() bool { return len(app.editReviewersEntries()) == 2 })
	if got := query(app.app, func() string { return lastEditReviewersRowText(app) }); containsSubstring(got, "loading") {
		t.Fatalf("last row after resolving = %q, want the loading marker gone", got)
	}
}
