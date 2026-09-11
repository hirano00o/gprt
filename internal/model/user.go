package model

// User is a GitHub account: an author, reviewer, commenter, or actor.
type User struct {
	Login string
	Name  string
}

// Label is a repository label attached to a pull request.
type Label struct {
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

// Reviewer is a requested reviewer: an individual user or a team.
type Reviewer struct {
	Login       string
	Kind        ReviewerKind
	AsCodeOwner bool
}
