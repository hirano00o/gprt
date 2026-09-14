// submitreview.go implements the "S" (pr.submit) submit-review dialog: a
// choice menu (dialogs.go's showChoiceMenu) picking Approve / Request
// changes / Comment, then a composer for the review's own body
// (composerKindReviewBody), sent through Store.SubmitReview.
package ui

import (
	"strings"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// reviewEventChoices lists the submit dialog's three choices, in the order
// shown and the order their index maps to a model.ReviewEvent.
var reviewEventChoices = []model.ReviewEvent{
	model.ReviewEventApprove, model.ReviewEventRequestChanges, model.ReviewEventComment,
}

// openSubmitReviewDialog implements "S" (pr.submit): a no-op toast when no
// pull request is open. The choice callback re-checks Store.CurrentRef()
// against ref itself, mirroring performReviewSend's own re-check
// (composer.go): it fires well after the menu was shown, so a different
// pull request may have become current in the meantime, and
// closeComposerIfWrongPR does not reach this menu at all (no composer is
// open yet for it to close).
func (a *App) openSubmitReviewDialog() {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	a.showChoiceMenu("Submit review", []string{"Approve", "Request changes", "Comment"}, func(i int) {
		if cur, ok := a.deps.Store.CurrentRef(); !ok || cur != ref {
			a.showToast("cannot submit: a different pull request is now open", theme.Warning)
			return
		}
		a.openReviewBodyComposer(reviewEventChoices[i])
	})
}

// reviewBodyComposerTitle names the composer's title line for event.
func reviewBodyComposerTitle(event model.ReviewEvent) string {
	switch event {
	case model.ReviewEventApprove:
		return "Approve: review body"
	case model.ReviewEventRequestChanges:
		return "Request changes: review body"
	default:
		return "Comment: review body"
	}
}

// openReviewBodyComposer opens the review-body composer for event.
// Prefill is the restored draft (openComposer's own Drafts.Load, checked
// first), else the current pull request's own pending review body, if it
// has one — matching GitHub's own UI, which shows whatever text a pending
// review already carries.
func (a *App) openReviewBodyComposer(event model.ReviewEvent) {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return
	}
	prefill := ""
	if rev := a.deps.Store.PendingReview(); rev != nil {
		prefill = rev.Body
	}
	a.openComposer(composerTarget{
		kind:        composerKindReviewBody,
		ref:         ref,
		reviewEvent: event,
		title:       reviewBodyComposerTitle(event),
		draftKey:    drafts.Key{PR: ref.Key(), Kind: drafts.KindReview, Anchor: "review"},
	}, prefill)
}

// sendReviewBodyComposer implements Send for the review-body composer
// (composerKindReviewBody). A COMMENT or REQUEST_CHANGES review with a
// blank (or whitespace-only) body and no pending review comments is
// refused locally with a toast — GitHub requires one or the other for
// those two events, and rejecting it here avoids a round trip only to be
// told the same thing by the API, matching GitHub's own UI. APPROVE has no
// such requirement and may always be sent with an empty body.
func (a *App) sendReviewBodyComposer(target composerTarget, text string) {
	if target.reviewEvent != model.ReviewEventApprove &&
		strings.TrimSpace(text) == "" && len(a.deps.Store.PendingComments()) == 0 {
		kind := "a comment review"
		if target.reviewEvent == model.ReviewEventRequestChanges {
			kind = "a request-changes review"
		}
		a.showToast(kind+" needs a body or pending comments", theme.Warning)
		return
	}
	a.finishSend(target, text, a.deps.Store.SubmitReview(target.reviewEvent, text))
}
