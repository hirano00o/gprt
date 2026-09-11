package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// detailCacheRest is the Key.Rest segment PR detail entries are cached
// under (see cache.PRKey), distinguishing them from any other per-PR cache
// namespace a later milestone might add (for example changed files).
const detailCacheRest = "detail"

// DetailState is a snapshot of the current pull request's detail fetch
// status, as returned by Store.DetailState.
type DetailState struct {
	// Loading reports whether a detail fetch is currently in flight.
	Loading bool
	// Stale reports whether CurrentPR was served from cache and has not
	// yet been confirmed by a network response.
	Stale bool
	// Err is the most recent detail fetch's standing error, or nil.
	Err error
	// Warnings holds gh.DetailResult.Warnings from the most recent
	// successful fetch: data that could not be fully resolved, or a
	// paginated connection truncated at its limit.
	Warnings []string
	// FetchedAt is when CurrentPR's data was last confirmed by the
	// network (the zero value if it has only ever come from cache, or
	// never loaded at all).
	FetchedAt time.Time
}

// CurrentRef returns the pull request reference opened by the most recent
// OpenPR call, or (zero value, false) when none is open (before the first
// OpenPR, or after ClosePR).
func (s *Store) CurrentRef() (model.PRRef, bool) {
	if s.current == nil {
		return model.PRRef{}, false
	}
	return *s.current, true
}

// CurrentPR returns the currently open pull request's most recently
// applied detail (from cache or the network), or nil when none is open or
// none has been applied yet. A failed fetch never clears this: see
// applyDetailResult.
func (s *Store) CurrentPR() *model.PullRequest {
	return s.currentPR
}

// DetailState returns a snapshot of the current pull request's detail
// fetch status.
func (s *Store) DetailState() DetailState {
	return DetailState{
		Loading:   s.detailLoading,
		Stale:     s.detailStale,
		Err:       s.detailErr,
		Warnings:  append([]string(nil), s.detailWarnings...),
		FetchedAt: s.detailFetchedAt,
	}
}

// OpenPR sets ref as the current pull request: any previously open pull
// request's in-flight fetch is cancelled and its detail discarded (a
// stale detail for a *different* pull request must never be shown under
// the new ref). Stale and FetchedAt are reset to their zero values first
// (not just Err/Warnings/currentPR): ref's own cache read below sets Stale
// itself on a hit, but on a miss nothing else would, and it must not read
// as true (or FetchedAt as non-zero) purely because the *previous* pull
// request happened to leave it that way. Any cached detail for ref is then
// applied immediately, marked Stale, before a network fetch starts under a
// new generation.
func (s *Store) OpenPR(ref model.PRRef) {
	s.current = &ref
	s.currentPR = nil
	s.detailErr = nil
	s.detailWarnings = nil
	s.detailStale = false
	s.detailFetchedAt = time.Time{}
	// Recomputed immediately, not left to whenever the next fetch
	// resolves: without this, LastError() would keep reporting the
	// *previous* pull request's standing error for however long the new
	// one's fetch takes, even though nothing about the newly opened pull
	// request is actually wrong yet.
	s.recomputeLastErr()
	s.startDetailFetch(true)
	s.emit(Event{Kind: EventPRChanged})
}

// RefreshPR re-fetches the current pull request's detail in the
// background, without showing a cached snapshot first: like Refresh, the
// store already holds live data (if any), so re-showing an on-disk
// snapshot on top of it would be a visible regression, not an
// improvement. It never invalidates the on-disk cache: a routine
// background refresh has no reason to believe the cache is wrong.
//
// A no-op when no pull request is open, or when a detail fetch is already
// in flight: RefreshPR is called by the same auto-refresh ticker as
// Refresh, and a routine tick has no reason to duplicate an
// already-running, more specific fetch (OpenPR/ReloadPR).
func (s *Store) RefreshPR() {
	if s.current == nil || s.detailLoading {
		return
	}
	s.startDetailFetch(false)
}

// ReloadPR invalidates the on-disk cache entry for the current pull
// request, marks it stale (so the UI shows the existing detail as stale
// immediately, rather than looking frozen until the fetch lands), and
// re-fetches it from the network only (used by the "R" key, via Reload).
// A no-op when no pull request is open.
func (s *Store) ReloadPR() {
	if s.current == nil {
		return
	}
	if s.viewer.Login != "" {
		if err := s.deps.Cache.InvalidatePR(s.deps.Host, s.viewer.Login, *s.current); err != nil {
			s.deps.Logger.Warn("cache invalidate failed", "err", err)
		}
	}
	s.detailStale = true
	s.emit(Event{Kind: EventPRChanged})
	s.startDetailFetch(false)
}

// ClosePR clears the current pull request and cancels any in-flight
// detail fetch for it.
func (s *Store) ClosePR() {
	s.detailGen++ // supersedes any fetch still in flight
	if s.detailCancel != nil {
		s.detailCancel()
	}
	s.detailCancel = nil
	s.detailFetchInFlight = false

	s.current = nil
	s.currentPR = nil
	s.detailLoading = false
	s.detailStale = false
	s.detailErr = nil
	s.detailWarnings = nil
	s.detailFetchedAt = time.Time{}
	s.recomputeLastErr()

	s.emit(Event{Kind: EventPRChanged})
	// A fetch that was in flight is cancelled above, but its own
	// dispatched result — once it arrives — returns early at the
	// generation check (gen != s.detailGen, bumped just above) before
	// ever reaching applyDetailResult, so it never gets the chance to
	// emit its own loading-changed events for the loading→not-loading
	// transition ClosePR just performed. Emitting them here unconditionally
	// (matching the pattern of every other lifecycle method in this file)
	// keeps a UI driven purely by those events from showing a stuck
	// spinner.
	s.emitLoadingChanged()
}

// startDetailFetch starts a new detail-fetch generation for the current
// pull request: it cancels the previous generation's context, optionally
// applies a cached snapshot synchronously (showCache), and starts the
// network fetch. Shared by OpenPR, RefreshPR, and ReloadPR so the
// generation/cancellation bookkeeping lives in exactly one place, mirroring
// startList's role for the list.
func (s *Store) startDetailFetch(showCache bool) {
	s.detailGen++
	gen := s.detailGen

	if s.detailCancel != nil {
		s.detailCancel()
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	s.detailCancel = cancel
	// Every previously registered in-flight fetch belongs to a superseded
	// generation and will be dropped at apply time regardless (see
	// fetchDetail); clearing it here lets this generation's fetch start
	// immediately rather than being dedup-blocked.
	s.detailFetchInFlight = false

	if showCache {
		s.applyCachedDetail()
	}

	s.fetchDetail(ctx, gen)
}

// applyCachedDetail synchronously applies the current pull request's
// cached detail, if any. It never touches the network: cache.Store.Get is
// a local disk/memory read. A no-op when no pull request is open or the
// viewer's login is not yet known (there is nothing meaningful cached
// under an unknown login).
func (s *Store) applyCachedDetail() {
	if s.current == nil || s.viewer.Login == "" {
		return
	}
	key, err := cache.PRKey(s.deps.Host, s.viewer.Login, *s.current, detailCacheRest)
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
	var pr model.PullRequest
	if json.Unmarshal(entry.Body, &pr) != nil {
		return
	}
	s.currentPR = &pr
	s.detailStale = true
	s.emit(Event{Kind: EventPRChanged})
}

// fetchDetail starts a background fetch of the current pull request's
// detail, tagged with generation gen. A no-op if a fetch is already
// registered in flight (should not normally happen: RefreshPR checks
// detailLoading itself, and OpenPR/ReloadPR always go through
// startDetailFetch, which clears the flag for the new generation first —
// kept as a safety net mirroring fetchSection's per-key dedup).
func (s *Store) fetchDetail(ctx context.Context, gen int) {
	if s.detailFetchInFlight {
		return
	}
	// Snapshot before emitting: subscribers run synchronously and may call
	// back into the Store (ClosePR would nil current), so nothing below
	// the emit may read Store fields, mirroring fetchSection.
	ref := *s.current
	login := s.viewer.Login

	s.detailFetchInFlight = true
	s.detailLoading = true
	s.emitLoadingChanged()

	go func() {
		var res gh.DetailResult
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, for the same reason
		// fetchSection/fetchViewer do (see list.go): without it, a panic
		// inside GitHub.PullRequest would leave detailLoading stuck true
		// and detailFetchInFlight registered forever.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: detail: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "detail", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				// The generation is checked before clearing
				// detailFetchInFlight, for the same reason
				// fetchSection's dispatch does (see list.go): otherwise a
				// late, dropped result from a superseded generation could
				// erase the *current* generation's own in-flight flag.
				if gen != s.detailGen {
					return
				}
				s.detailFetchInFlight = false
				s.applyDetailResult(gen, res, err, cancelled)
			})
		}()
		res, err = s.deps.GitHub.PullRequest(ctx, ref, login)
	}()
}

// applyDetailResult applies (or reports the failure of) one detail fetch
// result. It runs only on the UI goroutine, from a Dispatch callback, and
// only for a result already confirmed (by fetchDetail's caller) to belong
// to the current generation.
func (s *Store) applyDetailResult(gen int, res gh.DetailResult, err error, cancelled bool) {
	s.detailLoading = false

	if err != nil {
		if cancelled {
			// Our own cancellation (OpenPR/ReloadPR/ClosePR superseding
			// this generation, or Stop) is not a failure worth reporting;
			// see fetchSection's identical rule in list.go.
			s.emitLoadingChanged()
			return
		}
		s.detailErr = err
		s.recomputeLastErr()
		ref, _ := s.CurrentRef()
		s.deps.Logger.Error("load pull request detail failed", "ref", ref.Key(), "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		s.emitLoadingChanged()
		return
	}

	pr := res.PR
	s.currentPR = &pr
	s.detailStale = false
	s.detailErr = nil
	s.detailWarnings = res.Warnings
	s.detailFetchedAt = s.deps.Now()
	s.recomputeLastErr()
	s.cacheDetail(pr)
	s.rateLimit = res.RateLimit

	s.emit(Event{Kind: EventPRChanged})
	s.emit(Event{Kind: EventRateLimitChanged})
	s.emitLoadingChanged()
}

// emitLoadingChanged emits both EventPRLoadingChanged (DetailState().Loading
// changed) and EventLoadingChanged (Loading() changed) together: Loading()
// folds in detailLoading (see store.go), so anything driving a spinner off
// EventLoadingChanged alone — the status bar's, today — must be told about
// every detail-loading transition too, not only list-section ones.
func (s *Store) emitLoadingChanged() {
	s.emit(Event{Kind: EventPRLoadingChanged})
	s.emit(Event{Kind: EventLoadingChanged})
}

// cacheDetail persists pr under the current pull request's cache key.
// Writing is skipped until the viewer has been network-confirmed, for the
// same reason cacheSectionPage skips writing in list.go: before that, the
// login-scoped cache key may be seeded from a previous run and could
// belong to a different account than the one that will actually resolve.
func (s *Store) cacheDetail(pr model.PullRequest) {
	if s.current == nil {
		return
	}
	if !s.viewerConfirmed || s.viewer.Login == "" {
		s.deps.Logger.Debug("detail cache write skipped: viewer login not yet confirmed")
		return
	}
	key, err := cache.PRKey(s.deps.Host, s.viewer.Login, *s.current, detailCacheRest)
	if err != nil {
		return
	}
	body, err := json.Marshal(pr)
	if err != nil {
		return
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
	}
}
