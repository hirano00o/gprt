// Package store's reaction operations (ToggleReaction) add or remove one of
// GitHub's eight reactions on a reactable subject of the current pull
// request: the pull request itself, an issue comment or a review in its
// conversation timeline, a review in LatestReviews, or a review comment in
// one of its review threads. Like review.go's send-mode/pending-review
// decisions, whether to add or remove is decided in a prepare closure
// immediately before the mutation runs, not at enqueue time (see
// mutations.go's enqueuePreparedMutation): two toggles on the same subject
// queued back to back must not both decide "add" from the same stale
// enqueue-time snapshot.
package store

import (
	"context"
	"errors"

	"github.com/hirano00o/gprt/internal/model"
)

// errReactionSubjectNotFound is emitted (as an EventError) by
// ToggleReaction's prepare closure when subjectID does not match any known
// reactable location on the current pull request at run-start time:
// nothing was sent to GitHub, mirroring a review mutation's
// errMutationTargetChanged failure (see mutations.go's mutation.prepare
// doc comment for why a prepare failure never starts run's goroutine at
// all).
var errReactionSubjectNotFound = errors.New("store: reaction subject not found on the current pull request")

// reactionGroupsFor returns subjectID's current ReactionGroups on the
// current pull request, wherever it appears — the pull request itself, an
// issue comment or a review in the conversation timeline, a review in
// LatestReviews, or a review comment in a review thread — or (nil, false)
// when no pull request is open or subjectID matches nothing. A submitted
// review can appear in both Timeline and LatestReviews at once; either
// copy's ReactionGroups is equally valid to read from here (applyReaction
// keeps both in sync), so the first match found is used.
func (s *Store) reactionGroupsFor(subjectID string) ([]model.ReactionGroup, bool) {
	if s.currentPR == nil {
		return nil, false
	}
	if s.currentPR.ID == subjectID {
		return s.currentPR.ReactionGroups, true
	}
	for _, item := range s.currentPR.Timeline {
		switch item.Kind {
		case model.TimelineKindIssueComment:
			if item.IssueComment != nil && item.IssueComment.ID == subjectID {
				return item.IssueComment.ReactionGroups, true
			}
		case model.TimelineKindReview:
			if item.Review != nil && item.Review.ID == subjectID {
				return item.Review.ReactionGroups, true
			}
		}
	}
	for _, r := range s.currentPR.LatestReviews {
		if r.ID == subjectID {
			return r.ReactionGroups, true
		}
	}
	for _, t := range s.currentPR.ReviewThreads {
		for _, c := range t.Comments {
			if c.ID == subjectID {
				return c.ReactionGroups, true
			}
		}
	}
	return nil, false
}

// reactionGroupsHasViewerReacted reports whether groups already has content
// with ViewerHasReacted set, so ToggleReaction's prepare closure can decide
// AddReaction vs RemoveReaction from local state.
func reactionGroupsHasViewerReacted(groups []model.ReactionGroup, content model.ReactionContent) bool {
	for _, g := range groups {
		if g.Content == content {
			return g.ViewerHasReacted
		}
	}
	return false
}

// ToggleReaction adds or removes content on subjectID — the current pull
// request itself, an issue comment, a review, or a review comment — via
// GitHub's addReaction/removeReaction mutations. Which one to call is
// decided in a prepare closure immediately before the mutation runs (see
// the package doc), from subjectID's current ViewerHasReacted flag; if
// subjectID cannot be found on the current pull request at that point, the
// mutation fails locally with errReactionSubjectNotFound and GitHub is
// never called. Returns false (besides emitting an EventError) without
// enqueueing anything when no pull request is open or its detail has not
// resolved yet — see AddComment's doc comment for why a caller relying on
// this call's effect must check the return value.
func (s *Store) ToggleReaction(subjectID string, content model.ReactionContent) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var remove bool
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		groups, found := s.reactionGroupsFor(subjectID)
		if !found {
			return errReactionSubjectNotFound
		}
		remove = reactionGroupsHasViewerReacted(groups, content)
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		var groups []model.ReactionGroup
		var rl model.RateLimit
		var err error
		if remove {
			s.deps.Logger.Debug("toggle_reaction: remove", "subject", subjectID, "content", content)
			groups, rl, err = s.deps.GitHub.RemoveReaction(ctx, subjectID, content)
		} else {
			s.deps.Logger.Debug("toggle_reaction: add", "subject", subjectID, "content", content)
			groups, rl, err = s.deps.GitHub.AddReaction(ctx, subjectID, content)
		}
		if err != nil {
			return nil, err
		}
		return func() {
			s.setRateLimit(rl)
			s.applyReactionGroups(subjectID, groups)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutation("toggle_reaction", prepare, run)
	return true
}

// applyReactionGroups replaces subjectID's ReactionGroups with groups
// everywhere it appears on the current pull request, copy-on-write (see
// mutations.go's appendTimelineComment for the pattern every apply helper
// in this package follows): the pull request itself, a Timeline issue
// comment or review, a LatestReviews review (a submitted review can appear
// in both places at once, and both copies are updated together so neither
// one goes stale), or a review comment in a review thread. A no-op if no
// pull request is open.
func (s *Store) applyReactionGroups(subjectID string, groups []model.ReactionGroup) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR

	if pr.ID == subjectID {
		pr.ReactionGroups = groups
	}

	timeline := append([]model.TimelineItem(nil), pr.Timeline...)
	for i, item := range timeline {
		switch item.Kind {
		case model.TimelineKindIssueComment:
			if item.IssueComment != nil && item.IssueComment.ID == subjectID {
				updated := *item.IssueComment
				updated.ReactionGroups = groups
				timeline[i] = model.TimelineItem{Kind: item.Kind, IssueComment: &updated}
			}
		case model.TimelineKindReview:
			if item.Review != nil && item.Review.ID == subjectID {
				updated := *item.Review
				updated.ReactionGroups = groups
				timeline[i] = model.TimelineItem{Kind: item.Kind, Review: &updated}
			}
		}
	}
	pr.Timeline = timeline

	reviews := append([]model.Review(nil), pr.LatestReviews...)
	for i, r := range reviews {
		if r.ID == subjectID {
			r.ReactionGroups = groups
			reviews[i] = r
		}
	}
	pr.LatestReviews = reviews

	threads := make([]model.ReviewThread, len(pr.ReviewThreads))
	for i, t := range pr.ReviewThreads {
		comments := t.Comments
		for j, c := range comments {
			if c.ID != subjectID {
				continue
			}
			comments = append([]model.ReviewComment(nil), comments...)
			comments[j].ReactionGroups = groups
			break
		}
		t.Comments = comments
		threads[i] = t
	}
	pr.ReviewThreads = threads

	s.currentPR = &pr
}
