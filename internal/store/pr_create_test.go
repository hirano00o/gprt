package store

import (
	"context"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func TestCreatePullRequest_Success_EmitsCreatedEventAndRefreshesList(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	events := collectEvents(s)

	newRef := model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 7}
	gitHub.setCreatePullRequestFunc(func(_ context.Context, in gh.CreatePullRequestInput) (model.PullRequest, model.RateLimit, error) {
		if in.Title != "Add feature" || in.RepositoryID != "R_1" {
			t.Errorf("CreatePullRequest called with %+v", in)
		}
		return model.PullRequest{ID: "PR_new", Ref: newRef}, model.RateLimit{}, nil
	})
	gitHub.setRequestReviewersFunc(func(_ context.Context, id string, userIDs, teamIDs []string, union bool) ([]model.Reviewer, model.RateLimit, error) {
		if id != "PR_new" || !union || len(userIDs) != 1 || userIDs[0] != "U_1" {
			t.Errorf("RequestReviewers called with id=%q userIDs=%v union=%v", id, userIDs, union)
		}
		return nil, model.RateLimit{}, nil
	})

	if !s.CreatePullRequest(gh.CreatePullRequestInput{RepositoryID: "R_1", Title: "Add feature"}, []string{"U_1"}, nil) {
		t.Fatal("CreatePullRequest() = false, want true")
	}
	runUntilIdle(t, disp)

	var gotRef *model.PRRef
	for _, e := range *events {
		if e.Kind == EventPullRequestCreated {
			gotRef = e.Ref
		}
	}
	if gotRef == nil || *gotRef != newRef {
		t.Errorf("EventPullRequestCreated.Ref = %v, want %+v", gotRef, newRef)
	}
	if len(gitHub.searchCalls()) == 0 {
		t.Error("no SearchPullRequests calls observed, want the list refreshed after a successful create")
	}
	if hasErrorEvent(*events) {
		t.Error("an EventError was emitted, want none: RequestReviewers succeeded")
	}
}

func TestCreatePullRequest_ReviewerFailure_StillEmitsCreatedEvent(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	events := collectEvents(s)

	newRef := model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 8}
	gitHub.setCreatePullRequestFunc(func(context.Context, gh.CreatePullRequestInput) (model.PullRequest, model.RateLimit, error) {
		return model.PullRequest{ID: "PR_new2", Ref: newRef}, model.RateLimit{}, nil
	})
	reviewerErr := errors.New("reviewer rejected")
	gitHub.setRequestReviewersFunc(func(context.Context, string, []string, []string, bool) ([]model.Reviewer, model.RateLimit, error) {
		return nil, model.RateLimit{}, reviewerErr
	})

	if !s.CreatePullRequest(gh.CreatePullRequestInput{RepositoryID: "R_1", Title: "Add feature"}, []string{"U_bad"}, nil) {
		t.Fatal("CreatePullRequest() = false, want true")
	}
	runUntilIdle(t, disp)

	var gotRef *model.PRRef
	var gotErr error
	for _, e := range *events {
		if e.Kind == EventPullRequestCreated {
			gotRef = e.Ref
		}
		if e.Kind == EventError && errors.Is(e.Err, reviewerErr) {
			gotErr = e.Err
		}
	}
	if gotRef == nil || *gotRef != newRef {
		t.Errorf("EventPullRequestCreated.Ref = %v, want %+v (created ref must survive a reviewer failure)", gotRef, newRef)
	}
	if gotErr == nil {
		t.Error("no EventError carrying the reviewer failure was observed")
	}
	if got := s.MutationError(); !errors.Is(got, reviewerErr) {
		t.Errorf("MutationError() = %v, want it to carry the reviewer failure %v", got, reviewerErr)
	}
}

func TestCreatePullRequest_CreateFailure_NoEventEmitted(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), gitHub, disp)
	events := collectEvents(s)

	wantErr := errors.New("create failed")
	gitHub.setCreatePullRequestFunc(func(context.Context, gh.CreatePullRequestInput) (model.PullRequest, model.RateLimit, error) {
		return model.PullRequest{}, model.RateLimit{}, wantErr
	})

	if !s.CreatePullRequest(gh.CreatePullRequestInput{RepositoryID: "R_1", Title: "Add feature"}, nil, nil) {
		t.Fatal("CreatePullRequest() = false, want true")
	}
	runUntilIdle(t, disp)

	for _, e := range *events {
		if e.Kind == EventPullRequestCreated {
			t.Errorf("EventPullRequestCreated was emitted despite CreatePullRequest itself failing")
		}
	}
	if !hasErrorEvent(*events) {
		t.Error("no EventError was emitted for the failed create")
	}
}
