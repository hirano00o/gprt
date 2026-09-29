package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_CompareBranches_AppliesResultAndCallsFn(t *testing.T) {
	fake := newFakeGitHub()
	want := gh.CompareResult{Files: []model.ChangedFile{{Path: "a.go", Status: model.FileStatusModified}}}
	fake.setCompareFilesFunc(func(context.Context, model.RepoRef, string, string) (gh.CompareResult, error) {
		return want, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	var mu sync.Mutex
	var fnRes gh.CompareResult
	var fnCalled bool
	s.CompareBranches(repoMetadataTestRepo(), "main", "feature", func(res gh.CompareResult, err error) {
		mu.Lock()
		defer mu.Unlock()
		fnRes = res
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
	if len(fnRes.Files) != 1 || fnRes.Files[0] != want.Files[0] {
		t.Errorf("fn result = %+v, want %+v", fnRes, want)
	}
}

func TestStore_CompareBranches_StaleAnswerDropped(t *testing.T) {
	fake := newFakeGitHub()
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls int
	fake.setCompareFilesFunc(func(context.Context, model.RepoRef, string, string) (gh.CompareResult, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			// The first call blocks until explicitly released, and only
			// after signalling it has actually started - guaranteeing the
			// second CompareBranches call below is issued strictly after
			// this one is already in flight, not racing it.
			close(started)
			<-release
			return gh.CompareResult{Files: []model.ChangedFile{{Path: "stale.go"}}}, nil
		}
		return gh.CompareResult{Files: []model.ChangedFile{{Path: "fresh.go"}}}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	var staleCalled bool
	var freshResult gh.CompareResult
	s.CompareBranches(repoMetadataTestRepo(), "main", "stale-head", func(gh.CompareResult, error) { staleCalled = true })
	<-started
	s.CompareBranches(repoMetadataTestRepo(), "main", "fresh-head", func(res gh.CompareResult, _ error) { freshResult = res })
	runUntilIdle(t, disp) // lets the second (non-blocking) call resolve and apply first
	close(release)
	runUntilIdle(t, disp) // then lets the first, now-superseded call resolve and be dropped

	if staleCalled {
		t.Error("fn for the superseded (stale) CompareBranches call was invoked, want dropped")
	}
	if len(freshResult.Files) != 1 || freshResult.Files[0].Path != "fresh.go" {
		t.Errorf("fresh call's fn result = %+v, want the newer call's result", freshResult)
	}
}

func TestStore_CompareBranches_Error(t *testing.T) {
	fake := newFakeGitHub()
	wantErr := errors.New("boom")
	fake.setCompareFilesFunc(func(context.Context, model.RepoRef, string, string) (gh.CompareResult, error) {
		return gh.CompareResult{}, wantErr
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)
	events := collectEvents(s)

	var gotErr error
	s.CompareBranches(repoMetadataTestRepo(), "main", "feature", func(_ gh.CompareResult, err error) { gotErr = err })
	runUntilIdle(t, disp)

	if !errors.Is(gotErr, wantErr) {
		t.Errorf("fn err = %v, want %v", gotErr, wantErr)
	}
	if n := countEventKind(*events, EventError); n == 0 {
		t.Error("no EventError observed for a failed CompareBranches call")
	}
}

func TestStore_CompareBranches_NilFn_DoesNotPanic(t *testing.T) {
	fake := newFakeGitHub()
	fake.setCompareFilesFunc(func(context.Context, model.RepoRef, string, string) (gh.CompareResult, error) {
		return gh.CompareResult{Files: []model.ChangedFile{{Path: "a.go"}}}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	s.CompareBranches(repoMetadataTestRepo(), "main", "feature", nil)
	runUntilIdle(t, disp)
}
