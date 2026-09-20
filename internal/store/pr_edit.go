// Package store's pull-request edit/merge/draft/reviewer mutations (M5,
// see docs/DESIGN.md's mutation-sequence table rows "Edit PR" and
// "Merge / close / reopen") all route through the same mutation queue and
// finishMutation apply/refetch discipline mutations.go documents, so this
// file only covers what is specific to each one. Every one of them also
// sets mutation.refreshList (see enqueueMutationWithListRefresh/
// enqueuePreparedMutationWithListRefresh in mutations.go), since a
// successful title/label/reviewer/draft/merge/close/reopen change is
// state the list itself displays and would otherwise look stale until the
// next scheduled refresh.
package store

import (
	"context"
	"errors"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// nonNilIDs returns a non-nil, empty slice for a nil ids, or ids
// unchanged otherwise — mirroring gh.Client.RequestReviewers' own
// nonNilIDs (internal/gh/pr_edit.go), duplicated here rather than
// exported across the package boundary since SetReviewers' own contract
// is with the GitHub interface, not with *gh.Client's internal behaviour:
// a fake substituted for GitHub in a test has no obligation to normalize
// nil the way the real client does (see nonNilIDs's fuller doc comment in
// internal/gh for the null-vs-empty-array GitHub semantics this exists
// for).
func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// errPullRequestNotOpen is Merge's prepare closure's own sentinel for "the
// pull request is not open" (already merged or closed): refused locally,
// with nothing sent to GitHub, mirroring errMutationTargetChanged.
// Mergeable/MergeStateStatus are deliberately *not* checked here (an
// UNKNOWN mergeable state, or a BLOCKED/DIRTY merge-state status, is left
// for the server to reject or accept on its own terms) - only the pull
// request's lifecycle state.
var errPullRequestNotOpen = errors.New("store: pull request is not open")

// UpdatePullRequestMeta edits the current pull request's title, body,
// base branch, and/or labels via GitHub's updatePullRequest mutation (see
// gh.UpdatePullRequestInput's own doc comment for its nil-means-unchanged
// convention). Returns false (besides emitting an EventError) without
// enqueueing anything when no pull request is open or its detail has not
// resolved yet, matching AddComment's own contract.
//
// On success, the returned fields (Title, Body, BaseRefName, Labels,
// UpdatedAt) are applied optimistically (see applyUpdatedPullRequestMeta),
// then the pull request's cache entry is invalidated and refetched, and
// the list is refreshed (a title/label change is state the list itself
// displays).
func (s *Store) UpdatePullRequestMeta(in gh.UpdatePullRequestInput) bool {
	if !s.mutationTarget() {
		return false
	}
	id := s.currentPR.ID

	s.enqueueMutationWithListRefresh("update_pull_request", func(ctx context.Context) (func(), error) {
		pr, rl, err := s.deps.GitHub.UpdatePullRequest(ctx, id, in)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyUpdatedPullRequestMeta(pr)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// applyUpdatedPullRequestMeta merges update's Title/Body/BaseRefName/
// Labels/UpdatedAt onto the current pull request, copy-on-write (see
// mutations.go's appendTimelineComment for the pattern every apply helper
// in this package follows). A no-op if no pull request is open.
func (s *Store) applyUpdatedPullRequestMeta(update model.PullRequest) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.Title = update.Title
	pr.Body = update.Body
	pr.BaseRefName = update.BaseRefName
	pr.Labels = update.Labels
	pr.UpdatedAt = update.UpdatedAt
	s.currentPR = &pr
}

// SetReviewers replaces the current pull request's requested reviewers
// (GitHub's requestReviews mutation with union: false, matching
// docs/DESIGN.md's mutation-sequence table) with userIDs/teamIDs. Returns
// false (besides emitting an EventError) without enqueueing anything when
// no pull request is open or its detail has not resolved yet.
//
// On success, the returned review requests replace ReviewRequests
// optimistically, then the pull request is invalidated/refetched and the
// list is refreshed.
func (s *Store) SetReviewers(userIDs, teamIDs []string) bool {
	if !s.mutationTarget() {
		return false
	}
	id := s.currentPR.ID
	// Normalized here, not left to gh.Client.RequestReviewers' own
	// nonNilIDs: GitHub's requestReviews treats a null userIds/teamIds as
	// "leave unspecified" but an empty array as "clear every reviewer of
	// that kind" (see nonNilIDs's doc comment in internal/gh/pr_edit.go),
	// and the Store's only real contract is with the GitHub interface, not
	// with *gh.Client's own internal behaviour — a test (or any other
	// implementation) substituted for it has no obligation to normalize
	// nil itself.
	userIDs = nonNilIDs(userIDs)
	teamIDs = nonNilIDs(teamIDs)

	s.enqueueMutationWithListRefresh("set_reviewers", func(ctx context.Context) (func(), error) {
		reviewers, rl, err := s.deps.GitHub.RequestReviewers(ctx, id, userIDs, teamIDs, false)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyReviewRequests(reviewers)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// applyReviewRequests replaces the current pull request's ReviewRequests
// with reviewers, copy-on-write. A no-op if no pull request is open.
func (s *Store) applyReviewRequests(reviewers []model.Reviewer) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.ReviewRequests = reviewers
	s.currentPR = &pr
}

// SetDraft toggles the current pull request's draft state: draft=true
// calls GitHub's convertPullRequestToDraft, draft=false calls
// markPullRequestReadyForReview. Returns false (besides emitting an
// EventError) without enqueueing anything when no pull request is open or
// its detail has not resolved yet.
//
// On success, the server's own post-toggle IsDraft is applied
// optimistically (not simply the requested draft value — every mutation
// in this file reads its result back from the server rather than
// assuming success, matching gh.Client's own convention), then the pull
// request is invalidated/refetched and the list is refreshed.
func (s *Store) SetDraft(draft bool) bool {
	if !s.mutationTarget() {
		return false
	}
	id := s.currentPR.ID

	s.enqueueMutationWithListRefresh("set_draft", func(ctx context.Context) (func(), error) {
		var isDraft bool
		var rl model.RateLimit
		var err error
		if draft {
			isDraft, rl, err = s.deps.GitHub.ConvertToDraft(ctx, id)
		} else {
			isDraft, rl, err = s.deps.GitHub.MarkReadyForReview(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyIsDraft(isDraft)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// applyIsDraft sets the current pull request's IsDraft, copy-on-write. A
// no-op if no pull request is open.
func (s *Store) applyIsDraft(isDraft bool) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.IsDraft = isDraft
	s.currentPR = &pr
}

// Merge merges the current pull request via GitHub's mergePullRequest
// mutation. headline/body are the merge commit's own headline/body
// (nil means let GitHub fill in its own default, matching
// gh.Client.MergePullRequest's optional-variable convention). Returns
// false (besides emitting an EventError) without enqueueing anything when
// no pull request is open or its detail has not resolved yet.
//
// Uses enqueuePreparedMutationWithListRefresh: prepare (run at the front
// of the queue, immediately before the mutation's GitHub call, not back
// at enqueue time — see mutations.go's own doc comment for why this
// matters for any mutation queued behind another) captures the pull
// request's own id and current HeadOID as expectedHeadOid, so the
// mutation targets the exact commit the user was looking at when they
// asked to merge, and refuses locally with errPullRequestNotOpen — no
// GitHub call at all — when the pull request is no longer OPEN by then
// (already merged or closed by a mutation ahead of it in the queue, say).
// A stale Mergeable/MergeStateStatus is deliberately not checked here
// (left for the server to accept or reject on its own, current terms).
//
// On success, State/Merged/MergedAt are applied optimistically, then the
// pull request is invalidated/refetched and the list is refreshed (a
// merged pull request must disappear from an "open" list section).
func (s *Store) Merge(method model.MergeMethod, headline, body *string) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var id, expectedHeadOID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		if s.currentPR.State != model.PRStateOpen {
			return errPullRequestNotOpen
		}
		id = s.currentPR.ID
		expectedHeadOID = s.currentPR.HeadOID
		return nil
	}
	run := func(ctx context.Context) (func(), error) {
		pr, rl, err := s.deps.GitHub.MergePullRequest(ctx, id, method, headline, body, expectedHeadOID)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyMergeResult(pr)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutationWithListRefresh("merge", prepare, run)
	return true
}

// applyMergeResult applies update's State/Merged/MergedAt to the current
// pull request, copy-on-write. A no-op if no pull request is open.
func (s *Store) applyMergeResult(update model.PullRequest) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.State = update.State
	pr.Merged = update.Merged
	pr.MergedAt = update.MergedAt
	s.currentPR = &pr
}

// Close closes the current pull request via GitHub's closePullRequest
// mutation. Returns false (besides emitting an EventError) without
// enqueueing anything when no pull request is open or its detail has not
// resolved yet.
//
// On success, the server's post-close State is applied optimistically,
// then the pull request is invalidated/refetched and the list is
// refreshed.
func (s *Store) Close() bool {
	if !s.mutationTarget() {
		return false
	}
	id := s.currentPR.ID

	s.enqueueMutationWithListRefresh("close", func(ctx context.Context) (func(), error) {
		state, rl, err := s.deps.GitHub.ClosePullRequest(ctx, id)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyPRState(state)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// Reopen reopens the current pull request via GitHub's reopenPullRequest
// mutation, mirroring Close.
func (s *Store) Reopen() bool {
	if !s.mutationTarget() {
		return false
	}
	id := s.currentPR.ID

	s.enqueueMutationWithListRefresh("reopen", func(ctx context.Context) (func(), error) {
		state, rl, err := s.deps.GitHub.ReopenPullRequest(ctx, id)
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyPRState(state)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// applyPRState sets the current pull request's State, copy-on-write. A
// no-op if no pull request is open.
func (s *Store) applyPRState(state model.PRState) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.State = state
	s.currentPR = &pr
}
