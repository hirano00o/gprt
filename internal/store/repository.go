// Package store's repository-metadata state (see EnsureRepositoryMetadata,
// startRepositoryMetadataFetch) loads a repository's RepositoryInfo,
// Labels, and PullRequestTemplates together, lazily, only when
// EnsureRepositoryMetadata is explicitly called — the UI calls it when an
// edit/merge/create-PR form opens, never automatically from OpenPR, to
// avoid three extra queries on every pull request switch. Follows
// mentionable.go's freshness+in-flight pattern (see that file's own doc
// comment): a repository whose last successful fetch is still fresh
// (repositoryMetadataFreshFor) short-circuits with no work at all; a
// repository a previous fetch failed for (its fetchedAt stays zero) is
// retried the next time EnsureRepositoryMetadata is called for it.
package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/model"
)

// repositoryMetadataCacheRest is the Key.Rest segment repository-metadata
// cache entries are stored under (see cache.RepoKey).
const repositoryMetadataCacheRest = "metadata"

// repositoryMetadataFreshFor is how long a repository's metadata — in
// memory or on disk — is treated as fresh enough to skip a network call.
const repositoryMetadataFreshFor = time.Hour

// repositoryMetadata is the combined shape of RepositoryInfo, Labels, and
// PullRequestTemplates for one repository. All three are always fetched,
// cached, and applied together (see repoMetadataEntry): a form that needs
// any one of them generally needs all three, and a partial result has no
// simpler consistency story worth the extra bookkeeping of tracking each
// independently.
type repositoryMetadata struct {
	Info      model.RepositoryInfo
	Labels    []model.Label
	Templates []model.PullRequestTemplate
}

// repoMetadataEntry is one repository's metadata fetch state, mirroring
// mentionableEntry's shape and freshness rule: fetchedAt is left untouched
// by a failed fetch, so a repository whose only attempt so far failed
// always reads as "not fresh" (see fresh) and is retried on the next
// EnsureRepositoryMetadata call rather than being permanently marked
// "already tried". persisted mirrors mentionableEntry.persisted (see
// cacheRepositoryMetadata's doc comment).
type repoMetadataEntry struct {
	metadata  repositoryMetadata
	fetchedAt time.Time
	loading   bool
	persisted bool
}

// fresh reports whether e's result is recent enough
// (repositoryMetadataFreshFor) to skip a network call.
func (e repoMetadataEntry) fresh(now time.Time) bool {
	return !e.fetchedAt.IsZero() && now.Sub(e.fetchedAt) < repositoryMetadataFreshFor
}

// RepositoryInfo returns repo's metadata, as loaded lazily by
// EnsureRepositoryMetadata, and whether it has resolved yet (from cache or
// the network).
func (s *Store) RepositoryInfo(repo model.RepoRef) (model.RepositoryInfo, bool) {
	entry := s.repoMetadata[repo]
	return entry.metadata.Info, !entry.fetchedAt.IsZero()
}

// Labels returns repo's labels, as loaded lazily by
// EnsureRepositoryMetadata, and whether they have resolved yet.
func (s *Store) Labels(repo model.RepoRef) ([]model.Label, bool) {
	entry := s.repoMetadata[repo]
	return entry.metadata.Labels, !entry.fetchedAt.IsZero()
}

// Templates returns repo's pull request templates, as loaded lazily by
// EnsureRepositoryMetadata, and whether they have resolved yet.
func (s *Store) Templates(repo model.RepoRef) ([]model.PullRequestTemplate, bool) {
	entry := s.repoMetadata[repo]
	return entry.metadata.Templates, !entry.fetchedAt.IsZero()
}

// EnsureRepositoryMetadata loads repo's RepositoryInfo/Labels/
// PullRequestTemplates together, unless they are already fresh
// (repositoryMetadataFreshFor) or a fetch for repo is already in flight.
// Called explicitly by the UI when an edit/merge/create-PR form opens —
// never automatically by OpenPR (see the package doc for why).
func (s *Store) EnsureRepositoryMetadata(repo model.RepoRef) {
	s.startRepositoryMetadataFetch(repo, false)
}

// startRepositoryMetadataFetch loads repo's metadata, mirroring
// startMentionableUsersFetch's shape: a fetch already in flight for repo
// (loading dedup) is never duplicated; unless force, an in-memory result
// still fresh short-circuits with no work at all, skipping even the disk
// cache read. Otherwise a cached disk entry is applied immediately if
// present (stale or not) before the network fetch starts.
func (s *Store) startRepositoryMetadataFetch(repo model.RepoRef, force bool) {
	entry := s.repoMetadata[repo]
	if entry.loading {
		return
	}
	if !force {
		if entry.fresh(s.deps.Now()) {
			return
		}
		if s.applyCachedRepositoryMetadata(repo) {
			return
		}
	}
	s.fetchRepositoryMetadata(repo)
}

// applyCachedRepositoryMetadata synchronously applies repo's cached
// metadata, if any (a local disk/memory read, never the network). It
// mirrors applyCachedMentionableUsers exactly: see that function's own doc
// comment for the full return-value contract (true only for a fresh cache
// hit that needs no network confirmation; a stale hit is still applied but
// returns false).
func (s *Store) applyCachedRepositoryMetadata(repo model.RepoRef) bool {
	if s.viewer.Login == "" {
		return false
	}
	key, err := cache.RepoKey(s.deps.Host, s.viewer.Login, repo, repositoryMetadataCacheRest)
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
	var metadata repositoryMetadata
	if json.Unmarshal(diskEntry.Body, &metadata) != nil {
		return false
	}
	s.repoMetadata[repo] = repoMetadataEntry{metadata: metadata, fetchedAt: diskEntry.FetchedAt, persisted: true}
	s.emit(Event{Kind: EventRepositoryMetadataChanged, Repo: &repo})
	return s.deps.Now().Sub(diskEntry.FetchedAt) < repositoryMetadataFreshFor
}

// fetchRepositoryMetadata starts a background fetch of repo's
// RepositoryInfo, Labels, and PullRequestTemplates: three separate GitHub
// calls made sequentially in a single goroutine and applied together as
// one result (see repositoryMetadata's own doc comment for why). The
// first call to fail aborts the remaining ones. Like
// startMentionableUsersFetch, this uses s.baseCtx directly rather than a
// dedicated, Stop-cancellable context: loading dedups a repository's own
// concurrent fetches, and there is no per-attempt generation to supersede.
func (s *Store) fetchRepositoryMetadata(repo model.RepoRef) {
	entry := s.repoMetadata[repo]
	entry.loading = true
	s.repoMetadata[repo] = entry

	ctx := s.baseCtx

	go func() {
		var metadata repositoryMetadata
		var rl model.RateLimit
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, matching every other fetch in
		// this package (see mentionable.go/detail.go/list.go).
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: repository_metadata: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "repository_metadata", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyRepositoryMetadataResult(repo, metadata, rl, err, cancelled)
			})
		}()

		metadata.Info, rl, err = s.deps.GitHub.Repository(ctx, repo)
		if err != nil {
			return
		}
		metadata.Labels, rl, err = s.deps.GitHub.Labels(ctx, repo)
		if err != nil {
			return
		}
		metadata.Templates, rl, err = s.deps.GitHub.PullRequestTemplates(ctx, repo)
	}()
}

// applyRepositoryMetadataResult applies (or reports the failure of) one
// repository-metadata fetch. It runs only on the UI goroutine, from a
// Dispatch callback. loading is cleared unconditionally in every branch,
// mirroring applyMentionableUsersResult; a failure (cancelled or not)
// otherwise leaves metadata/fetchedAt/persisted exactly as they were, so a
// stale result (if any) stays visible and the next call retries.
func (s *Store) applyRepositoryMetadataResult(
	repo model.RepoRef, metadata repositoryMetadata, rl model.RateLimit, err error, cancelled bool,
) {
	entry := s.repoMetadata[repo]
	entry.loading = false

	if err != nil {
		s.repoMetadata[repo] = entry
		if cancelled {
			return
		}
		s.deps.Logger.Error("load repository metadata failed", "repo", repo.NameWithOwner(), "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}

	entry.metadata = metadata
	entry.fetchedAt = s.deps.Now()
	entry.persisted = s.cacheRepositoryMetadata(repo, metadata)
	s.repoMetadata[repo] = entry
	s.setRateLimit(rl)
	s.emit(Event{Kind: EventRepositoryMetadataChanged, Repo: &repo})
}

// cacheRepositoryMetadata persists metadata under repo's cache key and
// reports whether the write actually happened. Writing is skipped until
// the viewer has been network-confirmed, for the same reason
// cacheMentionableUsers does (see that function's own doc comment): a
// fetch that succeeded before confirmation is simply retried the next time
// EnsureRepositoryMetadata is called for repo, rather than being persisted
// under a possibly-wrong login (unlike mentionable's own
// persistMentionableUsersIfPending, there is no dedicated retry-on-confirm
// hook for this — an accepted, narrower gap: a repository metadata form
// opened during the brief unconfirmed-viewer window at startup simply
// isn't cached that one time).
func (s *Store) cacheRepositoryMetadata(repo model.RepoRef, metadata repositoryMetadata) bool {
	if !s.viewerConfirmed || s.viewer.Login == "" {
		s.deps.Logger.Debug("repository metadata cache write skipped: viewer login not yet confirmed")
		return false
	}
	key, err := cache.RepoKey(s.deps.Host, s.viewer.Login, repo, repositoryMetadataCacheRest)
	if err != nil {
		return false
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return false
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
		return false
	}
	return true
}
