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
