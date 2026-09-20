package model

import "time"

// PRState is the lifecycle state of a pull request.
type PRState string

// Known pull request states.
const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

// ReviewDecision is the aggregate review decision GitHub computes for a
// pull request. The empty string means no decision has been reached (for
// example, no reviews are required or none have been submitted yet).
type ReviewDecision string

// Known review decisions.
const (
	ReviewDecisionApproved         ReviewDecision = "APPROVED"
	ReviewDecisionChangesRequested ReviewDecision = "CHANGES_REQUESTED"
	ReviewDecisionReviewRequired   ReviewDecision = "REVIEW_REQUIRED"
)

// MergeableState reports whether a pull request's head can be merged into
// its base without conflicts.
type MergeableState string

// Known mergeable states.
const (
	MergeableStateMergeable   MergeableState = "MERGEABLE"
	MergeableStateConflicting MergeableState = "CONFLICTING"
	MergeableStateUnknown     MergeableState = "UNKNOWN"
)

// MergeStateStatus is GitHub's more detailed merge-readiness status for a
// pull request (whether it is out of date, blocked by required checks or
// reviews, in draft, etc.).
type MergeStateStatus string

// Known merge state statuses.
const (
	MergeStateStatusBehind   MergeStateStatus = "BEHIND"
	MergeStateStatusBlocked  MergeStateStatus = "BLOCKED"
	MergeStateStatusClean    MergeStateStatus = "CLEAN"
	MergeStateStatusDirty    MergeStateStatus = "DIRTY"
	MergeStateStatusDraft    MergeStateStatus = "DRAFT"
	MergeStateStatusHasHooks MergeStateStatus = "HAS_HOOKS"
	MergeStateStatusUnknown  MergeStateStatus = "UNKNOWN"
	MergeStateStatusUnstable MergeStateStatus = "UNSTABLE"
)

// PullRequest is the full detail of a single GitHub pull request, combining
// metadata, review state, checks, and conversation.
type PullRequest struct {
	ID               string
	Ref              PRRef
	RepositoryID     string
	Title            string
	Body             string
	Author           User
	State            PRState
	IsDraft          bool
	ReviewDecision   ReviewDecision
	Mergeable        MergeableState
	MergeStateStatus MergeStateStatus
	BaseRefName      string
	HeadRefName      string
	HeadOID          string
	Additions        int
	Deletions        int
	ChangedFiles     int
	Labels           []Label
	ReviewRequests   []Reviewer
	LatestReviews    []Review
	Checks           []Check
	Timeline         []TimelineItem
	ReviewThreads    []ReviewThread
	PendingReview    *Review
	ReactionGroups   []ReactionGroup
	ViewerCanUpdate  bool
	ViewerDidAuthor  bool
	ViewerCanClose   bool
	ViewerCanReopen  bool
	ViewerCanReact   bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	URL              string
}
