package store

import (
	"context"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// commentTestRef is a fixed pull request reference used across mutation
// tests.
func commentTestRef(number int) model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "o", Name: "r"}, Number: number}
}

// openPRWithID opens ref on s and applies a minimal detail result with the
// given pull request node ID, via the fake's detailFunc, so AddComment has
// a subject ID to send.
func openPRWithID(t *testing.T, s *Store, gitHub *fakeGitHub, disp *fakeDispatcher, ref model.PRRef, id string) {
	t.Helper()
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: id, Ref: r}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
}

func collectEvents(s *Store) *[]Event {
	events := &[]Event{}
	s.Subscribe(func(e Event) { *events = append(*events, e) })
	return events
}

func TestAddComment_NoPullRequestOpen_EmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	events := collectEvents(s)

	s.AddComment("hello")
	runUntilIdle(t, disp)

	if s.Mutating() {
		t.Error("Mutating() = true, want false: nothing should have been enqueued")
	}
	if n := s.PendingMutations(); n != 0 {
		t.Errorf("PendingMutations() = %d, want 0", n)
	}
	if len(gitHub.addCommentCallsSnapshot()) != 0 {
		t.Error("AddIssueComment was called, want no-op when no pull request is open")
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for AddComment with no pull request open")
	}
}

func hasErrorEvent(events []Event) bool {
	for _, e := range events {
		if e.Kind == EventError {
			return true
		}
	}
	return false
}

func TestAddComment_Success_OptimisticApplyThenRefetch(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	// The refetch triggered after a successful mutation (finishMutation's
	// startDetailFetch(false)) is a second call to detailFunc: it returns
	// the new comment too, simulating GitHub's own post-mutation state,
	// so the assertions below observe the refetch-confirmed Timeline
	// rather than only the transient optimistic one.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_node_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.Timeline = []model.TimelineItem{{
				Kind:         model.TimelineKindIssueComment,
				IssueComment: &model.IssueComment{ID: "IC_1", Body: "hello", Author: model.User{Login: "octocat"}},
			}}
		}
		return gh.DetailResult{PR: pr}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setAddCommentFunc(func(_ context.Context, subjectID, body string) (model.IssueComment, error) {
		return model.IssueComment{ID: "IC_1", Body: body, Author: model.User{Login: "octocat"}}, nil
	})

	s.AddComment("hello")
	runUntilIdle(t, disp)

	calls := gitHub.addCommentCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("AddIssueComment call count = %d, want 1", len(calls))
	}
	if calls[0].subjectID != "PR_node_1" || calls[0].body != "hello" {
		t.Errorf("AddIssueComment call = %+v, want subjectID=PR_node_1 body=hello", calls[0])
	}

	pr := s.CurrentPR()
	if pr == nil {
		t.Fatal("CurrentPR() = nil after AddComment")
	}
	found := false
	for _, item := range pr.Timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == "IC_1" {
			found = true
		}
	}
	if !found {
		t.Errorf("Timeline does not contain the new comment: %+v", pr.Timeline)
	}

	// The detail was refetched after the mutation: once for OpenPR, once
	// for the post-mutation refetch.
	if got := gitHub.detailCallCount(); got != 2 {
		t.Errorf("detail call count = %d, want 2 (open + post-mutation refetch)", got)
	}

	if s.Mutating() {
		t.Error("Mutating() = true after completion, want false")
	}
	if n := s.PendingMutations(); n != 0 {
		t.Errorf("PendingMutations() = %d, want 0", n)
	}
	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil", s.LastError())
	}
}

func TestEditComment_Success_ReplacesInPlace(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	// As in TestAddComment_Success_OptimisticApplyThenRefetch, the
	// post-mutation refetch (detailFunc's second call) reflects the edit
	// server-side, so the final, refetch-confirmed state is what gets
	// asserted below.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		body := "original"
		if gitHub.detailCallCount() > 1 {
			body = "edited"
		}
		return gh.DetailResult{PR: model.PullRequest{
			ID:  "PR_node_1",
			Ref: r,
			Timeline: []model.TimelineItem{{
				Kind:         model.TimelineKindIssueComment,
				IssueComment: &model.IssueComment{ID: "IC_1", Body: body},
			}},
		}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setUpdateCommentFunc(func(_ context.Context, id, body string) (model.IssueComment, error) {
		return model.IssueComment{ID: id, Body: body}, nil
	})

	s.EditComment("IC_1", "edited")
	runUntilIdle(t, disp)

	calls := gitHub.updateCommentCallsSnapshot()
	if len(calls) != 1 || calls[0].id != "IC_1" || calls[0].body != "edited" {
		t.Fatalf("UpdateIssueComment calls = %+v, want one call with id=IC_1 body=edited", calls)
	}

	pr := s.CurrentPR()
	if pr == nil || len(pr.Timeline) != 1 || pr.Timeline[0].IssueComment.Body != "edited" {
		t.Fatalf("Timeline after edit = %+v, want one comment with Body=edited", pr.Timeline)
	}
}

func TestDeleteComment_Success_RemovesFromTimeline(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	// The post-mutation refetch (detailFunc's second call) no longer
	// includes the comment, simulating GitHub's own post-delete state.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_node_1", Ref: r}
		if gitHub.detailCallCount() == 1 {
			pr.Timeline = []model.TimelineItem{{
				Kind:         model.TimelineKindIssueComment,
				IssueComment: &model.IssueComment{ID: "IC_1", Body: "bye"},
			}}
		}
		return gh.DetailResult{PR: pr}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setDeleteCommentFunc(func(context.Context, string) error {
		return nil
	})

	s.DeleteComment("IC_1")
	runUntilIdle(t, disp)

	calls := gitHub.deleteCommentCallsSnapshot()
	if len(calls) != 1 || calls[0].id != "IC_1" {
		t.Fatalf("DeleteIssueComment calls = %+v, want one call with id=IC_1", calls)
	}

	pr := s.CurrentPR()
	if pr == nil {
		t.Fatal("CurrentPR() = nil after DeleteComment")
	}
	for _, item := range pr.Timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == "IC_1" {
			t.Error("deleted comment is still present in Timeline")
		}
	}
}

func TestMutationQueue_FIFOSingleFlight(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_node_1")

	var order []string
	block1 := make(chan struct{})
	// started fires once the "first" mutation's own goroutine has
	// actually begun executing GitHub.AddIssueComment (as opposed to
	// merely having been enqueued): a plain assertion right after
	// s.AddComment("first") would otherwise race the goroutine's own
	// scheduling, since starting it is asynchronous by construction (see
	// startNextMutation).
	started := make(chan struct{}, 1)
	gitHub.setAddCommentFunc(func(ctx context.Context, _, body string) (model.IssueComment, error) {
		if body == "first" {
			started <- struct{}{}
			<-block1
		}
		order = append(order, body)
		return model.IssueComment{ID: "IC_" + body, Body: body}, nil
	})

	s.AddComment("first")
	s.AddComment("second")
	<-started

	if n := s.PendingMutations(); n != 2 {
		t.Fatalf("PendingMutations() = %d immediately after enqueuing two, want 2", n)
	}
	if !s.Mutating() {
		t.Fatal("Mutating() = false, want true once the first mutation has started")
	}
	if len(gitHub.addCommentCallsSnapshot()) != 1 {
		t.Fatalf("AddIssueComment call count = %d before the first finishes, want 1 (single-flight)", len(gitHub.addCommentCallsSnapshot()))
	}

	close(block1)
	runUntilIdle(t, disp)

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("mutation execution order = %v, want [first second]", order)
	}
	if s.Mutating() {
		t.Error("Mutating() = true after both finished, want false")
	}
	if n := s.PendingMutations(); n != 0 {
		t.Errorf("PendingMutations() = %d, want 0", n)
	}
}

func TestMutation_Error_KeepsStateEmitsEventError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	events := collectEvents(s)

	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_node_1")

	wantErr := &gh.Error{Kind: gh.KindValidation, Message: "body too long"}
	gitHub.setAddCommentFunc(func(context.Context, string, string) (model.IssueComment, error) {
		return model.IssueComment{}, wantErr
	})

	s.AddComment("x")
	runUntilIdle(t, disp)

	pr := s.CurrentPR()
	if pr == nil || len(pr.Timeline) != 0 {
		t.Errorf("Timeline changed after a failed mutation: %+v", pr)
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for a failed mutation")
	}
	if s.LastError() == nil {
		t.Error("LastError() = nil after a failed mutation, want the mutation's error")
	}
	if s.Mutating() {
		t.Error("Mutating() = true after the failed mutation finished, want false")
	}

	// A subsequent successful mutation clears the standing mutation error.
	gitHub.setAddCommentFunc(func(_ context.Context, _, body string) (model.IssueComment, error) {
		return model.IssueComment{ID: "IC_ok", Body: body}, nil
	})
	s.AddComment("y")
	runUntilIdle(t, disp)

	if s.LastError() != nil {
		t.Errorf("LastError() = %v after a subsequent successful mutation, want nil", s.LastError())
	}
}

// TestMutation_Error_TriggersRefetch confirms finishMutation's failure path
// invalidates and refetches the target pull request exactly like its
// success path does (see invalidateAndRefetch): a failed mutation's own
// network call may have partially taken effect server-side even though it
// ultimately reported an error, so the local state must converge to
// GitHub's own rather than silently drift until the next auto-refresh (up
// to 5 minutes later). AddComment stands in for any mutation here, since
// the refetch trigger lives in the queue's shared finishMutation, not in
// any one mutation's own apply.
func TestMutation_Error_TriggersRefetch(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_node_1")
	before := gitHub.detailCallCount()

	gitHub.setAddCommentFunc(func(context.Context, string, string) (model.IssueComment, error) {
		return model.IssueComment{}, errors.New("boom: network failure")
	})

	s.AddComment("x")
	runUntilIdle(t, disp)

	if got := gitHub.detailCallCount() - before; got != 1 {
		t.Errorf("detail calls after the failed mutation = %d, want 1 (the failure-triggered refetch)", got)
	}
}

func TestMutation_GenerationGuard_SwitchPRSkipsApplyButStillInvalidates(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		return model.User{Login: "octocat"}, model.RateLimit{}, nil
	}
	s.Start(context.Background())
	runUntilIdle(t, disp)

	refA := commentTestRef(1)
	refB := commentTestRef(2)

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

	s.AddComment("for-a")
	// AddComment/enqueueMutation/startNextMutation run synchronously on
	// this goroutine before the mutation's own goroutine (blocked on
	// <-block) gets a chance to run, so OpenPR(refB) below is guaranteed
	// to see the mutation still targeting refA, without any sleep.
	s.OpenPR(refB)
	runUntilIdle(t, disp)

	close(block)
	runUntilIdle(t, disp)

	pr := s.CurrentPR()
	if pr == nil || pr.Ref != refB {
		t.Fatalf("CurrentPR() = %+v, want the pull request for refB", pr)
	}
	for _, item := range pr.Timeline {
		if item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil && item.IssueComment.ID == "IC_a" {
			t.Error("apply must be skipped once the current pull request changed")
		}
	}
}

func TestMutation_Stop_CancelsSilently(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	events := collectEvents(s)

	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_node_1")

	block := make(chan struct{})
	gitHub.setAddCommentFunc(func(ctx context.Context, _, _ string) (model.IssueComment, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return model.IssueComment{}, ctx.Err()
		}
		return model.IssueComment{ID: "IC_1"}, nil
	})

	s.AddComment("hi")
	s.Stop()
	close(block)
	runUntilIdle(t, disp)

	if hasErrorEvent(*events) {
		t.Error("Stop-cancelled mutation emitted EventError, want silence")
	}
	if s.Mutating() {
		t.Error("Mutating() = true after Stop, want false")
	}
	if n := s.PendingMutations(); n != 0 {
		t.Errorf("PendingMutations() = %d after Stop, want 0", n)
	}
}

func TestMutating_EmitsEventMutationChanged(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_node_1")

	var mutationEvents int
	s.Subscribe(func(e Event) {
		if e.Kind == EventMutationChanged {
			mutationEvents++
		}
	})

	s.AddComment("hi")
	runUntilIdle(t, disp)

	if mutationEvents < 2 {
		t.Errorf("EventMutationChanged fired %d times, want at least 2 (start and finish)", mutationEvents)
	}
}

func TestAddComment_DetailNotLoaded_EmitsNotLoadedError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	// The detail fetch fails, so the pull request is open but has no
	// loaded detail (and no cached one) to attach a comment to.
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{}, errors.New("boom")
	})
	s.OpenPR(commentTestRef(7))
	runUntilIdle(t, disp)
	events := collectEvents(s)

	s.AddComment("hello")
	runUntilIdle(t, disp)

	if len(gitHub.addCommentCallsSnapshot()) != 0 {
		t.Fatal("AddIssueComment was called although the detail is not loaded")
	}
	var got error
	for _, e := range *events {
		if e.Kind == EventError {
			got = e.Err
		}
	}
	if !errors.Is(got, errPullRequestNotLoaded) {
		t.Fatalf("EventError = %v, want errPullRequestNotLoaded", got)
	}
}

func TestAddComment_ReturnsWhetherItEnqueued(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	if got := s.AddComment("hello"); got {
		t.Error("AddComment(...) = true with no pull request open, want false")
	}

	openPRWithID(t, s, gitHub, disp, commentTestRef(1), "PR_1")
	if got := s.AddComment("hello"); !got {
		t.Error("AddComment(...) = false with a loaded pull request, want true")
	}
	runUntilIdle(t, disp)
}

func TestEditComment_ReturnsWhetherItEnqueued(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	if got := s.EditComment("IC_1", "hello"); got {
		t.Error("EditComment(...) = true with no pull request open, want false")
	}

	openPRWithID(t, s, gitHub, disp, commentTestRef(1), "PR_1")
	if got := s.EditComment("IC_1", "hello"); !got {
		t.Error("EditComment(...) = false with a loaded pull request, want true")
	}
	runUntilIdle(t, disp)
}

// TestMutationError_IsolatedFromStandingSectionError reproduces the bug an
// App-level composer must route around: LastError() also surfaces a lower
// priority, unrelated standing error (here, a section's), so a caller that
// wants to know specifically "did *my* mutation fail" needs
// MutationError() instead, not LastError().
func TestMutationError_IsolatedFromStandingSectionError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	openPRWithID(t, s, gitHub, disp, commentTestRef(1), "PR_1")

	s.LoadList(false)
	runUntilIdle(t, disp)
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, errors.New("section boom")
	}
	s.Refresh()
	runUntilIdle(t, disp)
	if s.LastError() == nil {
		t.Fatal("precondition: LastError() = nil, want the standing section error")
	}

	s.AddComment("hello")
	runUntilIdle(t, disp)

	if err := s.MutationError(); err != nil {
		t.Errorf("MutationError() = %v after a successful mutation, want nil even though an unrelated section error still stands", err)
	}
	if s.LastError() == nil {
		t.Error("LastError() = nil after AddComment succeeded; want the standing section error still surfaced (LastError is not mutation-specific)")
	}
}
