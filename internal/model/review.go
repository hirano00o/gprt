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
