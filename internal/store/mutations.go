// Package store's mutation queue publishes comment mutations
// (AddComment/EditComment/DeleteComment today; more arrive in M3b/M4/M5)
// through a single-flight FIFO: at most one mutation talks to GitHub at a
// time, in the order it was enqueued, so a composer's "send" action can
// simply disable itself while Mutating() is true rather than reasoning
// about overlapping in-flight requests.
//
// A mutation's own network call (mutation.run) executes in a goroutine
// with only ctx and values captured by the enqueuing call (never the
// Store itself, matching every other fetch in this package - see
// docs/DESIGN.md's concurrency rules). Its result is applied back on the
// UI goroutine (mutation.run's returned apply, called from
// finishMutation) only if the pull request that was open when the
// mutation was enqueued (mutation.ref) is still the one open: otherwise
// the optimistic apply is skipped (there is nothing sensible to apply it
// to), but the target pull request's cache entry is still invalidated, so
// a later re-open of it does not show data that mutation has since made
// stale.
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
// the enqueuing call (see enqueueMutation's callers below). Its returned
// apply, if non-nil, is invoked on the UI goroutine by finishMutation, but
// only when ref is still the currently open pull request.
type mutation struct {
	// name identifies the mutation for logging (for example
	// "add_comment").
	name string
	// ref is the pull request that was open when this mutation was
	// enqueued (see enqueueMutation), used both for the generation guard
	// in finishMutation and to know which pull request's cache entry to
	// invalidate on success.
	ref model.PRRef
	run func(ctx context.Context) (apply func(), err error)
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
	s.mutationQueue = append(s.mutationQueue, mutation{name: name, ref: *s.current, run: run})
	s.startNextMutation()
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
				s.finishMutation(m, apply, err, cancelled)
			})
		}()
		apply, err = m.run(ctx)
	}()
}

// finishMutation applies (or reports the failure of) one mutation's
// result, then starts the next queued mutation, if any. It runs only on
// the UI goroutine, from a Dispatch callback.
func (s *Store) finishMutation(m mutation, apply func(), err error, cancelled bool) {
	s.mutating = false

	if err != nil {
		if !cancelled {
			s.mutationErr = err
			s.recomputeLastErr()
			s.deps.Logger.Error("mutation failed", "mutation", m.name, "ref", m.ref.Key(), "err", err)
			s.emit(Event{Kind: EventError, Err: err})
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

	current := s.current != nil && *s.current == m.ref
	if current && apply != nil {
		apply()
	}

	if s.viewer.Login != "" {
		if err := s.deps.Cache.InvalidatePR(s.deps.Host, s.viewer.Login, m.ref); err != nil {
			s.deps.Logger.Warn("cache invalidate failed", "err", err)
		}
	}
	if current {
		// Always a fresh network fetch, never a cache read: the mutation
		// just changed this pull request server-side, so re-showing a
		// (now invalidated, but still held in memory) cached snapshot on
		// top of the optimistic apply above would be a visible
		// regression, mirroring RefreshPR's own reasoning.
		s.startDetailFetch(false)
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
