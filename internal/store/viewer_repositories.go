// Package store's viewer-repositories state (see EnsureViewerRepositories,
// startViewerRepositoriesFetch) loads the viewer's own repositories once,
// lazily, only when EnsureViewerRepositories is explicitly called — the UI
// calls it when the create-pull-request form opens (the repository
// picker's candidate list, filtered locally as the user types). It
// follows repository.go's/mentionable.go's freshness+in-flight pattern,
// but as a single, non-keyed entry: unlike a repository's own metadata or
// mentionable users, there is only one viewer.
package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/model"
)

// viewerRepositoriesCacheRest is the Key.Rest segment the viewer's
// repositories are cached under, in the search/list namespace (see
// cache.SearchKey): the result is not tied to any specific repository or
// pull request, only to the viewer's login, matching a search/list result.
const viewerRepositoriesCacheRest = "viewer-repositories"

// viewerRepositoriesFreshFor is how long the viewer's repositories — in
// memory or on disk — are treated as fresh enough to skip a network call.
const viewerRepositoriesFreshFor = time.Hour

// viewerRepositoriesFirst is the page size requested; gprt does not
// paginate this connection further (the repository picker is filtered
// locally, and a viewer with more than 100 repositories simply has a
// smaller candidate list — an accepted limitation, matching
// MentionableUsers' own).
const viewerRepositoriesFirst = 100

// ViewerRepositories returns the viewer's own repositories, as loaded
// lazily by EnsureViewerRepositories, or nil before the first successful
// fetch (from cache or the network).
func (s *Store) ViewerRepositories() []model.RepositorySummary {
	return s.viewerRepos
}

// EnsureViewerRepositories loads the viewer's own repositories unless they
// are already fresh (viewerRepositoriesFreshFor) or a fetch is already in
// flight. Called explicitly by the UI when the create-pull-request form
// opens.
func (s *Store) EnsureViewerRepositories() {
	s.startViewerRepositoriesFetch(false)
}

// startViewerRepositoriesFetch mirrors startRepositoryMetadataFetch's
// shape (see that function's own doc comment) for the single,
// non-keyed viewer-repositories entry.
func (s *Store) startViewerRepositoriesFetch(force bool) {
	if s.viewerReposLoading {
		return
	}
	if !force {
		if !s.viewerReposFetchedAt.IsZero() && s.deps.Now().Sub(s.viewerReposFetchedAt) < viewerRepositoriesFreshFor {
			return
		}
		if s.applyCachedViewerRepositories() {
			return
		}
	}
	s.fetchViewerRepositories()
}

// applyCachedViewerRepositories mirrors applyCachedRepositoryMetadata: see
// that function's own doc comment for the full return-value contract.
func (s *Store) applyCachedViewerRepositories() bool {
	if s.viewer.Login == "" {
		return false
	}
	key, err := cache.SearchKey(s.deps.Host, s.viewer.Login, viewerRepositoriesCacheRest)
	if err != nil {
		return false
	}
	diskEntry, ok, err := s.deps.Cache.Get(key)
	if err != nil {
		s.warnCacheError(err)
		return false
	}
	if !ok {
		return false
	}
	var repos []model.RepositorySummary
	if json.Unmarshal(diskEntry.Body, &repos) != nil {
		return false
	}
	s.viewerRepos = repos
	s.viewerReposFetchedAt = diskEntry.FetchedAt
	s.viewerReposPersisted = true
	s.emit(Event{Kind: EventViewerRepositoriesChanged})
	return s.deps.Now().Sub(diskEntry.FetchedAt) < viewerRepositoriesFreshFor
}

// fetchViewerRepositories starts a background fetch of the viewer's
// repositories, mirroring fetchMentionableUsers'/fetchRepositoryMetadata's
// shape and its reasoning for using s.baseCtx directly.
func (s *Store) fetchViewerRepositories() {
	s.viewerReposLoading = true
	ctx := s.baseCtx

	go func() {
		var repos []model.RepositorySummary
		var rl model.RateLimit
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: viewer_repositories: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "viewer_repositories", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyViewerRepositoriesResult(repos, rl, err, cancelled)
			})
		}()
		repos, rl, err = s.deps.GitHub.ViewerRepositories(ctx, viewerRepositoriesFirst)
	}()
}

// applyViewerRepositoriesResult applies (or reports the failure of) one
// viewer-repositories fetch, mirroring applyRepositoryMetadataResult's
// shape and reasoning.
func (s *Store) applyViewerRepositoriesResult(repos []model.RepositorySummary, rl model.RateLimit, err error, cancelled bool) {
	s.viewerReposLoading = false

	if err != nil {
		if cancelled {
			return
		}
		s.deps.Logger.Error("load viewer repositories failed", "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}

	s.viewerRepos = repos
	s.viewerReposFetchedAt = s.deps.Now()
	s.viewerReposPersisted = s.cacheViewerRepositories(repos)
	s.setRateLimit(rl)
	s.emit(Event{Kind: EventViewerRepositoriesChanged})
}

// cacheViewerRepositories persists repos and reports whether the write
// actually happened, mirroring cacheRepositoryMetadata's/
// cacheMentionableUsers' own gating on viewer confirmation.
func (s *Store) cacheViewerRepositories(repos []model.RepositorySummary) bool {
	if !s.viewerConfirmed || s.viewer.Login == "" {
		s.deps.Logger.Debug("viewer repositories cache write skipped: viewer login not yet confirmed")
		return false
	}
	key, err := cache.SearchKey(s.deps.Host, s.viewer.Login, viewerRepositoriesCacheRest)
	if err != nil {
		return false
	}
	body, err := json.Marshal(repos)
	if err != nil {
		return false
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
		return false
	}
	return true
}
