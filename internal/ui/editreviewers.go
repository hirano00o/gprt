// editreviewers.go implements the "Reviewers" button shared by the edit-PR
// form (editform.go) and the create-PR form (createform.go): a stacked
// overlay (App.overlay == "editreviewers") combining a search InputField
// and a multi-select *tview.List — Tab toggles focus between them, Space
// toggles the row under the cursor, Enter confirms (writing the result
// back to whichever form opened it, and that form's own button text),
// Esc/q cancel. App.reviewersOwner ("editform" or "createform") and
// App.reviewersRepo name which form opened it and which repository to
// search — see openReviewersOverlay's own doc comment.
//
// Candidates come from three sources, merged and deduplicated by ID: every
// currently-selected reviewer (user or team, always shown so it stays
// toggleable regardless of the typed query), reviewersRepo's own
// Store.MentionableUsersOf(reviewersRepo) filtered locally by substring
// against the typed query (each carrying a real GraphQL node ID,
// model.User.ID, selected by internal/gh/queries/mentionable_users.graphql
// — this is what makes adding a brand-new individual user reviewer
// possible here at all), and Store.SearchTeams(reviewersRepo.Owner, query, …)
// results, debounced.
package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
)

// editReviewersSearchDebounce is how long onEditReviewersSearchChanged
// waits after the last keystroke before calling Store.SearchTeams — a
// package var (not a const) so tests can shorten it.
var editReviewersSearchDebounce = 300 * time.Millisecond

// reviewerEntry is one row of the "editreviewers" list: either a
// currently-selected reviewer (removable via Space) or a team from the
// latest search result not yet selected (addable via Space).
type reviewerEntry struct {
	reviewer model.Reviewer
	selected bool
}

// openEditReviewersOverlay opens the "editreviewers" overlay for the
// edit-PR form.
func (a *App) openEditReviewersOverlay() {
	a.openReviewersOverlay("editform", a.editFormRepo, a.editFormSelectedReviewers)
}

// openReviewersOverlay is openEditReviewersOverlay's/openCreateReviewersOverlay's
// (createform.go) shared implementation: a no-op unless owner is the
// overlay currently open (matching the two callers' own single-caller
// guard); repo is whose mentionable users/teams to search
// (App.reviewersRepo, read by editReviewersEntries/rebuildEditReviewersList/
// onEditReviewersSearchChanged below); selected is the caller's own current
// reviewer selection, copied into the overlay's own working set so Esc/q
// can discard it untouched.
func (a *App) openReviewersOverlay(owner string, repo model.RepoRef, selected map[string]model.Reviewer) {
	if a.overlay != owner {
		return
	}
	a.overlay = "editreviewers"
	a.reviewersOwner = owner
	a.reviewersRepo = repo
	a.editReviewersWorking = copyReviewerSet(selected)
	a.editReviewersTeams = nil
	a.deps.Store.EnsureMentionableUsers(repo)

	a.editReviewersSearch = tview.NewInputField().SetLabel("Search: ")
	a.editReviewersSearch.SetChangedFunc(a.onEditReviewersSearchChanged)
	a.editReviewersView = tview.NewList().ShowSecondaryText(false)

	a.editReviewersFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	a.editReviewersFlex.AddItem(a.editReviewersSearch, 1, 0, true)
	a.editReviewersFlex.AddItem(a.editReviewersView, 0, 1, false)
	a.editReviewersFlex.SetBorder(true).SetTitle(" Reviewers (Tab switches focus, Space toggles, Enter confirms) ")

	a.rebuildEditReviewersList()
	a.root.AddPage("editreviewers", a.editReviewersFlex, true, true)
	a.app.SetFocus(a.editReviewersSearch)
}

// editReviewersEntries returns the overlay's current rows: every
// currently-selected reviewer first (in login order, always shown so it
// stays toggleable regardless of the typed search text), then every
// mentionable user matching the typed query not already selected, then
// every team from the latest search result not already selected.
func (a *App) editReviewersEntries() []reviewerEntry {
	var entries []reviewerEntry
	seen := map[string]bool{}

	selected := make([]model.Reviewer, 0, len(a.editReviewersWorking))
	for _, r := range a.editReviewersWorking {
		selected = append(selected, r)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Login < selected[j].Login })
	for _, r := range selected {
		seen[r.ID] = true
		entries = append(entries, reviewerEntry{reviewer: r, selected: true})
	}

	query := ""
	if a.editReviewersSearch != nil {
		query = strings.ToLower(a.editReviewersSearch.GetText())
	}
	for _, u := range a.deps.Store.MentionableUsersOf(a.reviewersRepo) {
		// A mentionable user with no ID (should not normally happen now
		// that mentionable_users.graphql selects one — an accepted,
		// defensive fallback rather than a case this form has a specific
		// story for) cannot be targeted by RequestReviewers at all.
		if u.ID == "" || seen[u.ID] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(u.Login), query) && !strings.Contains(strings.ToLower(u.Name), query) {
			continue
		}
		seen[u.ID] = true
		entries = append(entries, reviewerEntry{reviewer: model.Reviewer{ID: u.ID, Login: u.Login, Kind: model.ReviewerKindUser}})
	}

	for _, tm := range a.editReviewersTeams {
		if seen[tm.ID] {
			continue
		}
		entries = append(entries, reviewerEntry{reviewer: model.Reviewer{ID: tm.ID, Login: tm.Slug, Kind: model.ReviewerKindTeam}})
	}
	return entries
}

// rebuildEditReviewersList re-renders the list from editReviewersEntries,
// preserving the cursor position. A trailing, non-toggleable "(loading
// users…)" row is appended while Store.MentionableUsersOf(reviewersRepo) is
// nil (that repository's own mentionable-users fetch has not resolved yet —
// see MentionableUsersOf's own doc comment for why nil, not merely an empty
// slice, means "not resolved"): toggleCurrentEditReviewer's own bounds
// check against editReviewersEntries (which never includes this marker
// row) already makes Space on it a safe no-op.
func (a *App) rebuildEditReviewersList() {
	cur := a.editReviewersView.GetCurrentItem()
	entries := a.editReviewersEntries()
	loading := a.deps.Store.MentionableUsersOf(a.reviewersRepo) == nil
	a.editReviewersView.Clear()
	if len(entries) == 0 && !loading {
		a.editReviewersView.AddItem("(no matching users or teams)", "", 0, nil)
	}
	for _, e := range entries {
		a.editReviewersView.AddItem(editReviewerItemText(e), "", 0, nil)
	}
	if loading {
		a.editReviewersView.AddItem("(loading users…)", "", 0, nil)
	}
	if cur >= 0 && cur < a.editReviewersView.GetItemCount() {
		a.editReviewersView.SetCurrentItem(cur)
	}
}

// editReviewerItemText renders one row: a "[x]"/"[ ]" marker, the login,
// and its kind (user/team).
func editReviewerItemText(e reviewerEntry) string {
	marker := "[ ]"
	if e.selected {
		marker = "[x]"
	}
	kind := "user"
	if e.reviewer.Kind == model.ReviewerKindTeam {
		kind = "team"
	}
	return marker + " @" + e.reviewer.Login + " (" + kind + ")"
}

// onEditReviewersSearchChanged rebuilds the list immediately for the
// mentionable-users half of the filter (a purely local, synchronous
// operation — no reason to wait for a debounce), then debounces a team
// search by editReviewersSearchDebounce (docs/DESIGN.md's timer-callback
// concurrency rule: a timer callback only ever dispatches into a store
// call), calling Store.SearchTeams(reviewersRepo.Owner, query, …) — safe
// regardless of whether the repository owner is actually an organization:
// Store.SearchTeams' own doc comment confirms a user-owned repository's
// org login simply resolves to an empty, non-error result.
func (a *App) onEditReviewersSearchChanged(query string) {
	a.rebuildEditReviewersList()

	if a.editReviewersSearchTimer != nil {
		a.editReviewersSearchTimer.Stop()
	}
	org := a.reviewersRepo.Owner
	a.editReviewersSearchTimer = time.AfterFunc(editReviewersSearchDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.deps.Store.SearchTeams(org, query, func(teams []model.Team, err error) {
				if err != nil || a.editReviewersView == nil {
					return
				}
				a.editReviewersTeams = teams
				a.rebuildEditReviewersList()
			})
		})
	})
}

// toggleCurrentEditReviewer implements Space: adds the team, or removes
// the reviewer, under the cursor in editReviewersWorking.
func (a *App) toggleCurrentEditReviewer() {
	entries := a.editReviewersEntries()
	idx := a.editReviewersView.GetCurrentItem()
	if idx < 0 || idx >= len(entries) {
		return
	}
	r := entries[idx].reviewer
	if _, ok := a.editReviewersWorking[r.ID]; ok {
		delete(a.editReviewersWorking, r.ID)
	} else {
		a.editReviewersWorking[r.ID] = r
	}
	a.rebuildEditReviewersList()
}

// closeEditReviewersOverlay closes the "editreviewers" overlay, returning
// to whichever form opened it (App.reviewersOwner). apply writes
// editReviewersWorking back to that form's own selected-reviewers field
// (App.editFormSelectedReviewers or App.createFormSelectedReviewers) and
// refreshes its "Reviewers (N selected)" button text (Enter); false
// discards it (Esc/q).
//
// The guard is a.editReviewersView == nil, not a.overlay != "editreviewers",
// mirroring closeEditForm's own a.editForm-based guard: a Ctrl-C-while-
// mutating confirm can be stacked on top (a.overlay == "confirm"), in
// which case a.overlay/focus are left alone below (the confirm stays the
// visible, focused overlay) — showConfirm's own done func notices via
// overlayStillOpen("editreviewers") and falls back once it closes.
func (a *App) closeEditReviewersOverlay(apply bool) {
	if a.editReviewersView == nil {
		return
	}
	if a.editReviewersSearchTimer != nil {
		a.editReviewersSearchTimer.Stop()
		a.editReviewersSearchTimer = nil
	}
	if apply {
		switch a.reviewersOwner {
		case "editform":
			a.editFormSelectedReviewers = a.editReviewersWorking
			if a.editForm != nil {
				a.editForm.GetButton(1).SetLabel(a.editFormReviewersButtonText())
			}
		case "createform":
			a.createFormSelectedReviewers = a.editReviewersWorking
			if a.createForm != nil {
				a.createForm.GetButton(createFormReviewersButtonIndex).SetLabel(a.createFormReviewersButtonText())
			}
		}
	}
	a.editReviewersWorking = nil
	a.editReviewersTeams = nil
	a.editReviewersView = nil
	a.editReviewersSearch = nil
	a.editReviewersFlex = nil
	a.root.RemovePage("editreviewers")
	owner := a.reviewersOwner
	a.reviewersOwner = ""
	if a.overlay == "confirm" {
		return
	}
	a.overlay = owner
	switch owner {
	case "editform":
		if a.editForm != nil {
			a.app.SetFocus(a.editForm)
		}
	case "createform":
		if a.createForm != nil {
			a.app.SetFocus(a.createForm)
		}
	}
}

// routeEditReviewersKey handles the "editreviewers" overlay: while the
// search field has focus, every key is forwarded to it (typing) except
// Tab (moves focus to the list), Esc (cancel), and Enter (confirm); while
// the list has focus, j/k move, Space toggles, Tab moves focus back to
// the search field, Enter confirms, Esc/q cancel.
func (a *App) routeEditReviewersKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	onSearch := a.app.GetFocus() == a.editReviewersSearch
	for _, k := range normalized {
		switch {
		case isEscKey(k):
			a.closeEditReviewersOverlay(false)
			return nil
		case isEnterKey(k):
			a.closeEditReviewersOverlay(true)
			return nil
		case k.Kind == keys.KindSpecial && k.Special == tcell.KeyTab:
			if onSearch {
				a.app.SetFocus(a.editReviewersView)
			} else {
				a.app.SetFocus(a.editReviewersSearch)
			}
			return nil
		case !onSearch && isPlainRune(k, 'q'):
			a.closeEditReviewersOverlay(false)
			return nil
		case !onSearch && isPlainRune(k, 'j'):
			moveListSelection(a.editReviewersView, 1)
		case !onSearch && isPlainRune(k, 'k'):
			moveListSelection(a.editReviewersView, -1)
		case !onSearch && isPlainRune(k, ' '):
			a.toggleCurrentEditReviewer()
		case onSearch:
			return ev
		}
	}
	return nil
}
