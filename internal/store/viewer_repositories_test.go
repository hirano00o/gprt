package store

import (
	"context"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_EnsureViewerRepositories_FetchesAndApplies(t *testing.T) {
	fake := newFakeGitHub()
	want := []model.RepositorySummary{{ID: "R_1", Ref: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}}}
	fake.setViewerRepositoriesFunc(func(context.Context, int) ([]model.RepositorySummary, model.RateLimit, error) {
		return want, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)
	events := collectEvents(s)

	if got := s.ViewerRepositories(); got != nil {
		t.Fatalf("ViewerRepositories() before Ensure = %+v, want nil", got)
	}

	s.EnsureViewerRepositories()
	runUntilIdle(t, disp)

	got := s.ViewerRepositories()
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("ViewerRepositories() = %+v, want %+v", got, want)
	}
	if n := countEventKind(*events, EventViewerRepositoriesChanged); n == 0 {
		t.Error("no EventViewerRepositoriesChanged observed")
	}
}

func TestStore_EnsureViewerRepositories_SkipsWhenFresh(t *testing.T) {
	fake := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	s.EnsureViewerRepositories()
	runUntilIdle(t, disp)
	if got := len(fake.viewerRepositoriesCallsSnapshot()); got != 1 {
		t.Fatalf("ViewerRepositories() calls after first Ensure = %d, want 1", got)
	}

	s.EnsureViewerRepositories()
	runUntilIdle(t, disp)
	if got := len(fake.viewerRepositoriesCallsSnapshot()); got != 1 {
		t.Errorf("ViewerRepositories() calls after second (fresh) Ensure = %d, want still 1", got)
	}
}

func TestStore_EnsureViewerRepositories_RetriesAfterFailure(t *testing.T) {
	fake := newFakeGitHub()
	fake.setViewerRepositoriesFunc(func(context.Context, int) ([]model.RepositorySummary, model.RateLimit, error) {
		return nil, model.RateLimit{}, errors.New("boom")
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	s.EnsureViewerRepositories()
	runUntilIdle(t, disp)
	s.EnsureViewerRepositories()
	runUntilIdle(t, disp)

	if got := len(fake.viewerRepositoriesCallsSnapshot()); got != 2 {
		t.Errorf("ViewerRepositories() calls after a failed then retried Ensure = %d, want 2", got)
	}
}
