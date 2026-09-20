package model

import "time"

// ReviewState is the outcome of a submitted (or pending) pull request review.
type ReviewState string

// Known review states.
const (
	ReviewStateApproved         ReviewState = "APPROVED"
	ReviewStateChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewStateCommented        ReviewState = "COMMENTED"
	ReviewStateDismissed        ReviewState = "DISMISSED"
	ReviewStatePending          ReviewState = "PENDING"
)

// ReviewEvent is the action taken when publishing a pull request review,
// either immediately (addPullRequestReview's own "event" input) or when
// submitting an existing pending review (submitPullRequestReview).
type ReviewEvent string

// Known review events. GitHub's schema also defines DISMISS, which gprt
// never sends: dismissing a review is out of scope (see docs/REQUIREMENTS.md
// F4).
const (
	ReviewEventApprove        ReviewEvent = "APPROVE"
	ReviewEventRequestChanges ReviewEvent = "REQUEST_CHANGES"
	ReviewEventComment        ReviewEvent = "COMMENT"
)

// Review is a pull request review: either submitted (Approved, Changes
// Requested, Commented, Dismissed) or the viewer's own pending review.
type Review struct {
	ID             string
	Author         User
	State          ReviewState
	Body           string
	SubmittedAt    time.Time
	ReactionGroups []ReactionGroup
	URL            string
}
