// Package store's mutation queue publishes comment mutations
// (AddComment/EditComment/DeleteComment today; more arrive in M3b/M4/M5)
// through a single-flight FIFO: at most one mutation talks to GitHub at a
// time, in the order it was enqueued, so a composer's "send" action can
// simply disable itself while Mutating() is true rather than reasoning
// about overlapping in-flight requests.
//
// A mutation's own network call (mutation.run) executes in a goroutine
// with only ctx and values captured by the enqueuing call or written by
// its prepare closure (never the Store itself, matching every other fetch
// in this package - see docs/DESIGN.md's concurrency rules). Its result is
// applied back on the UI goroutine (mutation.run's returned apply, called
// from finishMutation) only if the pull request that was open when the
// mutation was enqueued (mutation.ref) is still the one open: otherwise
// the optimistic apply is skipped (there is nothing sensible to apply it
// to). Once a mutation whose run actually reached GitHub finishes —
// successfully or not — its target pull request's cache entry is
// invalidated and, if it is still the currently open one, refetched (see
// invalidateAndRefetch): a failed *network call* may still have partially
// taken effect server-side (a multi-call run failing partway, or a
// request that timed out after the server already applied it), so there
// is no "known safe to leave alone" failure mode to special-case, and
// converging on GitHub's own state unconditionally is simpler and safer
// than guessing. A prepare failure never reached GitHub at all (see
// below), so it skips this convergence step entirely: there is nothing
// server-side that could have changed for it to converge on, and treating
// it as if there were would invalidate/refetch a pull request — possibly
// one no longer even open — for no reason every time a mutation's target
// simply moved out from under it while queued.
//
// Some mutations (see enqueuePreparedMutation) also carry a prepare
// closure that decides, on the UI goroutine immediately before run's
// goroutine starts (not back at enqueue time), which GitHub call to
// actually make: enqueue time is too early for an operation whose
// behaviour depends on Store state another still-queued mutation might
// change first (most notably, which send mode currently applies and
// which pending review's ID to reuse for a review-comment mutation - see
// internal/store/review.go).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/hirano00o/gprt/internal/model"
)

// errNoPullRequestOpen is emitted (as an EventError) by every mutation
// operation when Store.current is nil: there is nothing to comment on,
// edit, or delete. It never becomes a standing error (mutationErr is not
// set for it): unlike a mutation that was actually attempted and failed,
// calling one of these methods with nothing open is a caller error with
// nothing left "pending" to report on Mutating()/PendingMutations().
var errNoPullRequestOpen = errors.New("store: no pull request open")

// errPullRequestNotLoaded is emitted when a pull request is open but its
// detail has not resolved yet (no node ID to attach a comment to). Kept
// distinct from errNoPullRequestOpen so the user is told to wait rather
// than to open a pull request they already opened.
var errPullRequestNotLoaded = errors.New("store: pull request detail not loaded yet")

// errMutationTargetChanged is returned by a mutation's prepare closure (see
// enqueuePreparedMutation) when the pull request that was open at enqueue
// time is no longer the open one, or its detail is no longer loaded, by the
// time this mutation actually reached the front of the queue: nothing was
// sent to GitHub, since whatever state prepare would otherwise have
// snapshotted (which send mode applies, which pending review to reuse) can
// no longer be trusted.
var errMutationTargetChanged = errors.New("store: pull request changed before the mutation started; nothing was sent")

// mutationTarget reports whether a mutation can be enqueued right now,
// emitting the appropriate EventError otherwise. It never sets a standing
// error: nothing was attempted.
func (s *Store) mutationTarget() bool {
	switch {
	case s.current == nil:
		s.emit(Event{Kind: EventError, Err: errNoPullRequestOpen})
		return false
	case s.currentPR == nil:
		s.emit(Event{Kind: EventError, Err: errPullRequestNotLoaded})
		return false
	}
	return true
}

// mutation is one queued unit of work for the mutation queue. run executes
// in a goroutine started by startNextMutation: it must not read or write
// any Store field, only ctx and values already captured in its closure by
// the enqueuing call or written by prepare (see enqueueMutation's/
// enqueuePreparedMutation's callers below). Its returned apply, if non-nil,
// is invoked on the UI goroutine by finishMutation, but only when ref is
// still the currently open pull request.
type mutation struct {
	// name identifies the mutation for logging (for example
	// "add_comment"). For an operation whose actual GitHub call depends on
	// state only known once prepare runs (single comment vs. add-to-review,
	// say), name is mode-independent; the specific path actually taken is
	// logged at Debug from inside run instead.
	name string
	// ref is the pull request that was open when this mutation was
	// enqueued (see enqueueMutation/enqueuePreparedMutation), used both for
	// the generation guard in finishMutation and to know which pull
	// request's cache entry to invalidate.
	ref model.PRRef
	// prepare, if non-nil, runs synchronously on the UI goroutine from
	// startNextMutation, immediately before run's goroutine is started —
	// see enqueuePreparedMutation's doc comment for why this exists (a
	// mutation queued behind another must not decide "which GitHub call to
	// make" from state snapshotted back at enqueue time, which may be
	// stale by the time it actually runs) and why writing to variables
	// run's closure later reads is race-free despite crossing goroutines
	// with no lock (prepare's write happens-before run's goroutine is even
	// started, let alone reads them). A non-nil error aborts the mutation
	// without ever starting run's goroutine or calling GitHub:
	// startNextMutation routes it straight to finishMutation as the
	// mutation's own failure, with no apply, cancelled=false, and
	// sent=false — GitHub was never called, so finishMutation's failure
	// path must not invalidate/refetch anything for it (see
	// invalidateAndRefetch and finishMutation's own doc comments).
	prepare func() error
	run     func(ctx context.Context) (apply func(), err error)
	// unscoped marks a mutation that does not target the currently open
	// pull request at all (M5, CreatePullRequest only: there is nothing
	// "current" yet for a pull request that does not exist until this
	// mutation succeeds). finishMutation always invokes an unscoped
	// mutation's apply on success (there is no "still current" ref to gate
	// it on, unlike every other mutation) and never calls
	// invalidateAndRefetch for it (there is nothing server-side yet to
	// invalidate or refetch).
	unscoped bool
	// refreshList marks a mutation whose successful completion also
	// starts the list's own non-forced refresh (finishMutation calls
	// Refresh directly — the same entry point the auto-refresh ticker
	// uses), for a mutation that changes state the list itself displays
	// (merge/close/reopen/draft/title/labels/reviewers/submit review, and
	// pull request creation): the pull request's row would otherwise show
	// stale state (or, for creation, not appear at all) until the next
	// scheduled refresh.
	refreshList bool
}

// Mutating reports whether a mutation is currently running (as opposed to
// merely queued behind one).
func (s *Store) Mutating() bool {
	return s.mutating
}

// PendingMutations returns how many mutations have not yet completed:
// the one currently running (if any) plus every one still queued behind
// it.
func (s *Store) PendingMutations() int {
	n := len(s.mutationQueue)
	if s.mutating {
		n++
	}
	return n
}

// MutationError returns the most recently finished mutation's own
// standing error, or nil if it succeeded (or none has run yet). Unlike
// LastError(), which also surfaces other, unrelated standing errors (the
// current pull request's detail, its files, or a list section) whenever
// none of them are masked by a higher-priority one, MutationError()
// reports only the mutation queue's own outcome — the question a caller
// tracking one specific send/edit/delete's own success actually needs
// answered.
func (s *Store) MutationError() error {
	return s.mutationErr
}

// AddComment publishes a new general (non-review) comment on the current
// pull request via GitHub's addComment mutation. Returns false (besides
// emitting an EventError) without enqueueing anything when no pull request
// is open, or when its detail has not resolved yet (there is no node ID to
// attach the comment to) — callers that hold text a caller-visible buffer
// depends on (a composer) must check this return value before discarding
// that buffer, since a false return means no mutation now exists whose
// completion could ever tell them the send failed.
//
// On success, the returned comment is appended to the current pull
// request's Timeline optimistically (see appendTimelineComment), then the
// pull request's cache entry is invalidated and a background refetch is
// started via startDetailFetch(false) - always, regardless of whether a
// fetch already happens to be in flight, so a mutation's own result is
// never left unconfirmed by a fresh network read purely because an
// unrelated fetch beat it to starting.
func (s *Store) AddComment(body string) bool {
	if !s.mutationTarget() {
		return false
	}
	subjectID := s.currentPR.ID

	s.enqueueMutation("add_comment", func(ctx context.Context) (func(), error) {
		comment, rl, err := s.deps.GitHub.AddIssueComment(ctx, subjectID, body)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.appendTimelineComment(comment)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// EditComment updates an existing issue comment's body via GitHub's
// updateIssueComment mutation. Returns false (besides emitting an
// EventError) without enqueueing anything when no pull request is open —
// see AddComment's doc comment for why a caller holding text on the
// strength of this call must check the return value.
//
// On success, the matching Timeline entry (found by id) is replaced
// optimistically with the server's returned comment (see
// replaceTimelineComment), then the pull request's cache entry is
// invalidated and refetched, mirroring AddComment.
func (s *Store) EditComment(id, body string) bool {
	if !s.mutationTarget() {
		return false
	}

	s.enqueueMutation("edit_comment", func(ctx context.Context) (func(), error) {
		comment, rl, err := s.deps.GitHub.UpdateIssueComment(ctx, id, body)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.replaceTimelineComment(comment)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// DeleteComment removes an existing issue comment via GitHub's
// deleteIssueComment mutation. A no-op (besides an EventError) when no
// pull request is open.
//
// On success, the matching Timeline entry (found by id) is removed
// optimistically (see removeTimelineComment), then the pull request's
// cache entry is invalidated and refetched, mirroring AddComment.
func (s *Store) DeleteComment(id string) {
	if !s.mutationTarget() {
		return
	}

	s.enqueueMutation("delete_comment", func(ctx context.Context) (func(), error) {
		rl, err := s.deps.GitHub.DeleteIssueComment(ctx, id)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.removeTimelineComment(id)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
}

// enqueueMutation appends a mutation targeting the current pull request to
// the queue and starts it if nothing else is running. Callers have
// already verified a pull request is open (s.current != nil).
func (s *Store) enqueueMutation(name string, run func(context.Context) (func(), error)) {
	s.enqueueMutationEntry(mutation{name: name, ref: *s.current, run: run})
}

// enqueueMutationWithListRefresh is enqueueMutation's counterpart for a
// state-changing mutation whose success should also trigger the list's own
// refresh (see mutation.refreshList's doc comment) — used by the M5
// pull-request-edit/merge/close/reopen/draft operations (pr_edit.go),
// none of which need enqueuePreparedMutation's late-bound prepare step.
func (s *Store) enqueueMutationWithListRefresh(name string, run func(context.Context) (func(), error)) {
	s.enqueueMutationEntry(mutation{name: name, ref: *s.current, run: run, refreshList: true})
}

// enqueueUnscopedMutation appends a mutation that does not target the
// currently open pull request at all (see mutation.unscoped's doc
// comment) — used only by CreatePullRequest (pr_create.go), which always
// also refreshes the list on success (the newly created pull request must
// appear in it).
func (s *Store) enqueueUnscopedMutation(name string, run func(context.Context) (func(), error)) {
	s.enqueueMutationEntry(mutation{name: name, unscoped: true, refreshList: true, run: run})
}

// enqueueMutationEntry appends m to the queue and starts it if nothing else
// is running. The shared tail of every enqueueMutation*/enqueuePreparedMutation*
// variant above and below.
func (s *Store) enqueueMutationEntry(m mutation) {
	s.mutationQueue = append(s.mutationQueue, m)
	s.startNextMutation()
}

// enqueuePreparedMutation appends a mutation whose prepare closure runs on
// the UI goroutine immediately before run's goroutine starts (see
// mutation.prepare's doc comment), rather than being decided once, back at
// enqueue time. This matters for any operation whose GitHub call depends
// on Store state that can change while this mutation was still queued
// behind another one — most importantly, which send mode currently applies
// and which pending review's ID to reuse, since two review-comment
// mutations enqueued back to back (before either has run) would otherwise
// both decide "no pending review exists yet" from the same stale snapshot
// and each try to create one, or a queued single-comment send would fail
// to notice that the mutation ahead of it just created a pending review it
// must now attach to instead. prepare re-validates the target itself
// (comparing against ref, the pull request open at enqueue time) rather
// than relying on the generation check finishMutation already does for
// apply, since a failed prepare must never even start run's goroutine or
// call GitHub. Callers have already verified a pull request is open
// (s.current != nil), matching enqueueMutation.
func (s *Store) enqueuePreparedMutation(name string, prepare func() error, run func(context.Context) (func(), error)) {
	s.enqueueMutationEntry(mutation{name: name, ref: *s.current, prepare: prepare, run: run})
}

// enqueuePreparedMutationWithListRefresh is enqueuePreparedMutation's
// counterpart for a state-changing mutation whose success should also
// trigger the list's own refresh (see mutation.refreshList's doc comment)
// — used by Merge (pr_edit.go, whose prepare captures the pull request's
// HeadOID at run-start time) and SubmitReview (review.go).
func (s *Store) enqueuePreparedMutationWithListRefresh(
	name string, prepare func() error, run func(context.Context) (func(), error),
) {
	s.enqueueMutationEntry(mutation{name: name, ref: *s.current, prepare: prepare, run: run, refreshList: true})
}

// startNextMutation starts the queue's head mutation if none is already
// running. A no-op when a mutation is already in flight (finishMutation
// calls this again once it completes) or the queue is empty.
func (s *Store) startNextMutation() {
	if s.mutating || len(s.mutationQueue) == 0 {
		return
	}
	m := s.mutationQueue[0]
	s.mutationQueue = s.mutationQueue[1:]
	s.mutating = true
	s.emit(Event{Kind: EventMutationChanged})

	if m.prepare != nil {
		if err := m.prepare(); err != nil {
			// No goroutine was ever started and GitHub was never called:
			// route straight through finishMutation anyway (with no apply,
			// cancelled=false, and sent=false — see finishMutation's own
			// doc comment for why sent=false skips invalidate/refetch) so
			// a prepare failure is reported, logged, and recovers the
			// queue through exactly the same single path a run failure
			// does, rather than a second, parallel one. finishMutation
			// resets s.mutating and calls startNextMutation again, so the
			// queue keeps draining.
			s.finishMutation(m, nil, err, false, false)
			return
		}
	}

	if s.mutationCtx == nil {
		// Created lazily, once, from baseCtx: unlike the list's or the
		// current pull request's fetches, a mutation has no per-attempt
		// generation to supersede a previous one with - every mutation
		// for the lifetime of this Store shares one context, cancelled
		// only by Stop (see Stop in list.go).
		s.mutationCtx, s.mutationCancel = context.WithCancel(s.baseCtx)
	}
	ctx := s.mutationCtx

	go func() {
		var apply func()
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, matching fetchDetail/
		// fetchSection's own discipline (see detail.go/list.go): without
		// it, a panic inside m.run would leave s.mutating stuck true and
		// the queue stalled forever.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: mutation %s: panic: %v", m.name, r)
				s.deps.Logger.Error("goroutine panic", "op", "mutation:"+m.name, "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				// sent is always true here: this path only ever runs once
				// m.run's goroutine has actually started (and so, GitHub
				// was actually called, or at least attempted), unlike a
				// prepare failure's own call to finishMutation, which
				// never gets this far.
				s.finishMutation(m, apply, err, cancelled, true)
			})
		}()
		apply, err = m.run(ctx)
	}()
}

// invalidateAndRefetch invalidates ref's on-disk cache entry and, if ref is
// still the currently open pull request, starts a fresh detail refetch.
// Called by finishMutation only when the mutation's run actually reached
// GitHub (sent=true): a failed *network call* may or may not have taken
// effect server-side before it failed (a partial multi-call run such as
// CommentOnFile's create-thread-submit sequence, or a request that timed
// out after the server had already applied it) — there is no way to tell
// which from here, so the simplest and safest response is to treat the
// target pull request's state as unknown and converge to whatever GitHub
// actually has, exactly like a successful mutation already does, rather
// than trying to enumerate which failure modes are "known safe" to leave
// the cache/UI alone for. A prepare failure (sent=false) never reaches
// this function at all: nothing was sent, so there is nothing server-side
// to converge on, and invalidating/refetching anyway would waste a
// network round trip (or a cache entry) for a pull request that may not
// even be the one open anymore.
func (s *Store) invalidateAndRefetch(ref model.PRRef) {
	if s.viewer.Login != "" {
		if err := s.deps.Cache.InvalidatePR(s.deps.Host, s.viewer.Login, ref); err != nil {
			s.deps.Logger.Warn("cache invalidate failed", "err", err)
		}
	}
	if s.current != nil && *s.current == ref {
		// Always a fresh network fetch, never a cache read: the mutation
		// just changed (or attempted to change) this pull request
		// server-side, so re-showing a (now invalidated, but still held
		// in memory) cached snapshot on top of the optimistic apply above
		// would be a visible regression, mirroring RefreshPR's own
		// reasoning.
		s.startDetailFetch(false)
	}
}

// finishMutation applies (or reports the failure of) one mutation's
// result, then starts the next queued mutation, if any. It runs only on
// the UI goroutine, from a Dispatch callback (or, for a prepare failure,
// synchronously from startNextMutation itself). sent reports whether this
// mutation's run actually reached GitHub (always true for a run-path
// failure or success; false only for a prepare failure, which never
// started run's goroutine at all — see mutation.prepare's and
// invalidateAndRefetch's doc comments for why that distinction matters
// for the failure branch below): err == nil is only ever reached via a
// successful run, so the success branch does not need to check it.
func (s *Store) finishMutation(m mutation, apply func(), err error, cancelled, sent bool) {
	s.mutating = false

	if err != nil {
		if !cancelled {
			s.mutationErr = err
			s.recomputeLastErr()
			s.deps.Logger.Error("mutation failed", "mutation", m.name, "ref", m.ref.Key(), "err", err)
			s.emit(Event{Kind: EventError, Err: err})
			if sent && !m.unscoped {
				s.invalidateAndRefetch(m.ref)
			}
		}
		// Our own cancellation (Stop superseding every queued and
		// running mutation) is not a failure worth reporting, matching
		// fetchDetail/fetchSection's identical rule for their own
		// cancellations.
		s.emit(Event{Kind: EventMutationChanged})
		s.startNextMutation()
		return
	}

	s.mutationErr = nil
	s.recomputeLastErr()

	// An unscoped mutation (CreatePullRequest) has no "current ref" to
	// gate its apply on — the pull request it targets did not exist until
	// this very call succeeded — so its apply always runs.
	current := m.unscoped || (s.current != nil && *s.current == m.ref)
	if current && apply != nil {
		apply()
	}
	if !m.unscoped {
		s.invalidateAndRefetch(m.ref)
	}
	if m.refreshList {
		// The same non-forced entry point the auto-refresh ticker uses
		// (see mutation.refreshList's doc comment): a state-changing
		// mutation's own optimistic apply above already updated the
		// current pull request, but the list's rows for it (and, for
		// CreatePullRequest, the brand new row) would otherwise stay
		// stale until the next scheduled refresh.
		s.Refresh()
	}

	s.emit(Event{Kind: EventMutationChanged})
	s.startNextMutation()
}

// appendTimelineComment appends comment to the current pull request's
// Timeline, copy-on-write (a fresh backing array and a fresh
// model.PullRequest value), so a caller holding an earlier CurrentPR()
// pointer never sees it mutate underneath them. A no-op if no pull
// request is open (should not normally happen: only called from a
// mutation's apply, which finishMutation only invokes while the mutation's
// target ref is still current).
func (s *Store) appendTimelineComment(comment model.IssueComment) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.Timeline = append(append([]model.TimelineItem(nil), pr.Timeline...), model.TimelineItem{
		Kind:         model.TimelineKindIssueComment,
		IssueComment: &comment,
	})
	s.currentPR = &pr
}

// replaceTimelineComment replaces the first IssueComment Timeline entry
// whose ID matches updated.ID with updated, copy-on-write (see
// appendTimelineComment). A no-op if no pull request is open, or if no
// entry matches (should not normally happen: EditComment always targets a
// comment the Timeline already holds).
func (s *Store) replaceTimelineComment(updated model.IssueComment) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	timeline := append([]model.TimelineItem(nil), pr.Timeline...)
	for i, item := range timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == updated.ID {
			timeline[i] = model.TimelineItem{Kind: model.TimelineKindIssueComment, IssueComment: &updated}
			break
		}
	}
	pr.Timeline = timeline
	s.currentPR = &pr
}

// removeTimelineComment removes the IssueComment Timeline entry whose ID
// is id, copy-on-write (see appendTimelineComment). A no-op if no pull
// request is open.
func (s *Store) removeTimelineComment(id string) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	timeline := make([]model.TimelineItem, 0, len(pr.Timeline))
	for _, item := range pr.Timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == id {
			continue
		}
		timeline = append(timeline, item)
	}
	pr.Timeline = timeline
	s.currentPR = &pr
}
