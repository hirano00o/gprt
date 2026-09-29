// mergedialog.go implements the ":merge" command (docs/KEYBINDINGS.md's
// "':' commands" table): a repository-settings-aware merge dialog — a
// method chooser limited to the repository's allowed merge strategies (a
// single allowed one preselected), a commit headline (defaulted to
// "<title> (#N)", disabled for Rebase, which GitHub ignores it for), a
// commit body, and a summary line hinting at Mergeable/MergeStateStatus/
// the review decision. Submit shows a confirm dialog before calling
// Store.Merge.
//
// The commit body is a plain *tview.TextArea embedded directly in the
// Form (FormItem is satisfied natively — see tview's own textarea.go)
// rather than the vim composer: the composer is a Flex-embedding
// primitive with its own focus/routing model (see composer.go's Router
// pitfall doc comment), which does not compose with tview.Form's own
// item-based focus cycling; a plain TextArea keeps this dialog within the
// same single-router, "forward to tview's own built-ins" shape every
// other overlay in this package uses.
package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// mergeMethodOrder names each allowed model.MergeMethod's dialog label, in
// the fixed order they are offered.
var mergeMethodOrder = []struct {
	method model.MergeMethod
	label  string
}{
	{model.MergeMethodMerge, "Merge commit"},
	{model.MergeMethodSquash, "Squash"},
	{model.MergeMethodRebase, "Rebase"},
}

// cmdMerge implements ":merge": requires an open, loaded pull request
// (toast otherwise), and refuses (no-op) while another overlay is already
// open, matching openReactionPicker's own rule. Loads the repository's
// settings (EnsureRepositoryMetadata) and opens the dialog immediately
// with the real form if RepositoryInfo already resolved (a repository
// visited by an earlier edit/merge/create form this session), otherwise a
// "loading repository settings…" placeholder until
// EventRepositoryMetadataChanged resolves it (onRepositoryMetadataChangedForMerge)
// — a failure closes the placeholder, the generic EventError toast
// (storeevents.go) already explaining why.
func (a *App) cmdMerge() {
	if a.overlay != "" {
		return
	}
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	ref, _ := a.deps.Store.CurrentRef()
	a.mergeRef = ref
	a.mergeRepo = ref.Repo
	a.deps.Store.EnsureRepositoryMetadata(ref.Repo)

	if info, ok := a.deps.Store.RepositoryInfo(ref.Repo); ok {
		a.savedFocus = a.app.GetFocus()
		a.openMergeForm(info)
		return
	}
	a.openMergeLoading()
}

// openMergeLoading shows the "loading repository settings…" placeholder.
func (a *App) openMergeLoading() {
	a.savedFocus = a.app.GetFocus()
	a.overlay = "merge"
	a.mergeLoading = true
	view := tview.NewTextView().SetText("loading repository settings…")
	view.SetBorder(true).SetTitle(" Merge ")
	a.root.AddPage("merge", view, true, true)
	a.app.SetFocus(view)
}

// mergeDialogOpen reports whether the merge dialog — the loading
// placeholder or the real form — is currently open, keyed on its own
// fields rather than a.overlay's string (which reads "confirm" for as long
// as a Ctrl-C-while-mutating confirm is stacked over it), exactly like
// closeEditForm's own a.editForm == nil guard.
func (a *App) mergeDialogOpen() bool {
	return a.mergeLoading || a.mergeForm != nil
}

// onRepositoryMetadataChangedForMerge reacts to
// store.EventRepositoryMetadataChanged: replaces the merge dialog's
// loading placeholder with the real form once repo's RepositoryInfo
// resolves. Ignored once the real form is already showing (mergeLoading
// false, so a later, unrelated event never discards a filled-in form) or
// for a repository other than the one the dialog was opened for.
func (a *App) onRepositoryMetadataChangedForMerge(repo model.RepoRef) {
	if !a.mergeLoading || repo != a.mergeRepo {
		return
	}
	info, ok := a.deps.Store.RepositoryInfo(repo)
	if !ok {
		return
	}
	a.root.RemovePage("merge")
	a.openMergeForm(info)
}

// closeMergeDialogOnError closes the merge dialog's loading placeholder on
// a repository-metadata fetch failure — the generic EventError toast
// (storeevents.go) already explains why. A no-op once the real form is
// already showing (a later, unrelated fetch failure must not discard a
// filled-in form) or when no merge dialog is open at all.
func (a *App) closeMergeDialogOnError() {
	if !a.mergeLoading {
		return
	}
	a.closeMergeDialog()
}

// closeMergeDialogIfWrongPR closes the merge dialog the instant the
// currently open pull request no longer matches mergeRef (a switch while
// it was open), mirroring composer.go's closeComposerIfWrongPR — called
// from EventPRChanged.
func (a *App) closeMergeDialogIfWrongPR() {
	if !a.mergeDialogOpen() {
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.mergeRef {
		a.closeMergeDialog()
	}
}

// closeMergeDialog closes the merge dialog (the loading placeholder or the
// real form) and restores focus. A no-op when it is not open.
//
// The guard is mergeDialogOpen(), not a.overlay != "merge": a Ctrl-C-while-
// mutating confirm can be stacked on top (a.overlay == "confirm") when a
// store event (closeMergeDialogIfWrongPR/closeMergeDialogOnError) closes
// this out from under it, mirroring closeEditForm's own a.editForm-based
// guard. When that happens, the page/fields below are still torn down, but
// a.overlay/focus are left alone (the confirm stays the visible, focused
// overlay) — showConfirm's own done func notices via
// overlayStillOpen("merge") and falls back once it closes.
func (a *App) closeMergeDialog() {
	if !a.mergeDialogOpen() {
		return
	}
	a.root.RemovePage("merge")
	a.mergeForm = nil
	a.mergeSummaryView = nil
	a.mergeLoading = false
	a.mergeMethods = nil
	if a.overlay == "confirm" {
		return
	}
	a.overlay = ""
	a.restoreFocus()
}

// refreshMergeSummaryIfOpen recomputes the merge dialog's own summary line
// (Mergeable/MergeStateStatus/the review decision) from the current pull
// request — called from EventPRChanged so an update to the pull request
// the dialog is open for (a background refresh landing, an unrelated
// mutation's own refetch, …) keeps the line current instead of freezing it
// at whatever mergeSummaryLine(pr) read when the form first opened. A
// no-op while only the loading placeholder is showing (mergeSummaryView is
// nil until openMergeForm actually builds the real form).
func (a *App) refreshMergeSummaryIfOpen() {
	if a.mergeSummaryView == nil {
		return
	}
	if pr := a.deps.Store.CurrentPR(); pr != nil {
		a.mergeSummaryView.SetText(mergeSummaryLine(pr))
	}
}

// allowedMergeMethods returns info's allowed merge methods, in
// mergeMethodOrder's fixed order.
func allowedMergeMethods(info model.RepositoryInfo) []model.MergeMethod {
	var methods []model.MergeMethod
	if info.MergeCommitAllowed {
		methods = append(methods, model.MergeMethodMerge)
	}
	if info.SquashMergeAllowed {
		methods = append(methods, model.MergeMethodSquash)
	}
	if info.RebaseMergeAllowed {
		methods = append(methods, model.MergeMethodRebase)
	}
	return methods
}

// mergeMethodLabel returns method's dialog label.
func mergeMethodLabel(method model.MergeMethod) string {
	for _, m := range mergeMethodOrder {
		if m.method == method {
			return m.label
		}
	}
	return string(method)
}

// mergeSummaryLine renders pr's merge-readiness hint line: MergeStateStatus/
// Mergeable/the review decision, for the form's read-only summary row.
func mergeSummaryLine(pr *model.PullRequest) string {
	decision := "no review decision"
	if pr.ReviewDecision != "" {
		decision = string(pr.ReviewDecision)
	}
	return fmt.Sprintf("mergeable: %s · state: %s · review: %s", pr.Mergeable, pr.MergeStateStatus, decision)
}

// defaultMergeHeadline returns the default commit headline, "<title> (#N)".
func defaultMergeHeadline(pr *model.PullRequest) string {
	return fmt.Sprintf("%s (#%d)", pr.Title, pr.Ref.Number)
}

// openMergeForm builds and shows the real merge form for info, replacing
// the loading placeholder if one was open. Refuses with a toast and closes
// the dialog when the repository allows no merge method at all (a
// misconfigured repository this dialog has nothing to offer for).
func (a *App) openMergeForm(info model.RepositoryInfo) {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.closeMergeDialog()
		return
	}
	methods := allowedMergeMethods(info)
	if len(methods) == 0 {
		a.showToast("no merge methods are allowed for this repository", theme.Warning)
		// cmdMerge's own direct-call path (RepositoryInfo already
		// resolved) reaches here with mergeLoading still false and
		// mergeForm still nil — nothing mounted yet for closeMergeDialog's
		// own mergeDialogOpen() guard to see — so this forces it true
		// first, purely so that call still consumes a.savedFocus (set by
		// cmdMerge just before this) via closeMergeDialog's own
		// restoreFocus(), exactly as if the loading placeholder had
		// briefly existed.
		a.mergeLoading = true
		a.closeMergeDialog()
		return
	}
	a.mergeLoading = false
	a.mergeMethods = methods

	headline := tview.NewInputField().SetLabel("Commit headline").SetText(defaultMergeHeadline(pr))
	body := tview.NewTextArea()
	body.SetLabel("Commit body")

	labels := make([]string, len(methods))
	for i, m := range methods {
		labels[i] = mergeMethodLabel(m)
	}
	methodDropDown := tview.NewDropDown().SetLabel("Method").SetOptions(labels, func(_ string, index int) {
		headline.SetDisabled(index >= 0 && index < len(methods) && methods[index] == model.MergeMethodRebase)
	})
	methodDropDown.SetCurrentOption(0) // fires the callback above, disabling headline immediately for a Rebase-only repository

	// Built directly (not via Form.AddTextView) so a reference survives for
	// refreshMergeSummaryIfOpen to update later, mirroring
	// Form.AddTextView's own defaults (fieldHeight 1, dynamic colors on,
	// not scrollable).
	summary := tview.NewTextView().SetLabel("Status").SetSize(1, 0).SetDynamicColors(true).SetScrollable(false).SetText(mergeSummaryLine(pr))

	form := tview.NewForm()
	form.AddFormItem(methodDropDown)
	form.AddFormItem(headline)
	form.AddFormItem(body)
	form.AddFormItem(summary)
	form.AddButton("Merge", a.submitMergeForm)
	form.AddButton("Cancel", a.closeMergeDialog)
	form.SetBorder(true).SetTitle(" Merge ")

	a.mergeForm = form
	a.mergeMethodDropDown = methodDropDown
	a.mergeHeadlineField = headline
	a.mergeBodyArea = body
	a.mergeSummaryView = summary

	a.root.AddPage("merge", form, true, true)
	if a.overlay == "confirm" {
		// The placeholder was replaced while a Ctrl-C confirm sat on top
		// of it: AddPage just drew the form over that confirm, so put the
		// confirm back in front and let it return to the form (not the
		// discarded placeholder it captured) when it closes.
		a.root.SendToFront("confirm")
		a.confirmReturnFocus = form
		return
	}
	a.overlay = "merge"
	a.app.SetFocus(form)
}

// submitMergeForm implements the form's "Merge" button: refuses with a
// toast (no confirm shown) when the pull request is no longer OPEN,
// otherwise closes the form and shows a confirm dialog ("Merge #N with
// <method>?") before calling Store.Merge — the confirm's own onConfirm
// re-checks Store.CurrentRef() against mergeRef (docs/DESIGN.md's
// "deferred callback" convention) via runSimpleMutation, which also tracks
// the outcome via pendingMutation.
func (a *App) submitMergeForm() {
	pr := a.deps.Store.CurrentPR()
	if pr == nil || pr.State != model.PRStateOpen {
		a.showToast("pull request is not open", theme.Warning)
		return
	}
	idx, _ := a.mergeMethodDropDown.GetCurrentOption()
	if idx < 0 || idx >= len(a.mergeMethods) {
		return
	}
	method := a.mergeMethods[idx]
	ref := a.mergeRef

	var headline, body *string
	if method != model.MergeMethodRebase {
		if h := a.mergeHeadlineField.GetText(); h != "" {
			headline = &h
		}
		if b := a.mergeBodyArea.GetText(); b != "" {
			body = &b
		}
	}

	a.closeMergeDialog()
	question := fmt.Sprintf("Merge #%d with %s?", ref.Number, mergeMethodLabel(method))
	a.showConfirm(prConfirmMessage(question, pr), "Merge", func() {
		a.runSimpleMutation(ref, "merged", func() bool {
			return a.deps.Store.Merge(method, headline, body)
		})
	})
}

// routeMergeKey handles the "merge" overlay: while still loading (the
// placeholder), q/Esc close it, matching help/messages' own shape; once
// the real form is showing, only Esc cancels it (q is ordinary text input
// for the headline/body fields) and every other key is forwarded
// unchanged to the Form's own InputHandler (DropDown/InputField/TextArea/
// buttons), exactly as the router does for the "confirm" overlay's Modal.
func (a *App) routeMergeKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		if isEscKey(k) || (a.mergeLoading && isPlainRune(k, 'q')) {
			a.closeMergeDialog()
			return nil
		}
	}
	if a.mergeLoading {
		return nil
	}
	return ev
}
