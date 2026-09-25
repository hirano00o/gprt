package store

import (
	"context"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// TestToggleReaction_NoPullRequestOpen_EmitsError mirrors
// TestAddComment_NoPullRequestOpen_EmitsError: nothing is enqueued, and no
// GitHub call is ever made, when no pull request is open.
func TestToggleReaction_NoPullRequestOpen_EmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	events := collectEvents(s)

	if got := s.ToggleReaction("PR_1", model.ReactionThumbsUp); got {
		t.Fatal("ToggleReaction() = true, want false: no pull request is open")
	}
	runUntilIdle(t, disp)

	if len(gitHub.addReactionCallsSnapshot()) != 0 || len(gitHub.removeReactionCallsSnapshot()) != 0 {
		t.Error("AddReaction/RemoveReaction was called, want no-op when no pull request is open")
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for ToggleReaction with no pull request open")
	}
}

// TestToggleReaction_OnPullRequest_NotYetReacted_CallsAddReaction covers the
// simplest subject location: the pull request itself, with no prior
// reaction of the given content, decides AddReaction from local state.
func TestToggleReaction_OnPullRequest_NotYetReacted_CallsAddReaction(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	wantGroups := []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 1, ViewerHasReacted: true}}
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{ID: "PR_1", ReactionGroups: wantGroups})

	gitHub.setAddReactionFunc(func(_ context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return wantGroups, nil
	})

	if got := s.ToggleReaction("PR_1", model.ReactionThumbsUp); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.addReactionCallsSnapshot(); len(calls) != 1 || calls[0].subjectID != "PR_1" || calls[0].content != model.ReactionThumbsUp {
		t.Fatalf("AddReaction calls = %+v, want one call for PR_1/THUMBS_UP", calls)
	}
	if len(gitHub.removeReactionCallsSnapshot()) != 0 {
		t.Error("RemoveReaction was called, want AddReaction only: the pull request had not reacted yet")
	}
	groups := s.CurrentPR().ReactionGroups
	if len(groups) != 1 || groups[0].Content != model.ReactionThumbsUp || !groups[0].ViewerHasReacted {
		t.Errorf("CurrentPR().ReactionGroups = %+v, want the applied group from AddReaction's payload", groups)
	}
}

// TestToggleReaction_OnPullRequest_AlreadyReacted_CallsRemoveReaction is
// AddReaction's mirror: ViewerHasReacted already true locally decides
// RemoveReaction.
func TestToggleReaction_OnPullRequest_AlreadyReacted_CallsRemoveReaction(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	before := model.PullRequest{
		ID:             "PR_1",
		ReactionGroups: []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 1, ViewerHasReacted: true}},
	}
	wantGroups := []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 0, ViewerHasReacted: false}}
	openPRWithDetail(t, s, gitHub, disp, ref, before)
	setPostMutationDetail(gitHub, ref, before, model.PullRequest{ID: "PR_1", ReactionGroups: wantGroups})

	gitHub.setRemoveReactionFunc(func(_ context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return wantGroups, nil
	})

	if got := s.ToggleReaction("PR_1", model.ReactionThumbsUp); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.removeReactionCallsSnapshot(); len(calls) != 1 || calls[0].subjectID != "PR_1" || calls[0].content != model.ReactionThumbsUp {
		t.Fatalf("RemoveReaction calls = %+v, want one call for PR_1/THUMBS_UP", calls)
	}
	if len(gitHub.addReactionCallsSnapshot()) != 0 {
		t.Error("AddReaction was called, want RemoveReaction only: the pull request had already reacted")
	}
	groups := s.CurrentPR().ReactionGroups
	if len(groups) != 1 || groups[0].ViewerHasReacted {
		t.Errorf("CurrentPR().ReactionGroups = %+v, want ViewerHasReacted=false after RemoveReaction's payload applied", groups)
	}
}

// TestToggleReaction_IssueCommentInTimeline covers a Timeline issue
// comment's ReactionGroups being replaced copy-on-write, without disturbing
// other Timeline entries.
func TestToggleReaction_IssueCommentInTimeline(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	other := model.IssueComment{ID: "IC_other"}
	target := model.IssueComment{ID: "IC_1"}
	before := model.PullRequest{
		ID: "PR_1",
		Timeline: []model.TimelineItem{
			{Kind: model.TimelineKindIssueComment, IssueComment: &other},
			{Kind: model.TimelineKindIssueComment, IssueComment: &target},
		},
	}
	wantGroups := []model.ReactionGroup{{Content: model.ReactionHeart, Count: 1, ViewerHasReacted: true}}
	updatedTarget := target
	updatedTarget.ReactionGroups = wantGroups
	openPRWithDetail(t, s, gitHub, disp, ref, before)
	setPostMutationDetail(gitHub, ref, before, model.PullRequest{
		ID: "PR_1",
		Timeline: []model.TimelineItem{
			{Kind: model.TimelineKindIssueComment, IssueComment: &other},
			{Kind: model.TimelineKindIssueComment, IssueComment: &updatedTarget},
		},
	})

	gitHub.setAddReactionFunc(func(_ context.Context, _ string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return wantGroups, nil
	})

	if got := s.ToggleReaction("IC_1", model.ReactionHeart); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	timeline := s.CurrentPR().Timeline
	if timeline[0].IssueComment.ReactionGroups != nil {
		t.Errorf("unrelated Timeline entry IC_other.ReactionGroups = %+v, want untouched (nil)", timeline[0].IssueComment.ReactionGroups)
	}
	got := timeline[1].IssueComment.ReactionGroups
	if len(got) != 1 || got[0].Content != model.ReactionHeart || !got[0].ViewerHasReacted {
		t.Errorf("IC_1.ReactionGroups = %+v, want the applied group", got)
	}
}

// TestToggleReaction_ReviewInTimelineAndLatestReviews covers a submitted
// review appearing in both Timeline and LatestReviews at once: both copies
// must be updated together (see docs/DESIGN.md's PullRequest.LatestReviews
// bullet).
func TestToggleReaction_ReviewInTimelineAndLatestReviews(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	review := model.Review{ID: "PRR_1", State: model.ReviewStateApproved}
	before := model.PullRequest{
		ID:            "PR_1",
		Timeline:      []model.TimelineItem{{Kind: model.TimelineKindReview, Review: &review}},
		LatestReviews: []model.Review{review},
	}
	wantGroups := []model.ReactionGroup{{Content: model.ReactionLaugh, Count: 1, ViewerHasReacted: true}}
	updatedReview := review
	updatedReview.ReactionGroups = wantGroups
	openPRWithDetail(t, s, gitHub, disp, ref, before)
	setPostMutationDetail(gitHub, ref, before, model.PullRequest{
		ID:            "PR_1",
		Timeline:      []model.TimelineItem{{Kind: model.TimelineKindReview, Review: &updatedReview}},
		LatestReviews: []model.Review{updatedReview},
	})

	gitHub.setAddReactionFunc(func(_ context.Context, _ string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return wantGroups, nil
	})

	if got := s.ToggleReaction("PRR_1", model.ReactionLaugh); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	pr := s.CurrentPR()
	timelineGroups := pr.Timeline[0].Review.ReactionGroups
	latestGroups := pr.LatestReviews[0].ReactionGroups
	if len(timelineGroups) != 1 || timelineGroups[0].Content != model.ReactionLaugh {
		t.Errorf("Timeline review ReactionGroups = %+v, want the applied group", timelineGroups)
	}
	if len(latestGroups) != 1 || latestGroups[0].Content != model.ReactionLaugh {
		t.Errorf("LatestReviews review ReactionGroups = %+v, want the same applied group", latestGroups)
	}
}

// TestToggleReaction_ReviewCommentInThread covers a review comment nested
// in ReviewThreads, without disturbing sibling comments or threads.
func TestToggleReaction_ReviewCommentInThread(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	before := model.PullRequest{
		ID: "PR_1",
		ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1"}, {ID: "RC_2"}}},
		},
	}
	wantGroups := []model.ReactionGroup{{Content: model.ReactionRocket, Count: 1, ViewerHasReacted: true}}
	openPRWithDetail(t, s, gitHub, disp, ref, before)
	setPostMutationDetail(gitHub, ref, before, model.PullRequest{
		ID: "PR_1",
		ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1"}, {ID: "RC_2", ReactionGroups: wantGroups}}},
		},
	})

	gitHub.setAddReactionFunc(func(_ context.Context, _ string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return wantGroups, nil
	})

	if got := s.ToggleReaction("RC_2", model.ReactionRocket); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	comments := s.CurrentPR().ReviewThreads[0].Comments
	if comments[0].ReactionGroups != nil {
		t.Errorf("unrelated comment RC_1.ReactionGroups = %+v, want untouched (nil)", comments[0].ReactionGroups)
	}
	if got := comments[1].ReactionGroups; len(got) != 1 || got[0].Content != model.ReactionRocket {
		t.Errorf("RC_2.ReactionGroups = %+v, want the applied group", got)
	}
}

// TestToggleReaction_SubjectNotFound_FailsLocallyWithoutGitHubCall covers
// the prepare-time failure path: no known subject matches, so nothing is
// sent to GitHub.
func TestToggleReaction_SubjectNotFound_FailsLocallyWithoutGitHubCall(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	events := collectEvents(s)

	if got := s.ToggleReaction("does-not-exist", model.ReactionEyes); !got {
		t.Fatal("ToggleReaction() = false, want true: enqueueing succeeds, prepare fails later")
	}
	runUntilIdle(t, disp)

	if len(gitHub.addReactionCallsSnapshot()) != 0 || len(gitHub.removeReactionCallsSnapshot()) != 0 {
		t.Error("AddReaction/RemoveReaction was called, want no GitHub call for an unknown subject")
	}
	if !errors.Is(firstErrorEvent(*events), errReactionSubjectNotFound) {
		t.Errorf("EventError = %v, want errReactionSubjectNotFound", firstErrorEvent(*events))
	}
}

// TestQueuedToggleReaction_SecondPrepareSeesFirstsAppliedState covers the
// mutation-queue ordering rule (see docs/DESIGN.md's concurrency rules and
// mutations.go's enqueuePreparedMutation): two toggles on the same subject
// enqueued back to back must not both decide "add" from the same
// enqueue-time snapshot. The first mutation's own apply must be visible to
// the second's prepare closure, which runs only once the first has
// finished and the second reaches the front of the queue - never at
// enqueue time.
func TestQueuedToggleReaction_SecondPrepareSeesFirstsAppliedState(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{ID: "PR_1"})

	release := make(chan struct{})
	gitHub.setAddReactionFunc(func(_ context.Context, _ string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		<-release // hold the first mutation in flight until both are enqueued
		return []model.ReactionGroup{{Content: content, Count: 1, ViewerHasReacted: true}}, nil
	})
	gitHub.setRemoveReactionFunc(func(_ context.Context, _ string, content model.ReactionContent) ([]model.ReactionGroup, error) {
		return []model.ReactionGroup{{Content: content, Count: 0, ViewerHasReacted: false}}, nil
	})

	// Both calls decide from the pull request's pre-toggle state (not yet
	// reacted) if their prepare closures ran at enqueue time; only the
	// second one's prepare running after the first's apply (once dequeued)
	// correctly sees ViewerHasReacted=true and calls RemoveReaction.
	s.ToggleReaction("PR_1", model.ReactionThumbsUp)
	s.ToggleReaction("PR_1", model.ReactionThumbsUp)
	if n := s.PendingMutations(); n != 2 {
		t.Fatalf("PendingMutations() = %d immediately after enqueuing two, want 2", n)
	}
	close(release)
	runUntilIdle(t, disp)

	if n := len(gitHub.addReactionCallsSnapshot()); n != 1 {
		t.Errorf("AddReaction called %d times, want 1", n)
	}
	if n := len(gitHub.removeReactionCallsSnapshot()); n != 1 {
		t.Errorf("RemoveReaction called %d times, want 1: the second toggle's prepare must see the first's applied ViewerHasReacted=true", n)
	}
}

// TestToggleReaction_TargetChangedBeforeStart_NoGitHubCallEmitsError
// mirrors TestReviewMutation_TargetChangedBeforeStart_NoGitHubCallEmitsError
// for reactions: the current pull request changes after enqueue but before
// this mutation's prepare runs.
func TestToggleReaction_TargetChangedBeforeStart_NoGitHubCallEmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	refA := reviewTestRef(1)
	refB := reviewTestRef(2)
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})
	s.OpenPR(refA)
	runUntilIdle(t, disp)

	block := make(chan struct{})
	gitHub.setAddCommentFunc(func(ctx context.Context, _, _ string) (model.IssueComment, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return model.IssueComment{}, ctx.Err()
		}
		return model.IssueComment{ID: "IC_a"}, nil
	})

	// An unrelated AddComment mutation occupies the queue's single flight
	// slot (blocked), so the ToggleReaction enqueued right after it is
	// still queued, not yet started, when OpenPR(refB) switches the
	// current pull request out from under it.
	s.AddComment("occupies the queue")
	events := collectEvents(s)
	s.ToggleReaction("id-"+refA.Key(), model.ReactionThumbsUp)
	s.OpenPR(refB)
	close(block)
	runUntilIdle(t, disp)

	if n := len(gitHub.addReactionCallsSnapshot()); n != 0 {
		t.Errorf("AddReaction was called %d times, want 0: the pull request changed before this mutation started", n)
	}
	if !errors.Is(firstErrorEvent(*events), errMutationTargetChanged) {
		t.Errorf("EventError = %v, want errMutationTargetChanged", firstErrorEvent(*events))
	}
}

// TestToggleReaction_Error_KeepsStateEmitsEventErrorAndRefetches mirrors
// TestCommentOnLines_Error_KeepsStateEmitsEventError: a failed (but
// actually sent) mutation must still trigger the refetch every finished
// mutation that reached GitHub triggers.
func TestToggleReaction_Error_KeepsStateEmitsEventErrorAndRefetches(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	before := gitHub.detailCallCount()

	wantErr := errors.New("boom")
	gitHub.setAddReactionFunc(func(context.Context, string, model.ReactionContent) ([]model.ReactionGroup, error) {
		return nil, wantErr
	})
	events := collectEvents(s)

	if got := s.ToggleReaction("PR_1", model.ReactionThumbsUp); !got {
		t.Fatal("ToggleReaction() = false, want true")
	}
	runUntilIdle(t, disp)

	if !errors.Is(firstErrorEvent(*events), wantErr) {
		t.Errorf("EventError = %v, want %v", firstErrorEvent(*events), wantErr)
	}
	if s.CurrentPR().ReactionGroups != nil {
		t.Errorf("ReactionGroups = %+v, want unchanged (nil) after a failed mutation", s.CurrentPR().ReactionGroups)
	}
	if got := gitHub.detailCallCount() - before; got != 1 {
		t.Errorf("detail call count = %d, want 1: a failed mutation that reached GitHub must still trigger a refetch", got)
	}
}
