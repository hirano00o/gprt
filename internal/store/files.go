package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/model"
)

// filesHighlightWorkers is the number of goroutines consuming one files
// generation's highlight job queue.
const filesHighlightWorkers = 2

// filesHighlightQueueSize bounds the channel between highlightFeeder and
// the worker pool (a local variable inside startHighlightPool, not a Store
// field - nothing outside that function needs to read it back): large
// enough that a page's worth of hunks rarely has to wait for buffer space,
// but bounded so a pathological single page cannot grow it without limit.
// This is safe to block the *feeder* goroutine on (it is not the UI
// goroutine); see enqueueHighlightJobs's doc comment for why the
// UI-goroutine-facing side of the queue (highlightQueue, Store.highlightJobs)
// never blocks at all, regardless of this value. A var, not a const, so a
// test can shrink it to exercise backpressure deterministically.
var filesHighlightQueueSize = 1024

// FileEntry is one changed file together with its parsed diff and
// highlighting progress.
type FileEntry struct {
	// id uniquely identifies this FileEntry within the store for the
	// lifetime of its files generation, assigned by assignFileEntryIDs
	// when it is first appended or replaced into Store.files. It exists
	// so a highlight job's result (queued asynchronously, potentially
	// still in flight when a later network response replaces this exact
	// entry - see replaceFilesPage) can be matched back to the right
	// FileEntry, or correctly discarded, without relying on File.Path
	// (which the replacement shares).
	id int

	File model.ChangedFile
	// Hunks is nil when File.HasPatch is false, or when ParseErr is set.
	Hunks []diff.Hunk
	// ParseErr holds diff.Parse's error for this file's patch, if any.
	// Other files in the same page are unaffected by one file's parse
	// failure.
	ParseErr error
	// Tokens holds one slice of per-line tokens per hunk (hunk index ->
	// line index -> tokens), nil until that hunk's highlight job has
	// been applied. It has len(Hunks) entries once parsing succeeds.
	Tokens [][][]highlight.Token
	// Highlighted is true once every hunk that could be highlighted has
	// had a result applied (or immediately, for a file with nothing to
	// highlight: no patch, a parse error, or zero hunks).
	Highlighted bool
}

// FilesState is a snapshot of the current pull request's changed-files
// fetch status, as returned by Store.FilesState.
type FilesState struct {
	// Loading reports whether a files page fetch is currently in flight.
	Loading bool
	// Stale reports whether the currently displayed page was served from
	// cache and has not yet been confirmed by a network response for
	// that same page.
	Stale bool
	// PagesLoaded is how many pages are currently reflected in Files.
	PagesLoaded int
	// HasNext reports whether another page remains to be fetched.
	HasNext bool
	// Err is the most recent page fetch's standing error, or nil.
	Err error
	// Warnings holds data-quality notes that did not fail a fetch: today,
	// a mismatch between the number of files actually loaded once
	// pagination finished and the pull request's own ChangedFiles count
	// (see checkFilesTruncation).
	Warnings []string
	// Highlighting is how many hunk highlight jobs are still enqueued or
	// in flight for the current files generation.
	Highlighting int
}

// filesPageCacheRest is the cache.PRKey Rest segment one changed-files page
// is cached under.
func filesPageCacheRest(page int) string {
	return fmt.Sprintf("files/page-%d", page)
}

// cachedFilesPage is the on-disk JSON shape of one cached changed-files
// page (see filesPageCacheRest). The page's ETag lives in cache.Entry.ETag
// instead of this struct, matching how the rest of the store caches
// REST/GraphQL results.
type cachedFilesPage struct {
	Files   []model.ChangedFile `json:"files"`
	HasNext bool                `json:"has_next"`
}

// Files returns a snapshot of the current pull request's changed files
// loaded so far, in page order. The returned slice is a shallow copy (safe
// to range over even if the store appends another page concurrently from
// the caller's perspective - Store methods only ever run on the UI
// goroutine, so "concurrently" here only means "before the next event"),
// but each FileEntry's own slices (Hunks, Tokens) are shared with the
// store's internal state and must be treated as read-only.
func (s *Store) Files() []FileEntry {
	return append([]FileEntry(nil), s.files...)
}

// FilesState returns a snapshot of the current pull request's changed-files
// fetch status.
func (s *Store) FilesState() FilesState {
	return FilesState{
		Loading:      s.filesLoading,
		Stale:        s.filesStale,
		PagesLoaded:  s.filesPagesLoaded,
		HasNext:      s.filesHasNext,
		Err:          s.filesErr,
		Warnings:     append([]string(nil), s.filesWarnings...),
		Highlighting: s.filesHighlightPending,
	}
}

// FileByPath returns the current pull request's FileEntry for path, if one
// has been loaded.
func (s *Store) FileByPath(path string) (FileEntry, bool) {
	for _, e := range s.files {
		if e.File.Path == path {
			return e, true
		}
	}
	return FileEntry{}, false
}

// LoadFiles starts loading the current pull request's changed files, one
// page at a time starting at page 1. A no-op when no pull request is open.
//
// When the current pull request's own detail has not resolved yet
// (CurrentPR() is nil, so there is no HeadOID to key a files generation
// on), the call is remembered (filesWanted) rather than silently dropped:
// applyDetailResult's success path starts the deferred load once a HeadOID
// becomes known. filesWanted does not itself remember force: the deferred
// start always runs as LoadFiles(false). This is an accepted limitation,
// not an oversight - the only caller in this position is the UI opening
// the Files tab for the first time on a pull request whose detail has not
// resolved yet, which has no reason to skip the cache.
//
// Otherwise it is idempotent for the pull request's head commit: a call
// with force == false is a no-op if a load for that same HeadOID has
// already started (loading or finished) - except that a *standing error*
// with nothing currently loading resumes the sequence from the next
// unfetched page instead of no-op'ing forever (see resumeFilesFetch). Call
// LoadFiles(true) to force a fresh sequence regardless (used by Reload and
// the HeadOID/ChangedFiles-change coupling in applyDetailResult).
//
// force also controls whether each page's own cached entry is shown before
// its network fetch resolves (force skips that cache read, mirroring
// LoadList's own force parameter): the HeadOID/ChangedFiles-change coupling
// always forces, since a cached page keyed only by page number - not by
// HeadOID, see cachedFilesPage - would otherwise briefly show a *different*
// commit's diff after a force-push.
func (s *Store) LoadFiles(force bool) {
	if s.current == nil {
		return
	}
	if s.currentPR == nil {
		s.filesWanted = true
		return
	}

	headOID := s.currentPR.HeadOID
	if !force && s.filesStarted && s.filesHeadOID == headOID {
		if s.filesErr != nil && !s.filesLoading {
			s.resumeFilesFetch()
		}
		return
	}

	s.filesWanted = false
	s.startFilesFetch(headOID, !force)
}

// resumeFilesFetch retries the next unfetched page of the current files
// generation after a standing error, without discarding the pages already
// loaded or starting a new generation: LoadFiles' own guard (filesErr !=
// nil && !filesLoading) is the only caller.
func (s *Store) resumeFilesFetch() {
	s.fetchFilesPage(s.filesCtx, s.filesGen, s.filesPagesLoaded+1)
}

// resetFiles clears every field LoadFiles/the highlight pool own, and
// cancels any in-flight fetch or worker pool, without starting a new one.
// Shared by OpenPR (a different pull request must not show a stale file
// list left over from the previous one) and ClosePR.
func (s *Store) resetFiles() {
	s.filesGen++ // supersedes any fetch or highlight job still in flight
	if s.filesCancel != nil {
		s.filesCancel()
	}
	s.filesCancel = nil
	s.filesCtx = nil
	s.highlightJobs = nil
	s.highlightWake = nil

	s.files = nil
	s.filesPageLens = nil
	s.filesLoading = false
	s.filesStale = false
	s.filesErr = nil
	s.filesWarnings = nil
	s.filesPagesLoaded = 0
	s.filesHasNext = false
	s.filesHeadOID = ""
	s.filesChangedFilesAt = 0
	s.filesStarted = false
	s.filesWanted = false
	s.filesShowCache = false
	s.filesFetchInFlight = false
	s.filesHighlightPending = 0
	s.filesPendingByID = nil
}

// startFilesFetch begins a new files generation for headOID: it resets
// every files field, starts a fresh highlight worker pool, and fetches page
// 1. s.currentPR is guaranteed non-nil by LoadFiles, the only caller.
func (s *Store) startFilesFetch(headOID string, showCache bool) {
	s.resetFiles()
	// resetFiles clears filesErr but does not itself recompute lastErr:
	// without this call, LastError() would keep reporting a files error
	// that no longer exists until page 1's own outcome happens to call
	// recomputeLastErr again, which - for a page 1 that is slow, or a
	// generation a subscriber cancels before it resolves - could be a long
	// or even indefinite wait.
	s.recomputeLastErr()

	gen := s.filesGen
	s.filesStarted = true
	s.filesHeadOID = headOID
	s.filesChangedFilesAt = s.currentPR.ChangedFiles
	s.filesShowCache = showCache
	s.filesHasNext = true // assumed until page 1 says otherwise

	ctx, cancel := context.WithCancel(s.baseCtx)
	s.filesCtx = ctx
	s.filesCancel = cancel
	s.startHighlightPool(ctx, gen)

	s.emit(Event{Kind: EventFilesChanged})
	if gen != s.filesGen {
		return
	}
	s.fetchFilesPage(ctx, gen, 1)
}

// fetchFilesPage fetches page of the current files generation gen,
// applying its cached entry first when s.filesShowCache is set. ref and
// tabWidth are snapshotted from Store fields *before* emitFilesLoadingChanged
// runs: emit calls subscribers synchronously, and a subscriber may call
// back into the store (for example ClosePR, which nils s.current) before
// this function reaches the point of dereferencing it, mirroring
// fetchSection/fetchDetail's identical snapshot-before-emit rule.
func (s *Store) fetchFilesPage(ctx context.Context, gen, page int) {
	if s.filesFetchInFlight {
		return
	}

	var etag string
	cacheApplied := false
	if s.filesShowCache {
		if entry, ok := s.applyCachedFilesPage(gen, page); ok {
			cacheApplied = true
			etag = entry.ETag
		}
		if gen != s.filesGen {
			return
		}
	}

	ref := *s.current
	tabWidth := s.deps.Config.TabWidth

	s.filesFetchInFlight = true
	s.filesLoading = true
	s.emitFilesLoadingChanged()
	if gen != s.filesGen {
		return
	}

	go func() {
		var res gh.FilesResult
		var entries []FileEntry
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, for the same reason
		// fetchSection/fetchDetail do (see list.go/detail.go).
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: files: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "files", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				if gen != s.filesGen {
					return
				}
				s.filesFetchInFlight = false
				s.applyFilesPageResult(ctx, gen, page, cacheApplied, res, entries, err, cancelled)
			})
		}()
		res, err = s.deps.GitHub.ChangedFiles(ctx, ref, page, etag)
		if err == nil && !res.NotModified {
			// Parsing (pure) happens here, in the fetch goroutine, not in
			// the dispatched apply path, per docs/DESIGN.md's "Files"
			// section.
			entries = parseFilesPage(res.Files, tabWidth)
		}
	}()
}

// applyCachedFilesPage synchronously applies page's cached entry, if any,
// marking the files list stale until the network confirms it. It never
// touches the network: cache.Store.Get is a local disk/memory read.
func (s *Store) applyCachedFilesPage(gen, page int) (cache.Entry, bool) {
	if s.current == nil || s.viewer.Login == "" {
		return cache.Entry{}, false
	}
	key, err := cache.PRKey(s.deps.Host, s.viewer.Login, *s.current, filesPageCacheRest(page))
	if err != nil {
		return cache.Entry{}, false
	}
	entry, ok, err := s.deps.Cache.Get(key)
	if err != nil {
		s.warnCacheError(err)
		return cache.Entry{}, false
	}
	if !ok {
		return cache.Entry{}, false
	}
	var cached cachedFilesPage
	if json.Unmarshal(entry.Body, &cached) != nil {
		return cache.Entry{}, false
	}

	entries := parseFilesPage(cached.Files, s.deps.Config.TabWidth)
	s.assignFileEntryIDs(entries)
	s.appendFilesPage(page, entries)
	s.filesHasNext = cached.HasNext
	s.filesStale = true
	s.emit(Event{Kind: EventFilesChanged})
	if gen != s.filesGen {
		return entry, true
	}
	s.enqueueHighlightJobs(gen, entries)
	return entry, true
}

// applyFilesPageResult applies (or reports the failure of) one page fetch's
// result, then continues to the next page while HasNext holds. It runs
// only on the UI goroutine, from a Dispatch callback, and only for a result
// already confirmed (by fetchFilesPage's caller) to belong to the current
// generation. Every emit below is followed by a fresh gen == s.filesGen
// check before continuing: an emit runs subscribers synchronously, and one
// may call back into the store (OpenPR/ClosePR) and start a new files
// generation before this function would otherwise resume mutating
// generation-scoped state.
func (s *Store) applyFilesPageResult(
	ctx context.Context, gen, page int, cacheApplied bool,
	res gh.FilesResult, entries []FileEntry, err error, cancelled bool,
) {
	s.filesLoading = false

	if err != nil {
		if cancelled {
			// Our own cancellation (a PR switch/close superseding this
			// generation) is not a failure worth reporting; see
			// fetchSection's identical rule in list.go.
			s.emitFilesLoadingChanged()
			return
		}
		if cacheApplied {
			// The page shown from cache was never actually confirmed:
			// discard it so a later resume (LoadFiles(false), see item 4's
			// resumeFilesFetch) re-fetches this exact page instead of
			// skipping straight to the next one. Without this,
			// filesPagesLoaded would already count this page as loaded,
			// permanently marking its unconfirmed - possibly wrong-commit
			// - content as no longer stale the moment a later page
			// succeeds, in violation of "at most one page is ever shown
			// but unconfirmed at a time" (see FilesState().Stale's own
			// doc comment).
			s.discardCachedPage(page)
		}
		s.filesErr = err
		s.recomputeLastErr()
		s.deps.Logger.Error("load changed files failed", "page", page, "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		if gen != s.filesGen {
			return
		}
		s.emit(Event{Kind: EventFilesChanged})
		if gen != s.filesGen {
			return
		}
		s.emitFilesLoadingChanged()
		return
	}

	s.filesErr = nil
	s.recomputeLastErr()

	if res.RateLimit.Known {
		s.rateLimit = res.RateLimit
		s.emit(Event{Kind: EventRateLimitChanged})
		if gen != s.filesGen {
			return
		}
	}

	if res.NotModified {
		// cacheApplied must be true here: a request only carries
		// If-None-Match (and so can only receive a 304) when a cached
		// entry was applied first.
		s.filesStale = false
	} else {
		s.assignFileEntryIDs(entries)
		s.logParseErrors(entries)
		if cacheApplied {
			s.replaceFilesPage(page, entries)
		} else {
			s.appendFilesPage(page, entries)
		}
		s.filesHasNext = res.HasNext
		s.filesStale = false
		s.cacheFilesPage(page, res)
	}
	if !s.filesHasNext {
		s.checkFilesTruncation()
	}

	s.emit(Event{Kind: EventFilesChanged})
	if gen != s.filesGen {
		return
	}

	if !res.NotModified {
		s.enqueueHighlightJobs(gen, entries)
	}

	s.emitFilesLoadingChanged()
	if gen != s.filesGen {
		return
	}

	if s.filesHasNext {
		s.fetchFilesPage(ctx, gen, page+1)
	}
}

// checkFilesTruncation compares the number of files actually loaded, once
// pagination has finished (filesHasNext is false), against the pull
// request's own ChangedFiles count: GitHub's REST files endpoint caps out
// at 3000 entries, and a base-branch advance between page fetches can shift
// the diff GitHub computes without changing the pull request's HeadOID or
// this count matching what was actually paged through. A mismatch is
// recorded as a warning rather than an error: the files that did load are
// still valid data, just possibly incomplete.
func (s *Store) checkFilesTruncation() {
	s.filesWarnings = nil
	if s.currentPR == nil {
		return
	}
	want := s.currentPR.ChangedFiles
	got := len(s.files)
	if want > 0 && got < want {
		s.filesWarnings = []string{
			fmt.Sprintf("%d of %d files loaded (GitHub returns at most 3000; try R)", got, want),
		}
	}
}

// logParseErrors logs each entry with a non-nil ParseErr at Warn: a patch
// GitHub itself returned failing to parse is most likely a bug in
// internal/diff (an edge case its own tests do not yet cover) rather than a
// data problem worth ignoring silently.
func (s *Store) logParseErrors(entries []FileEntry) {
	for _, e := range entries {
		if e.ParseErr != nil {
			s.deps.Logger.Warn("parse changed file patch failed", "path", e.File.Path, "err", e.ParseErr)
		}
	}
}

// assignFileEntryIDs assigns each of entries a fresh, store-unique id (see
// FileEntry.id's doc comment). Called once, on the UI goroutine, right
// before entries are appended or replace an existing page in s.files.
func (s *Store) assignFileEntryIDs(entries []FileEntry) {
	for i := range entries {
		s.nextFileEntryID++
		entries[i].id = s.nextFileEntryID
	}
}

// appendFilesPage appends entries as page's files, recording its length so
// a later network result for the same page can replace exactly that slice
// (see replaceFilesPage).
func (s *Store) appendFilesPage(page int, entries []FileEntry) {
	s.files = append(s.files, entries...)
	s.filesPageLens = append(s.filesPageLens, len(entries))
	s.filesPagesLoaded = page
}

// replaceFilesPage replaces page's slice of s.files (previously applied
// from cache) with entries, which may have a different length. Before
// replacing, any of the old entries' hunks still pending a highlight
// result are dropped from the global/per-id pending bookkeeping: those
// jobs were queued for a FileEntry.id that no longer exists in s.files, so
// a stale result that later arrives for one of them is discarded by
// applyHighlightResult's own id lookup regardless, but without this the
// pending *count* they represent would otherwise never be decremented
// (since the id it would have decremented under is now gone), permanently
// overstating FilesState().Highlighting.
func (s *Store) replaceFilesPage(page int, entries []FileEntry) {
	offset := 0
	for i := range page - 1 {
		offset += s.filesPageLens[i]
	}
	oldLen := s.filesPageLens[page-1]
	s.dropPendingHighlights(s.files[offset : offset+oldLen])

	tail := append([]FileEntry(nil), s.files[offset+oldLen:]...)
	s.files = append(s.files[:offset], entries...)
	s.files = append(s.files, tail...)
	s.filesPageLens[page-1] = len(entries)
	if page > s.filesPagesLoaded {
		s.filesPagesLoaded = page
	}
}

// discardCachedPage removes page's cache-applied-but-unconfirmed entries
// from s.files entirely (no replacement), undoing applyCachedFilesPage's
// optimistic append. Used when that same page's own network confirmation
// fails (see applyFilesPageResult's error branch), so a subsequent
// resumeFilesFetch (LoadFiles(false) after a standing error, see item 4)
// re-fetches this exact page instead of skipping past it: filesPagesLoaded
// is restored to page-1, undoing applyCachedFilesPage's premature
// increment, which is what would otherwise make the resume compute
// page+1 as "next". filesStale is restored to false: pages before page
// were already confirmed by the time page's own fetch started (pages are
// fetched strictly sequentially), so once page's own unconfirmed content
// is gone, nothing remaining in s.files is unconfirmed.
func (s *Store) discardCachedPage(page int) {
	if page < 1 || page > len(s.filesPageLens) {
		return // defensive: cacheApplied implies appendFilesPage ran for exactly this page
	}
	offset := 0
	for i := range page - 1 {
		offset += s.filesPageLens[i]
	}
	pageLen := s.filesPageLens[page-1]
	s.dropPendingHighlights(s.files[offset : offset+pageLen])

	s.files = append(s.files[:offset], s.files[offset+pageLen:]...)
	s.filesPageLens = s.filesPageLens[:page-1]
	s.filesPagesLoaded = page - 1
	s.filesStale = false
}

// dropPendingHighlights removes any highlight bookkeeping
// (filesPendingByID and its share of filesHighlightPending) for entries
// being removed from s.files outside of a completed highlight result:
// shared by replaceFilesPage (an entry superseded by fresh content) and
// discardCachedPage (an entry discarded after its own confirmation
// failed) so a job still queued or in flight for one of entries' ids
// cannot leave FilesState().Highlighting permanently overstating
// outstanding work once applyHighlightResult's own id lookup silently
// discards its eventual result.
func (s *Store) dropPendingHighlights(entries []FileEntry) {
	for _, e := range entries {
		remaining, ok := s.filesPendingByID[e.id]
		if !ok {
			continue
		}
		delete(s.filesPendingByID, e.id)
		s.filesHighlightPending -= remaining
		if s.filesHighlightPending < 0 {
			s.filesHighlightPending = 0
		}
	}
}

// cacheFilesPage persists res as page's cached entry. Writing is skipped
// until the viewer has been network-confirmed, for the same reason
// cacheSectionPage/cacheDetail skip writing in list.go/detail.go.
func (s *Store) cacheFilesPage(page int, res gh.FilesResult) {
	if s.current == nil || !s.viewerConfirmed || s.viewer.Login == "" {
		return
	}
	key, err := cache.PRKey(s.deps.Host, s.viewer.Login, *s.current, filesPageCacheRest(page))
	if err != nil {
		return
	}
	body, err := json.Marshal(cachedFilesPage{Files: res.Files, HasNext: res.HasNext})
	if err != nil {
		return
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, ETag: res.ETag, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
	}
}

// emitFilesLoadingChanged emits both EventFilesLoadingChanged
// (FilesState().Loading changed) and EventLoadingChanged (Loading()
// changed) together, mirroring emitLoadingChanged in detail.go.
func (s *Store) emitFilesLoadingChanged() {
	s.emit(Event{Kind: EventFilesLoadingChanged})
	s.emit(Event{Kind: EventLoadingChanged})
}

// parseFilesPage parses each file's patch (when present) into hunks, with
// tabs already expanded to tabWidth columns (see diff.ExpandTabs), and
// pre-sizes Tokens to match. A file with no patch, a parse error, or zero
// hunks has nothing to highlight and is marked Highlighted immediately,
// since no highlight job will ever be enqueued for it (see
// enqueueHighlightJobs). This is pure and runs in the fetch goroutine, per
// docs/DESIGN.md's "Files" section - it never touches Store fields (in
// particular, entries are not yet assigned an id; see assignFileEntryIDs,
// called once entries reach the UI goroutine).
func parseFilesPage(files []model.ChangedFile, tabWidth int) []FileEntry {
	entries := make([]FileEntry, len(files))
	for i, f := range files {
		entries[i].File = f
		if !f.HasPatch {
			entries[i].Highlighted = true
			continue
		}

		hunks, err := diff.Parse(f.Patch)
		if err != nil {
			entries[i].ParseErr = err
			entries[i].Highlighted = true
			continue
		}
		for hi := range hunks {
			for li := range hunks[hi].Lines {
				hunks[hi].Lines[li].Text = diff.ExpandTabs(hunks[hi].Lines[li].Text, tabWidth)
			}
		}
		entries[i].Hunks = hunks
		if len(hunks) == 0 {
			entries[i].Highlighted = true
			continue
		}
		entries[i].Tokens = make([][][]highlight.Token, len(hunks))
	}
	return entries
}

// highlightJob is one hunk's worth of highlighting work: tokenise path's
// lines (already stripped of their diff +/-/  prefix and tab-expanded, see
// parseFilesPage) for hunkIndex within fileID's Hunks/Tokens.
type highlightJob struct {
	fileID    int
	path      string
	hunkIndex int
	lines     []string
}

// highlightQueue is a mutex-protected, unbounded FIFO of highlightJob,
// shared between the UI goroutine (push, from enqueueHighlightJobs - never
// blocking) and one feeder goroutine per files generation (drain, from
// highlightFeeder). This indirection exists specifically so the UI
// goroutine never sends directly on the bounded channel the worker pool
// reads from: if that channel were ever full while every worker happened
// to be blocked in Dispatch waiting for the UI goroutine to process an
// earlier result (Dispatch is app.QueueUpdateDraw in production, which
// blocks until the UI goroutine runs the queued function), the UI goroutine
// blocking on the same channel to enqueue more work would deadlock the
// whole application - the UI goroutine can never make progress to drain
// Dispatch, and the workers can never make progress to drain the channel.
type highlightQueue struct {
	mu   sync.Mutex
	jobs []highlightJob
}

// push appends jobs to the queue. Never blocks.
func (q *highlightQueue) push(jobs ...highlightJob) {
	q.mu.Lock()
	q.jobs = append(q.jobs, jobs...)
	q.mu.Unlock()
}

// drain removes and returns every job currently queued. Never blocks.
func (q *highlightQueue) drain() []highlightJob {
	q.mu.Lock()
	jobs := q.jobs
	q.jobs = nil
	q.mu.Unlock()
	return jobs
}

// startHighlightPool starts one feeder goroutine and filesHighlightWorkers
// worker goroutines for the files generation gen, all stopped by ctx (a PR
// switch/close, via resetFiles, or Stop).
func (s *Store) startHighlightPool(ctx context.Context, gen int) {
	queue := &highlightQueue{}
	wake := make(chan struct{}, 1)
	out := make(chan highlightJob, filesHighlightQueueSize)

	s.highlightJobs = queue
	s.highlightWake = wake

	go highlightFeeder(ctx, queue, wake, out)
	for range filesHighlightWorkers {
		go s.highlightWorker(ctx, gen, out)
	}
}

// highlightFeeder moves jobs from queue to out, blocking (only itself,
// never the UI goroutine - see highlightQueue's doc comment) on a full out
// or an empty queue, until ctx is done. wake is a coalescing signal
// (buffered 1): enqueueHighlightJobs sends to it without blocking whenever
// it pushes to queue, and a dropped, buffer-already-full send is harmless,
// since drain() always empties the whole queue in one pass regardless of
// how many wake signals arrived in the meantime - the loop only needs to
// know "queue is non-empty at least once more", not "how many times".
func highlightFeeder(ctx context.Context, queue *highlightQueue, wake <-chan struct{}, out chan<- highlightJob) {
	for {
		for _, job := range queue.drain() {
			select {
			case out <- job:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return
		}
	}
}

// highlightWorker consumes jobs from queue until ctx is done or the queue
// is closed (queue is never explicitly closed today; ctx cancellation is
// how a generation's pool is stopped - see resetFiles).
func (s *Store) highlightWorker(ctx context.Context, gen int, queue <-chan highlightJob) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-queue:
			if !ok {
				return
			}
			s.processHighlightJob(ctx, gen, job)
		}
	}
}

// processHighlightJob tokenises one job and dispatches its result. A panic
// from the highlighter is recovered here (not by the worker loop) so one
// bad job does not take the rest of the pool down with it; the job's result
// is then reported as "no highlighting" (nil tokens), which is exactly how
// highlight.Lines itself represents "could not/should not highlight this".
// A tokenising error is not otherwise actionable (see highlight.Lines' own
// doc comment: most causes are routine, not failures) but is logged at
// Debug rather than silently discarded, in case a persistent one turns out
// to matter.
//
// No result is dispatched at all once ctx is done: a cancelled pool (PR
// switch/close) must not keep queuing Dispatch callbacks for jobs it no
// longer has a generation to apply them to.
func (s *Store) processHighlightJob(ctx context.Context, gen int, job highlightJob) {
	var tokens [][]highlight.Token
	defer func() {
		if r := recover(); r != nil {
			s.deps.Logger.Error("goroutine panic", "op", "highlight", "err", fmt.Errorf("store: highlight: panic: %v", r))
			tokens = nil
		}
		if ctx.Err() != nil {
			return
		}
		s.deps.Dispatch(func() {
			if gen != s.filesGen {
				return
			}
			s.applyHighlightResult(job.fileID, job.hunkIndex, tokens)
		})
	}()
	var lineErr error
	tokens, lineErr = s.highlighter.Lines(job.path, job.lines)
	if lineErr != nil {
		s.deps.Logger.Debug("highlight lines failed", "path", job.path, "err", lineErr)
	}
}

// enqueueHighlightJobs enqueues one job per hunk of every file in entries
// that has something to highlight (HasPatch, no ParseErr, at least one
// hunk - see parseFilesPage, which already marks every other file
// Highlighted with nothing enqueued for it). A no-op once gen is no longer
// the current files generation, or before any pool has started for it.
// entries must already have their id assigned (see assignFileEntryIDs).
//
// This only ever pushes to the mutex-protected highlightQueue and sends a
// non-blocking wake signal - never to the worker-facing channel directly -
// so it can never block regardless of how backed up the worker pool is
// (see highlightQueue's doc comment for the deadlock this avoids).
func (s *Store) enqueueHighlightJobs(gen int, entries []FileEntry) {
	if gen != s.filesGen || s.highlightJobs == nil {
		return
	}

	var jobs []highlightJob
	for _, e := range entries {
		if e.Highlighted {
			continue
		}
		if s.filesPendingByID == nil {
			s.filesPendingByID = make(map[int]int)
		}
		s.filesPendingByID[e.id] = len(e.Hunks)

		for hi, h := range e.Hunks {
			lines := make([]string, len(h.Lines))
			for li, l := range h.Lines {
				lines[li] = l.Text
			}
			jobs = append(jobs, highlightJob{fileID: e.id, path: e.File.Path, hunkIndex: hi, lines: lines})
			s.filesHighlightPending++
		}
	}
	if len(jobs) == 0 {
		return
	}

	s.highlightJobs.push(jobs...)
	select {
	case s.highlightWake <- struct{}{}:
	default:
	}
}

// applyHighlightResult applies one hunk's highlight result to the FileEntry
// identified by fileID, and marks that file Highlighted once every one of
// its hunks has a result applied. A fileID no longer present in s.files
// (its page was replaced - see replaceFilesPage - or the pull request
// switched/closed, though the generation check in processHighlightJob's
// dispatch already covers that case) is silently discarded: any pending
// count it represented was already subtracted at the point it was
// invalidated, so no further bookkeeping is owed here.
func (s *Store) applyHighlightResult(fileID, hunkIndex int, tokens [][]highlight.Token) {
	for i := range s.files {
		if s.files[i].id != fileID {
			continue
		}
		if hunkIndex < 0 || hunkIndex >= len(s.files[i].Tokens) {
			return
		}
		s.files[i].Tokens[hunkIndex] = tokens

		s.filesHighlightPending--
		if s.filesHighlightPending < 0 {
			s.filesHighlightPending = 0
		}
		if remaining, ok := s.filesPendingByID[fileID]; ok {
			remaining--
			if remaining <= 0 {
				delete(s.filesPendingByID, fileID)
				s.files[i].Highlighted = true
			} else {
				s.filesPendingByID[fileID] = remaining
			}
		}

		s.emit(Event{Kind: EventFileHighlighted, Path: s.files[i].File.Path})
		return
	}
}
