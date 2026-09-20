package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func TestMentionableUsers_NoPullRequestOpen_ReturnsNil(t *testing.T) {
	s := newTestStore(t, config.Default(), newFakeGitHub(), newFakeDispatcher())
	if got := s.MentionableUsers(); got != nil {
		t.Errorf("MentionableUsers() = %+v, want nil when no pull request is open", got)
	}
}

func TestOpenPR_MentionableUsers_FirstOpenFetchesOnce(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)
	events := collectEvents(s)

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "octocat", Name: "The Octocat"}}, model.RateLimit{}, nil
	})

	s.OpenPR(ref)
	runUntilIdle(t, disp)

	calls := gitHub.mentionableUsersCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("MentionableUsers call count = %d, want 1", len(calls))
	}
	if calls[0].repo != ref.Repo || calls[0].query != "" || calls[0].first != mentionableUsersFirst {
		t.Errorf("MentionableUsers call = %+v, want repo=%+v query=\"\" first=%d", calls[0], ref.Repo, mentionableUsersFirst)
	}
	users := s.MentionableUsers()
	if len(users) != 1 || users[0] != (model.User{Login: "octocat", Name: "The Octocat"}) {
		t.Errorf("MentionableUsers() = %+v, want [{octocat The Octocat}]", users)
	}
	if n := countEventKind(*events, EventMentionableChanged); n == 0 {
		t.Error("no EventMentionableChanged was emitted after the first fetch resolved")
	}
}

// TestOpenPR_MentionableUsers_SecondPRInSameRepoDoesNotRefetch's premise is
// that the second OpenPR happens well within mentionableUsersFreshFor of
// the first's successful fetch (both run against the same fixed
// s.deps.Now()): see TestOpenPR_MentionableUsers_FailureThenRetryOnNextOpenPR
// for the opposite case (the first fetch failed) and
// TestReloadPR_MentionableUsers_ForcesRefetchEvenWhenFresh for force
// bypassing freshness entirely.
func TestOpenPR_MentionableUsers_SecondPRInSameRepoDoesNotRefetch(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	repo := model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
	ref1 := model.PRRef{Repo: repo, Number: 1}
	ref2 := model.PRRef{Repo: repo, Number: 2}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "octocat"}}, model.RateLimit{}, nil
	})

	s.OpenPR(ref1)
	runUntilIdle(t, disp)
	s.OpenPR(ref2)
	runUntilIdle(t, disp)

	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 1 {
		t.Errorf("MentionableUsers call count = %d, want 1: a second pull request in the same repository must not refetch", len(calls))
	}
	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "octocat" {
		t.Errorf("MentionableUsers() = %+v, want the repository's already-fetched list", users)
	}
}

func TestOpenPR_MentionableUsers_FreshCacheAvoidsNetwork(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)

	key, err := cache.RepoKey(s.deps.Host, "tester", ref.Repo, mentionableUsersCacheRest)
	if err != nil {
		t.Fatalf("RepoKey: %v", err)
	}
	body, err := json.Marshal([]model.User{{Login: "cached-user"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Fetched exactly "now" (s.deps.Now()), well within the freshness
	// window, so this entry is used without any network call.
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed mentionable-users cache: %v", err)
	}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})

	s.OpenPR(ref)
	runUntilIdle(t, disp)

	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "cached-user" {
		t.Errorf("MentionableUsers() = %+v, want the cached entry applied without a network call", users)
	}
	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 0 {
		t.Errorf("MentionableUsers was called %d times, want 0: a fresh cache entry must avoid the network", len(calls))
	}
}

func TestOpenPR_MentionableUsers_StaleCacheServedThenRefreshed(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)

	key, err := cache.RepoKey(s.deps.Host, "tester", ref.Repo, mentionableUsersCacheRest)
	if err != nil {
		t.Fatalf("RepoKey: %v", err)
	}
	body, err := json.Marshal([]model.User{{Login: "stale-user"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	staleAt := s.deps.Now().Add(-2 * mentionableUsersFreshFor)
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: staleAt}); err != nil {
		t.Fatalf("seed mentionable-users cache: %v", err)
	}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	block := make(chan struct{})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		<-block
		return []model.User{{Login: "fresh-user"}}, model.RateLimit{}, nil
	})

	s.OpenPR(ref)

	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "stale-user" {
		t.Fatalf("MentionableUsers() = %+v, want the stale cache entry applied immediately", users)
	}

	close(block)
	runUntilIdle(t, disp)

	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "fresh-user" {
		t.Errorf("MentionableUsers() = %+v, want the refreshed network result", users)
	}
	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 1 {
		t.Errorf("MentionableUsers call count = %d, want 1: a stale cache entry must trigger exactly one background refresh", len(calls))
	}
}

func TestOpenPR_MentionableUsers_FailurePath(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)
	events := collectEvents(s)

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	wantErr := errors.New("boom")
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return nil, model.RateLimit{}, wantErr
	})

	s.OpenPR(ref)
	runUntilIdle(t, disp)

	if users := s.MentionableUsers(); users != nil {
		t.Errorf("MentionableUsers() = %+v, want nil after a failed fetch", users)
	}
	if !errors.Is(firstErrorEvent(*events), wantErr) {
		t.Errorf("EventError = %v, want %v", firstErrorEvent(*events), wantErr)
	}
	// The pull request's own detail must be unaffected by an unrelated
	// mentionable-users failure: nothing blocks on it (see the package
	// doc).
	if s.CurrentPR() == nil || s.CurrentPR().ID != "PR_1" {
		t.Errorf("CurrentPR() = %+v, want PR_1 unaffected by the mentionable-users failure", s.CurrentPR())
	}
}

// TestOpenPR_MentionableUsers_FailureThenRetryOnNextOpenPR covers the
// package doc's central claim: a repository a previous fetch failed for
// (fetchedAt stays at its zero value) always reads as "not fresh" and is
// retried the next time OpenPR opens a pull request in it, rather than
// being permanently marked "already tried" for the rest of the session.
func TestOpenPR_MentionableUsers_FailureThenRetryOnNextOpenPR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	repo := model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
	ref1 := model.PRRef{Repo: repo, Number: 1}
	ref2 := model.PRRef{Repo: repo, Number: 2}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})
	wantErr := errors.New("boom")
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return nil, model.RateLimit{}, wantErr
	})

	s.OpenPR(ref1)
	runUntilIdle(t, disp)
	if users := s.MentionableUsers(); users != nil {
		t.Fatalf("MentionableUsers() = %+v, want nil after the first fetch fails", users)
	}

	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "octocat"}}, model.RateLimit{}, nil
	})
	s.OpenPR(ref2) // a different pull request, same (previously failed) repository
	runUntilIdle(t, disp)

	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 2 {
		t.Errorf("MentionableUsers call count = %d, want 2: a repository a previous fetch failed for must be retried", len(calls))
	}
	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "octocat" {
		t.Errorf("MentionableUsers() = %+v, want the retry's successful result", users)
	}
}

// TestOpenPR_MentionableUsers_InFlightDedupOnDoubleOpen covers
// startMentionableUsersFetch's loading-based in-flight dedup: a second
// OpenPR for a pull request in the same repository, while the first
// fetch is still in flight, must not start a second one.
func TestOpenPR_MentionableUsers_InFlightDedupOnDoubleOpen(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	repo := model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
	ref1 := model.PRRef{Repo: repo, Number: 1}
	ref2 := model.PRRef{Repo: repo, Number: 2}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})
	block := make(chan struct{})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		<-block
		return []model.User{{Login: "octocat"}}, model.RateLimit{}, nil
	})

	s.OpenPR(ref1) // starts a fetch for repo, held in flight by block
	s.OpenPR(ref2) // same repository, still in flight: must not duplicate it
	close(block)
	runUntilIdle(t, disp)

	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 1 {
		t.Errorf("MentionableUsers call count = %d, want 1: a fetch already in flight for the repository must not be duplicated", len(calls))
	}
}

// TestReloadPR_MentionableUsers_ForcesRefetchEvenWhenFresh covers the "R"
// key's "ignore the cache and refetch" semantics extending to mentionable
// users: ReloadPR must force a refetch even though the in-memory result is
// still well within mentionableUsersFreshFor.
func TestReloadPR_MentionableUsers_ForcesRefetchEvenWhenFresh(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "octocat"}}, model.RateLimit{}, nil
	})

	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 1 {
		t.Fatalf("MentionableUsers call count after OpenPR = %d, want 1", len(calls))
	}

	s.ReloadPR()
	runUntilIdle(t, disp)

	if calls := gitHub.mentionableUsersCallsSnapshot(); len(calls) != 2 {
		t.Errorf(
			"MentionableUsers call count after ReloadPR = %d, want 2: \"R\" must ignore an in-memory result still within the freshness window",
			len(calls),
		)
	}
}

// TestOpenPR_MentionableUsers_FailureLeavesStaleUsersInPlace covers a
// background refresh (triggered by a stale cache hit) failing: the stale
// result already shown must be kept, not cleared, so the candidate list
// never regresses from "something, if outdated" to "nothing" purely
// because a refresh attempt failed.
func TestOpenPR_MentionableUsers_FailureLeavesStaleUsersInPlace(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	ref := testDetailRef(1)

	key, err := cache.RepoKey(s.deps.Host, "tester", ref.Repo, mentionableUsersCacheRest)
	if err != nil {
		t.Fatalf("RepoKey: %v", err)
	}
	body, err := json.Marshal([]model.User{{Login: "stale-user"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	staleAt := s.deps.Now().Add(-2 * mentionableUsersFreshFor)
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: staleAt}); err != nil {
		t.Fatalf("seed mentionable-users cache: %v", err)
	}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	wantErr := errors.New("boom")
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return nil, model.RateLimit{}, wantErr
	})
	events := collectEvents(s)

	s.OpenPR(ref)
	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "stale-user" {
		t.Fatalf("MentionableUsers() = %+v, want the stale cache entry applied immediately", users)
	}
	runUntilIdle(t, disp)

	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "stale-user" {
		t.Errorf("MentionableUsers() = %+v, want the stale entry kept in place after the background refresh failed", users)
	}
	if !errors.Is(firstErrorEvent(*events), wantErr) {
		t.Errorf("EventError = %v, want %v", firstErrorEvent(*events), wantErr)
	}
}

// TestOpenPR_MentionableUsers_RepoChangeEmitsEventSynchronously covers
// EventMentionableChanged's own doc comment: switching to a pull request in
// a different repository fires it immediately (synchronously, from OpenPR
// itself), since MentionableUsers() can genuinely jump between two
// repositories' independent lists at that instant; switching to another
// pull request in the *same* repository must not fire a redundant one.
func TestOpenPR_MentionableUsers_RepoChangeEmitsEventSynchronously(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	repoA := model.RepoRef{Host: "example.com", Owner: "acme", Name: "a"}
	repoB := model.RepoRef{Host: "example.com", Owner: "acme", Name: "b"}
	refA := model.PRRef{Repo: repoA, Number: 1}
	refB1 := model.PRRef{Repo: repoB, Number: 1}
	refB2 := model.PRRef{Repo: repoB, Number: 2}

	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "id-" + r.Key(), Ref: r}}, nil
	})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "octocat"}}, model.RateLimit{}, nil
	})

	s.OpenPR(refA)
	runUntilIdle(t, disp)

	events := collectEvents(s)
	s.OpenPR(refB1) // switching repositories
	if n := countEventKind(*events, EventMentionableChanged); n == 0 {
		t.Error("no EventMentionableChanged emitted synchronously when OpenPR switches to a different repository")
	}
	runUntilIdle(t, disp)

	events2 := collectEvents(s)
	s.OpenPR(refB2) // same repository, a different pull request
	if n := countEventKind(*events2, EventMentionableChanged); n != 0 {
		t.Errorf("EventMentionableChanged emitted %d times synchronously for a same-repository pull request switch, want 0", n)
	}
}

// TestApplyViewerResult_PersistsPendingMentionableUsersOnceConfirmed covers
// mentionableEntry.persisted's own reason for existing: a fetch that
// succeeds while the viewer's login is still unconfirmed cannot write its
// cache entry yet (cacheMentionableUsers itself refuses), but must not
// stay unwritten for the rest of the session once confirmation resolves.
// The viewer fetch is held blocked until after the mentionable-users fetch
// has already resolved, so the two are deterministically ordered rather
// than racing within one runUntilIdle drain.
func TestApplyViewerResult_PersistsPendingMentionableUsersOnceConfirmed(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	ref := testDetailRef(1)

	viewerBlock := make(chan struct{})
	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		<-viewerBlock
		return model.User{Login: "octocat"}, model.RateLimit{}, nil
	}
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r}}, nil
	})
	gitHub.setMentionableUsersFunc(func(context.Context, model.RepoRef, string, int) ([]model.User, model.RateLimit, error) {
		return []model.User{{Login: "mentionable-user"}}, model.RateLimit{}, nil
	})

	s.Start(context.Background())
	s.OpenPR(ref) // s.viewer.Login is still "": the viewer fetch is blocked
	runUntilIdle(t, disp)

	if users := s.MentionableUsers(); len(users) != 1 || users[0].Login != "mentionable-user" {
		t.Fatalf("MentionableUsers() = %+v, want the fetched list applied even before the viewer is confirmed", users)
	}
	key, err := cache.RepoKey(s.deps.Host, "octocat", ref.Repo, mentionableUsersCacheRest)
	if err != nil {
		t.Fatalf("RepoKey: %v", err)
	}
	if _, ok, err := s.deps.Cache.Get(key); err != nil || ok {
		t.Fatalf("cache.Get() = (ok=%v, err=%v), want a miss: nothing is written before the viewer is confirmed", ok, err)
	}

	close(viewerBlock)
	runUntilIdle(t, disp)

	if _, ok, err := s.deps.Cache.Get(key); err != nil || !ok {
		t.Errorf("cache.Get() = (ok=%v, err=%v), want a hit: the pending write must be retried once the viewer is confirmed", ok, err)
	}
}
