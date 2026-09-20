package model

import "time"

// IssueComment is a general comment on a pull request's conversation
// timeline (as opposed to a review comment anchored to a diff line).
type IssueComment struct {
	ID              string
	Author          User
	Body            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ReactionGroups  []ReactionGroup
	ViewerCanUpdate bool
	ViewerCanDelete bool
	URL             string
}

// ReviewCommentState distinguishes a review comment that belongs to the
// viewer's pending review from one that has already been submitted.
type ReviewCommentState string

// Known review comment states.
const (
	ReviewCommentStatePending   ReviewCommentState = "PENDING"
	ReviewCommentStateSubmitted ReviewCommentState = "SUBMITTED"
)

// ReviewComment is a single comment within a review thread, anchored to a
// diff line (or file).
type ReviewComment struct {
	ID              string
	ReviewID        string
	Author          User
	Body            string
	CreatedAt       time.Time
	State           ReviewCommentState
	ReactionGroups  []ReactionGroup
	ViewerCanUpdate bool
	ViewerCanDelete bool
	URL             string
}
