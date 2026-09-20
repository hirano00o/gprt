// Package store owns gprt's application state: it holds the PR list
// sections, talks to GitHub and the disk cache from goroutines it starts
// itself, and publishes results back to the UI goroutine through a
// caller-supplied Dispatch function. See docs/DESIGN.md's "Concurrency
// rules" for the ten rules every method here follows: no mutex on Store's
// own fields (they are owned by the UI goroutine), dispatch only from
// store-started goroutines, and every async result tagged with a
// generation token so a superseded result is dropped rather than applied
// out of order.
package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// GitHub is the subset of *gh.Client the store depends on, so tests can
// substitute a fake without touching the network.
type GitHub interface {
	Viewer(ctx context.Context) (model.User, model.RateLimit, error)
	SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error)
}

// Deps are the Store's dependencies, supplied once at construction.
type Deps struct {
	// GitHub is the API client (or a fake in tests).
	GitHub GitHub
	// Cache backs the stale-while-revalidate list cache.
	Cache *cache.Store
	// Dispatch runs f on the UI goroutine. The Store calls it only from
	// goroutines it starts itself (never from within a dispatched
	// callback), matching docs/DESIGN.md's concurrency rules. Production
	// wires this to app.QueueUpdateDraw; tests use a channel-driven fake.
	Dispatch func(func())
	// Logger receives structured logs for failed fetches and recovered
	// panics.
	Logger *slog.Logger
	// Config is the loaded user configuration (list state, custom
	// sections).
	Config config.Config
	// Host is the GitHub host sections are searched against and cache
	// entries are namespaced under.
	Host string
	// Now returns the current time. Defaults to time.Now; tests override
	// it for deterministic FetchedAt/LastRefresh values.
	Now func() time.Time
}

// builtinSection is one of the store's four fixed sections, before its
// search query is resolved against the configured list state.
type builtinSection struct {
	kind model.SectionKind
	name string
}

// builtinSections lists the four built-in sections in the priority order
// used both for display and for Rows' cross-section deduplication.
var builtinSections = []builtinSection{
	{model.SectionKindDirectReview, "Review requested"},
	{model.SectionKindTeamReview, "Team review requested"},
	{model.SectionKindMine, "My pull requests"},
	{model.SectionKindInvolved, "Involved"},
}

// sectionState is one list section's live state: its search query (rebuilt
// when the list state changes) and everything fetched for it so far.
type sectionState struct {
	kind   model.SectionKind
	name   string
	custom string // the config query, for kind == SectionKindCustom only
	query  string // current resolved GitHub search query

	items    []model.PullRequest
	cursor   string
	hasNext  bool
	total    int
	loading  bool
	stale    bool
	lastErr  error
	warnings []string

	// loadedPages is how many consecutive pages (starting at page 1) are
	// currently reflected in items. Reset to 1 on every page-1
	// replacement, incremented on every appended page (LoadMore, or an
	// automatic refresh-chain page — see refreshTargetPages).
	loadedPages int
	// refreshTargetPages is nonzero while a non-stale page-1 refresh is
	// automatically re-fetching pages 2..refreshTargetPages to restore
	// the section to the depth it had before the refresh (see
	// applyFetchResult); 0 means no chain is in progress.
	refreshTargetPages int
}

// section returns this state's model.Section view, as returned by
// Store.Sections and embedded in Row and Event.
func (s *sectionState) section() model.Section {
	return model.Section{Name: s.name, Query: s.query, Kind: s.kind}
}

// Store is gprt's UI-goroutine-owned application state for the PR list.
// See the package doc for its concurrency contract.
type Store struct {
	deps Deps

	state    string // current list state: open|closed|merged|all
	sections []*sectionState

	filterText string
	filter     parsedFilter

	viewer      model.User
	rateLimit   model.RateLimit
	lastRefresh time.Time
	// lastErr is always recomputed by recomputeLastErr (panicErr if set,
	// else viewerLastErr if set, else the first section with a standing
	// error, else nil) rather than assigned directly from whichever fetch
	// happens to dispatch last: independent, uncoordinated event streams
	// (a recovered goroutine panic, the viewer fetch, each section's
	// fetch) can otherwise race, so an unrelated success clearing a
	// genuine standing error (or vice versa) would depend on dispatch
	// order instead of being deterministic.
	lastErr error
	// viewerLastErr is the viewer fetch's own standing error, tracked
	// separately from lastErr for the reason above; see
	// applyViewerResult and recomputeLastErr.
	viewerLastErr error
	// panicErr is set by recoverGoroutine when a store-started goroutine
	// (for example StartAutoRefresh's ticker) panics with no more
	// specific error source of its own to attribute it to. It takes
	// priority over every other source in recomputeLastErr precisely
	// because a panic is not attributable to one section or the viewer,
	// and outranks a routine fetch error in severity; it is cleared only
	// when the next list generation starts (startList), not by an
	// unrelated fetch succeeding in the meantime.
	panicErr error

	// viewerFetchInFlight is true while a Viewer network call is running,
	// so startList never starts a second, redundant one while retrying an
	// unresolved login (see fetchViewer and startList).
	viewerFetchInFlight bool
	// viewerConfirmed is true once the viewer has been resolved by the
	// network at least once in this session. Before that, s.viewer may
	// only be a value seeded from a previous run's cache (see
	// applyCachedViewer), which could belong to a different account
	// (after "gh auth switch") than the one that will actually resolve;
	// section cache writes are gated on this (see cacheSectionPage) so
	// they are never attributed to a login that turns out to be wrong.
	viewerConfirmed bool
	// cacheWarned is set the first time a cache Get/Put call fails, so
	// that failure is logged (see warnCacheError) only once per Store
	// rather than on every subsequent fetch.
	cacheWarned bool
	// warnedMessages tracks which (section, warning message) pairs from
	// gh.SearchResult.Warnings have already been logged, so a persistent
	// per-page warning (for example a permission error that recurs on
	// every refresh) is logged only once rather than on every fetch.
	warnedMessages map[warnedKey]struct{}
	// lastInvalidStateFilter is the last "state:" filter value that was
	// neither a valid state nor a prefix of one, so SetFilter emits
	// EventError for it only once while it persists unchanged (see
	// SetFilter).
	lastInvalidStateFilter string

	generation int
	baseCtx    context.Context
	listCtx    context.Context
	cancelList context.CancelFunc

	// pendingPageOne and generationHadError track one list generation's
	// progress toward a full refresh: pendingPageOne starts at
	// len(sections) and counts down as each section's page-1 fetch (not
	// LoadMore, not a refresh-chain page) resolves; generationHadError
	// latches true if any of them failed. LastRefresh is only updated
	// once pendingPageOne reaches 0 with generationHadError still false —
	// see LastRefresh's doc comment.
	pendingPageOne     int
	generationHadError bool

	inFlight map[inFlightKey]struct{}

	subscribers []func(Event)
}

// warnedKey identifies one (section, message) pair for warnedMessages.
type warnedKey struct {
	section string
	message string
}

// inFlightKey identifies one in-flight fetch, so a duplicate request for
// the same section and page is never started twice.
type inFlightKey struct {
	section int
	cursor  string
}

// New builds a Store from deps. It does not start any network activity;
// call Start to load the viewer and the first page of every section.
func New(deps Deps) *Store {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}

	state := deps.Config.List.State
	s := &Store{
		deps:           deps,
		state:          state,
		sections:       buildSections(deps.Config, state),
		inFlight:       make(map[inFlightKey]struct{}),
		warnedMessages: make(map[warnedKey]struct{}),
		baseCtx:        context.Background(),
	}
	return s
}

// buildSections constructs the store's section list: the four built-ins
// followed by cfg's custom sections, in that fixed priority order.
func buildSections(cfg config.Config, state string) []*sectionState {
	sections := make([]*sectionState, 0, len(builtinSections)+len(cfg.List.Sections))
	for _, b := range builtinSections {
		sec := &sectionState{kind: b.kind, name: b.name}
		sec.query = buildSectionQuery(sec, state)
		sections = append(sections, sec)
	}
	for _, custom := range cfg.List.Sections {
		sec := &sectionState{kind: model.SectionKindCustom, name: custom.Name, custom: custom.Query}
		sec.query = buildSectionQuery(sec, state)
		sections = append(sections, sec)
	}
	return sections
}

// buildSectionQuery resolves sec's current GitHub search query against
// state. Shared by buildSections (initial construction) and
// rebuildSectionQueries (after a "state:" filter change) so the two never
// drift apart.
func buildSectionQuery(sec *sectionState, state string) string {
	return gh.BuildSearchQuery(sec.kind, sec.custom, state)
}

// Sections returns the store's sections in display/priority order.
func (s *Store) Sections() []model.Section {
	out := make([]model.Section, len(s.sections))
	for i, sec := range s.sections {
		out[i] = sec.section()
	}
	return out
}

// SectionState is a snapshot of one section's live state, as returned by
// Store.SectionStates. It exists so the UI can show per-section fetch
// status and data-quality warnings (see gh.SearchResult.Warnings)
// without exposing the mutable *sectionState the store keeps internally.
type SectionState struct {
	Section  model.Section
	Loading  bool
	Stale    bool
	HasNext  bool
	Total    int
	Err      error
	Warnings []string
}

// SectionStates returns a snapshot of every section's current state, in
// Sections' priority order. Warnings surfaces gh.SearchResult.Warnings
// from each section's most recent successful fetch (for example a
// permission error affecting one reviewer that did not fail the whole
// page): those warnings are logged once each (see applyFetchResult) but,
// since the ":messages" ring buffer keeps only slog.LevelError and above,
// a Warn-level log entry never reaches it — SectionStates is how the UI is
// meant to surface them instead.
func (s *Store) SectionStates() []SectionState {
	out := make([]SectionState, len(s.sections))
	for i, sec := range s.sections {
		out[i] = SectionState{
			Section:  sec.section(),
			Loading:  sec.loading,
			Stale:    sec.stale,
			HasNext:  sec.hasNext,
			Total:    sec.total,
			Err:      sec.lastErr,
			Warnings: append([]string(nil), sec.warnings...),
		}
	}
	return out
}

// Viewer returns the authenticated user, as loaded by Start.
func (s *Store) Viewer() model.User {
	return s.viewer
}

// RateLimit returns the GraphQL rate limit reported by the most recent
// successful request.
func (s *Store) RateLimit() model.RateLimit {
	return s.rateLimit
}

// LastRefresh returns when every section last had its page-1 fetch
// succeed together, as one list generation (see startList): a generation
// where even one section's page-1 fetch failed does not update it, and
// neither does LoadMore or an automatic refresh-chain page (both fetch
// pages beyond page 1). The zero value means no generation has fully
// succeeded yet.
func (s *Store) LastRefresh() time.Time {
	return s.lastRefresh
}

// Loading reports whether any section currently has a fetch in flight.
func (s *Store) Loading() bool {
	for _, sec := range s.sections {
		if sec.loading {
			return true
		}
	}
	return false
}

// LastError returns the most recently observed error, or nil.
func (s *Store) LastError() error {
	return s.lastErr
}
