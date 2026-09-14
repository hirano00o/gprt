// prbody.go implements editing the pull request's own body: "e" on the PR
// tab's description block (composer.go's editCurrentComment) opens a
// composer kind (composerKindPRBody), prefilled with the current body,
// sending Store.UpdatePullRequestMeta with only Body set on success; "d"
// on the same block (deleteCurrentComment) is a no-op toast — a pull
// request's own body cannot be deleted, only edited.
package ui

import (
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// openPRBodyEditComposer opens the PR-body composer, prefilled with the
// pull request's live body (or a restored draft, if one exists — see
// openComposer's own Drafts.Load). Refused with a toast when no pull
// request is open or ViewerCanUpdate is false.
func (a *App) openPRBodyEditComposer() {
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
	a.openComposer(composerTarget{
		kind:     composerKindPRBody,
		ref:      ref,
		title:    "Edit PR body",
		draftKey: drafts.Key{PR: ref.Key(), Kind: drafts.KindPRBody, Anchor: "body"},
	}, pr.Body)
}
