package store

import "github.com/hirano00o/gprt/internal/model"

// EventKind identifies what changed in an Event.
type EventKind int

// Known event kinds.
const (
	// EventViewerLoaded fires once the authenticated user is known (from
	// cache, from the network, or both).
	EventViewerLoaded EventKind = iota
	// EventListChanged fires whenever Rows would return different data:
	// new items applied, a filter change, or a state change.
	EventListChanged
	// EventLoadingChanged fires whenever any section's loading flag
	// changes, or (M1b) whenever the current pull request's detail
	// loading flag changes — matching Loading(), which folds in both. It
	// always accompanies EventPRLoadingChanged for the latter case.
	EventLoadingChanged
	// EventRateLimitChanged fires whenever a fresh RateLimit is observed.
	EventRateLimitChanged
	// EventError fires whenever a fetch fails or a goroutine panics.
	// Section is set when the error is scoped to one section.
	EventError
	// EventPRChanged fires whenever CurrentRef or CurrentPR would return
	// something different: OpenPR, a cached or network detail applied,
	// or ClosePR.
	EventPRChanged
	// EventPRLoadingChanged fires whenever DetailState().Loading changes.
	EventPRLoadingChanged
	// EventFilesChanged fires whenever Files or FilesState (other than
	// Loading, which has its own event below) would return different
	// data: a page applied from cache or the network, a page replaced, or
	// the files list reset (OpenPR/ClosePR).
	EventFilesChanged
	// EventFileHighlighted fires whenever one hunk's highlight result is
	// applied to a file already in Files. Event.Path names the file.
	EventFileHighlighted
	// EventFilesLoadingChanged fires whenever FilesState().Loading
	// changes. It always accompanies EventLoadingChanged, matching
	// EventPRLoadingChanged's own pairing.
	EventFilesLoadingChanged
	// EventMutationChanged fires whenever Mutating() or
	// PendingMutations() would return something different: a queued
	// mutation starts running, or one finishes (successfully or not).
	EventMutationChanged
	// EventNotice fires for an informational message that is not an error
	// (Event.Message; Event.Err is nil): for example a review-comment send
	// silently coerced from "single comment" to "add to review" because a
	// pending review already existed by the time the mutation actually
	// ran (see internal/store/review.go). A generic, reusable kind rather
	// than one dedicated to that single case, since later milestones (a
	// submitted review's own confirmation, say) need the same shape.
	EventNotice
	// EventMentionableChanged fires whenever MentionableUsers() would
	// return different data for the current pull request's repository: a
	// cached or network result applied, or a switch to a pull request in a
	// different repository whose own list has not resolved yet.
	EventMentionableChanged
	// EventRepositoryMetadataChanged fires whenever RepositoryInfo/Labels/
	// Templates would return different data for Event.Repo: a cached or
	// network result applied for that repository (see repository.go).
	EventRepositoryMetadataChanged
	// EventViewerRepositoriesChanged fires whenever ViewerRepositories()
	// would return different data: a cached or network result applied
	// (see viewer_repositories.go).
	EventViewerRepositoriesChanged
	// EventPullRequestCreated fires once CreatePullRequest's mutation
	// succeeds in creating the pull request server-side (see
	// pr_create.go). Event.Ref names the new pull request; the UI is
	// expected to open it in response. Fired even when a follow-up
	// RequestReviewers call for the same mutation then fails (see
	// pr_create.go's own doc comment) - the pull request itself was still
	// created, and its ref must not be lost.
	EventPullRequestCreated
)

// Event is published synchronously, on the UI goroutine, by Subscribe
// callbacks. It is never emitted from a goroutine directly: every apply
// site runs inside a Dispatch callback, per docs/DESIGN.md's concurrency
// rules.
type Event struct {
	Kind    EventKind
	Section *model.Section
	Err     error
	// Path names the file an EventFileHighlighted result belongs to.
	Path string
	// Message carries an EventNotice's informational text.
	Message string
	// Repo names the repository an EventRepositoryMetadataChanged result
	// belongs to.
	Repo *model.RepoRef
	// Ref names the pull request an EventPullRequestCreated result
	// belongs to.
	Ref *model.PRRef
}

// Subscribe registers fn to be called for every Event published from now
// on. There is no Unsubscribe: gprt has exactly one long-lived UI
// subscriber for the lifetime of a Store.
func (s *Store) Subscribe(fn func(Event)) {
	s.subscribers = append(s.subscribers, fn)
}

func (s *Store) emit(e Event) {
	for _, fn := range s.subscribers {
		fn(e)
	}
}

// RowKind distinguishes a section heading row from a pull request row.
type RowKind int

// Known row kinds.
const (
	RowHeader RowKind = iota
	RowItem
)

// Row is one line of the rendered PR list: either a section heading or a
// single pull request, already deduplicated, filtered, and sorted by
// Store.Rows.
type Row struct {
	Kind    RowKind
	Section model.Section
	Item    model.ListItem
	// Stale reports whether Item was served from cache and has not yet
	// been confirmed by a network response.
	Stale bool
}
