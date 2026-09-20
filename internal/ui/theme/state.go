package theme

import (
	"github.com/gdamore/tcell/v2"
	"github.com/hirano00o/gprt/internal/model"
)

// ReviewerStyle picks the style a reviewer's login is drawn with in the PR
// list, based on their latest review (state, empty when they have not
// reviewed at all) and whether they are still a requested reviewer.
// A requested reviewer who has not reviewed yet is drawn Stale (dimmed),
// the same treatment as cache-shown-not-yet-confirmed data, since both
// represent "this is not settled yet, don't treat it as final".
func ReviewerStyle(state model.ReviewState, requested bool) tcell.Style {
	switch state {
	case model.ReviewStateApproved:
		return Success
	case model.ReviewStateChangesRequested:
		return Error
	case model.ReviewStateDismissed:
		return Muted
	case model.ReviewStatePending:
		return Pending
	case model.ReviewStateCommented:
		return Base
	default:
		if requested {
			return Stale
		}
		return Base
	}
}

// RollupStyle picks the style a pull request's overall status-check rollup
// icon is drawn with.
func RollupStyle(state model.StatusState) tcell.Style {
	switch state {
	case model.StatusStateSuccess:
		return Success
	case model.StatusStateFailure, model.StatusStateError:
		return Error
	case model.StatusStatePending, model.StatusStateExpected:
		return Pending
	default:
		return Muted
	}
}
