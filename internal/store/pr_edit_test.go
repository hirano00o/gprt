package store

import (
	"context"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// openPRWithFullState mirrors openPRWithID but lets the caller control the
// full initial pull request state (State, HeadOID, ...), needed by tests
// that exercise Merge's prepare-time local refusal.
func openPRWithFullState(t *testing.T, s *Store, gitHub *fakeGitHub, disp *fakeDispatcher, pr model.PullRequest) {
	t.Helper()
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		out := pr
		out.Ref = r
		return gh.DetailResult{PR: out}, nil
	})
	s.OpenPR(pr.Ref)
	runUntilIdle(t, disp)
}

func TestUpdatePullRequestMeta_NoPullRequestOpen_EmitsError(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	events := collectEvents(s)

	if s.UpdatePullRequestMeta(gh.UpdatePullRequestInput{}) {
		t.Error("UpdatePullRequestMeta() = true, want false when no pull request is open")
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted")
	}
}

func TestUpdatePullRequestMeta_Success_AppliesAndRefreshesList(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_1")

	newTitle := "New title"
	// The refetch that finishMutation always triggers after a successful
	// mutation (invalidateAndRefetch) is a second call to detailFunc; it
	// must reflect the mutation's own effect (matching
	// TestAddComment_Success_OptimisticApplyThenRefetch's own pattern) so
	// the assertions below observe the refetch-confirmed state rather than
	// the fixture set up by openPRWithID getting re-applied over it.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.Title = newTitle
			pr.BaseRefName = "main"
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setUpdatePullRequestFunc(func(_ context.Context, id string, in gh.UpdatePullRequestInput) (model.PullRequest, model.RateLimit, error) {
		if id != "PR_1" || in.Title == nil || *in.Title != newTitle {
			t.Errorf("UpdatePullRequest called with id=%q in=%+v", id, in)
		}
		return model.PullRequest{Title: newTitle, BaseRefName: "main"}, model.RateLimit{}, nil
	})

	if !s.UpdatePullRequestMeta(gh.UpdatePullRequestInput{Title: &newTitle}) {
		t.Fatal("UpdatePullRequestMeta() = false, want true")
	}
	runUntilIdle(t, disp)

	if got := s.CurrentPR().Title; got != newTitle {
		t.Errorf("CurrentPR().Title = %q, want %q", got, newTitle)
	}
	if len(gitHub.searchCalls()) == 0 {
		t.Error("no SearchPullRequests calls observed, want the list refreshed after a successful mutation")
	}
}

func TestSetReviewers_Success_AppliesReviewRequests(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_1")

	want := []model.Reviewer{{ID: "U_1", Login: "bob", Kind: model.ReviewerKindUser}}
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.ReviewRequests = want
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setRequestReviewersFunc(func(_ context.Context, id string, userIDs, teamIDs []string, union bool) ([]model.Reviewer, model.RateLimit, error) {
		if id != "PR_1" || union {
			t.Errorf("RequestReviewers called with id=%q union=%v, want id=PR_1 union=false", id, union)
		}
		return want, model.RateLimit{}, nil
	})

	if !s.SetReviewers([]string{"U_1"}, nil) {
		t.Fatal("SetReviewers() = false, want true")
	}
	runUntilIdle(t, disp)

	got := s.CurrentPR().ReviewRequests
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("CurrentPR().ReviewRequests = %+v, want %+v", got, want)
	}
}

// TestSetReviewers_NilSlices_PassesEmptyArraysToGitHub covers the same
// null-vs-empty-array distinction gh.Client.RequestReviewers itself
// normalizes for (see nonNilIDs's doc comment in internal/gh/pr_edit.go):
// the store's own call to the GitHub interface must not rely solely on
// that internal normalization, since a fake substituted for GitHub in a
// test (or any other implementation) has no obligation to apply it.
func TestSetReviewers_NilSlices_PassesEmptyArraysToGitHub(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_1")

	if !s.SetReviewers(nil, nil) {
		t.Fatal("SetReviewers() = false, want true")
	}
	runUntilIdle(t, disp)

	calls := gitHub.requestReviewersCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("RequestReviewers calls = %d, want 1", len(calls))
	}
	if calls[0].userIDs == nil || len(calls[0].userIDs) != 0 {
		t.Errorf("RequestReviewers userIDs = %#v, want a non-nil empty slice", calls[0].userIDs)
	}
	if calls[0].teamIDs == nil || len(calls[0].teamIDs) != 0 {
		t.Errorf("RequestReviewers teamIDs = %#v, want a non-nil empty slice", calls[0].teamIDs)
	}
}

func TestSetDraft_ToDraft_CallsConvertToDraft(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_1")

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.IsDraft = true
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setConvertToDraftFunc(func(context.Context, string) (bool, model.RateLimit, error) {
		return true, model.RateLimit{}, nil
	})

	if !s.SetDraft(true) {
		t.Fatal("SetDraft(true) = false, want true")
	}
	runUntilIdle(t, disp)

	if !s.CurrentPR().IsDraft {
		t.Error("CurrentPR().IsDraft = false, want true after SetDraft(true)")
	}
	if len(gitHub.convertToDraftCallsSnapshot()) != 1 {
		t.Errorf("ConvertToDraft calls = %d, want 1", len(gitHub.convertToDraftCallsSnapshot()))
	}
	if len(gitHub.markReadyForReviewCallsSnapshot()) != 0 {
		t.Errorf("MarkReadyForReview calls = %d, want 0", len(gitHub.markReadyForReviewCallsSnapshot()))
	}
}

func TestSetDraft_ToReady_CallsMarkReadyForReview(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithFullState(t, s, gitHub, disp, model.PullRequest{ID: "PR_1", Ref: ref, IsDraft: true})

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r, IsDraft: true}
		if gitHub.detailCallCount() > 1 {
			pr.IsDraft = false
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setMarkReadyForReviewFunc(func(context.Context, string) (bool, model.RateLimit, error) {
		return false, model.RateLimit{}, nil
	})

	if !s.SetDraft(false) {
		t.Fatal("SetDraft(false) = false, want true")
	}
	runUntilIdle(t, disp)

	if s.CurrentPR().IsDraft {
		t.Error("CurrentPR().IsDraft = true, want false after SetDraft(false)")
	}
	if len(gitHub.markReadyForReviewCallsSnapshot()) != 1 {
		t.Errorf("MarkReadyForReview calls = %d, want 1", len(gitHub.markReadyForReviewCallsSnapshot()))
	}
}

func TestMerge_Success_AppliesResultAndRefreshesList(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithFullState(t, s, gitHub, disp, model.PullRequest{
		ID: "PR_1", Ref: ref, State: model.PRStateOpen, HeadOID: "headsha1",
	})

	mergedAt := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r, State: model.PRStateOpen, HeadOID: "headsha1"}
		if gitHub.detailCallCount() > 1 {
			pr.State = model.PRStateMerged
			pr.Merged = true
			pr.MergedAt = mergedAt
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setMergePullRequestFunc(func(
		_ context.Context, id string, method model.MergeMethod, headline, body *string, expectedHeadOID string,
	) (model.PullRequest, model.RateLimit, error) {
		if id != "PR_1" || method != model.MergeMethodSquash || expectedHeadOID != "headsha1" {
			t.Errorf("MergePullRequest called with id=%q method=%v expectedHeadOID=%q", id, method, expectedHeadOID)
		}
		return model.PullRequest{State: model.PRStateMerged, Merged: true, MergedAt: mergedAt}, model.RateLimit{}, nil
	})

	if !s.Merge(model.MergeMethodSquash, nil, nil) {
		t.Fatal("Merge() = false, want true")
	}
	runUntilIdle(t, disp)

	pr := s.CurrentPR()
	if pr.State != model.PRStateMerged || !pr.Merged || !pr.MergedAt.Equal(mergedAt) {
		t.Errorf("CurrentPR() = %+v, want merged state applied", pr)
	}
	if len(gitHub.searchCalls()) == 0 {
		t.Error("no SearchPullRequests calls observed, want the list refreshed after a successful merge")
	}
}

func TestMerge_RefusesLocallyWhenNotOpen(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithFullState(t, s, gitHub, disp, model.PullRequest{
		ID: "PR_1", Ref: ref, State: model.PRStateClosed, HeadOID: "headsha1",
	})
	events := collectEvents(s)

	if !s.Merge(model.MergeMethodMerge, nil, nil) {
		t.Fatal("Merge() = false, want true (the mutation was enqueued; prepare refuses it later)")
	}
	runUntilIdle(t, disp)

	if len(gitHub.mergePullRequestCallsSnapshot()) != 0 {
		t.Error("MergePullRequest was called, want prepare to refuse locally for a non-open pull request")
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for a local Merge refusal")
	}
}

func TestClose_Success(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithID(t, s, gitHub, disp, ref, "PR_1")

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r, State: model.PRStateOpen}
		if gitHub.detailCallCount() > 1 {
			pr.State = model.PRStateClosed
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setClosePullRequestFunc(func(context.Context, string) (model.PRState, model.RateLimit, error) {
		return model.PRStateClosed, model.RateLimit{}, nil
	})

	if !s.Close() {
		t.Fatal("Close() = false, want true")
	}
	runUntilIdle(t, disp)

	if got := s.CurrentPR().State; got != model.PRStateClosed {
		t.Errorf("CurrentPR().State = %v, want %v", got, model.PRStateClosed)
	}
}

func TestReopen_Success(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	ref := commentTestRef(1)
	openPRWithFullState(t, s, gitHub, disp, model.PullRequest{ID: "PR_1", Ref: ref, State: model.PRStateClosed})

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r, State: model.PRStateClosed}
		if gitHub.detailCallCount() > 1 {
			pr.State = model.PRStateOpen
		}
		return gh.DetailResult{PR: pr}, nil
	})
	gitHub.setReopenPullRequestFunc(func(context.Context, string) (model.PRState, model.RateLimit, error) {
		return model.PRStateOpen, model.RateLimit{}, nil
	})

	if !s.Reopen() {
		t.Fatal("Reopen() = false, want true")
	}
	runUntilIdle(t, disp)

	if got := s.CurrentPR().State; got != model.PRStateOpen {
		t.Errorf("CurrentPR().State = %v, want %v", got, model.PRStateOpen)
	}
}
