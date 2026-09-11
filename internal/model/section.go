package model

import "time"

// SectionKind identifies one of the built-in list sections, or a
// user-defined custom search.
type SectionKind string

// Known section kinds.
const (
	SectionKindDirectReview SectionKind = "DirectReview"
	SectionKindTeamReview   SectionKind = "TeamReview"
	SectionKindMine         SectionKind = "Mine"
	SectionKindInvolved     SectionKind = "Involved"
	SectionKindCustom       SectionKind = "Custom"
)

// Section is a group of pull requests in the list view, defined by a search
// query and rendered under its own heading.
type Section struct {
	Name  string
	Query string
	Kind  SectionKind
}

// ListItem is a single pull request as it appears in a list section.
type ListItem struct {
	PR      *PullRequest
	Section Section
}

// RateLimit reports the GitHub API rate limit state returned alongside a
// query result.
type RateLimit struct {
	Remaining int
	ResetAt   time.Time
}
