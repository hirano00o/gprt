// prcommands.go implements the ":close" and ":reopen" command-line
// commands (docs/KEYBINDINGS.md's "':' commands" table): a confirm dialog
// gates the actual Store.Close/Reopen call, and pendingMutation tracks its
// outcome (Store.MutationError(), never LastError()) for a success/failure
// toast — mirroring composer.go's pendingSend for a mutation with no
// composer text of its own. ":merge" is implemented in mergedialog.go.
package ui

import (
	"fmt"

	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// pendingSimpleMutation is pendingSend's counterpart for a non-composer
// mutation: successVerb names the outcome shown by the success toast
// ("closed", "reopened", "merged").
type pendingSimpleMutation struct {
	successVerb string
}

// cmdClose implements ":close": requires an open, loaded pull request
// (toast otherwise); refuses with a toast when the viewer cannot close it
// (ViewerCanClose false) or it is not currently OPEN (already closed or
// merged); otherwise confirms ("Close #N?") before calling Store.Close().
func (a *App) cmdClose() {
	a.confirmSimplePRMutation("Close", "closed", "cannot close this pull request",
		func(pr *model.PullRequest) bool { return pr.ViewerCanClose },
		func(pr *model.PullRequest) bool { return pr.State == model.PRStateOpen },
		"not open",
		func() bool { return a.deps.Store.Close() },
	)
}

// cmdReopen implements ":reopen", mirroring cmdClose for Store.Reopen().
func (a *App) cmdReopen() {
	a.confirmSimplePRMutation("Reopen", "reopened", "cannot reopen this pull request",
		func(pr *model.PullRequest) bool { return pr.ViewerCanReopen },
		func(pr *model.PullRequest) bool { return pr.State == model.PRStateClosed },
		"not closed",
		func() bool { return a.deps.Store.Reopen() },
	)
}

// confirmSimplePRMutation is cmdClose's/cmdReopen's shared shape: requires
// an open, loaded pull request; refuses with a toast when canAct(pr) is
// false ("you <permissionMsg>") or fitsState(pr) is false ("pull request is
// <stateMsg>"); otherwise shows a confirm dialog ("<title> #N?") whose own
// onConfirm re-checks Store.CurrentRef() against the ref captured here
// (docs/DESIGN.md's "deferred callback" convention: every store call made
// from a deferred callback re-checks the ref the dialog opened for) before
// calling enqueue and tracking its outcome (successVerb, "closed"/
// "reopened") via pendingMutation.
func (a *App) confirmSimplePRMutation(
	title, successVerb, permissionMsg string,
	canAct, fitsState func(pr *model.PullRequest) bool,
	stateMsg string,
	enqueue func() bool,
) {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	if !canAct(pr) {
		a.showToast("you "+permissionMsg, theme.Warning)
		return
	}
	if !fitsState(pr) {
		a.showToast("pull request is "+stateMsg, theme.Warning)
		return
	}
	ref, _ := a.deps.Store.CurrentRef()
	a.showConfirm(prConfirmMessage(fmt.Sprintf("%s #%d?", title, ref.Number), pr), title, func() {
		a.runSimpleMutation(ref, successVerb, enqueue)
	})
}

// prConfirmMessage follows question with the pull request it acts on —
// repository, number, title and author — so a state-changing confirmation
// can be checked against more than a bare number. The title is escaped
// because tview.Modal renders its text with style tags enabled.
func prConfirmMessage(question string, pr *model.PullRequest) string {
	return fmt.Sprintf("%s\n\n%s #%d\n%s\nby @%s",
		question, pr.Ref.Repo.NameWithOwner(), pr.Ref.Number, tview.Escape(pr.Title), pr.Author.Login)
}

// runSimpleMutation re-checks Store.CurrentRef() against ref, refuses with
// a toast while a mutation is already in flight (matching sendComposer's
// own guard), then calls enqueue and starts tracking its outcome via
// pendingMutation/onSimpleMutationChanged.
func (a *App) runSimpleMutation(ref model.PRRef, successVerb string, enqueue func() bool) {
	if cur, ok := a.deps.Store.CurrentRef(); !ok || cur != ref {
		a.showToast("cannot proceed: a different pull request is now open", theme.Warning)
		return
	}
	if a.deps.Store.Mutating() {
		a.showToast("a mutation is already in progress; try again shortly", theme.Warning)
		return
	}
	if !enqueue() {
		return
	}
	a.pendingMutation = &pendingSimpleMutation{successVerb: successVerb}
}

// onSimpleMutationChanged reacts to store.EventMutationChanged for a
// non-composer mutation tracked in pendingMutation: mirrors composer.go's
// onMutationChanged for pendingSend, reading success/failure from
// Store.MutationError() — never LastError(), which would also surface
// other, unrelated standing errors (see onMutationChanged's own doc
// comment for why).
func (a *App) onSimpleMutationChanged() {
	if a.deps.Store.Mutating() || a.pendingMutation == nil {
		return
	}
	pm := a.pendingMutation
	a.pendingMutation = nil

	if err := a.deps.Store.MutationError(); err != nil {
		a.showToast("not "+pm.successVerb+": "+err.Error(), theme.Error)
		return
	}
	a.showToast(pm.successVerb, theme.Success)
}
