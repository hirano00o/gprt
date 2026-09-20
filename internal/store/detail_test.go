package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func testDetailRef(number int) model.PRRef {
	return model.PRRef{
		Repo:   model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"},
		Number: number,
	}
}

// newDetailTestStore builds a Store already primed with a confirmed viewer
// login, so OpenPR/RefreshPR/ReloadPR's cache reads/writes are never
// skipped for an unknown or unconfirmed login (see applyCachedDetail and
// cacheDetail). Tests in this file are not exercising Start's own
// viewer-resolution flow, so setting these fields directly (this file is
// an in-package, white-box test, like list_test.go) is simpler than
// driving a fake Viewer call through Start.
func newDetailTestStore(t *testing.T, gitHub *fakeGitHub, disp *fakeDispatcher) *Store {
	t.Helper()
	s := newTestStore(t, config.Default(), gitHub, disp)
	s.viewer = model.User{Login: "tester"}
	s.viewerConfirmed = true
	return s
}

func TestOpenPR_CacheFirstThenNetworkReplaces(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(42)
	cachedPR := model.PullRequest{ID: "PR_CACHED", Ref: ref, Title: "cached title"}
	key, err := cache.PRKey(s.deps.Host, "tester", ref, detailCacheRest)
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, err := json.Marshal(cachedPR)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed detail cache: %v", err)
	}

	networkPR := model.PullRequest{ID: "PR_NETWORK", Ref: ref, Title: "network title"}
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: networkPR}, nil
	})

	s.OpenPR(ref)

	if got := s.CurrentPR(); got == nil || got.ID != "PR_CACHED" {
		t.Fatalf("CurrentPR() = %+v, want the cached PR immediately after OpenPR", got)
	}
	if !s.DetailState().Stale {
		t.Error("DetailState().Stale = false, want true before the network fetch completes")
	}

	runUntilIdle(t, disp)

	got := s.CurrentPR()
	if got == nil || got.ID != "PR_NETWORK" {
		t.Fatalf("CurrentPR() = %+v, want the network PR after the fetch completes", got)
	}
	if s.DetailState().Stale {
		t.Error("DetailState().Stale = true, want false once the network fetch completes")
	}
}

func TestOpenPR_GenerationDropOnRapidSwitch(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref1 := testDetailRef(1)
	ref2 := testDetailRef(2)

	block1 := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, ref model.PRRef, _ string) (gh.DetailResult, error) {
		if ref.Number == 1 {
			<-block1
			return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref1}}, nil
		}
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_2", Ref: ref2}}, nil
	})

	s.OpenPR(ref1)
	s.OpenPR(ref2) // switches before PR #1's slow fetch has returned

	close(block1) // let PR #1's now-superseded fetch finish
	runUntilIdle(t, disp)

	got := s.CurrentPR()
	if got == nil || got.ID != "PR_2" {
		t.Fatalf("CurrentPR() = %+v, want PR_2 (PR_1's late result must be dropped)", got)
	}
	if gotRef, ok := s.CurrentRef(); !ok || gotRef != ref2 {
		t.Errorf("CurrentRef() = %+v, %v, want %+v, true", gotRef, ok, ref2)
	}
}

func TestRefreshPR_ErrorKeepsPreviousDataAndSetsErr(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(3)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_3", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if got := s.CurrentPR(); got == nil || got.ID != "PR_3" {
		t.Fatalf("CurrentPR() = %+v, want PR_3 after the initial fetch", got)
	}

	wantErr := errors.New("boom")
	var sawErrorEvent bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			sawErrorEvent = true
		}
	})
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{}, wantErr
	})

	s.RefreshPR()
	runUntilIdle(t, disp)

	if got := s.CurrentPR(); got == nil || got.ID != "PR_3" {
		t.Errorf("CurrentPR() = %+v, want the previous data kept after a failed refresh", got)
	}
	if !errors.Is(s.DetailState().Err, wantErr) {
		t.Errorf("DetailState().Err = %v, want %v", s.DetailState().Err, wantErr)
	}
	if !errors.Is(s.LastError(), wantErr) {
		t.Errorf("LastError() = %v, want the detail error to surface", s.LastError())
	}
	if !sawErrorEvent {
		t.Error("expected an EventError to be emitted for the failed refresh")
	}
}

func TestClosePR_CancelsInFlightFetchSilently(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	release := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-release
		<-ctx.Done()
		return gh.DetailResult{}, ctx.Err()
	})

	s.OpenPR(testDetailRef(9))

	var gotErrorEvent bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotErrorEvent = true
		}
	})

	s.ClosePR()
	close(release)
	runUntilIdle(t, disp)

	if gotErrorEvent {
		t.Error("a fetch cancelled by ClosePR must not emit EventError")
	}
	if s.CurrentPR() != nil {
		t.Errorf("CurrentPR() = %+v, want nil after ClosePR", s.CurrentPR())
	}
	if _, ok := s.CurrentRef(); ok {
		t.Error("CurrentRef() ok = true, want false after ClosePR")
	}
	state := s.DetailState()
	if state.Loading {
		t.Error("DetailState().Loading = true, want false after ClosePR")
	}
	if state.Err != nil {
		t.Errorf("DetailState().Err = %v, want nil after our own cancellation", state.Err)
	}
	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil after our own cancellation", s.LastError())
	}
}

func TestStartAutoRefresh_AlsoRefreshesCurrentPR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(5)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_5", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if calls := gitHub.detailCallCount(); calls != 1 {
		t.Fatalf("detailCallCount() = %d, want 1 after OpenPR", calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartAutoRefresh(ctx, 10*time.Millisecond)

	select {
	case f := <-disp.ch:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the auto-refresh ticker to dispatch")
	}
	cancel()
	runUntilIdle(t, disp)

	if calls := gitHub.detailCallCount(); calls < 2 {
		t.Errorf("detailCallCount() = %d, want at least 2 (RefreshPR must run alongside Refresh on each tick)", calls)
	}
}

func TestReloadPR_InvalidatesPRCache(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(11)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_11", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	key, err := cache.PRKey(s.deps.Host, "tester", ref, detailCacheRest)
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	if _, ok, _ := s.deps.Cache.Get(key); !ok {
		t.Fatal("expected the detail to be cached after OpenPR")
	}

	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{}, errors.New("network down")
	})

	s.ReloadPR()
	if _, ok, _ := s.deps.Cache.Get(key); ok {
		t.Error("ReloadPR must invalidate the pull request's cache entry before refetching")
	}
	runUntilIdle(t, disp)
}

func TestReload_AlsoReloadsCurrentPR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(13)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_13", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)

	before := gitHub.detailCallCount()
	s.Reload()
	runUntilIdle(t, disp)

	if after := gitHub.detailCallCount(); after <= before {
		t.Errorf("detailCallCount() = %d, want more than %d (Reload must also refetch the current pull request)", after, before)
	}
}

func TestOpenPR_NoCurrentPR_AccessorsReportNone(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	if s.CurrentPR() != nil {
		t.Errorf("CurrentPR() = %+v, want nil before any OpenPR call", s.CurrentPR())
	}
	if _, ok := s.CurrentRef(); ok {
		t.Error("CurrentRef() ok = true, want false before any OpenPR call")
	}

	// RefreshPR/ReloadPR must be no-ops when nothing is open.
	s.RefreshPR()
	s.ReloadPR()
	runUntilIdle(t, disp)
	if gitHub.detailCallCount() != 0 {
		t.Errorf("detailCallCount() = %d, want 0 (RefreshPR/ReloadPR must no-op with no current PR)", gitHub.detailCallCount())
	}
}

func TestOpenPR_ResetsFetchedAtOnSwitch(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	refA := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_A", Ref: refA}}, nil
	})
	s.OpenPR(refA)
	runUntilIdle(t, disp)
	if s.DetailState().FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set after PR A's fetch succeeds")
	}

	refB := testDetailRef(2)
	block := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-block
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_B", Ref: refB}}, nil
	})
	s.OpenPR(refB)

	if got := s.DetailState().FetchedAt; !got.IsZero() {
		t.Errorf("FetchedAt = %v, want zero immediately after switching to PR B, before its own fetch completes", got)
	}
	close(block)
	runUntilIdle(t, disp)
}

func TestClosePR_ResetsFetchedAt(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if s.DetailState().FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set after the fetch succeeds")
	}

	s.ClosePR()

	if got := s.DetailState().FetchedAt; !got.IsZero() {
		t.Errorf("FetchedAt = %v, want zero after ClosePR", got)
	}
}

func TestOpenPR_StaleResetWhenSwitchingToUncachedPR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	refA := testDetailRef(1)
	keyA, err := cache.PRKey(s.deps.Host, "tester", refA, detailCacheRest)
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, err := json.Marshal(model.PullRequest{ID: "PR_A_CACHED", Ref: refA})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.deps.Cache.Put(keyA, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed detail cache: %v", err)
	}

	block := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-block
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_A_NETWORK", Ref: refA}}, nil
	})
	s.OpenPR(refA)
	if !s.DetailState().Stale {
		t.Fatal("expected Stale = true immediately after OpenPR with a cache hit")
	}
	close(block)
	runUntilIdle(t, disp)

	refB := testDetailRef(2) // no cache entry for B
	wantErr := errors.New("boom")
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{}, wantErr
	})
	s.OpenPR(refB)

	if s.DetailState().Stale {
		t.Error("Stale = true immediately after OpenPR(refB), want false: PR B has no cache entry")
	}
	runUntilIdle(t, disp)

	state := s.DetailState()
	if state.Stale {
		t.Error("Stale = true, want false: PR B has no cache entry, so it must not inherit PR A's stale flag")
	}
	if !errors.Is(state.Err, wantErr) {
		t.Errorf("Err = %v, want %v", state.Err, wantErr)
	}
}

func TestClosePR_EmitsLoadingChangedWhenFetchWasInFlight(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	release := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-release
		<-ctx.Done()
		return gh.DetailResult{}, ctx.Err()
	})

	s.OpenPR(testDetailRef(1))
	if !s.DetailState().Loading {
		t.Fatal("expected Loading = true right after OpenPR")
	}

	var loadingChangedCount int
	s.Subscribe(func(e Event) {
		if e.Kind == EventPRLoadingChanged {
			loadingChangedCount++
		}
	})

	s.ClosePR()

	if s.DetailState().Loading {
		t.Error("Loading = true, want false immediately after ClosePR")
	}
	if loadingChangedCount != 1 {
		t.Errorf("EventPRLoadingChanged fired %d times from ClosePR, want 1", loadingChangedCount)
	}

	close(release)
	runUntilIdle(t, disp)
}

func TestApplyViewerResult_RefreshesOpenPRWhenViewerBecomesKnown(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp) // viewer starts unknown

	ref := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref}}, nil
	})

	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if calls := gitHub.detailCallCount(); calls != 1 {
		t.Fatalf("detailCallCount() = %d, want 1 after the initial OpenPR", calls)
	}

	s.applyViewerResult(model.User{Login: "tester"}, model.RateLimit{}, nil, false)
	runUntilIdle(t, disp)

	if calls := gitHub.detailCallCount(); calls < 2 {
		t.Errorf("detailCallCount() = %d, want at least 2 (the open PR must be refreshed once the viewer login is known)", calls)
	}
}

func TestApplyViewerResult_MismatchReloadsOpenPR(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	// Seed an unconfirmed login, as applyCachedViewer would from a
	// previous run's cache.
	s.viewer = model.User{Login: "old-user"}
	s.viewerConfirmed = false

	ref := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_OLD_USER", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if got := s.CurrentPR(); got == nil || got.ID != "PR_OLD_USER" {
		t.Fatalf("CurrentPR() = %+v, want the pull request fetched under the seeded login", got)
	}

	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_NEW_USER", Ref: ref}}, nil
	})

	// The network now confirms a DIFFERENT account than the seeded one.
	s.applyViewerResult(model.User{Login: "new-user"}, model.RateLimit{}, nil, false)

	if got := s.CurrentPR(); got != nil {
		t.Errorf("CurrentPR() = %+v, want nil discarded immediately on a login mismatch", got)
	}
	runUntilIdle(t, disp)

	if got := s.CurrentPR(); got == nil || got.ID != "PR_NEW_USER" {
		t.Errorf("CurrentPR() = %+v, want the pull request refetched under the confirmed login", got)
	}
}

func TestStop_AlsoCancelsDetailFetchSilently(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	release := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-release
		<-ctx.Done()
		return gh.DetailResult{}, ctx.Err()
	})

	s.OpenPR(testDetailRef(1))

	var gotErrorEvent bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotErrorEvent = true
		}
	})

	s.Stop()
	close(release)
	runUntilIdle(t, disp)

	if gotErrorEvent {
		t.Error("a detail fetch cancelled by Stop() must not emit EventError")
	}
	if s.DetailState().Loading {
		t.Error("DetailState().Loading = true, want false after Stop() cancels the detail fetch")
	}
	// Stop() only cancels fetches; it must not close the pull request.
	if _, ok := s.CurrentRef(); !ok {
		t.Error("CurrentRef() ok = false, want true: Stop() must not clear the current pull request")
	}
}

func TestFetchDetail_EmitsEventLoadingChangedAlongsideEventPRLoadingChanged(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref}}, nil
	})

	var loadingTransitions []bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventLoadingChanged {
			loadingTransitions = append(loadingTransitions, s.Loading())
		}
	})

	s.OpenPR(ref)
	if len(loadingTransitions) == 0 || !loadingTransitions[0] {
		t.Fatalf("loadingTransitions = %v, want the first EventLoadingChanged to report Loading() = true", loadingTransitions)
	}

	runUntilIdle(t, disp)

	if len(loadingTransitions) < 2 || loadingTransitions[len(loadingTransitions)-1] {
		t.Fatalf("loadingTransitions = %v, want the last EventLoadingChanged to report Loading() = false once the fetch completes", loadingTransitions)
	}
}

func TestOpenPR_ClearsLastErrorFromPreviousPR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	refA := testDetailRef(1)
	wantErr := errors.New("boom")
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{}, wantErr
	})
	s.OpenPR(refA)
	runUntilIdle(t, disp)
	if !errors.Is(s.LastError(), wantErr) {
		t.Fatalf("LastError() = %v, want %v after PR A's fetch fails", s.LastError(), wantErr)
	}

	refB := testDetailRef(2)
	block := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-block
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_B", Ref: refB}}, nil
	})
	s.OpenPR(refB)

	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil immediately after opening PR B, before its own fetch resolves", s.LastError())
	}
	close(block)
	runUntilIdle(t, disp)
}

func TestReloadPR_EmitsEventPRChangedWhenMarkingStale(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)

	ref := testDetailRef(1)
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref}}, nil
	})
	s.OpenPR(ref)
	runUntilIdle(t, disp)
	if s.DetailState().Stale {
		t.Fatal("expected Stale = false after the initial fetch succeeds")
	}

	block := make(chan struct{})
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, _ string) (gh.DetailResult, error) {
		<-block
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1_RELOADED", Ref: ref}}, nil
	})

	var prChangedCount int
	s.Subscribe(func(e Event) {
		if e.Kind == EventPRChanged {
			prChangedCount++
		}
	})

	s.ReloadPR()

	if !s.DetailState().Stale {
		t.Error("expected Stale = true immediately after ReloadPR")
	}
	if prChangedCount == 0 {
		t.Error("expected EventPRChanged to be emitted synchronously when ReloadPR marks the detail stale")
	}

	close(block)
	runUntilIdle(t, disp)
}

func TestApplyViewerResult_RestartsInFlightDetailFetchWhenViewerBecomesKnown(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp) // viewer starts unknown

	ref := testDetailRef(1)
	block := make(chan struct{})
	var mu sync.Mutex
	var gotViewerLogins []string
	gitHub.setDetailFunc(func(ctx context.Context, _ model.PRRef, viewerLogin string) (gh.DetailResult, error) {
		mu.Lock()
		gotViewerLogins = append(gotViewerLogins, viewerLogin)
		mu.Unlock()
		if viewerLogin == "" {
			// The first fetch (started before the viewer resolved) blocks
			// until released, simulating it still being in flight when
			// the viewer login becomes known below.
			<-block
			<-ctx.Done()
			return gh.DetailResult{}, ctx.Err()
		}
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_CORRECT", Ref: ref}}, nil
	})

	s.OpenPR(ref)
	if !s.DetailState().Loading {
		t.Fatal("expected Loading = true right after OpenPR")
	}

	// The viewer resolves while that first ($viewer == "") fetch is still
	// in flight: it must be restarted, not left to complete and applied.
	s.applyViewerResult(model.User{Login: "tester"}, model.RateLimit{}, nil, false)

	close(block) // release the first, now-superseded fetch
	runUntilIdle(t, disp)

	got := s.CurrentPR()
	if got == nil || got.ID != "PR_CORRECT" {
		t.Fatalf("CurrentPR() = %+v, want the pull request fetched under the correct ($viewer == \"tester\") login", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotViewerLogins) != 2 || gotViewerLogins[0] != "" || gotViewerLogins[1] != "tester" {
		t.Errorf("gotViewerLogins = %v, want [\"\", \"tester\"]", gotViewerLogins)
	}
}
