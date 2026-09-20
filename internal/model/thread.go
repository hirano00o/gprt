package model

// DiffSide identifies which side of a diff (the base or the head) a line or
// comment anchor refers to.
type DiffSide string

// Known diff sides.
const (
	DiffSideLeft  DiffSide = "LEFT"
	DiffSideRight DiffSide = "RIGHT"
)

// ThreadSubject distinguishes a review thread anchored to a specific line
// from one anchored to a whole file.
type ThreadSubject string

// Known review thread subject types.
const (
	ThreadSubjectLine ThreadSubject = "LINE"
	ThreadSubjectFile ThreadSubject = "FILE"
)

// ReviewThread is a conversation anchored to a location in a pull request's
// diff: a single line, a line range, or a whole file.
type ReviewThread struct {
	ID                 string
	Path               string
	Line               int
	StartLine          int
	Side               DiffSide
	StartSide          DiffSide
	SubjectType        ThreadSubject
	IsResolved         bool
	IsOutdated         bool
	ViewerCanReply     bool
	ViewerCanResolve   bool
	ViewerCanUnresolve bool
	Comments           []ReviewComment
}
