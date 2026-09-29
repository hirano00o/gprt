// editform.go implements the "E" (pr.edit) edit-PR form: title, base
// branch (autocompleted via Store.SearchBranches, debounced), labels and
// reviewers (each a stacked multi-select overlay — editlabels.go/
// editreviewers.go), and Draft. Esc asks to discard changes only when the
// live diff against the pull request (editFormComputeDiff, also used by
// Save) is non-empty; Ctrl-s computes that same diff and enqueues only
// what changed, in order: UpdatePullRequestMeta (title/base/labels), then
// SetReviewers, then SetDraft — "nothing to save" when none of them did.
// Each enqueued mutation is tracked (editFormSteps) for its own success/
// failure toast; the form stays open until every step finishes, closing
// only once none of them failed (a failure leaves the form's values
// intact, reopenable via another Save).
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// editFormBranchDebounce is how long editFormBranchAutocomplete waits
// after the last keystroke before calling Store.SearchBranches — a package
// var (not a const) so tests can shorten it, mirroring toastDuration's own
// test-only override.
var editFormBranchDebounce = 300 * time.Millisecond

// editFormSnapshot is the pull request's own field values at the moment
// the edit form opened, diffed against the live field values on demand
// (editFormComputeDiff) by both Save and Esc's own dirty check — no
// separate "dirty" boolean is tracked.
type editFormSnapshot struct {
	title       string
	base        string
	labelIDs    map[string]bool
	reviewerIDs map[string]bool
	draft       bool
}

// editFormSnapshotFrom builds pr's own editFormSnapshot. Labels/reviewers
// with no ID (see model.Label.ID/model.Reviewer.ID's own doc comments —
// empty for one this milestone's data cannot target) are excluded: they
// could never be sent back in an UpdatePullRequestMeta/SetReviewers call
// anyway, so including them in the diff base would make an otherwise
// unrelated Save look like it needs to "remove" them.
func editFormSnapshotFrom(pr *model.PullRequest) editFormSnapshot {
	labelIDs := make(map[string]bool, len(pr.Labels))
	for _, l := range pr.Labels {
		if l.ID != "" {
			labelIDs[l.ID] = true
		}
	}
	return editFormSnapshot{
		title: pr.Title, base: pr.BaseRefName, labelIDs: labelIDs,
		reviewerIDs: reviewerIDSet(pr.ReviewRequests), draft: pr.IsDraft,
	}
}

// reviewerIDSet returns the ID set of reviewers that actually have one.
func reviewerIDSet(reviewers []model.Reviewer) map[string]bool {
	ids := make(map[string]bool, len(reviewers))
	for _, r := range reviewers {
		if r.ID != "" {
			ids[r.ID] = true
		}
	}
	return ids
}

// reviewersByID indexes reviewers with a non-empty ID by that ID — the
// edit form's own working selection (App.editFormSelectedReviewers) is
// keyed this way so a Space toggle in editreviewers.go's overlay is a
// simple map add/delete.
func reviewersByID(reviewers []model.Reviewer) map[string]model.Reviewer {
	out := make(map[string]model.Reviewer, len(reviewers))
	for _, r := range reviewers {
		if r.ID != "" {
			out[r.ID] = r
		}
	}
	return out
}

// copyBoolSet returns a shallow copy of m, so a stacked overlay (editlabels.go)
// can mutate its own working copy without touching the form's own
// selection until it is explicitly confirmed.
func copyBoolSet(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// copyReviewerSet mirrors copyBoolSet for editreviewers.go's own working
// copy.
func copyReviewerSet(m map[string]model.Reviewer) map[string]model.Reviewer {
	out := make(map[string]model.Reviewer, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// equalStringSet reports whether a and b hold the same set of keys.
func equalStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// sortedKeys returns m's keys in ascending order — used to build a
// deterministic LabelIDs slice for UpdatePullRequestInput (map iteration
// order is not).
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// editFormDiff is editFormComputeDiff's result: which of the three
// mutations Save would enqueue, and with what arguments.
type editFormDiff struct {
	metaChanged      bool
	meta             gh.UpdatePullRequestInput
	reviewersChanged bool
	userIDs, teamIDs []string
	draftChanged     bool
	draft            bool
}

// empty reports whether the diff has nothing to enqueue at all.
func (d editFormDiff) empty() bool {
	return !d.metaChanged && !d.reviewersChanged && !d.draftChanged
}

// editFormStep is one of Save's own in-flight mutation steps (see
// App.editFormSteps' own doc comment for why a single pendingMutation slot
// is not enough for a batch).
type editFormStep struct {
	successVerb string
}

// isCtrlS reports whether k is Ctrl-s (Save).
func isCtrlS(k keys.Key) bool {
	return k.Kind == keys.KindRune && k.Rune == 's' && k.Mod == tcell.ModCtrl
}

// openEditPRForm implements "E" (pr.edit): refused with a toast when no
// pull request is open, ViewerCanUpdate is false, or another overlay is
// already open (matching openReactionPicker's own rule). Loads the
// repository's metadata (EnsureRepositoryMetadata) so the labels overlay
// has something to show once opened.
func (a *App) openEditPRForm() {
	if a.overlay != "" {
		return
	}
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	if !pr.ViewerCanUpdate {
		a.showToast("you cannot edit this pull request", theme.Warning)
		return
	}
	ref, _ := a.deps.Store.CurrentRef()
	a.editFormRef = ref
	a.editFormRepo = ref.Repo
	a.deps.Store.EnsureRepositoryMetadata(ref.Repo)

	a.editFormOriginal = editFormSnapshotFrom(pr)
	a.editFormSelectedLabelIDs = copyBoolSet(a.editFormOriginal.labelIDs)
	a.editFormSelectedReviewers = reviewersByID(pr.ReviewRequests)

	a.buildEditForm(pr)
}

// buildEditForm constructs and shows the *tview.Form itself.
func (a *App) buildEditForm(pr *model.PullRequest) {
	a.editFormReturnFocus = a.app.GetFocus()
	a.overlay = "editform"

	title := tview.NewInputField().SetLabel("Title").SetText(pr.Title)
	base := tview.NewInputField().SetLabel("Base branch").SetText(pr.BaseRefName)
	wireFormAutocomplete(base, &a.editFormBaseSuggesting, a.editFormBranchAutocomplete)
	draft := tview.NewCheckbox().SetLabel("Draft").SetChecked(pr.IsDraft)

	form := tview.NewForm()
	form.AddFormItem(title)
	form.AddFormItem(base)
	form.AddFormItem(draft)
	form.AddButton(a.editFormLabelsButtonText(), a.openEditLabelsOverlay)
	form.AddButton(a.editFormReviewersButtonText(), a.openEditReviewersOverlay)
	form.AddButton("Save", a.saveEditForm)
	form.AddButton("Cancel", a.cancelEditForm)
	form.SetBorder(true).SetTitle(fmt.Sprintf(" Edit #%d ", pr.Ref.Number))

	a.editForm = form
	a.editFormTitleField = title
	a.editFormBaseField = base
	a.editFormDraftBox = draft

	a.root.AddPage("editform", form, true, true)
	a.app.SetFocus(form)
}

// editFormLabelsButtonText/editFormReviewersButtonText render the two
// buttons' own "(N selected)" labels from the form's current selection —
// recomputed by editlabels.go/editreviewers.go on confirm, via
// a.editForm.GetButton(0)/GetButton(1) (fixed indices: these two buttons
// are always added first, in this order, in buildEditForm).
func (a *App) editFormLabelsButtonText() string {
	return fmt.Sprintf("Labels (%d selected)", len(a.editFormSelectedLabelIDs))
}

func (a *App) editFormReviewersButtonText() string {
	return fmt.Sprintf("Reviewers (%d selected)", len(a.editFormSelectedReviewers))
}

// editFormBranchAutocomplete is editFormBaseField's own SetAutocompleteFunc
// callback: it returns editFormBranchSuggestions synchronously (whatever
// the last resolved search returned) and (re)starts a debounce timer for
// text, which — after editFormBranchDebounce — dispatches a
// Store.SearchBranches call (docs/DESIGN.md's timer-callback concurrency
// rule: a timer callback only ever dispatches into a store call). Its own
// callback, delivered already on the UI goroutine, updates
// editFormBranchSuggestions/editFormBranchLastQuery and re-invokes
// InputField.Autocomplete() to refresh the popup with the new results.
//
// editFormBranchLastQuery guards against that very re-invocation:
// InputField.Autocomplete() unconditionally calls this same function again
// (it is the registered SetAutocompleteFunc callback), and with no such
// guard that recursive call would see the same, now-current text and arm
// *another* debounce timer, forever re-fetching the same query on every
// tick — a real bug (an infinite timer chain), not merely test flakiness,
// caught by a -count=2 run: once text matches the query the current
// suggestions were already resolved for, this returns them synchronously
// with no new timer at all.
func (a *App) editFormBranchAutocomplete(text string) []string {
	if text == a.editFormBranchLastQuery {
		return a.editFormBranchSuggestions
	}
	if a.editFormBranchTimer != nil {
		a.editFormBranchTimer.Stop()
	}
	repo := a.editFormRepo
	a.editFormBranchTimer = time.AfterFunc(editFormBranchDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.deps.Store.SearchBranches(repo, text, func(branches []model.Branch, err error) {
				if err != nil || a.editFormBaseField == nil {
					return
				}
				names := make([]string, len(branches))
				for i, b := range branches {
					names[i] = b.Name
				}
				a.editFormBranchSuggestions = names
				a.editFormBranchLastQuery = text
				a.editFormBaseField.Autocomplete()
			})
		})
	})
	return a.editFormBranchSuggestions
}

// editFormComputeDiff computes what Save would enqueue right now, by
// comparing the form's live field values against editFormOriginal — used
// by both saveEditForm and cancelEditForm's own dirty check, so "is there
// anything to discard" and "is there anything to save" always agree.
// Title/Base are trimmed of leading/trailing whitespace before either
// comparison or being sent (found missing in review): an accidental space
// typed into either field must not itself count as, or be saved as, a
// change.
func (a *App) editFormComputeDiff() editFormDiff {
	var diff editFormDiff

	title := strings.TrimSpace(a.editFormTitleField.GetText())
	base := strings.TrimSpace(a.editFormBaseField.GetText())
	draft := a.editFormDraftBox.IsChecked()

	if title != a.editFormOriginal.title {
		diff.metaChanged = true
		diff.meta.Title = &title
	}
	if base != a.editFormOriginal.base {
		diff.metaChanged = true
		diff.meta.BaseRefName = &base
	}
	if !equalStringSet(a.editFormSelectedLabelIDs, a.editFormOriginal.labelIDs) {
		diff.metaChanged = true
		ids := sortedKeys(a.editFormSelectedLabelIDs)
		diff.meta.LabelIDs = &ids
	}

	curReviewerIDs := make(map[string]bool, len(a.editFormSelectedReviewers))
	for id := range a.editFormSelectedReviewers {
		curReviewerIDs[id] = true
	}
	if !equalStringSet(curReviewerIDs, a.editFormOriginal.reviewerIDs) {
		diff.reviewersChanged = true
		for _, r := range a.editFormSelectedReviewers {
			if r.Kind == model.ReviewerKindTeam {
				diff.teamIDs = append(diff.teamIDs, r.ID)
			} else {
				diff.userIDs = append(diff.userIDs, r.ID)
			}
		}
	}

	if draft != a.editFormOriginal.draft {
		diff.draftChanged = true
		diff.draft = draft
	}

	return diff
}

// saveEditForm implements the form's "Save" button (and Ctrl-s): refused
// with a toast while a mutation is already in flight, or if the current
// pull request no longer matches editFormRef (a switch while the form was
// open — closeEditFormIfWrongPR, EventPRChanged, should already have
// closed it by then, but Save must never depend on that alone, matching
// every other mutation trigger's own defensive re-check). "nothing to
// save" when the diff is empty. Otherwise enqueues each changed part, in
// UpdatePullRequestMeta/SetReviewers/SetDraft order, tracking every one
// that actually enqueued (its own bool return — see Store.UpdatePullRequestMeta's
// own doc comment for why a false return means nothing was enqueued) via
// editFormSteps.
func (a *App) saveEditForm() {
	if a.deps.Store.Mutating() {
		a.showToast("a mutation is already in progress; try again shortly", theme.Warning)
		return
	}
	if cur, ok := a.deps.Store.CurrentRef(); !ok || cur != a.editFormRef {
		a.showToast("cannot save: a different pull request is now open", theme.Warning)
		return
	}
	diff := a.editFormComputeDiff()
	if diff.empty() {
		a.showToast("nothing to save", theme.Warning)
		return
	}

	a.editFormAnyFailed = false
	if diff.metaChanged {
		if a.deps.Store.UpdatePullRequestMeta(diff.meta) {
			a.editFormSteps = append(a.editFormSteps, editFormStep{successVerb: "pull request updated"})
		}
	}
	if diff.reviewersChanged {
		if a.deps.Store.SetReviewers(diff.userIDs, diff.teamIDs) {
			a.editFormSteps = append(a.editFormSteps, editFormStep{successVerb: "reviewers updated"})
		}
	}
	if diff.draftChanged {
		if a.deps.Store.SetDraft(diff.draft) {
			a.editFormSteps = append(a.editFormSteps, editFormStep{successVerb: "draft state updated"})
		}
	}
}

// onEditFormMutationChanged reacts to store.EventMutationChanged while
// editFormSteps holds outstanding Save steps: each event where
// Store.Mutating() has just gone false is exactly one step finishing (see
// docs/DESIGN.md and pendingSimpleMutation's own doc comment for why —
// finishMutation sets s.mutating=false and emits before calling
// startNextMutation, which sets it true again for the next queued step,
// so this fires once per step, not only once at the very end). Pops the
// front step, toasts its own success/failure (reading Store.MutationError(),
// never LastError(), for the same reason onMutationChanged's own doc
// comment gives), and — once every step has finished — closes the form
// only if none of them failed, leaving it open with its values intact
// otherwise.
func (a *App) onEditFormMutationChanged() {
	if a.deps.Store.Mutating() || len(a.editFormSteps) == 0 {
		return
	}
	step := a.editFormSteps[0]
	a.editFormSteps = a.editFormSteps[1:]

	if err := a.deps.Store.MutationError(); err != nil {
		a.editFormAnyFailed = true
		a.showToast("not sent: "+step.successVerb+": "+err.Error(), theme.Error)
	} else {
		a.showToast(step.successVerb, theme.Success)
	}

	if len(a.editFormSteps) == 0 {
		failed := a.editFormAnyFailed
		a.editFormAnyFailed = false
		if !failed {
			a.closeEditForm()
		}
	}
}

// cancelEditForm implements Esc/the form's "Cancel" button: closes
// immediately when the computed diff is empty, otherwise asks to discard
// via a confirm dialog first.
func (a *App) cancelEditForm() {
	if a.editFormComputeDiff().empty() {
		a.closeEditForm()
		return
	}
	a.showConfirm("Discard changes?", "Discard", a.closeEditForm)
}

// closeEditForm closes the edit form (a no-op when it is not open) and
// restores focus.
//
// The guard is a.editForm == nil, not a.overlay != "editform" (a real bug
// the first M5 review round found): a.overlay can legitimately be
// "editlabels"/"editreviewers" while the form itself is still open — its
// own two child overlays are separate tview.Pages entries stacked on top
// of it — and a bare "a.overlay != 'editform'" guard made closeEditForm a
// silent no-op in exactly that state, in two different ways: (1)
// closeEditFormIfWrongPR (a PR switch) never actually tore the form down
// while a child overlay was open over it, and (2) cancelEditForm's own
// "Discard changes?" confirm passes closeEditForm as onConfirm, and
// showConfirm then reset a.overlay to "" *before* calling onConfirm — so
// even with no child overlay involved at all, the old guard compared
// "" != "editform" and refused to close, silently leaving the discarded
// form's own page (and every editForm* field) exactly as they were;
// keying on the form's own field keeps closeEditForm independent of what
// a.overlay reads at the time. Closing a still-open child overlay first
// (discarding its own in-progress selection, matching what Esc/q there
// already does) avoids leaving its page orphaned in a.root once
// "editform" itself is removed underneath it.
//
// The same a.overlay independence also covers a Ctrl-C-while-mutating
// confirm stacked on top of "editform" itself (a.overlay == "confirm")
// when closeEditFormIfWrongPR runs while it is showing: every field below
// is still cleared, but a.overlay and editFormReturnFocus's own SetFocus
// are skipped so the confirm stays the visible, focused overlay —
// showConfirm's own done func notices via overlayStillOpen("editform") and
// falls back once it closes.
func (a *App) closeEditForm() {
	if a.editForm == nil {
		return
	}
	a.closeEditLabelsOverlay(false)
	a.closeEditReviewersOverlay(false)

	if a.editFormBranchTimer != nil {
		a.editFormBranchTimer.Stop()
		a.editFormBranchTimer = nil
	}
	a.root.RemovePage("editform")
	a.editForm = nil
	a.editFormTitleField = nil
	a.editFormBaseField = nil
	a.editFormDraftBox = nil
	a.editFormSelectedLabelIDs = nil
	a.editFormSelectedReviewers = nil
	a.editFormBranchSuggestions = nil
	a.editFormBranchLastQuery = ""
	a.editFormBaseSuggesting = false
	returnFocus := a.editFormReturnFocus
	a.editFormReturnFocus = nil
	if a.overlay == "confirm" {
		return
	}
	a.overlay = ""
	if returnFocus != nil {
		a.app.SetFocus(returnFocus)
	} else {
		a.focusDetail()
	}
}

// closeEditFormIfWrongPR closes the edit form the instant the currently
// open pull request no longer matches editFormRef (a switch while it was
// open), mirroring composer.go's closeComposerIfWrongPR — called from
// EventPRChanged.
func (a *App) closeEditFormIfWrongPR() {
	if a.editForm == nil {
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.editFormRef {
		a.closeEditForm()
	}
}

// routeEditFormKey handles the "editform" overlay: forwards every key to
// the Form's own InputHandler (InputField/Checkbox/buttons), rewritten
// first by rewriteFormNavKey (formnav.go, see routeCreateFormKey's own doc
// comment for the exact rules and why they never collide) so Up/Down also
// move between items exactly like Tab/Backtab already do — except while
// the Base field's own autocomplete drop-down is shown
// (editFormBaseSuggesting, wrapFormAutocomplete, forced false whenever the
// field does not currently have focus), in which case Tab/Backtab instead
// navigate its own candidates. Esc cancels (asking to discard when dirty)
// — except while that drop-down is shown, when a literal Escape must only
// close it (see routeCreateFormKey's own doc comment for why this router
// resets the tracked state itself here rather than leaving it stale) — and
// Ctrl-s saves.
func (a *App) routeEditFormKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	if a.app.GetFocus() != a.editFormBaseField {
		a.editFormBaseSuggesting = false
	}
	suggesting := a.editFormBaseSuggesting

	for _, k := range normalized {
		if isEscKey(k) {
			if suggesting {
				a.editFormBaseSuggesting = false
				return ev
			}
			a.cancelEditForm()
			return nil
		}
		if isCtrlS(k) {
			a.saveEditForm()
			return nil
		}
	}
	return rewriteFormNavKey(ev, suggesting)
}
