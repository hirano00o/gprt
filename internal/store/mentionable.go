// Package store's mentionable-users state (see MentionableUsers,
// startMentionableUsersFetch) loads a repository's mentionable users
// lazily, the first time OpenPR opens a pull request in it, and keeps the
// most recently applied result per repository for the Store's lifetime.
// Freshness (mentionableUsersFreshFor), not a one-shot "already tried"
// flag, decides whether OpenPR needs to do any work at all: a repository
// whose last successful fetch is still fresh short-circuits immediately,
// with no cache read and no network call; a repository a previous fetch
// failed for (its fetchedAt stays at the zero value — see mentionableEntry)
// or that has simply gone stale is retried the next time OpenPR opens a
// pull request in it (or a ReloadPR forces one — see startMentionableUsersFetch's
// force parameter), so a transient network failure never silently starves
// `@`-mention candidates for the rest of the session. Results are cached on
// disk (see mentionableUsersCacheRest) with the same freshness rule applied
// to the cached entry's own FetchedAt. A failed fetch is logged and
// surfaced as an EventError but never blocks anything else: mention
// completion simply has fewer candidates until the next retry succeeds.
package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/model"
)

// mentionableUsersCacheRest is the Key.Rest segment mentionable-users cache
// entries are stored under (see cache.RepoKey). Bumped to a "-v2" suffix
// (M5) when model.User gained an ID field (needed to add a brand-new
// individual reviewer via the edit-PR form's reviewers picker): an older,
// still-cached "mentionable-users" entry decodes fine into []model.User
// (an added struct field is backward-compatible for JSON decoding) but
// every one of its users would silently have an empty ID until the next
// network refetch — a stale disk cache from before this change must never
// be read back at all, not merely allowed to expire on its own hourly
// freshness window, so a new cache key name is simpler than adding an
// empty-ID staleness check to every read path.
const mentionableUsersCacheRest = "mentionable-users-v2"

// mentionableUsersFreshFor is how long a repository's mentionable-users
// result — in memory or on disk — is treated as fresh enough to skip a
// network call (see startMentionableUsersFetch and mentionableEntry.fresh).
const mentionableUsersFreshFor = time.Hour

// mentionableUsersFirst is the page size requested: unfiltered
// (query: ""), first: 100, per docs/DESIGN.md's mention-candidates bullet.
// gprt does not paginate this connection further; a repository with more
// than 100 mentionable users simply has a smaller candidate list than
// GitHub's own UI would offer, an accepted limitation for a first cut.
const mentionableUsersFirst = 100

// mentionableEntry is one repository's mentionable-users fetch state.
// fetchedAt is the zero value until the first successful apply (from cache
// or the network) and is left untouched by a failed one, so a repository
// whose only attempt so far failed always reads as "not fresh" (see fresh)
// and is retried on the next call rather than being permanently marked
// "already tried". persisted reports whether users has been written to
// disk under the viewer's confirmed login (see cacheMentionableUsers and
// persistMentionableUsersIfPending): a successful fetch that lands before
// the viewer is network-confirmed cannot write its cache entry yet (the
// login-scoped key might be wrong), so it is retried once confirmation
// resolves instead of being lost for the rest of the session.
type mentionableEntry struct {
	users     []model.User
	fetchedAt time.Time
	loading   bool
	persisted bool
}

// fresh reports whether e's result is recent enough
// (mentionableUsersFreshFor) to skip a network call.
func (e mentionableEntry) fresh(now time.Time) bool {
	return !e.fetchedAt.IsZero() && now.Sub(e.fetchedAt) < mentionableUsersFreshFor
}

// MentionableUsers returns the current pull request's repository's
// mentionable users (login/name), as loaded lazily by OpenPR, or nil when
// no pull request is open or the repository's list has not resolved yet
// (from cache or the network).
func (s *Store) MentionableUsers() []model.User {
	if s.current == nil {
		return nil
	}
	return s.mentionable[s.current.Repo].users
}

// startMentionableUsersFetch loads repo's mentionable users. A fetch
// already in flight for repo is never duplicated (loading dedup, checked
// on the UI goroutine — a second OpenPR for the same repository while the
// first is still in flight is a no-op here). Otherwise, unless force is
// true, an in-memory result still fresh (mentionableUsersFreshFor)
// short-circuits with no work at all. force skips both that check and the
// disk-cache read below, going straight to the network — matching
// ReloadPR's "ignore whatever is cached and refetch anyway" semantics for
// the pull request's own detail. Failing all of that, a cached disk entry
// is applied immediately (a fresh one needs nothing further; a stale one
// is still shown right away while the network call below confirms or
// replaces it) before the network fetch starts.
func (s *Store) startMentionableUsersFetch(repo model.RepoRef, force bool) {
	entry := s.mentionable[repo]
	if entry.loading {
		return
	}
	if !force {
		if entry.fresh(s.deps.Now()) {
			return
		}
		if s.applyCachedMentionableUsers(repo) {
			return
		}
	}
	s.fetchMentionableUsers(repo)
}

// applyCachedMentionableUsers synchronously applies repo's cached
// mentionable users, if any (a local disk/memory read, never the network).
// It returns true when the cached entry is fresh enough
// (mentionableUsersFreshFor) that no network fetch is needed at all; a
// stale hit is still applied (so the UI has something to show right away)
// but returns false, so the caller starts a background refetch. A miss, an
// unreadable cache, or an unknown viewer login (there is nothing
// meaningful cached under one) also returns false. The applied entry's
// fetchedAt is the cache entry's own original FetchedAt, not the time of
// this read: freshness must be measured from when the data actually came
// from GitHub, not from whenever it happened to be loaded off disk.
func (s *Store) applyCachedMentionableUsers(repo model.RepoRef) bool {
	if s.viewer.Login == "" {
		return false
	}
	key, err := cache.RepoKey(s.deps.Host, s.viewer.Login, repo, mentionableUsersCacheRest)
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
	var users []model.User
	if json.Unmarshal(diskEntry.Body, &users) != nil {
		return false
	}
	s.mentionable[repo] = mentionableEntry{users: users, fetchedAt: diskEntry.FetchedAt, persisted: true}
	s.emit(Event{Kind: EventMentionableChanged})
	return s.deps.Now().Sub(diskEntry.FetchedAt) < mentionableUsersFreshFor
}

// fetchMentionableUsers starts a background fetch of repo's mentionable
// users, marking its entry loading first (on the UI goroutine, before the
// goroutine starts, so a concurrent call sees it immediately). Values the
// goroutine needs are snapshotted before it starts, per docs/DESIGN.md's
// concurrency rules; the result is applied on the UI goroutine via
// Dispatch. This uses s.baseCtx directly rather than a dedicated, per-fetch
// cancellable context — see the package doc for why that is sufficient
// here.
func (s *Store) fetchMentionableUsers(repo model.RepoRef) {
	entry := s.mentionable[repo]
	entry.loading = true
	s.mentionable[repo] = entry

	ctx := s.baseCtx

	go func() {
		var users []model.User
		var rl model.RateLimit
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, matching every other fetch in
		// this package (see detail.go/list.go/mutations.go).
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: mentionable_users: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "mentionable_users", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyMentionableUsersResult(repo, users, rl, err, cancelled)
			})
		}()
		users, rl, err = s.deps.GitHub.MentionableUsers(ctx, repo, "", mentionableUsersFirst)
	}()
}

// applyMentionableUsersResult applies (or reports the failure of) one
// mentionable-users fetch. It runs only on the UI goroutine, from a
// Dispatch callback. loading is cleared unconditionally, in every branch
// (success, failure, or cancellation): without that, startMentionableUsersFetch's
// in-flight dedup would block this repository's fetch forever. A failure
// (cancelled or not) otherwise leaves users/fetchedAt/persisted exactly as
// they were — a stale result, if any, is kept on screen rather than
// cleared, and fetchedAt staying put is what makes the next call see this
// repository as "not fresh" and retry (see the package doc).
func (s *Store) applyMentionableUsersResult(
	repo model.RepoRef, users []model.User, rl model.RateLimit, err error, cancelled bool,
) {
	entry := s.mentionable[repo]
	entry.loading = false

	if err != nil {
		s.mentionable[repo] = entry
		if cancelled {
			// Our own cancellation (app shutdown) is not a failure worth
			// reporting, matching every other fetch's identical rule.
			return
		}
		s.deps.Logger.Error("load mentionable users failed", "repo", repo.NameWithOwner(), "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}

	entry.users = users
	entry.fetchedAt = s.deps.Now()
	entry.persisted = s.cacheMentionableUsers(repo, users)
	s.mentionable[repo] = entry
	s.setRateLimit(rl)
	s.emit(Event{Kind: EventMentionableChanged})
}

// cacheMentionableUsers persists users under repo's cache key and reports
// whether the write actually happened. Writing is skipped until the viewer
// has been network-confirmed, for the same reason cacheDetail/
// cacheSectionPage skip writing (see detail.go/list.go): before that, the
// login-scoped cache key may be seeded from a previous run and could
// belong to a different account than the one that will actually resolve.
// The caller (applyMentionableUsersResult, persistMentionableUsersIfPending)
// records the outcome in mentionableEntry.persisted so a fetch that
// succeeded before confirmation is retried once it resolves, rather than
// silently never reaching disk for the rest of the session.
func (s *Store) cacheMentionableUsers(repo model.RepoRef, users []model.User) bool {
	if !s.viewerConfirmed || s.viewer.Login == "" {
		s.deps.Logger.Debug("mentionable users cache write skipped: viewer login not yet confirmed")
		return false
	}
	key, err := cache.RepoKey(s.deps.Host, s.viewer.Login, repo, mentionableUsersCacheRest)
	if err != nil {
		return false
	}
	body, err := json.Marshal(users)
	if err != nil {
		return false
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		s.warnCacheError(err)
		return false
	}
	return true
}

// persistMentionableUsersIfPending retries cacheMentionableUsers for repo
// if a successful fetch already landed (fetchedAt is non-zero) but was
// never written to disk (persisted is false) — the case where the fetch
// resolved while the viewer's login was not yet network-confirmed. Called
// from applyViewerResult once confirmation resolves (see list.go), so a
// pull request open before Start's own viewer fetch completes does not
// leave its repository's mentionable users un-cached for the rest of the
// session. A no-op when repo has no entry, or its result is already
// persisted.
func (s *Store) persistMentionableUsersIfPending(repo model.RepoRef) {
	entry, ok := s.mentionable[repo]
	if !ok || entry.persisted || entry.fetchedAt.IsZero() {
		return
	}
	entry.persisted = s.cacheMentionableUsers(repo, entry.users)
	s.mentionable[repo] = entry
}
