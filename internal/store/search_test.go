package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_SearchBranches_AppliesResultAndCallsFn(t *testing.T) {
	fake := newFakeGitHub()
	want := []model.Branch{{Name: "main", HeadOID: "abc"}}
	fake.setBranchesFunc(func(context.Context, model.RepoRef, string, int) ([]model.Branch, model.RateLimit, error) {
		return want, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	var mu sync.Mutex
	var fnBranches []model.Branch
	var fnCalled bool
	s.SearchBranches(repoMetadataTestRepo(), "ma", func(branches []model.Branch, err error) {
		mu.Lock()
		defer mu.Unlock()
		fnBranches = branches
		fnCalled = true
		if err != nil {
			t.Errorf("fn err = %v, want nil", err)
		}
	})
	runUntilIdle(t, disp)

	mu.Lock()
	defer mu.Unlock()
	if !fnCalled {
		t.Fatal("fn was not called")
	}
	if len(fnBranches) != 1 || fnBranches[0] != want[0] {
		t.Errorf("fn branches = %+v, want %+v", fnBranches, want)
	}
}

func TestStore_SearchBranches_StaleAnswerDropped(t *testing.T) {
	fake := newFakeGitHub()
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls int
	fake.setBranchesFunc(func(context.Context, model.RepoRef, string, int) ([]model.Branch, model.RateLimit, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			// The first call blocks until explicitly released, and only
			// after signalling it has actually started - guaranteeing the
			// second SearchBranches call below is issued strictly after
			// this one is already in flight, not racing it.
			close(started)
			<-release
			return []model.Branch{{Name: "stale"}}, model.RateLimit{}, nil
		}
		return []model.Branch{{Name: "fresh"}}, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	var staleCalled bool
	var freshResult []model.Branch
	s.SearchBranches(repoMetadataTestRepo(), "s", func([]model.Branch, error) { staleCalled = true })
	<-started
	s.SearchBranches(repoMetadataTestRepo(), "f", func(branches []model.Branch, _ error) { freshResult = branches })
	runUntilIdle(t, disp) // lets the second (non-blocking) call resolve and apply first
	close(release)
	runUntilIdle(t, disp) // then lets the first, now-superseded call resolve and be dropped

	if staleCalled {
		t.Error("fn for the superseded (stale) SearchBranches call was invoked, want dropped")
	}
	if len(freshResult) != 1 || freshResult[0].Name != "fresh" {
		t.Errorf("fresh call's fn result = %+v, want the newer call's result", freshResult)
	}
}

func TestStore_SearchBranches_Error(t *testing.T) {
	fake := newFakeGitHub()
	wantErr := errors.New("boom")
	fake.setBranchesFunc(func(context.Context, model.RepoRef, string, int) ([]model.Branch, model.RateLimit, error) {
		return nil, model.RateLimit{}, wantErr
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)
	events := collectEvents(s)

	var gotErr error
	s.SearchBranches(repoMetadataTestRepo(), "x", func(_ []model.Branch, err error) { gotErr = err })
	runUntilIdle(t, disp)

	if !errors.Is(gotErr, wantErr) {
		t.Errorf("fn err = %v, want %v", gotErr, wantErr)
	}
	if n := countEventKind(*events, EventError); n == 0 {
		t.Error("no EventError observed for a failed SearchBranches call")
	}
}

func TestStore_SearchBranches_NilFn_DoesNotPanic(t *testing.T) {
	fake := newFakeGitHub()
	fake.setBranchesFunc(func(context.Context, model.RepoRef, string, int) ([]model.Branch, model.RateLimit, error) {
		return []model.Branch{{Name: "main"}}, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	s.SearchBranches(repoMetadataTestRepo(), "m", nil)
	runUntilIdle(t, disp)
}

func TestStore_SearchTeams_AppliesResult(t *testing.T) {
	fake := newFakeGitHub()
	want := []model.Team{{ID: "T_1", Slug: "core", Name: "Core"}}
	fake.setTeamsFunc(func(context.Context, string, string, int) ([]model.Team, model.RateLimit, error) {
		return want, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	var mu sync.Mutex
	var got []model.Team
	s.SearchTeams("acme", "co", func(teams []model.Team, err error) {
		mu.Lock()
		defer mu.Unlock()
		got = teams
		if err != nil {
			t.Errorf("fn err = %v, want nil", err)
		}
	})
	runUntilIdle(t, disp)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("fn teams = %+v, want %+v", got, want)
	}
}
