// Package store's CreatePullRequest (M5, docs/DESIGN.md's mutation-sequence
// table row "Create") is the mutation queue's only unscoped mutation (see
// mutations.go's mutation.unscoped doc comment): unlike every other
// mutation in this package, it does not target the currently open pull
// request — there may be none open at all — so it does not go through
// mutationTarget()'s "is a pull request open" check, and finishMutation
// always applies its result rather than gating it on "is ref still
// current".
package store

import (
	"context"

	"github.com/hirano00o/gprt/internal/gh"
)

// CreatePullRequest opens a new pull request via GitHub's createPullRequest
// mutation and, if reviewerUserIDs/reviewerTeamIDs are non-empty, requests
// them on it (union: true, since they are the pull request's very first
// reviewers — there is nothing yet to replace). Always enqueues (there is
// no "no pull request open" precondition to fail on) and always returns
// true.
//
// On success, EventPullRequestCreated{Ref} fires so the UI can open the
// new pull request, and the list is refreshed (the new pull request must
// appear in it). A failure in the follow-up RequestReviewers call does not
// fail the whole mutation — the pull request itself was still created
// server-side, and losing its ref because a second, unrelated call then
// failed would be a worse outcome than surfacing the two independently:
// run instead returns a success apply that emits EventPullRequestCreated
// unconditionally and additionally reports the reviewer failure via its
// own EventError (see the run closure below).
func (s *Store) CreatePullRequest(in gh.CreatePullRequestInput, reviewerUserIDs, reviewerTeamIDs []string) bool {
	run := func(ctx context.Context) (func(), error) {
		pr, err := s.deps.GitHub.CreatePullRequest(ctx, in)
		if err != nil {
			return nil, err
		}

		var reviewerErr error
		if len(reviewerUserIDs) > 0 || len(reviewerTeamIDs) > 0 {
			_, rerr := s.deps.GitHub.RequestReviewers(ctx, pr.ID, reviewerUserIDs, reviewerTeamIDs, true)
			if rerr != nil {
				reviewerErr = rerr
			}
		}

		ref := pr.Ref
		return func() {
			s.emit(Event{Kind: EventPullRequestCreated, Ref: &ref})
			if reviewerErr != nil {
				// finishMutation's success path already reset mutationErr
				// to nil and called recomputeLastErr before invoking this
				// apply (see mutations.go), so setting it here — and
				// recomputing again — is what actually makes the reviewer
				// failure a standing error MutationError()/LastError() can
				// report; without this, the EventError emitted just below
				// would be the only trace of it (a one-shot notification
				// nothing else can query afterwards).
				s.mutationErr = reviewerErr
				s.recomputeLastErr()
				s.deps.Logger.Error("request reviewers after create failed", "ref", ref.Key(), "err", reviewerErr)
				s.emit(Event{Kind: EventError, Err: reviewerErr})
			}
		}, nil
	}

	s.enqueueUnscopedMutation("create_pull_request", run)
	return true
}
