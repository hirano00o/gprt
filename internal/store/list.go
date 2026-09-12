package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// viewerPlaceholderLogin is the login segment used to cache the viewer
// lookup itself, since the real login is not known until that lookup
// completes (see cachedViewer and Start).
const viewerPlaceholderLogin = "-"

// cachedViewer is the on-disk shape of the cached viewer lookup.
type cachedViewer struct {
	User      model.User      `json:"user"`
	RateLimit model.RateLimit `json:"rate_limit"`
}

// Start begins loading the store's data. It first applies any previously
// cached viewer synchronously (a local read, not a network call) so the
// real login is available for the section cache reads LoadList is about to
// do; it then starts a network refresh of the viewer and, without waiting
// for it, loads the first page of every section.
func (s *Store) Start(ctx context.Context) {
	s.baseCtx = ctx
	s.applyCachedViewer()
	s.fetchViewer(ctx)
	s.LoadList(false)
}

// Stop cancels every in-flight list fetch, the current pull request's
// in-flight detail fetch, and its files fetch/highlight pool, if any
// (unlike ClosePR, it leaves current/currentPR/DetailState/Files/
// FilesState untouched — Stop is about halting background work, not
// closing the pull request view). It does not wait for the goroutines it
// cancels to exit: per docs/DESIGN.md's concurrency rules, nothing may
// block on a store-started goroutine after the caller decides to stop.
func (s *Store) Stop() {
	if s.cancelList != nil {
		s.cancelList()
	}
	if s.detailCancel != nil {
		s.detailCancel()
	}
	if s.filesCancel != nil {
		// filesCancel's ctx is shared by the page-fetch chain and the
		// highlight pool for the current files generation (see
		// startFilesFetch/startHighlightPool), so this one call stops
		// both.
		s.filesCancel()
	}
	if s.mutationCancel != nil {
		// Cancels whichever mutation is currently running, if any (see
		// mutations.go: finishMutation treats a cancelled mutation's
		// result as silent, matching every other fetch's own
		// cancellation handling). Every mutation not yet started is
		// dropped outright: nothing has been sent to GitHub for them yet,
		// so there is nothing to invalidate or apply.
		s.mutationCancel()
	}
	s.mutationQueue = nil
}

// applyCachedViewer synchronously applies the previously cached viewer, if
// any. It never touches the network: cache.Store.Get is a local disk/memory
// read. The applied viewer is not marked confirmed: it may belong to a
// different account than the one that will actually resolve over the
// network (for example after "gh auth switch"), which fetchViewer's result
// handler checks for once the real login is known.
func (s *Store) applyCachedViewer() {
	key, err := cache.SearchKey(s.deps.Host, viewerPlaceholderLogin, "viewer")
	if err != nil {
		return
	}
	entry, ok, err := s.deps.Cache.Get(key)
	if err != nil {
		s.warnCacheError(err)
		return
	}
	if !ok {
		return
	}
	var cached cachedViewer
	if json.Unmarshal(entry.Body, &cached) != nil {
		return
	}
	s.viewer = cached.User
	s.setRateLimit(cached.RateLimit)
	s.emit(Event{Kind: EventViewerLoaded})
}

// fetchViewer starts a background fetch of the authenticated user.
// viewerFetchInFlight is set synchronously (before the goroutine starts)
// so a concurrent startList never starts a second, redundant fetch while
// retrying an unresolved login (see startList).
func (s *Store) fetchViewer(ctx context.Context) {
	s.viewerFetchInFlight = true

	go func() {
		var user model.User
		var rl model.RateLimit
		var err error
		// A single deferred closure recovers a panic into err (rather
		// than delegating to the shared recoverGoroutine helper) so
		// that, panic or not, exactly one path applies the result:
		// without this, a panic inside GitHub.Viewer would skip the
		// dispatch entirely and leave viewerFetchInFlight stuck true
		// forever, permanently blocking startList's retry-when-unknown
		// logic below.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: viewer: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "viewer", "err", err)
			}
			// Computed from ctx, not from errors.Is(err,
			// context.Canceled/DeadlineExceeded): go-gh's underlying
			// http.Client{Timeout: ...} produces an error that also
			// satisfies errors.Is(err, context.DeadlineExceeded) on a
			// plain request timeout, unrelated to ctx ever being
			// cancelled. Checking ctx.Err() directly asks the only
			// question that matters here: did *we* give up on this
			// fetch (Stop, or ctx's deadline/cancellation), as opposed
			// to the request itself timing out on an otherwise-healthy
			// ctx, which is a real failure worth reporting.
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyViewerResult(user, rl, err, cancelled)
			})
		}()
		user, rl, err = s.deps.GitHub.Viewer(ctx)
	}()
}

// applyViewerResult applies the result of a fetchViewer call. It runs only
// on the UI goroutine, from a Dispatch callback.
func (s *Store) applyViewerResult(user model.User, rl model.RateLimit, err error, cancelled bool) {
	s.viewerFetchInFlight = false

	if err != nil {
		if cancelled {
			// Our own cancellation (see fetchViewer's doc comment for
			// why this is ctx-based, not errors.Is-based) is not a
			// failure worth reporting.
			return
		}
		s.viewerLastErr = err
		s.recomputeLastErr()
		s.deps.Logger.Error("load viewer failed", "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}

	// A login seeded from a previous run's cache (see applyCachedViewer)
	// is only ever a guess. If the network now confirms a different,
	// real account (for example after "gh auth switch" between runs),
	// every section fetched under the guessed login during the
	// unconfirmed window may have shown or cached the wrong account's
	// data; discarding every section's items before a full,
	// cache-skipping reload guarantees nothing from the wrong account
	// lingers, rather than leaving stale cross-account rows on screen or
	// merged into the reload's result. A confirmed empty login is not
	// treated as "a real, different account": GitHub's viewer query never
	// legitimately returns one, so this only guards against a
	// misconfigured or faked GitHub implementation reporting a zero value
	// that would otherwise be indistinguishable from an actual account
	// switch.
	mismatch := !s.viewerConfirmed && s.viewer.Login != "" && user.Login != "" && s.viewer.Login != user.Login

	s.viewer = user
	s.setRateLimit(rl)
	s.viewerConfirmed = true
	s.viewerLastErr = nil
	s.recomputeLastErr()
	s.cacheViewer(user, rl)
	s.emit(Event{Kind: EventViewerLoaded})

	if mismatch {
		s.discardAllSectionItems()
		s.LoadList(true)
		if s.current != nil {
			// The pull request currently open may have been fetched (or
			// its cache read applied) under the seeded, now-confirmed-
			// wrong login: viewer-scoped fields (PendingReview,
			// ViewerCanUpdate/Close/Reopen/React) could belong to the
			// wrong account. Discard it immediately, the same way
			// discardAllSectionItems does for the list, rather than
			// leaving it on screen (even marked stale) until ReloadPR's
			// fetch completes.
			s.currentPR = nil
			s.detailFetchedAt = time.Time{}
			s.emit(Event{Kind: EventPRChanged})
			s.ReloadPR()
		}
	} else if s.current != nil {
		// The viewer login just became known (the common case: OpenPR
		// was called before Start's own viewer fetch resolved, so the
		// pull request's first fetch ran with an empty $viewer) or was
		// reconfirmed unchanged. Either way, refresh the open pull
		// request now that the login is trustworthy: its pending-review
		// lookup and viewer-scoped flags need the real login, not "" or
		// a stale guess.
		//
		// startDetailFetch is called directly here, not RefreshPR:
		// RefreshPR no-ops while a detail fetch is already in flight (by
		// design, so the auto-refresh ticker never duplicates one), but
		// an in-flight fetch at exactly this moment is very likely the
		// pull request's own first fetch — the one started under the
		// stale/empty login OpenPR saw before the viewer resolved, i.e.
		// precisely the fetch that needs restarting, not skipping.
		// startDetailFetch always cancels-and-restarts under a new
		// generation (the same way ReloadPR already does, unconditionally,
		// for the mismatch branch above), so the wrong-$viewer fetch's
		// eventual result is dropped by the generation check in
		// fetchDetail instead of being applied.
		s.startDetailFetch(false)
	}
}

// discardAllSectionItems clears every section's items and pagination
// state and notifies subscribers immediately, so the UI drops the rows
// without waiting for an unrelated event. Used only when a
// network-confirmed login turns out to differ from a seeded (unconfirmed)
// one: none of the currently held items can be trusted as belonging to the
// confirmed account, so there is nothing worth preserving across the
// reload that follows.
func (s *Store) discardAllSectionItems() {
	for _, sec := range s.sections {
		sec.items = nil
		sec.cursor = ""
		sec.hasNext = false
		// The wrong account's page depth must not be inherited: without
		// this, the new account's first page-1 fetch would see a
		// nonzero previousLoadedPages left over from the old account
		// and chain-fetch extra pages that have nothing to do with the
		// new account's actual result set.
		sec.loadedPages = 0
		sec.refreshTargetPages = 0
	}
	s.emit(Event{Kind: EventListChanged})
}

// cacheViewer persists the viewer lookup under the placeholder login. A
// cache-key or marshal failure is not fatal: caching is a best-effort
// optimisation, not required for correctness.
func (s *Store) cacheViewer(user model.User, rl model.RateLimit) {
	key, err := cache.SearchKey(s.deps.Host, viewerPlaceholderLogin, "viewer")
	if err != nil {
		return
	}
	body, err := json.Marshal(cachedViewer{User: user, RateLimit: rl})
	if err != nil {
		return
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
	}
}

// warnCacheError logs a cache Get/Put failure at Warn level, once per
// Store: repeating it on every subsequent fetch (stale-while-revalidate
// retries constantly) would spam the log without adding information once
// the cause is known. Get "no entry" misses are not errors and never reach
// this; only a genuine I/O failure (for example a permission error) does.
func (s *Store) warnCacheError(err error) {
	if s.cacheWarned {
		return
	}
	s.cacheWarned = true
	s.deps.Logger.Warn("cache read/write failed", "err", err)
}

// LoadList starts a full re-fetch of page 1 for every section. When force
// is false, each section's cached page-1 entry (if any) is applied
// immediately, marked Stale, before the network fetch starts, so the list
// has something to show without waiting on the network; force skips that
// cache read entirely (used by Reload).
func (s *Store) LoadList(force bool) {
	s.startList(!force)
}

// Refresh re-fetches page 1 for every section in the background. Unlike
// LoadList(false), it never re-applies a cached page: sections already
// hold their last-known items, and re-showing a stale on-disk snapshot on
// top of already-live data would be a visible regression, not an
// improvement. Unlike Reload, it does not invalidate the on-disk cache:
// a routine background refresh has no reason to believe the cache is
// wrong, only that it might be a few minutes old.
func (s *Store) Refresh() {
	s.startList(false)
}

// Reload discards any cached data and re-fetches page 1 of every section,
// and the current pull request's detail (if one is open), from the
// network only (used by the "R" key): it invalidates the on-disk search
// cache for the current login before starting the fetches, so a later
// LoadList's cache-first read can never resurrect the data Reload was
// asked to throw away. Marking every section stale first means the
// incoming page 1 replaces its items outright (see applyFetchResult)
// instead of automatically chaining fetches to restore a previous
// multi-page depth, the way a plain Refresh does: Reload is a deliberate,
// full reset, not a "keep everything, just re-verify it" refresh.
func (s *Store) Reload() {
	s.invalidateSearchCache()
	s.markSectionsStale()
	s.startList(false)
	s.ReloadPR()
}

// invalidateSearchCache removes every cached search/list entry for the
// current login. It is a no-op when the login is not yet known: there is
// nothing meaningful cached under an unknown login to invalidate.
func (s *Store) invalidateSearchCache() {
	if s.viewer.Login == "" {
		return
	}
	if err := s.deps.Cache.InvalidateSearch(s.deps.Host, s.viewer.Login); err != nil {
		s.deps.Logger.Warn("cache invalidate failed", "err", err)
	}
}

// startList is the shared implementation behind LoadList, Refresh, and
// Reload. It starts a new generation (dropping any result still in flight
// from a previous one) and, for every section, optionally shows a cached
// page 1 before starting a fresh network fetch.
func (s *Store) startList(showCache bool) {
	s.generation++
	gen := s.generation

	if s.cancelList != nil {
		s.cancelList()
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	s.listCtx = ctx
	s.cancelList = cancel

	// This generation's LastRefresh gate: see LastRefresh's doc comment
	// and applyFetchResult's page-1 completion tracking.
	s.pendingPageOne = len(s.sections)
	s.generationHadError = false

	// A panic recorded by recoverGoroutine is considered handled once a
	// new generation starts: starting a fresh list load is the store's
	// way of "trying again", so carrying the old panic forward would
	// keep reporting a problem the caller has already acted on.
	s.panicErr = nil
	s.recomputeLastErr()

	// Every previously registered in-flight fetch belongs to a superseded
	// generation and will be dropped at apply time regardless; clearing
	// the map here (rather than leaving stale entries to expire on their
	// own) lets this generation's fetches register and start immediately
	// instead of being dedup-blocked by an old, still-running fetch for
	// the same (section, cursor).
	s.inFlight = make(map[inFlightKey]struct{})

	// A transient failure (or an unlucky race with Start's own viewer
	// fetch) can leave the login unresolved indefinitely, which silently
	// disables every section's cache read/write (see
	// applyCachedFirstPage and cacheSectionPage); retry it here, guarded
	// by viewerFetchInFlight so this never duplicates a fetch already
	// running. Uses s.baseCtx, not this generation's ctx: the retry should
	// outlive the list generation that happened to trigger it, so the
	// *next* startList call (a new generation, which cancels ctx) does
	// not cancel an otherwise-healthy viewer retry and turn it into a
	// spurious error.
	if s.viewer.Login == "" && !s.viewerFetchInFlight {
		s.fetchViewer(s.baseCtx)
	}

	for i, sec := range s.sections {
		sec.cursor = ""
		sec.hasNext = false
		// If a previous generation's refresh-chain was still in
		// progress (interrupted by this new generation before it
		// finished restoring the section's depth), loadedPages only
		// reflects the mid-chain page count, not the depth the section
		// actually had before that refresh started. Restoring it from
		// the abandoned target here means this generation's own page-1
		// apply computes the correct previousLoadedPages and re-chains
		// to the right depth, instead of the interrupted chain's
		// smaller, in-progress count silently becoming permanent.
		if sec.refreshTargetPages > sec.loadedPages {
			sec.loadedPages = sec.refreshTargetPages
		}
		sec.refreshTargetPages = 0
		if showCache {
			s.applyCachedFirstPage(i)
		}
		s.fetchSection(ctx, gen, i, "")
	}
}

// applyCachedFirstPage synchronously applies section i's cached page-1
// entry, if any, marking it Stale until the network fetch confirms it.
func (s *Store) applyCachedFirstPage(i int) {
	sec := s.sections[i]
	if s.viewer.Login == "" {
		s.deps.Logger.Debug("cache read skipped: viewer login unknown", "section", sec.name)
		return
	}
	key, err := cache.SearchKey(s.deps.Host, s.viewer.Login, sec.query+"\x00")
	if err != nil {
		return
	}
	entry, ok, err := s.deps.Cache.Get(key)
	if err != nil {
		s.warnCacheError(err)
		return
	}
	if !ok {
		return
	}
	var cached gh.SearchResult
	if json.Unmarshal(entry.Body, &cached) != nil {
		return
	}
	sec.items = cached.Items
	sec.cursor = cached.EndCursor
	sec.hasNext = cached.HasNextPage
	sec.total = cached.TotalCount
	sec.stale = true
	s.emit(Event{Kind: EventListChanged})
}

// LoadMore fetches the next page of section sectionIndex, appending it to
// the section's items. It is a no-op when the section has no next page or
// already has a fetch in flight.
func (s *Store) LoadMore(sectionIndex int) {
	sec := s.sections[sectionIndex]
	if !sec.hasNext || sec.loading {
		return
	}
	s.fetchSection(s.listCtx, s.generation, sectionIndex, sec.cursor)
}

// fetchSection starts a background fetch of section i's page at cursor,
// tagged with generation gen. A duplicate call for the same (section,
// cursor) while one is already in flight is a no-op.
func (s *Store) fetchSection(ctx context.Context, gen, i int, cursor string) {
	key := inFlightKey{section: i, cursor: cursor}
	if _, dup := s.inFlight[key]; dup {
		return
	}
	s.inFlight[key] = struct{}{}

	sec := s.sections[i]
	sec.loading = true
	s.emit(Event{Kind: EventLoadingChanged})

	query := sec.query
	go func() {
		var res gh.SearchResult
		var err error
		// A single deferred closure recovers a panic into err and
		// always dispatches the same apply path (rather than
		// delegating to the shared recoverGoroutine helper, which
		// would skip unregistering key and applying the result
		// entirely on a panic): without this, a panic inside
		// GitHub.SearchPullRequests would leave sec.loading stuck true
		// and key registered in s.inFlight forever, permanently
		// blocking Loading() and any future fetch for this
		// (section, cursor).
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: list: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "list", "err", err)
			}
			// Computed from ctx, not from errors.Is(err,
			// context.Canceled/DeadlineExceeded): go-gh's underlying
			// http.Client{Timeout: ...} produces an error that also
			// satisfies errors.Is(err, context.DeadlineExceeded) on a
			// plain request timeout, unrelated to ctx ever being
			// cancelled. Checking ctx.Err() directly asks the only
			// question that matters here: did *we* give up on this
			// fetch (Stop, or a superseded generation cancelling ctx),
			// as opposed to the request itself timing out on an
			// otherwise-healthy ctx, which is a real failure worth
			// reporting.
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				// The in-flight key is only cleared once this
				// result is known to belong to the current
				// generation: unregistering it unconditionally
				// would let a late, superseded generation's
				// dispatch erase the *current* generation's own
				// in-flight entry for the same key (if one
				// happens to be running), opening a window where
				// a duplicate fetch for that key would no longer
				// be deduped.
				if gen != s.generation {
					return
				}
				delete(s.inFlight, key)
				s.applyFetchResult(gen, i, cursor, res, err, cancelled)
			})
		}()
		res, err = s.deps.GitHub.SearchPullRequests(ctx, query, cursor)
	}()
}

// applyFetchResult applies (or reports the failure of) one section's fetch
// result. It runs only on the UI goroutine, from a Dispatch callback.
// gen is the generation this fetch was started under, and cancelled
// reports whether ctx.Err() was non-nil when the fetch returned (see
// fetchSection's doc comment for why that, rather than
// errors.Is(err, context.Canceled/DeadlineExceeded), is the right test).
func (s *Store) applyFetchResult(gen, i int, cursor string, res gh.SearchResult, err error, cancelled bool) {
	sec := s.sections[i]
	sec.loading = false
	isPageOne := cursor == ""

	if err != nil {
		if cancelled {
			// Our own cancellation (Stop, or a superseded generation's
			// context being cancelled by the next startList call) is
			// not a failure worth reporting: the fetch simply never
			// got a chance to finish, and a fresh one will normally
			// follow right behind it (a new generation) or the store
			// is shutting down (Stop) and nothing will observe
			// lastErr anyway. Loading/in-flight state still needs
			// clearing either way, which happened above/below.
			s.emit(Event{Kind: EventLoadingChanged})
			return
		}
		sec.lastErr = err
		s.recomputeLastErr()
		sec.refreshTargetPages = 0 // stop any in-progress refresh-chain
		if isPageOne {
			s.notePageOneOutcome(gen, false)
		}
		secModel := sec.section()
		s.deps.Logger.Error("load section failed", "section", sec.name, "err", err)
		s.emit(Event{Kind: EventError, Section: &secModel, Err: err})
		s.emit(Event{Kind: EventLoadingChanged})
		return
	}

	if isPageOne {
		// Page 1 (or the whole result set, when !res.HasNextPage) is
		// always authoritative on its own: replacing outright, rather
		// than grafting old items onto it, is what makes a PR that
		// disappeared from the search results actually disappear from
		// the list instead of lingering forever. If this section had
		// more pages loaded before a *non-stale* refresh (one that
		// is not just confirming a cache-shown preview for the first
		// time — see applyCachedFirstPage), automatically chain
		// fetches for pages 2..N below to restore that depth against
		// fresh data, rather than either dropping back to 50 rows or
		// merging in possibly-stale leftovers.
		previousLoadedPages := sec.loadedPages
		wasStale := sec.stale
		sec.items = res.Items
		sec.loadedPages = 1
		if !wasStale && res.HasNextPage && previousLoadedPages > 1 {
			sec.refreshTargetPages = previousLoadedPages
		} else {
			sec.refreshTargetPages = 0
		}
		s.cacheSectionPage(sec, res)
		s.notePageOneOutcome(gen, true)
	} else {
		// LoadMore, or an automatic refresh-chain page: dedupe against
		// what is already held, since an overlap between page
		// boundaries (GitHub's ranking can shift between requests, or
		// a chained re-fetch can legitimately re-see an item) must not
		// duplicate a row.
		sec.items = appendDedup(sec.items, res.Items)
		sec.loadedPages++
	}
	sec.cursor = res.EndCursor
	sec.hasNext = res.HasNextPage
	sec.total = res.TotalCount
	sec.stale = false
	sec.lastErr = nil
	s.recomputeLastErr()
	if isPageOne {
		// Page 1 is authoritative for warnings the same way it is for
		// items: a clean re-fetch means whatever page 1 previously
		// warned about is resolved (or at least not being reported
		// anymore), so the slate is wiped rather than accumulating
		// forever.
		sec.warnings = res.Warnings
	} else {
		// A later page (LoadMore or a refresh-chain page) only adds to
		// what page 1 already reported: overwriting here would erase
		// page 1's warnings the moment a clean chained page 2 applies,
		// even though nothing about page 1's data quality changed.
		sec.warnings = appendNewWarnings(sec.warnings, res.Warnings)
	}
	s.logNewWarnings(sec, res.Warnings)

	s.setRateLimit(res.RateLimit)

	s.emit(Event{Kind: EventListChanged})
	s.emit(Event{Kind: EventLoadingChanged})

	if sec.refreshTargetPages > 0 && sec.loadedPages < sec.refreshTargetPages && sec.hasNext {
		s.fetchSection(s.listCtx, s.generation, i, sec.cursor)
	} else {
		sec.refreshTargetPages = 0
	}
}

// recomputeLastErr sets lastErr to viewerLastErr if it is set, otherwise to
// mutationErr, otherwise to detailErr, otherwise to filesErr, otherwise to
// the first section (in priority order) with a standing error, otherwise
// nil. It is called after every viewer, mutation, detail, files, or
// section fetch outcome instead of assigning lastErr directly, so a
// success clears it (or an error sets it) as a pure function of the
// current, persistent per-source error state rather than "whichever
// dispatch happened to run last": the viewer fetch and each section's
// fetch are independent, uncoordinated goroutines, so without this an
// unrelated success (say, the viewer's own retry-fetch, which shares the
// same dispatch channel as every section's fetch) could race with and
// silently clear a genuine, still-standing error from a different source
// purely because of dispatch ordering.
func (s *Store) recomputeLastErr() {
	if s.panicErr != nil {
		s.lastErr = s.panicErr
		return
	}
	if s.viewerLastErr != nil {
		s.lastErr = s.viewerLastErr
		return
	}
	if s.mutationErr != nil {
		// Ranked above detailErr/filesErr/section errors: a failed
		// mutation is a direct result of something the user just did
		// (send a comment, edit, delete), so it is more immediately
		// relevant than a routine background fetch's standing error.
		s.lastErr = s.mutationErr
		return
	}
	if s.detailErr != nil {
		// Ranked above files/section errors: the current pull request's
		// detail is whatever the user is actively looking at, so its own
		// standing error is more immediately relevant than an unrelated
		// list section's or its own files list's.
		s.lastErr = s.detailErr
		return
	}
	if s.filesErr != nil {
		// Ranked above section errors for the same reason detailErr is:
		// still scoped to the pull request the user has open, just one
		// step further into it (the Files tab) than the detail itself.
		s.lastErr = s.filesErr
		return
	}
	for _, sec := range s.sections {
		if sec.lastErr != nil {
			s.lastErr = sec.lastErr
			return
		}
	}
	s.lastErr = nil
}

// notePageOneOutcome records that section i's page-1 fetch resolved
// (successfully or not) within generation gen, updating LastRefresh once
// every section's page-1 fetch for that generation has resolved
// successfully (see LastRefresh's doc comment). A result from a
// superseded generation is ignored: it was already dropped before
// applyFetchResult ever ran, so gen == s.generation always holds here, but
// the check is kept as a cheap, explicit guard against that invariant ever
// being violated by a future change.
func (s *Store) notePageOneOutcome(gen int, success bool) {
	if gen != s.generation || s.pendingPageOne <= 0 {
		return
	}
	if !success {
		s.generationHadError = true
	}
	s.pendingPageOne--
	if s.pendingPageOne == 0 && !s.generationHadError {
		s.lastRefresh = s.deps.Now()
	}
}

// logNewWarnings logs each of sec's warnings not already logged for sec,
// at Warn level, once per (section, message) pair for the lifetime of the
// Store: a permission error affecting one item recurs on every refresh,
// and logging it again on every single fetch would spam the log without
// adding information.
func (s *Store) logNewWarnings(sec *sectionState, warnings []string) {
	for _, msg := range warnings {
		key := warnedKey{section: sec.name, message: msg}
		if _, seen := s.warnedMessages[key]; seen {
			continue
		}
		s.warnedMessages[key] = struct{}{}
		s.deps.Logger.Warn("search result warning", "section", sec.name, "message", msg)
	}
}

// appendDedup appends newItems to existing, skipping any whose ID is
// already present.
func appendDedup(existing, newItems []model.PullRequest) []model.PullRequest {
	seen := make(map[string]struct{}, len(existing))
	for _, pr := range existing {
		seen[pr.ID] = struct{}{}
	}

	merged := existing
	for _, pr := range newItems {
		if _, dup := seen[pr.ID]; dup {
			continue
		}
		seen[pr.ID] = struct{}{}
		merged = append(merged, pr)
	}
	return merged
}

// appendNewWarnings appends newWarnings to existing, skipping any exact
// duplicate string already present. Used when a later page's own warnings
// (if any) are added on top of an earlier page's, rather than replacing
// them (see applyFetchResult).
func appendNewWarnings(existing, newWarnings []string) []string {
	if len(newWarnings) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, w := range existing {
		seen[w] = struct{}{}
	}

	merged := existing
	for _, w := range newWarnings {
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		merged = append(merged, w)
	}
	return merged
}

// cacheSectionPage persists section's page-1 result (the only page
// applyCachedFirstPage ever reads back). Writing is skipped until the
// viewer has been network-confirmed: before that, sec's login-scoped cache
// key (s.viewer.Login) may be seeded from a previous run and could belong
// to a different account than the one that will actually resolve (see
// applyViewerResult), and writing under the wrong login would pollute that
// account's on-disk cache.
func (s *Store) cacheSectionPage(sec *sectionState, res gh.SearchResult) {
	if !s.viewerConfirmed {
		s.deps.Logger.Debug("cache write skipped: viewer login not yet confirmed", "section", sec.name)
		return
	}
	if s.viewer.Login == "" {
		s.deps.Logger.Debug("cache write skipped: viewer login unknown", "section", sec.name)
		return
	}
	key, err := cache.SearchKey(s.deps.Host, s.viewer.Login, sec.query+"\x00")
	if err != nil {
		return
	}
	body, err := json.Marshal(res)
	if err != nil {
		return
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
	}
}

// recoverGoroutine must be deferred directly (defer s.recoverGoroutine(op),
// never wrapped in a closure) so that recover, called in its body, is
// "called directly by a deferred function" as the language requires. Used
// by goroutines (StartAutoRefresh's ticker) that have no result of their
// own to apply on a panic; fetchSection and fetchViewer recover inline
// instead, since they must always dispatch their (possibly panic-derived)
// result through the same single path — see their doc comments.
func (s *Store) recoverGoroutine(op string) {
	r := recover()
	if r == nil {
		return
	}
	err := fmt.Errorf("store: %s: panic: %v", op, r)
	s.deps.Logger.Error("goroutine panic", "op", op, "err", err)
	s.deps.Dispatch(func() {
		s.panicErr = err
		s.recomputeLastErr()
		s.emit(Event{Kind: EventError, Err: err})
	})
}

// Rows returns the current PR list as display rows: items are
// deduplicated across sections by PR ID (keeping the section that appears
// first in Sections' priority order), the current filter is applied,
// remaining items in each section are sorted by UpdatedAt descending, and
// a section with no items left after filtering is omitted entirely
// (including its heading).
func (s *Store) Rows() []Row {
	claimedBy := make(map[string]int)
	for i, sec := range s.sections {
		for _, it := range sec.items {
			if _, ok := claimedBy[it.ID]; !ok {
				claimedBy[it.ID] = i
			}
		}
	}

	var rows []Row
	for i, sec := range s.sections {
		secModel := sec.section()

		kept := make([]model.PullRequest, 0, len(sec.items))
		for _, it := range sec.items {
			if claimedBy[it.ID] != i {
				continue
			}
			if !s.filter.matches(it, secModel) {
				continue
			}
			kept = append(kept, it)
		}
		if len(kept) == 0 {
			continue
		}

		sort.SliceStable(kept, func(a, b int) bool {
			return kept[a].UpdatedAt.After(kept[b].UpdatedAt)
		})

		rows = append(rows, Row{Kind: RowHeader, Section: secModel})
		for idx := range kept {
			pr := kept[idx]
			rows = append(rows, Row{
				Kind:    RowItem,
				Section: secModel,
				Item:    model.ListItem{PR: &pr, Section: secModel},
				Stale:   sec.stale,
			})
		}
	}
	return rows
}
