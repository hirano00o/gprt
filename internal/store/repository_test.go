package store

import (
	"context"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/model"
)

func repoMetadataTestRepo() model.RepoRef {
	return model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
}

func TestStore_EnsureRepositoryMetadata_FetchesAndAppliesAllThree(t *testing.T) {
	fake := newFakeGitHub()
	fake.setRepositoryFunc(func(context.Context, model.RepoRef) (model.RepositoryInfo, model.RateLimit, error) {
		return model.RepositoryInfo{ID: "R_1", DefaultBranch: "main"}, model.RateLimit{Remaining: 100, Known: true}, nil
	})
	fake.setLabelsFunc(func(context.Context, model.RepoRef) ([]model.Label, model.RateLimit, error) {
		return []model.Label{{ID: "LA_1", Name: "bug"}}, model.RateLimit{}, nil
	})
	fake.setPullRequestTemplatesFunc(func(context.Context, model.RepoRef) ([]model.PullRequestTemplate, model.RateLimit, error) {
		return []model.PullRequestTemplate{{Body: "template"}}, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)
	events := collectEvents(s)

	repo := repoMetadataTestRepo()
	s.EnsureRepositoryMetadata(repo)
	runUntilIdle(t, disp)

	info, ok := s.RepositoryInfo(repo)
	if !ok || info.ID != "R_1" || info.DefaultBranch != "main" {
		t.Errorf("RepositoryInfo() = %+v, %v", info, ok)
	}
	labels, ok := s.Labels(repo)
	if !ok || len(labels) != 1 || labels[0].ID != "LA_1" {
		t.Errorf("Labels() = %+v, %v", labels, ok)
	}
	templates, ok := s.Templates(repo)
	if !ok || len(templates) != 1 || templates[0].Body != "template" {
		t.Errorf("Templates() = %+v, %v", templates, ok)
	}

	found := false
	for _, e := range *events {
		if e.Kind == EventRepositoryMetadataChanged && e.Repo != nil && *e.Repo == repo {
			found = true
		}
	}
	if !found {
		t.Errorf("no EventRepositoryMetadataChanged for %+v observed in %+v", repo, *events)
	}
}

func TestStore_EnsureRepositoryMetadata_NotYetLoaded(t *testing.T) {
	s := newTestStore(t, config.Default(), newFakeGitHub(), newFakeDispatcher())

	repo := repoMetadataTestRepo()
	if _, ok := s.RepositoryInfo(repo); ok {
		t.Errorf("RepositoryInfo() ok = true before any fetch, want false")
	}
	if _, ok := s.Labels(repo); ok {
		t.Errorf("Labels() ok = true before any fetch, want false")
	}
	if _, ok := s.Templates(repo); ok {
		t.Errorf("Templates() ok = true before any fetch, want false")
	}
}

func TestStore_EnsureRepositoryMetadata_SkipsWhenFresh(t *testing.T) {
	fake := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	repo := repoMetadataTestRepo()
	s.EnsureRepositoryMetadata(repo)
	runUntilIdle(t, disp)
	if got := len(fake.repositoryCallsSnapshot()); got != 1 {
		t.Fatalf("Repository() calls after first Ensure = %d, want 1", got)
	}

	s.EnsureRepositoryMetadata(repo)
	runUntilIdle(t, disp)
	if got := len(fake.repositoryCallsSnapshot()); got != 1 {
		t.Errorf("Repository() calls after second (fresh) Ensure = %d, want still 1", got)
	}
}

func TestStore_EnsureRepositoryMetadata_RetriesAfterFailure(t *testing.T) {
	fake := newFakeGitHub()
	wantErr := errors.New("boom")
	fake.setRepositoryFunc(func(context.Context, model.RepoRef) (model.RepositoryInfo, model.RateLimit, error) {
		return model.RepositoryInfo{}, model.RateLimit{}, wantErr
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	repo := repoMetadataTestRepo()
	s.EnsureRepositoryMetadata(repo)
	runUntilIdle(t, disp)

	if _, ok := s.RepositoryInfo(repo); ok {
		t.Fatalf("RepositoryInfo() ok = true after a failed fetch, want false")
	}

	s.EnsureRepositoryMetadata(repo)
	runUntilIdle(t, disp)
	if got := len(fake.repositoryCallsSnapshot()); got != 2 {
		t.Errorf("Repository() calls after a failed then retried Ensure = %d, want 2", got)
	}
}

func TestStore_EnsureRepositoryMetadata_DedupesInFlight(t *testing.T) {
	fake := newFakeGitHub()
	block := make(chan struct{})
	fake.setRepositoryFunc(func(context.Context, model.RepoRef) (model.RepositoryInfo, model.RateLimit, error) {
		<-block
		return model.RepositoryInfo{ID: "R_1"}, model.RateLimit{}, nil
	})

	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), fake, disp)

	repo := repoMetadataTestRepo()
	s.EnsureRepositoryMetadata(repo)
	s.EnsureRepositoryMetadata(repo)
	close(block)
	runUntilIdle(t, disp)

	if got := len(fake.repositoryCallsSnapshot()); got != 1 {
		t.Errorf("Repository() calls for two concurrent Ensure calls = %d, want 1 (deduped)", got)
	}
}
