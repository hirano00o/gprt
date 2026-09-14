package model

// User is a GitHub account: an author, reviewer, commenter, or actor.
type User struct {
	Login string
	Name  string
}

// Label is a repository label attached to a pull request. ID is the
// GraphQL node ID, needed to build an UpdatePullRequestInput's labelIds
// when editing a pull request's labels (M5); it is empty for a label
// mapped from a context that never selected it (none does, as of M5).
type Label struct {
	ID    string
	Name  string
	Color string
}

// ReviewerKind distinguishes the kind of entity a review was requested from.
type ReviewerKind string

// Known reviewer kinds.
const (
	ReviewerKindUser  ReviewerKind = "User"
	ReviewerKindTeam  ReviewerKind = "Team"
	ReviewerKindOther ReviewerKind = "Other"
)

// Reviewer is a requested reviewer: an individual user or a team. ID is
// the GraphQL node ID (a User or Team ID), needed to build a
// RequestReviewsInput's userIds/teamIds when editing a pull request's
// reviewers (M5); it is empty for a Bot/Mannequin/EnterpriseTeam/removed
// reviewer, none of which RequestReviewers can target directly.
type Reviewer struct {
	ID          string
	Login       string
	Kind        ReviewerKind
	AsCodeOwner bool
}
