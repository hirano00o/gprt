package model

import "time"

// TimelineKind identifies the concrete type held by a TimelineItem.
type TimelineKind string

// Known timeline item kinds.
const (
	TimelineKindIssueComment TimelineKind = "IssueComment"
	TimelineKindReview       TimelineKind = "Review"
	TimelineKindCommit       TimelineKind = "Commit"
	TimelineKindEvent        TimelineKind = "Event"
)

// Commit is a single commit pushed to a pull request's head branch.
type Commit struct {
	OID         string
	Message     string
	Author      User
	CommittedAt time.Time
}

// Event is a non-comment timeline entry, such as a label added, a review
// requested, or a status change.
type Event struct {
	Type   string
	Actor  User
	At     time.Time
	Detail string
}

// TimelineItem is one entry in a pull request's conversation timeline.
// Exactly one of IssueComment, Review, Commit, or Event is set, matching
// Kind.
type TimelineItem struct {
	Kind         TimelineKind
	IssueComment *IssueComment
	Review       *Review
	Commit       *Commit
	Event        *Event
}
