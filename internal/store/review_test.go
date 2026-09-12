package store

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// reviewTestRef is a fixed pull request reference used across review tests.
func reviewTestRef(number int) model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "o", Name: "r"}, Number: number}
}

// openPRWithDetail opens ref on s and applies before as its detail (via the
// fake's detailFunc), so review operations have a node ID and any seeded
// threads/pending review to act on.
func openPRWithDetail(t *testing.T, s *Store, gitHub *fakeGitHub, disp *fakeDispatcher, ref model.PRRef, before model.PullRequest) {
	t.Helper()
	before.Ref = ref
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := before
		pr.Ref = r
		return gh.DetailResult{PR: pr}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
}

// setPostMutationDetail arranges for gitHub's detailFunc to keep returning
// before until the first mutation-triggered refetch, then switch to
// returning after for every call from then on: the post-mutation refetch
// finishMutation always triggers must confirm the same state the
// optimistic apply produced, mirroring mutations_test.go's own convention
// (see TestAddComment_Success_OptimisticApplyThenRefetch).
func setPostMutationDetail(gitHub *fakeGitHub, ref model.PRRef, before, after model.PullRequest) {
	before.Ref, after.Ref = ref, ref
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := before
		if gitHub.detailCallCount() > 1 {
			pr = after
		}
		pr.Ref = r
		return gh.DetailResult{PR: pr}, nil
	})
}

func TestPendingReview_NoneAndSome(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	if s.PendingReview() != nil {
		t.Errorf("PendingReview() = %+v, want nil with no pending review", s.PendingReview())
	}
	if s.HasPendingReview() {
		t.Error("HasPendingReview() = true, want false")
	}
	if !s.CanSendSingle() {
		t.Error("CanSendSingle() = false, want true with no pending review")
	}

	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})
	if got := s.PendingReview(); got == nil || got.ID != "PRR_1" {
		t.Errorf("PendingReview() = %+v, want ID=PRR_1", got)
	}
	if !s.HasPendingReview() {
		t.Error("HasPendingReview() = false, want true")
	}
	if s.CanSendSingle() {
		t.Error("CanSendSingle() = true, want false once a pending review exists")
	}
}

func TestPendingComments_OnlyPendingStateAcrossThreads(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID: "PR_1",
		ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{
				{ID: "RC_1", State: model.ReviewCommentStatePending},
				{ID: "RC_2", State: model.ReviewCommentStateSubmitted},
			}},
			{ID: "RT_2", Comments: []model.ReviewComment{
				{ID: "RC_3", State: model.ReviewCommentStatePending},
			}},
		},
	})

	got := s.PendingComments()
	if len(got) != 2 {
		t.Fatalf("PendingComments() = %+v, want 2 entries", got)
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids["RC_1"] || !ids["RC_3"] {
		t.Errorf("PendingComments() ids = %v, want RC_1 and RC_3", ids)
	}
}

func TestCommentOnLines_SendSingle_PublishesImmediately(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})

	if got := s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 10}, "nice", SendSingle); !got {
		t.Fatal("CommentOnLines(...) = false, want true")
	}
	runUntilIdle(t, disp)

	calls := gitHub.addReviewNowCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("AddReviewNow call count = %d, want 1", len(calls))
	}
	if calls[0].prID != "PR_1" || len(calls[0].threads) != 1 {
		t.Fatalf("AddReviewNow call = %+v, want prID=PR_1 with one thread", calls[0])
	}
	th := calls[0].threads[0]
	if th.Path != "a.go" || th.Line != 10 || th.Side != model.DiffSideRight || th.StartLine != 0 || th.Body != "nice" {
		t.Errorf("thread = %+v, want Path=a.go Line=10 Side=RIGHT StartLine=0 Body=nice", th)
	}
	if len(gitHub.createPendingReviewCallsSnapshot()) != 0 {
		t.Error("CreatePendingReview was called for a SendSingle comment")
	}
}

func TestCommentOnLines_SendSingle_RangeIncludesStartLine(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})

	r := diff.Range{Side: model.DiffSideRight, StartSide: model.DiffSideRight, Line: 15, StartLine: 10}
	s.CommentOnLines("a.go", r, "range", SendSingle)
	runUntilIdle(t, disp)

	th := gitHub.addReviewNowCallsSnapshot()[0].threads[0]
	if th.StartLine != 10 || th.StartSide != model.DiffSideRight {
		t.Errorf("thread = %+v, want StartLine=10 StartSide=RIGHT", th)
	}
}

func TestCommentOnLines_SendToReview_CreatesPendingThenAddsThread(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
		ReviewThreads: []model.ReviewThread{{ID: "RT_1", Path: "a.go", Line: 10, Side: model.DiffSideRight}},
	})

	var order []string
	gitHub.setCreatePendingReviewFunc(func(_ context.Context, prID string) (model.Review, model.RateLimit, error) {
		order = append(order, "create")
		if prID != "PR_1" {
			t.Errorf("CreatePendingReview prID = %q, want PR_1", prID)
		}
		return model.Review{ID: "PRR_1", State: model.ReviewStatePending}, model.RateLimit{Known: true, Remaining: 100}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		order = append(order, "thread")
		if in.PullRequestReviewID != "PRR_1" {
			t.Errorf("AddReviewThread PullRequestReviewID = %q, want PRR_1", in.PullRequestReviewID)
		}
		return model.ReviewThread{ID: "RT_1", Path: in.Path, Line: in.Line, Side: in.Side}, model.RateLimit{}, nil
	})

	if got := s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 10}, "hi", SendToReview); !got {
		t.Fatal("CommentOnLines(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(order) != 2 || order[0] != "create" || order[1] != "thread" {
		t.Errorf("call order = %v, want [create thread]", order)
	}

	pr := s.CurrentPR()
	if pr == nil || pr.PendingReview == nil || pr.PendingReview.ID != "PRR_1" {
		t.Fatalf("CurrentPR().PendingReview = %+v, want ID=PRR_1", pr)
	}
	found := false
	for _, th := range pr.ReviewThreads {
		if th.ID == "RT_1" {
			found = true
		}
	}
	if !found {
		t.Errorf("ReviewThreads = %+v, want RT_1 present", pr.ReviewThreads)
	}
}

func TestCommentOnLines_PendingReviewExists_CoercesSendSingleToReview(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT_1", Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendSingle)
	runUntilIdle(t, disp)

	if len(gitHub.addReviewNowCallsSnapshot()) != 0 {
		t.Error("AddReviewNow was called although a pending review exists; SendSingle must coerce to SendToReview")
	}
	if len(gitHub.createPendingReviewCallsSnapshot()) != 0 {
		t.Error("CreatePendingReview was called although a pending review already exists")
	}
	calls := gitHub.addReviewThreadCallsSnapshot()
	if len(calls) != 1 || calls[0].in.PullRequestReviewID != "PRR_1" {
		t.Fatalf("AddReviewThread calls = %+v, want one call using the existing pending review", calls)
	}
}

func TestCommentOnFile_SendSingle_CreatesThreadThenSubmits(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})

	var order []string
	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		order = append(order, "create")
		return model.Review{ID: "PRR_1"}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		order = append(order, "thread")
		if in.SubjectType != model.ThreadSubjectFile {
			t.Errorf("SubjectType = %v, want FILE", in.SubjectType)
		}
		if in.Line != 0 || in.Side != "" {
			t.Errorf("Line/Side = %d/%v, want zero for a file-level thread", in.Line, in.Side)
		}
		if in.PullRequestReviewID != "PRR_1" {
			t.Errorf("PullRequestReviewID = %q, want PRR_1", in.PullRequestReviewID)
		}
		return model.ReviewThread{ID: "RT_1", SubjectType: model.ThreadSubjectFile}, model.RateLimit{}, nil
	})
	gitHub.setSubmitReviewFunc(func(_ context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error) {
		order = append(order, "submit")
		if reviewID != "PRR_1" || event != model.ReviewEventComment || body != "" {
			t.Errorf("SubmitReview(reviewID=%q, event=%v, body=%q), want PRR_1/COMMENT/\"\"", reviewID, event, body)
		}
		return model.Review{ID: "PRR_1", State: model.ReviewStateCommented}, model.RateLimit{}, nil
	})

	if got := s.CommentOnFile("a.go", "file comment", SendSingle); !got {
		t.Fatal("CommentOnFile(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(order) != 3 || order[0] != "create" || order[1] != "thread" || order[2] != "submit" {
		t.Errorf("call order = %v, want [create thread submit]", order)
	}
}

// TestCommentOnFile_SendSingle_FailureMidwayConvergesViaRefetch replaces an
// earlier version of this test that asserted PendingReview() stayed nil
// after AddReviewThread failed partway through CommentOnFile's
// create-thread-submit sequence: since finishMutation now refetches after
// every finished mutation, successful or not (see mutations.go's
// invalidateAndRefetch), the pending review CreatePendingReview actually
// left behind on GitHub before the failure is surfaced locally by that
// refetch instead of silently drifting out of sync until the next
// auto-refresh (up to 5 minutes later).
func TestCommentOnFile_SendSingle_FailureMidwayConvergesViaRefetch(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	// The first call is OpenPR's own fetch (no pending review yet); the
	// second is the refetch the failed mutation triggers, reporting the
	// pending review CreatePendingReview created before AddReviewThread
	// failed - simulating GitHub's own post-failure state.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.PendingReview = &model.Review{ID: "PRR_1", State: model.ReviewStatePending}
		}
		return gh.DetailResult{PR: pr}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	events := collectEvents(s)

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1"}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, _ gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{}, model.RateLimit{}, &gh.Error{Kind: gh.KindValidation, Message: "boom"}
	})

	s.CommentOnFile("a.go", "file comment", SendSingle)
	runUntilIdle(t, disp)

	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for a mid-way CommentOnFile failure")
	}
	if len(gitHub.submitReviewCallsSnapshot()) != 0 {
		t.Error("SubmitReview was called although AddReviewThread failed")
	}
	if got := gitHub.detailCallCount(); got != 2 {
		t.Fatalf("detail call count = %d, want 2 (OpenPR + the failure-triggered refetch)", got)
	}
	if s.PendingReview() == nil {
		t.Error("PendingReview() = nil after the refetch; want the pending review left on GitHub before AddReviewThread failed")
	}
	if s.CanSendSingle() {
		t.Error("CanSendSingle() = true after the refetch surfaced a pending review, want false")
	}
}

// TestCommentOnLines_SendToReview_PartialFailureTriggersRefetch covers the
// same convergence-via-refetch behaviour for CommentOnLines' add-to-review
// path: CreatePendingReview succeeds, AddReviewThread then fails.
func TestCommentOnLines_SendToReview_PartialFailureTriggersRefetch(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		pr := model.PullRequest{ID: "PR_1", Ref: r}
		if gitHub.detailCallCount() > 1 {
			pr.PendingReview = &model.Review{ID: "PRR_1", State: model.ReviewStatePending}
		}
		return gh.DetailResult{PR: pr}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1"}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, _ gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{}, model.RateLimit{}, &gh.Error{Kind: gh.KindValidation, Message: "line must be part of the diff"}
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 999}, "x", SendToReview)
	runUntilIdle(t, disp)

	if got := gitHub.detailCallCount(); got != 2 {
		t.Fatalf("detail call count = %d, want 2 (OpenPR + the failure-triggered refetch)", got)
	}
	if s.PendingReview() == nil {
		t.Error("PendingReview() = nil after the refetch; want the pending review CreatePendingReview succeeded at creating")
	}
}

func TestCommentOnFile_SendToReview_WithExistingPending_OnlyAddsThread(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT_1", SubjectType: in.SubjectType}, model.RateLimit{}, nil
	})

	if got := s.CommentOnFile("a.go", "file comment", SendToReview); !got {
		t.Fatal("CommentOnFile(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(gitHub.createPendingReviewCallsSnapshot()) != 0 {
		t.Error("CreatePendingReview was called although a pending review already exists")
	}
	if len(gitHub.submitReviewCallsSnapshot()) != 0 {
		t.Error("SubmitReview was called for an add-to-review file comment")
	}
	calls := gitHub.addReviewThreadCallsSnapshot()
	if len(calls) != 1 || calls[0].in.PullRequestReviewID != "PRR_1" {
		t.Fatalf("AddReviewThread calls = %+v, want one call using the existing pending review", calls)
	}
}

func TestReplyToThread_SendSingle(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{ID: "PR_1", ReviewThreads: []model.ReviewThread{{ID: "RT_1"}}},
		model.PullRequest{ID: "PR_1", ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", Body: "reply"}}},
		}},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setAddThreadReplyFunc(func(_ context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error) {
		if threadID != "RT_1" || body != "reply" || pendingReviewID != "" {
			t.Errorf("AddThreadReply(%q, %q, %q), want RT_1/reply/\"\"", threadID, body, pendingReviewID)
		}
		return model.ReviewComment{ID: "RC_1", Body: body}, model.RateLimit{}, nil
	})

	if got := s.ReplyToThread("RT_1", "reply", SendSingle); !got {
		t.Fatal("ReplyToThread(...) = false, want true")
	}
	runUntilIdle(t, disp)

	calls := gitHub.addThreadReplyCallsSnapshot()
	if len(calls) != 1 || calls[0].threadID != "RT_1" || calls[0].body != "reply" || calls[0].pendingReviewID != "" {
		t.Fatalf("AddThreadReply calls = %+v, want one call RT_1/reply/\"\"", calls)
	}
	pr := s.CurrentPR()
	if pr == nil || len(pr.ReviewThreads) != 1 || len(pr.ReviewThreads[0].Comments) != 1 ||
		pr.ReviewThreads[0].Comments[0].Body != "reply" {
		t.Fatalf("ReviewThreads after reply = %+v, want RT_1 with one reply comment", pr.ReviewThreads)
	}
}

func TestReplyToThread_SendToReview_EnsuresPendingReview(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{ID: "PR_1", ReviewThreads: []model.ReviewThread{{ID: "RT_1"}}},
		model.PullRequest{
			ID:            "PR_1",
			PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
			ReviewThreads: []model.ReviewThread{{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", Body: "reply"}}}},
		},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	var order []string
	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		order = append(order, "create")
		return model.Review{ID: "PRR_1"}, model.RateLimit{}, nil
	})
	gitHub.setAddThreadReplyFunc(func(_ context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error) {
		order = append(order, "reply")
		if pendingReviewID != "PRR_1" {
			t.Errorf("AddThreadReply pendingReviewID = %q, want PRR_1", pendingReviewID)
		}
		return model.ReviewComment{ID: "RC_1", Body: body}, model.RateLimit{}, nil
	})

	if got := s.ReplyToThread("RT_1", "reply", SendToReview); !got {
		t.Fatal("ReplyToThread(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(order) != 2 || order[0] != "create" || order[1] != "reply" {
		t.Errorf("call order = %v, want [create reply]", order)
	}
	if pr := s.CurrentPR(); pr == nil || pr.PendingReview == nil || pr.PendingReview.ID != "PRR_1" {
		t.Errorf("CurrentPR().PendingReview = %+v, want ID=PRR_1", pr)
	}
}

func TestEditReviewComment_Success_ReplacesInPlace(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{ID: "PR_1", ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", Body: "orig"}}},
		}},
		model.PullRequest{ID: "PR_1", ReviewThreads: []model.ReviewThread{
			{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", Body: "edited"}}},
		}},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setUpdateReviewCommentFunc(func(_ context.Context, id, body string) (model.ReviewComment, model.RateLimit, error) {
		return model.ReviewComment{ID: id, Body: body}, model.RateLimit{}, nil
	})

	if got := s.EditReviewComment("RC_1", "edited"); !got {
		t.Fatal("EditReviewComment(...) = false, want true")
	}
	runUntilIdle(t, disp)

	calls := gitHub.updateReviewCommentCallsSnapshot()
	if len(calls) != 1 || calls[0].id != "RC_1" || calls[0].body != "edited" {
		t.Fatalf("UpdateReviewComment calls = %+v, want one call id=RC_1 body=edited", calls)
	}
	pr := s.CurrentPR()
	if pr == nil || pr.ReviewThreads[0].Comments[0].Body != "edited" {
		t.Fatalf("ReviewThreads after edit = %+v, want Body=edited", pr.ReviewThreads)
	}
}

func TestDeleteReviewComment_Success_RemovesAndDropsEmptiedThread(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	// RC_1 is the pending review's own (and only) pending comment: deleting
	// it must drop the now-commentless RT_1 locally but leave the pending
	// review itself in place (review.go:356-361's documented behaviour) —
	// both the "before" and "after" fixtures below keep PendingReview set,
	// confirming the refetch agrees.
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{
			ID:            "PR_1",
			PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStatePending}}},
				{ID: "RT_2", Comments: []model.ReviewComment{{ID: "RC_2"}, {ID: "RC_3"}}},
			},
		},
		model.PullRequest{
			ID:            "PR_1",
			PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_2", Comments: []model.ReviewComment{{ID: "RC_2"}, {ID: "RC_3"}}},
			},
		},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setDeleteReviewCommentFunc(func(context.Context, string) (model.RateLimit, error) {
		return model.RateLimit{}, nil
	})

	if got := s.DeleteReviewComment("RC_1"); !got {
		t.Fatal("DeleteReviewComment(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.deleteReviewCommentCallsSnapshot(); len(calls) != 1 || calls[0].id != "RC_1" {
		t.Fatalf("DeleteReviewComment calls = %+v, want one call for RC_1", calls)
	}

	pr := s.CurrentPR()
	if pr == nil {
		t.Fatal("CurrentPR() = nil after DeleteReviewComment")
	}
	if s.PendingReview() == nil {
		t.Error("PendingReview() = nil after deleting a pending review's last comment, want the (now empty) review left in place")
	}
	for _, th := range pr.ReviewThreads {
		if th.ID == "RT_1" {
			t.Error("RT_1 should have been dropped once its only comment was deleted")
		}
	}
	found := false
	for _, th := range pr.ReviewThreads {
		if th.ID == "RT_2" {
			found = true
			if len(th.Comments) != 2 {
				t.Errorf("RT_2 comments = %+v, want 2 (unaffected)", th.Comments)
			}
		}
	}
	if !found {
		t.Error("RT_2 should remain untouched")
	}
}

func TestSetThreadResolved_RefusedWithPendingComments(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID: "PR_1",
		ReviewThreads: []model.ReviewThread{
			{
				ID: "RT_1", ViewerCanResolve: true,
				Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStatePending}},
			},
		},
	})
	events := collectEvents(s)

	if got := s.SetThreadResolved("RT_1", true); got {
		t.Error("SetThreadResolved(...) = true, want false for a thread with pending comments")
	}
	runUntilIdle(t, disp)

	if len(gitHub.resolveThreadCallsSnapshot()) != 0 {
		t.Error("ResolveThread was called for a thread with pending comments")
	}
	if !errors.Is(firstErrorEvent(*events), errThreadHasPendingComments) {
		t.Errorf("EventError = %v, want errThreadHasPendingComments", firstErrorEvent(*events))
	}
}

// firstErrorEvent returns the first EventError's Err in events, or nil.
func firstErrorEvent(events []Event) error {
	for _, e := range events {
		if e.Kind == EventError {
			return e.Err
		}
	}
	return nil
}

func TestSetThreadResolved_ResolveAndUnresolve(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	// detailCallCount() == 1 is OpenPR's own fetch (IsResolved:false); == 2
	// is the refetch after SetThreadResolved(true) (must confirm
	// IsResolved:true); >= 3 is the refetch after SetThreadResolved(false)
	// (back to IsResolved:false) - see setPostMutationDetail's doc comment
	// for why the refetch must always echo the optimistic apply.
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		resolved := gitHub.detailCallCount() == 2
		// GitHub never reports both viewer-can-* flags true (or both
		// false) at once: a resolved thread offers only "unresolve", an
		// unresolved one only "resolve" - tied to resolved here so this
		// fixture cannot silently hide a bug that only shows up against a
		// realistic (mutually exclusive) combination.
		return gh.DetailResult{PR: model.PullRequest{
			ID: "PR_1", Ref: r, ReviewThreads: []model.ReviewThread{
				{ID: "RT_1", IsResolved: resolved, ViewerCanResolve: !resolved, ViewerCanUnresolve: resolved},
			},
		}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setResolveThreadFunc(func(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: threadID, IsResolved: true, ViewerCanResolve: false, ViewerCanUnresolve: true}, model.RateLimit{}, nil
	})

	if got := s.SetThreadResolved("RT_1", true); !got {
		t.Fatal("SetThreadResolved(true) = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.resolveThreadCallsSnapshot(); len(calls) != 1 || calls[0].threadID != "RT_1" {
		t.Fatalf("ResolveThread calls = %+v, want one call for RT_1", calls)
	}
	pr := s.CurrentPR()
	if pr == nil || !pr.ReviewThreads[0].IsResolved {
		t.Fatalf("ReviewThreads after resolve = %+v, want IsResolved=true", pr.ReviewThreads)
	}

	gitHub.setUnresolveThreadFunc(func(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: threadID, IsResolved: false, ViewerCanResolve: true, ViewerCanUnresolve: false}, model.RateLimit{}, nil
	})
	if got := s.SetThreadResolved("RT_1", false); !got {
		t.Fatal("SetThreadResolved(false) = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.unresolveThreadCallsSnapshot(); len(calls) != 1 || calls[0].threadID != "RT_1" {
		t.Fatalf("UnresolveThread calls = %+v, want one call for RT_1", calls)
	}
	pr = s.CurrentPR()
	if pr == nil || pr.ReviewThreads[0].IsResolved {
		t.Fatalf("ReviewThreads after unresolve = %+v, want IsResolved=false", pr.ReviewThreads)
	}
}

func TestDiscardPendingReview_Success(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{
			ID:            "PR_1",
			PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStatePending}}},
				{ID: "RT_2", Comments: []model.ReviewComment{{ID: "RC_2", State: model.ReviewCommentStateSubmitted}}},
			},
		},
		model.PullRequest{
			ID: "PR_1",
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_2", Comments: []model.ReviewComment{{ID: "RC_2", State: model.ReviewCommentStateSubmitted}}},
			},
		},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setDeletePendingReviewFunc(func(context.Context, string) (model.RateLimit, error) {
		return model.RateLimit{}, nil
	})

	if got := s.DiscardPendingReview(); !got {
		t.Fatal("DiscardPendingReview() = false, want true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.deletePendingReviewCallsSnapshot(); len(calls) != 1 || calls[0].reviewID != "PRR_1" {
		t.Fatalf("DeletePendingReview calls = %+v, want one call for PRR_1", calls)
	}
	pr := s.CurrentPR()
	if pr == nil || pr.PendingReview != nil {
		t.Errorf("PendingReview after discard = %+v, want nil", pr)
	}
	for _, th := range pr.ReviewThreads {
		if th.ID == "RT_1" {
			t.Error("RT_1 (only a pending comment) should have been dropped")
		}
	}
}

func TestDiscardPendingReview_NoneEmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	events := collectEvents(s)

	if got := s.DiscardPendingReview(); got {
		t.Error("DiscardPendingReview() = true, want false with no pending review")
	}
	runUntilIdle(t, disp)

	if len(gitHub.deletePendingReviewCallsSnapshot()) != 0 {
		t.Error("DeletePendingReview was called with no pending review")
	}
	if !errors.Is(firstErrorEvent(*events), errNoPendingReview) {
		t.Errorf("EventError = %v, want errNoPendingReview", firstErrorEvent(*events))
	}
}

func TestSubmitReview_WithPendingReview_UsesSubmitReview(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{
			ID:            "PR_1",
			PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStatePending}}},
			},
		},
		model.PullRequest{
			ID: "PR_1",
			ReviewThreads: []model.ReviewThread{
				{ID: "RT_1", Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStateSubmitted}}},
			},
		},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setSubmitReviewFunc(func(_ context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error) {
		if reviewID != "PRR_1" || event != model.ReviewEventApprove || body != "lgtm" {
			t.Errorf("SubmitReview(%q, %v, %q), want PRR_1/APPROVE/lgtm", reviewID, event, body)
		}
		return model.Review{ID: reviewID, State: model.ReviewStateApproved}, model.RateLimit{}, nil
	})

	if got := s.SubmitReview(model.ReviewEventApprove, "lgtm"); !got {
		t.Fatal("SubmitReview(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(gitHub.addReviewNowWithEventCallsSnapshot()) != 0 {
		t.Error("AddReviewNowWithEvent was called although a pending review exists")
	}
	pr := s.CurrentPR()
	if pr == nil || pr.PendingReview != nil {
		t.Errorf("PendingReview after submit = %+v, want nil", pr)
	}
	if pr.ReviewThreads[0].Comments[0].State != model.ReviewCommentStateSubmitted {
		t.Errorf("comment state after submit = %v, want SUBMITTED", pr.ReviewThreads[0].Comments[0].State)
	}
}

func TestSubmitReview_NoPendingReview_UsesAddReviewNowWithEvent(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})

	gitHub.setAddReviewNowWithEventFunc(func(_ context.Context, prID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error) {
		if prID != "PR_1" || event != model.ReviewEventRequestChanges || body != "please fix" {
			t.Errorf("AddReviewNowWithEvent(%q, %v, %q), want PR_1/REQUEST_CHANGES/please fix", prID, event, body)
		}
		return model.Review{ID: "PRR_2", State: model.ReviewStateChangesRequested}, model.RateLimit{}, nil
	})

	if got := s.SubmitReview(model.ReviewEventRequestChanges, "please fix"); !got {
		t.Fatal("SubmitReview(...) = false, want true")
	}
	runUntilIdle(t, disp)

	if len(gitHub.submitReviewCallsSnapshot()) != 0 {
		t.Error("SubmitReview (the pending-review path) was called with no pending review")
	}
	if calls := gitHub.addReviewNowWithEventCallsSnapshot(); len(calls) != 1 {
		t.Fatalf("AddReviewNowWithEvent call count = %d, want 1", len(calls))
	}
}

func TestReviewMutations_NoPullRequestOpen_EmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	events := collectEvents(s)

	if got := s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendSingle); got {
		t.Error("CommentOnLines(...) = true with no pull request open, want false")
	}
	if got := s.CommentOnFile("a.go", "x", SendSingle); got {
		t.Error("CommentOnFile(...) = true with no pull request open, want false")
	}
	if got := s.ReplyToThread("RT_1", "x", SendSingle); got {
		t.Error("ReplyToThread(...) = true with no pull request open, want false")
	}
	if got := s.EditReviewComment("RC_1", "x"); got {
		t.Error("EditReviewComment(...) = true with no pull request open, want false")
	}
	if got := s.DeleteReviewComment("RC_1"); got {
		t.Error("DeleteReviewComment(...) = true with no pull request open, want false")
	}
	if got := s.SetThreadResolved("RT_1", true); got {
		t.Error("SetThreadResolved(...) = true with no pull request open, want false")
	}
	if got := s.DiscardPendingReview(); got {
		t.Error("DiscardPendingReview() = true with no pull request open, want false")
	}
	if got := s.SubmitReview(model.ReviewEventComment, "x"); got {
		t.Error("SubmitReview(...) = true with no pull request open, want false")
	}
	runUntilIdle(t, disp)

	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for review operations with no pull request open")
	}
	if s.Mutating() || s.PendingMutations() != 0 {
		t.Error("a mutation was enqueued despite no pull request being open")
	}
}

func TestCommentOnLines_GenerationGuard_SwitchPRSkipsApply(t *testing.T) {
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
	gitHub.setAddReviewNowFunc(func(ctx context.Context, _ string, _ []gh.DraftThread, _ string) (model.Review, model.RateLimit, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return model.Review{}, model.RateLimit{}, ctx.Err()
		}
		return model.Review{ID: "PRR_a"}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "for-a", SendSingle)
	s.OpenPR(refB)
	runUntilIdle(t, disp)

	close(block)
	runUntilIdle(t, disp)

	pr := s.CurrentPR()
	if pr == nil || pr.Ref != refB {
		t.Fatalf("CurrentPR() = %+v, want the pull request for refB", pr)
	}
}

func TestCommentOnLines_Error_KeepsStateEmitsEventError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	events := collectEvents(s)

	wantErr := &gh.Error{Kind: gh.KindValidation, Message: "line must be part of the diff"}
	gitHub.setAddReviewNowFunc(func(context.Context, string, []gh.DraftThread, string) (model.Review, model.RateLimit, error) {
		return model.Review{}, model.RateLimit{}, wantErr
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendSingle)
	runUntilIdle(t, disp)

	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for a failed CommentOnLines mutation")
	}
	if s.LastError() == nil {
		t.Error("LastError() = nil after a failed mutation, want the mutation's error")
	}
	if s.Mutating() {
		t.Error("Mutating() = true after the failed mutation finished, want false")
	}
	pr := s.CurrentPR()
	if pr == nil || len(pr.ReviewThreads) != 0 || pr.PendingReview != nil {
		t.Errorf("CurrentPR() changed after a failed mutation: %+v", pr)
	}
}

// TestQueuedSendToReview_ReusesPendingReviewCreatedByEarlierMutation covers
// finding A: two SendToReview comments enqueued back to back, before the
// first one's own apply has recorded the pending review it created. Since
// CommentOnLines/CommentOnFile/ReplyToThread/SubmitReview/
// DiscardPendingReview now decide "which pending review to use" via a
// prepare closure that runs immediately before each mutation's own run
// (not back at enqueue time - see review.go's package doc), the second
// mutation's prepare only ever executes from inside the first's own
// finishMutation (which applies the first's result, including the newly
// created pending review, before starting the next queued mutation), so it
// always sees the up-to-date pendingReviewID.
func TestQueuedSendToReview_ReusesPendingReviewCreatedByEarlierMutation(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{
		ID: "PR_1", PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1", State: model.ReviewStatePending}, model.RateLimit{}, nil
	})
	var threadReviewIDs []string
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		threadReviewIDs = append(threadReviewIDs, in.PullRequestReviewID)
		return model.ReviewThread{ID: "RT_" + in.Body, Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "first", SendToReview)
	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 2}, "second", SendToReview)
	runUntilIdle(t, disp)

	if n := len(gitHub.createPendingReviewCallsSnapshot()); n != 1 {
		t.Errorf("CreatePendingReview call count = %d, want 1 (the second send must reuse the review the first created)", n)
	}
	for i, id := range threadReviewIDs {
		if id != "PRR_1" {
			t.Errorf("thread %d used PullRequestReviewID = %q, want PRR_1 for both", i, id)
		}
	}
}

// TestQueuedSendSingleBehindSendToReview_Coerces covers finding A's second
// case: a SendSingle comment enqueued directly behind a SendToReview one.
// Before the fix, sendModeFor's enqueue-time snapshot saw no pending
// review yet and never coerced, so AddReviewNow was called even though a
// pending review existed by the time this mutation actually ran.
func TestQueuedSendSingleBehindSendToReview_Coerces(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{
		ID: "PR_1", PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1", State: model.ReviewStatePending}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT", Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "first", SendToReview)
	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 2}, "second", SendSingle)
	runUntilIdle(t, disp)

	if n := len(gitHub.addReviewNowCallsSnapshot()); n != 0 {
		t.Errorf("AddReviewNow call count = %d, want 0 (SendSingle must coerce once the queued mutation ahead created a pending review)", n)
	}
	if n := len(gitHub.addReviewThreadCallsSnapshot()); n != 2 {
		t.Errorf("AddReviewThread call count = %d, want 2 (both comments attached to the pending review)", n)
	}
}

// TestQueuedSubmitReviewBehindSendToReview_SubmitsTheCreatedReview covers
// finding A's third, most severe case: SubmitReview enqueued directly
// behind a SendToReview comment. Before the fix, SubmitReview's
// enqueue-time pendingID snapshot was "" (no pending review existed yet),
// so it took the "no pending review" path (AddReviewNowWithEvent),
// creating and submitting a second, empty review while the first
// (carrying the user's comment) was left unpublished.
func TestQueuedSubmitReviewBehindSendToReview_SubmitsTheCreatedReview(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{
		ID: "PR_1", PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1", State: model.ReviewStatePending}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT", Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "first", SendToReview)
	s.SubmitReview(model.ReviewEventApprove, "lgtm")
	runUntilIdle(t, disp)

	if n := len(gitHub.submitReviewCallsSnapshot()); n != 1 {
		t.Errorf("SubmitReview (pending path) call count = %d, want 1", n)
	}
	if calls := gitHub.submitReviewCallsSnapshot(); len(calls) == 1 && calls[0].reviewID != "PRR_1" {
		t.Errorf("SubmitReview reviewID = %q, want PRR_1 (the review the queued comment created)", calls[0].reviewID)
	}
	if n := len(gitHub.addReviewNowWithEventCallsSnapshot()); n != 0 {
		t.Errorf("AddReviewNowWithEvent call count = %d, want 0 (a pending review with the queued comment exists by then)", n)
	}
}

// TestBackToBackSendToReview_WhileFirstStillInFlight_CreatesPendingReviewOnce
// forces genuine concurrency (rather than relying on both calls simply
// completing before the queue drains): the first mutation's
// CreatePendingReview blocks until both CommentOnLines calls have been
// enqueued, so PendingMutations() == 2 is observed while nothing has been
// applied yet, before either is allowed to proceed.
func TestBackToBackSendToReview_WhileFirstStillInFlight_CreatesPendingReviewOnce(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	setPostMutationDetail(gitHub, ref, model.PullRequest{ID: "PR_1"}, model.PullRequest{
		ID: "PR_1", PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})

	var creates int32
	release := make(chan struct{})
	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		atomic.AddInt32(&creates, 1)
		<-release // hold the first mutation in flight until both are enqueued
		return model.Review{ID: "PRR_1", State: model.ReviewStatePending}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT_" + in.Body, Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "one", SendToReview)
	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 2}, "two", SendToReview)
	if n := s.PendingMutations(); n != 2 {
		t.Fatalf("PendingMutations() = %d immediately after enqueuing two, want 2", n)
	}
	close(release)
	runUntilIdle(t, disp)

	if n := atomic.LoadInt32(&creates); n != 1 {
		t.Errorf("CreatePendingReview called %d times, want 1", n)
	}
}

// TestReviewMutation_TargetChangedBeforeStart_NoGitHubCallEmitsError covers
// the case enqueuePreparedMutation's prepare closure exists for: the
// current pull request changes (via OpenPR) after a review mutation was
// enqueued but before it reached the front of the queue. GitHub must never
// be called for a mutation whose target has moved out from under it.
func TestReviewMutation_TargetChangedBeforeStart_NoGitHubCallEmitsError(t *testing.T) {
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
	gitHub.setAddCommentFunc(func(ctx context.Context, _, _ string) (model.IssueComment, model.RateLimit, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return model.IssueComment{}, model.RateLimit{}, ctx.Err()
		}
		return model.IssueComment{ID: "IC_a"}, model.RateLimit{}, nil
	})

	// An unrelated AddComment mutation occupies the queue's single flight
	// slot (blocked), so the CommentOnLines enqueued right after it is
	// still queued, not yet started, when OpenPR(refB) switches the
	// current pull request out from under it.
	s.AddComment("occupies the queue")
	events := collectEvents(s)
	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "for-a", SendSingle)
	s.OpenPR(refB)
	close(block)
	runUntilIdle(t, disp)

	if n := len(gitHub.addReviewNowCallsSnapshot()); n != 0 {
		t.Errorf("AddReviewNow was called %d times, want 0: the pull request changed before this mutation started", n)
	}
	if !errors.Is(firstErrorEvent(*events), errMutationTargetChanged) {
		t.Errorf("EventError = %v, want errMutationTargetChanged", firstErrorEvent(*events))
	}
}

// countEventKind returns how many events in events have the given Kind.
func countEventKind(events []Event, kind EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// TestCommentOnLines_SendSingleCoercedToReview_EmitsEventNotice covers
// finding C: a SendSingle call whose actual path was coerced to
// SendToReview (because a pending review already existed by run time) must
// tell the UI so, since the bool return alone cannot: it is decided after
// CommentOnLines has already returned true.
func TestCommentOnLines_SendSingleCoercedToReview_EmitsEventNotice(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})
	events := collectEvents(s)

	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT_1", Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendSingle)
	runUntilIdle(t, disp)

	if n := countEventKind(*events, EventNotice); n != 1 {
		t.Errorf("EventNotice count = %d, want 1 for a coerced send", n)
	}
}

// TestCommentOnLines_SendToReview_NoCoercion_NoEventNotice confirms the
// converse: requesting SendToReview directly (already the effective mode,
// nothing coerced) emits no EventNotice.
func TestCommentOnLines_SendToReview_NoCoercion_NoEventNotice(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending},
	})
	events := collectEvents(s)

	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: "RT_1", Path: in.Path}, model.RateLimit{}, nil
	})

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendToReview)
	runUntilIdle(t, disp)

	if n := countEventKind(*events, EventNotice); n != 0 {
		t.Errorf("EventNotice count = %d, want 0: SendToReview was already the requested mode", n)
	}
}

// TestCommentOnLines_SendSingle_NoPendingReview_NoEventNotice confirms a
// genuine (uncoerced) SendSingle send emits no EventNotice either.
func TestCommentOnLines_SendSingle_NoPendingReview_NoEventNotice(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})
	events := collectEvents(s)

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "x", SendSingle)
	runUntilIdle(t, disp)

	if n := countEventKind(*events, EventNotice); n != 0 {
		t.Errorf("EventNotice count = %d, want 0: no pending review existed to coerce into", n)
	}
}

// TestSetThreadResolved_RefusedWhenViewerCannotResolve covers finding E:
// GitHub's own per-thread ViewerCanResolve flag is checked as the primary
// judgment before the pending-comments check, since it also covers cases
// (already resolved, thread ownership) that check does not.
func TestSetThreadResolved_RefusedWhenViewerCannotResolve(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	// Both viewer-can-* flags false (rather than the other one true, which
	// GitHub never reports alongside this one - see
	// TestSetThreadResolved_ResolveAndUnresolve's fixture comment)
	// represents a viewer with no permission on the thread at all, e.g. a
	// read-only collaborator - realistic regardless of IsResolved.
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		ReviewThreads: []model.ReviewThread{{ID: "RT_1", ViewerCanResolve: false, ViewerCanUnresolve: false}},
	})
	events := collectEvents(s)

	if got := s.SetThreadResolved("RT_1", true); got {
		t.Error("SetThreadResolved(true) = true, want false when ViewerCanResolve is false")
	}
	runUntilIdle(t, disp)

	if len(gitHub.resolveThreadCallsSnapshot()) != 0 {
		t.Error("ResolveThread was called although ViewerCanResolve is false")
	}
	if !errors.Is(firstErrorEvent(*events), errThreadNotResolvable) {
		t.Errorf("EventError = %v, want errThreadNotResolvable", firstErrorEvent(*events))
	}
}

// TestSetThreadResolved_RefusedWhenViewerCannotUnresolve is
// RefusedWhenViewerCannotResolve's mirror for the unresolve direction.
func TestSetThreadResolved_RefusedWhenViewerCannotUnresolve(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	// Both viewer-can-* flags false, mirroring the "cannot resolve" test's
	// fixture comment above; IsResolved: true only so the thread is at
	// least in the state SetThreadResolved(false) would apply to.
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{
		ID:            "PR_1",
		ReviewThreads: []model.ReviewThread{{ID: "RT_1", ViewerCanResolve: false, ViewerCanUnresolve: false, IsResolved: true}},
	})
	events := collectEvents(s)

	if got := s.SetThreadResolved("RT_1", false); got {
		t.Error("SetThreadResolved(false) = true, want false when ViewerCanUnresolve is false")
	}
	runUntilIdle(t, disp)

	if len(gitHub.unresolveThreadCallsSnapshot()) != 0 {
		t.Error("UnresolveThread was called although ViewerCanUnresolve is false")
	}
	if !errors.Is(firstErrorEvent(*events), errThreadNotUnresolvable) {
		t.Errorf("EventError = %v, want errThreadNotUnresolvable", firstErrorEvent(*events))
	}
}

// TestSetThreadResolved_ThreadNotFound_NotRefusedLocally confirms a thread
// the store does not (yet) hold locally is not refused client-side: the
// mutation is enqueued and left to GitHub's own validation, since there is
// no ViewerCanResolve/ViewerCanUnresolve/pending-comments state to check
// locally for it.
func TestSetThreadResolved_ThreadNotFound_NotRefusedLocally(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	openPRWithDetail(t, s, gitHub, disp, ref, model.PullRequest{ID: "PR_1"})

	gitHub.setResolveThreadFunc(func(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: threadID, IsResolved: true}, model.RateLimit{}, nil
	})

	if got := s.SetThreadResolved("RT_unknown", true); !got {
		t.Fatal("SetThreadResolved(...) = false, want true: an unknown thread is left to GitHub's own validation, not refused locally")
	}
	runUntilIdle(t, disp)

	if len(gitHub.resolveThreadCallsSnapshot()) != 1 {
		t.Error("ResolveThread was not called for a thread not found locally")
	}
}

// TestCommentOnFile_SendSingle_KeepsThreadMarkedSubmitted covers finding F:
// CommentOnFile's SendSingle path used to discard the thread
// AddReviewThread returned instead of applying it. The detail refetch that
// always follows a finished mutation is blocked here (via detailBlock)
// specifically so the assertions observe CommentOnFile's own optimistic
// apply, not the refetch that would otherwise immediately confirm (and so
// mask a bug in) the same state.
func TestCommentOnFile_SendSingle_KeepsThreadMarkedSubmitted(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	detailBlock := make(chan struct{})
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		if gitHub.detailCallCount() > 1 {
			<-detailBlock
		}
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setCreatePendingReviewFunc(func(_ context.Context, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: "PRR_1"}, model.RateLimit{}, nil
	})
	gitHub.setAddReviewThreadFunc(func(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{
			ID: "RT_1", Path: in.Path, SubjectType: model.ThreadSubjectFile,
			Comments: []model.ReviewComment{{ID: "RC_1", State: model.ReviewCommentStatePending, Body: in.Body}},
		}, model.RateLimit{}, nil
	})
	gitHub.setSubmitReviewFunc(func(_ context.Context, reviewID string, _ model.ReviewEvent, _ string) (model.Review, model.RateLimit, error) {
		return model.Review{ID: reviewID, State: model.ReviewStateCommented}, model.RateLimit{}, nil
	})

	s.CommentOnFile("a.go", "file comment", SendSingle)
	runUntilIdle(t, disp) // drains CommentOnFile's own apply; the refetch it starts is now blocked on detailBlock.

	pr := s.CurrentPR()
	if pr == nil || len(pr.ReviewThreads) != 1 {
		t.Fatalf("ReviewThreads = %+v, want one FILE thread from CommentOnFile's own apply", pr)
	}
	th := pr.ReviewThreads[0]
	if th.SubjectType != model.ThreadSubjectFile || len(th.Comments) != 1 {
		t.Fatalf("thread = %+v, want one FILE-subject thread with one comment", th)
	}
	if th.Comments[0].State != model.ReviewCommentStateSubmitted {
		t.Errorf("comment State = %v, want SUBMITTED (the review that owns it was just submitted)", th.Comments[0].State)
	}

	close(detailBlock)
	runUntilIdle(t, disp)
}

// TestSetThreadResolved_ToggleAgainBeforeRefetchUsesFreshFlags covers the
// final review's finding 1: ResolveThread/UnresolveThread's payload
// carries the server's own post-toggle ViewerCanResolve/ViewerCanUnresolve
// (see gh.threadResolutionNode's doc comment), and applyThreadResolution
// overwrites all three fields together — so a second toggle enqueued right
// after the first succeeds, before the refetch that follows it has landed,
// is judged against the up-to-date flags the first toggle's own response
// already supplied, not stale ones this Store never updated. The detail
// refetch is blocked (detailBlock) specifically to observe this window.
func TestSetThreadResolved_ToggleAgainBeforeRefetchUsesFreshFlags(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)

	detailBlock := make(chan struct{})
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		if gitHub.detailCallCount() > 1 {
			<-detailBlock
		}
		return gh.DetailResult{PR: model.PullRequest{
			ID: "PR_1", Ref: r,
			ReviewThreads: []model.ReviewThread{{ID: "RT_1", ViewerCanResolve: true, ViewerCanUnresolve: false}},
		}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	gitHub.setResolveThreadFunc(func(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: threadID, IsResolved: true, ViewerCanResolve: false, ViewerCanUnresolve: true}, model.RateLimit{}, nil
	})
	gitHub.setUnresolveThreadFunc(func(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
		return model.ReviewThread{ID: threadID, IsResolved: false, ViewerCanResolve: true, ViewerCanUnresolve: false}, model.RateLimit{}, nil
	})

	if got := s.SetThreadResolved("RT_1", true); !got {
		t.Fatal("SetThreadResolved(true) = false, want true")
	}
	runUntilIdle(t, disp) // applies ResolveThread's result; the refetch it starts is now blocked on detailBlock.

	events := collectEvents(s)
	if got := s.SetThreadResolved("RT_1", false); !got {
		t.Fatal("SetThreadResolved(false) = false, want true: the resolve's own response already reported ViewerCanUnresolve=true")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.unresolveThreadCallsSnapshot(); len(calls) != 1 {
		t.Errorf("UnresolveThread call count = %d, want 1", len(calls))
	}
	if hasErrorEvent(*events) {
		t.Error("SetThreadResolved(false) was refused although the resolve's own apply already updated the viewer-can-unresolve flag")
	}

	close(detailBlock)
	runUntilIdle(t, disp)
}

// TestDiscardPendingReview_QueuedTwice_SecondPrepareFailureDoesNotRefetch
// covers the final review's finding 2: a prepare failure never reached
// GitHub, so it must not trigger invalidateAndRefetch the way a run
// failure does. Two DiscardPendingReview calls enqueued back to back (the
// second's own enqueue-time "does a pending review exist" snapshot is
// still non-empty, since the first has not applied yet) end with only the
// first — the one that actually called DeletePendingReview — triggering a
// refetch; the second's prepare re-check finds the pending review already
// gone and fails without ever reaching GitHub.
func TestDiscardPendingReview_QueuedTwice_SecondPrepareFailureDoesNotRefetch(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := reviewTestRef(1)
	setPostMutationDetail(gitHub, ref,
		model.PullRequest{ID: "PR_1", PendingReview: &model.Review{ID: "PRR_1", State: model.ReviewStatePending}},
		model.PullRequest{ID: "PR_1"},
	)
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	before := gitHub.detailCallCount()

	gitHub.setDeletePendingReviewFunc(func(context.Context, string) (model.RateLimit, error) {
		return model.RateLimit{}, nil
	})
	events := collectEvents(s)

	if got := s.DiscardPendingReview(); !got {
		t.Fatal("first DiscardPendingReview() = false, want true")
	}
	if got := s.DiscardPendingReview(); !got {
		t.Fatal("second DiscardPendingReview() = false, want true: its enqueue-time pendingID snapshot was still non-empty")
	}
	runUntilIdle(t, disp)

	if calls := gitHub.deletePendingReviewCallsSnapshot(); len(calls) != 1 {
		t.Errorf("DeletePendingReview call count = %d, want 1 (the second's prepare must refuse once the first already discarded it)", len(calls))
	}
	if !errors.Is(firstErrorEvent(*events), errNoPendingReview) {
		t.Errorf("EventError = %v, want errNoPendingReview from the second call's prepare failure", firstErrorEvent(*events))
	}
	if got := gitHub.detailCallCount() - before; got != 1 {
		t.Errorf("detail call count = %d, want 1: only the first (successful) discard reaches GitHub and triggers a refetch; the second's prepare failure must not trigger a second one", got)
	}
}

// TestReviewMutation_TargetChangedBeforeStart_DoesNotInvalidateCache is
// TestReviewMutation_TargetChangedBeforeStart_NoGitHubCallEmitsError's
// companion for finding 2's other half: an errMutationTargetChanged
// prepare failure must not invalidate the cache entry of the pull request
// it was originally enqueued against either — that pull request may no
// longer even be open (as here), and nothing was ever sent to GitHub on
// its behalf to invalidate anything for.
func TestReviewMutation_TargetChangedBeforeStart_DoesNotInvalidateCache(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		return model.User{Login: "octocat"}, model.RateLimit{}, nil
	}
	s.Start(context.Background())
	runUntilIdle(t, disp)

	refA := reviewTestRef(1)
	refB := reviewTestRef(2)
	// refC hosts the occupying AddComment mutation, deliberately kept
	// separate from refA/refB: its own successful completion legitimately
	// invalidates *its own* target's cache entry (see finding B), which
	// would otherwise make refA's entry disappear for a reason unrelated
	// to the thing this test actually checks (whether the prepare failure,
	// on its own, invalidates anything) - using a third pull request keeps
	// that invalidation from ever touching refA's key.
	refC := reviewTestRef(3)
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})

	s.OpenPR(refC)
	runUntilIdle(t, disp)

	block := make(chan struct{})
	gitHub.setAddCommentFunc(func(ctx context.Context, _, _ string) (model.IssueComment, model.RateLimit, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return model.IssueComment{}, model.RateLimit{}, ctx.Err()
		}
		return model.IssueComment{ID: "IC_c"}, model.RateLimit{}, nil
	})
	// Occupies the queue's single flight slot (blocked) so CommentOnLines
	// below, enqueued for refA, is still queued rather than started when
	// the current pull request later moves to refB.
	s.AddComment("occupies the queue")

	s.OpenPR(refA)
	runUntilIdle(t, disp) // resolves refA's own detail fetch; the still-blocked occupier dispatches nothing here.

	// Seed refA's on-disk cache entry directly, so its continued presence
	// below can only be explained by the prepare-failed mutation never
	// having invalidated it.
	key, err := cache.PRKey(s.deps.Host, "octocat", refA, "detail")
	if err != nil {
		t.Fatalf("cache.PRKey() error = %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
		t.Fatalf("Cache.Put() error = %v", err)
	}

	s.CommentOnLines("a.go", diff.Range{Side: model.DiffSideRight, Line: 1}, "for-a", SendSingle)

	s.OpenPR(refB)
	runUntilIdle(t, disp) // resolves refB's own detail fetch; the occupier is still blocked, CommentOnLines is still queued.

	close(block)
	runUntilIdle(t, disp) // the occupier (refC) finishes; CommentOnLines' prepare then fails (target moved to refB) without ever reaching GitHub.

	if _, ok, err := s.deps.Cache.Get(key); err != nil || !ok {
		t.Errorf("refA's cache entry Get() = (ok=%v, err=%v), want ok=true: a prepare failure must never invalidate it", ok, err)
	}
}
