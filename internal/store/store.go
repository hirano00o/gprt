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
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/model"
)

// GitHub is the subset of *gh.Client the store depends on, so tests can
// substitute a fake without touching the network.
type GitHub interface {
	Viewer(ctx context.Context) (model.User, model.RateLimit, error)
	SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error)
	PullRequest(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error)
	ChangedFiles(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error)
	AddIssueComment(ctx context.Context, subjectID, body string) (model.IssueComment, model.RateLimit, error)
	UpdateIssueComment(ctx context.Context, id, body string) (model.IssueComment, model.RateLimit, error)
	DeleteIssueComment(ctx context.Context, id string) (model.RateLimit, error)

	// Review mutations (see review.go). CreatePendingReview/AddReviewNow/
	// AddReviewNowWithEvent all wrap GraphQL's addPullRequestReview: the
	// first two mirror gh.Client's own split (create a PENDING review with
	// no event; publish threads immediately with event: COMMENT), and the
	// third covers the store's SubmitReview when no pending review exists
	// yet (an arbitrary event, no threads).
	CreatePendingReview(ctx context.Context, prID string) (model.Review, model.RateLimit, error)
	AddReviewNow(ctx context.Context, prID string, threads []gh.DraftThread, body string) (model.Review, model.RateLimit, error)
	AddReviewNowWithEvent(ctx context.Context, prID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error)
	AddReviewThread(ctx context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error)
	AddThreadReply(ctx context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error)
	SubmitReview(ctx context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error)
	DeletePendingReview(ctx context.Context, reviewID string) (model.RateLimit, error)
	UpdateReviewComment(ctx context.Context, id, body string) (model.ReviewComment, model.RateLimit, error)
	DeleteReviewComment(ctx context.Context, id string) (model.RateLimit, error)
	ResolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error)
	UnresolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error)

	// Reaction mutations (see reaction.go): AddReaction/RemoveReaction both
	// return the reacted-on subject's full, refreshed set of reaction
	// groups, mirroring gh.Client's own signatures.
	AddReaction(ctx context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, model.RateLimit, error)
	RemoveReaction(ctx context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, model.RateLimit, error)

	// MentionableUsers returns a repository's mentionable users (see
	// mentionable.go), the store's `@`-mention autocomplete candidate
	// source.
	MentionableUsers(ctx context.Context, repo model.RepoRef, query string, first int) ([]model.User, model.RateLimit, error)

	// Repository metadata reads (see repository.go): loaded together by
	// EnsureRepositoryMetadata to drive the edit/merge/create-PR forms.
	Repository(ctx context.Context, repo model.RepoRef) (model.RepositoryInfo, model.RateLimit, error)
	Labels(ctx context.Context, repo model.RepoRef) ([]model.Label, model.RateLimit, error)
	PullRequestTemplates(ctx context.Context, repo model.RepoRef) ([]model.PullRequestTemplate, model.RateLimit, error)

	// ViewerRepositories returns the viewer's own repositories (see
	// viewer_repositories.go), the create-pull-request repository picker's
	// candidate list.
	ViewerRepositories(ctx context.Context, first int) ([]model.RepositorySummary, model.RateLimit, error)

	// Branches/Teams are on-demand, query-filtered reads (see search.go's
	// SearchBranches/SearchTeams) backing the base/head branch picker and
	// the reviewer picker's team candidates.
	Branches(ctx context.Context, repo model.RepoRef, query string, first int) ([]model.Branch, model.RateLimit, error)
	Teams(ctx context.Context, org, query string, first int) ([]model.Team, model.RateLimit, error)

	// Pull request edit/merge/create mutations (see pr_edit.go, pr_create.go).
	UpdatePullRequest(ctx context.Context, id string, in gh.UpdatePullRequestInput) (model.PullRequest, model.RateLimit, error)
	RequestReviewers(ctx context.Context, id string, userIDs, teamIDs []string, union bool) ([]model.Reviewer, model.RateLimit, error)
	MarkReadyForReview(ctx context.Context, id string) (bool, model.RateLimit, error)
	ConvertToDraft(ctx context.Context, id string) (bool, model.RateLimit, error)
	MergePullRequest(
		ctx context.Context, id string, method model.MergeMethod, commitHeadline, commitBody *string, expectedHeadOID string,
	) (model.PullRequest, model.RateLimit, error)
	ClosePullRequest(ctx context.Context, id string) (model.PRState, model.RateLimit, error)
	ReopenPullRequest(ctx context.Context, id string) (model.PRState, model.RateLimit, error)
	CreatePullRequest(ctx context.Context, in gh.CreatePullRequestInput) (model.PullRequest, model.RateLimit, error)
}

// lineHighlighter is the subset of *highlight.Highlighter the store depends
// on for hunk highlighting (see the highlighter field's doc comment for why
// this is an interface rather than the concrete type directly).
type lineHighlighter interface {
	Lines(path string, lines []string) ([][]highlight.Token, error)
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

	// current is the pull request reference opened by OpenPR, or nil when
	// none is open. currentPR is its most recently applied detail (from
	// cache or the network); it is not cleared merely because a later
	// fetch fails (see applyDetailResult), only by OpenPR switching to a
	// different pull request or by ClosePR.
	current   *model.PRRef
	currentPR *model.PullRequest

	// detailLoading, detailStale, detailErr, detailWarnings, and
	// detailFetchedAt mirror sectionState's loading/stale/lastErr/
	// warnings fields, but for the single "current" pull request rather
	// than a list section — see DetailState.
	detailLoading   bool
	detailStale     bool
	detailErr       error
	detailWarnings  []string
	detailFetchedAt time.Time

	// detailGen is the current PR detail's generation token: it
	// increments on every OpenPR/RefreshPR/ReloadPR/ClosePR call, and a
	// dispatched fetch result whose token no longer matches is dropped
	// (see fetchDetail). detailCancel cancels the in-flight fetch tagged
	// with that generation (unlike the list's listCtx, there is no
	// detailCtx field: nothing in this slice needs to read the context
	// itself back out — fetchDetail's own ctx parameter, captured in its
	// closure, is the only reader — only cancel it, so only the
	// CancelFunc is kept). detailFetchInFlight is the (single-slot)
	// in-flight dedup flag required by docs/DESIGN.md's concurrency rule
	// 5 — there is only ever one "current" pull request, so a map keyed
	// like sections' inFlight is unnecessary.
	detailGen           int
	detailCancel        context.CancelFunc
	detailFetchInFlight bool

	// Files: the open pull request's changed files (M2, data side only —
	// see files.go). files holds every page fetched so far, in page
	// order; filesPageLens records each page's length within files so a
	// later page's own network result can replace exactly that page's
	// slice of files (see replaceFilesPage) without disturbing the
	// others. nextFileEntryID hands out the id every FileEntry appended or
	// replaced into files gets (see assignFileEntryIDs): a highlight job's
	// result is matched back to a file by this id, not by path, so a job
	// still in flight for an entry that replaceFilesPage has since
	// replaced (same path, new content) can never be mistaken for a
	// result belonging to its replacement.
	files            []FileEntry
	filesPageLens    []int
	nextFileEntryID  int
	filesLoading     bool
	filesStale       bool
	filesErr         error
	filesPagesLoaded int
	filesHasNext     bool
	// filesWarnings surfaces data-quality notes that do not fail a fetch
	// (today: a truncation/base-advance mismatch between the fully-paged
	// file count and the pull request's own ChangedFiles - see
	// checkFilesTruncation).
	filesWarnings []string
	// filesHeadOID and filesStarted together answer "is LoadFiles already
	// loaded/loading for the pull request's current head commit": a fresh
	// force-push (a new HeadOID) must not be satisfied by a no-op that
	// keeps showing the previous commit's diff. filesChangedFilesAt is the
	// pull request's own ChangedFiles count at the moment the current
	// files generation started, compared against a fresh detail fetch's
	// value the same way HeadOID is (see applyDetailResult): the count can
	// shift without HeadOID changing (for example a base-branch advance
	// GitHub recomputes the merge-base diff against), and that alone is
	// reason enough to reload.
	filesHeadOID        string
	filesChangedFilesAt int
	filesStarted        bool
	// filesWanted records that LoadFiles was called before the current
	// pull request's own detail had resolved (CurrentPR() still nil, so
	// there is no HeadOID yet to key a files generation on): rather than
	// silently no-op forever, applyDetailResult's success path starts the
	// deferred load once a HeadOID becomes known.
	filesWanted bool
	// filesShowCache is the showCache flag the current files generation
	// was started with (LoadFiles(false) shows cache first; force/reload
	// paths pass false): every sequential page fetch within one
	// generation reuses it, since fetchFilesPage's later calls to itself
	// (for page 2, 3, ...) happen from inside a Dispatch callback, not
	// from LoadFiles' own call site.
	filesShowCache     bool
	filesGen           int
	filesCtx           context.Context
	filesCancel        context.CancelFunc
	filesFetchInFlight bool

	// highlighter tokenises hunk lines for syntax colouring; it is safe
	// for concurrent use (see internal/highlight's own doc comments), so
	// one instance is shared across every files generation's worker pool.
	// Typed as the lineHighlighter interface (rather than the concrete
	// *highlight.Highlighter New builds it as) purely so a test can
	// substitute a fake that returns a controlled error, exercising
	// processHighlightJob's Debug-logging path deterministically without
	// depending on coaxing a real chroma lexer into failing.
	highlighter lineHighlighter
	// highlightJobs is the current files generation's mutex-protected,
	// UI-goroutine-facing pending queue (see highlightQueue's own doc
	// comment): enqueueHighlightJobs only ever pushes to it and never
	// blocks. It is nil when no files generation is active. The worker
	// pool's own bounded channel (what highlightFeeder actually drains
	// this queue into) is a local variable inside startHighlightPool, not
	// a Store field: nothing outside that function ever needs to read it
	// back. There is no separate cancel function for the pool: filesCancel's
	// ctx is shared by the page-fetch chain and the highlight pool for one
	// files generation, so cancelling it (resetFiles/Stop) stops both
	// together.
	highlightJobs *highlightQueue
	highlightWake chan struct{}
	// filesHighlightPending is the total count of hunk highlight jobs
	// still enqueued (in highlightJobs, in flight to a worker, or being
	// processed) across every file, surfaced as FilesState().Highlighting.
	filesHighlightPending int
	// filesPendingByID tracks, per FileEntry.id, how many of its hunks
	// still have no highlight result applied yet, so applyHighlightResult
	// knows when a file's FileEntry.Highlighted should flip to true.
	// Keyed by id rather than path so a page replacement (replaceFilesPage)
	// can drop a superseded entry's own leftover count precisely, without
	// disturbing its replacement's identically-pathed, freshly started one.
	filesPendingByID map[int]int

	// Mutations (see mutations.go): a single-flight FIFO queue of
	// comment mutations. mutationQueue holds every not-yet-started
	// mutation; at most one ever runs at a time (mutating tracks that),
	// and finishMutation starts the next queued one once the current
	// one's result has been applied. mutationCtx/mutationCancel are
	// created lazily (on the first enqueue) from baseCtx and shared by
	// every mutation for the lifetime of the Store: unlike the list's or
	// the current pull request's fetches, a mutation has no "generation"
	// of its own to supersede - only Stop cancels it, silently (see
	// finishMutation). mutationErr is this subsystem's own standing
	// error, folded into recomputeLastErr like viewerLastErr/detailErr/
	// filesErr.
	mutationQueue  []mutation
	mutating       bool
	mutationCtx    context.Context
	mutationCancel context.CancelFunc
	mutationErr    error

	// Mentionable users (see mentionable.go): a repository's mentionable
	// users, loaded lazily by OpenPR the first time a pull request in it is
	// opened, and kept per repository (mentionableEntry{users, fetchedAt,
	// loading, persisted}) for the Store's lifetime. Freshness — not a one-shot
	// "already tried" flag — decides whether a later OpenPR for the same
	// repository does any work at all: a fresh result short-circuits
	// immediately, but a repository a previous fetch failed for (fetchedAt
	// stays zero) or that has simply gone stale is retried the next time
	// OpenPR opens a pull request in it, or forced by ReloadPR. Fetches use
	// s.baseCtx directly rather than a dedicated, Stop-cancellable context:
	// loading dedups a repository's own concurrent fetches, and there is no
	// "generation" to supersede the way list/detail/files fetches have —
	// app shutdown cancelling baseCtx itself (see internal/ui/app.go's Run)
	// is enough.
	mentionable map[model.RepoRef]mentionableEntry

	// Repository metadata (see repository.go): a repository's
	// RepositoryInfo/Labels/PullRequestTemplates, loaded together, lazily,
	// only when EnsureRepositoryMetadata(repo) is explicitly called (the
	// UI calls it when an edit/merge/create-PR form opens — never
	// automatically on OpenPR, to avoid three extra queries on every PR
	// switch). Kept per repository for the Store's lifetime, following
	// mentionable's freshness+in-flight pattern.
	repoMetadata map[model.RepoRef]repoMetadataEntry

	// Viewer's own repositories (see viewer_repositories.go): the
	// create-pull-request repository picker's candidate list, loaded
	// lazily by EnsureViewerRepositories, following the same
	// freshness+in-flight pattern as repository metadata (a single,
	// non-keyed entry: there is only one viewer).
	viewerRepos          []model.RepositorySummary
	viewerReposFetchedAt time.Time
	viewerReposLoading   bool
	viewerReposPersisted bool

	// On-demand branch/team search (see search.go): SearchBranches/
	// SearchTeams back a query-filtered picker (base/head branch, review
	// team), delivering each result only to the caller's own callback
	// (not a Store-held result field or Event — see the package's own doc
	// comment for why). Each has its own generation token so a stale
	// answer for an abandoned keystroke's query is dropped rather than
	// applied over a newer one; results are not cached (see the same doc
	// comment).
	branchSearchGen int
	teamSearchGen   int

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
		highlighter:    highlight.New(highlight.Options{}),
		mentionable:    make(map[model.RepoRef]mentionableEntry),
		repoMetadata:   make(map[model.RepoRef]repoMetadataEntry),
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

// setRateLimit records rl as the current rate limit and emits
// EventRateLimitChanged, returning true, unless rl is unknown (GitHub
// Enterprise Server with rate limiting disabled reports null), in which
// case the standing value is kept and false is returned: every fetch and
// mutation goes through here so no path can replace a known value with
// "unknown".
func (s *Store) setRateLimit(rl model.RateLimit) bool {
	if !rl.Known {
		return false
	}
	s.rateLimit = rl
	s.emit(Event{Kind: EventRateLimitChanged})
	return true
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

// Loading reports whether any section, the current pull request's detail,
// or its changed files, currently has a fetch in flight.
func (s *Store) Loading() bool {
	if s.detailLoading || s.filesLoading {
		return true
	}
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
