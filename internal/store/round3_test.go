package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// Item 21: a timeout unrelated to our own cancellation must still be
// reported as an error, even though it satisfies
// errors.Is(err, context.DeadlineExceeded) the same way our own
// cancellation does (go-gh's underlying http.Client{Timeout} produces
// exactly such an error).
func TestApplyFetchResult_DeadlineExceededWithoutCancellationIsAnError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, context.DeadlineExceeded
	}

	var gotError bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotError = true
		}
	})

	s.LoadList(false)
	runUntilIdle(t, disp)

	if !gotError {
		t.Error("a timeout unrelated to our own cancellation must emit EventError")
	}
	if s.LastError() == nil {
		t.Error("LastError() = nil, want the timeout error")
	}
}

// Item 21 (viewer side): same distinction for fetchViewer/applyViewerResult.
func TestApplyViewerResult_DeadlineExceededWithoutCancellationIsAnError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		return model.User{}, model.RateLimit{}, context.DeadlineExceeded
	}

	var gotError bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotError = true
		}
	})

	s.fetchViewer(context.Background())
	runUntilIdle(t, disp)

	if !gotError {
		t.Error("a viewer timeout unrelated to our own cancellation must emit EventError")
	}
}

// Item 24: the viewer retry started by startList must use a context that
// outlives the list generation, so a new generation superseding the old
// one does not cancel (and thus falsely error-report) the still-useful
// viewer retry.
func TestStartList_ViewerRetryUsesBaseContextNotListContext(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	release := make(chan struct{})
	gitHub.viewerFunc = func(ctx context.Context) (model.User, model.RateLimit, error) {
		<-release
		if ctx.Err() != nil {
			return model.User{}, model.RateLimit{}, ctx.Err()
		}
		return model.User{Login: "tester"}, model.RateLimit{}, nil
	}

	s.LoadList(false) // generation 1: login unknown, triggers a (blocked) viewer retry
	s.LoadList(false) // generation 2: cancels generation 1's list ctx

	var gotError bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotError = true
		}
	})

	close(release)
	runUntilIdle(t, disp)

	if gotError {
		t.Error("a viewer retry fetch must not be cancelled by a superseded list generation")
	}
	if s.viewer.Login != "tester" {
		t.Errorf("viewer.Login = %q, want %q", s.viewer.Login, "tester")
	}
	if s.viewerFetchInFlight {
		t.Error("viewerFetchInFlight = true after the fetch completed, want false")
	}
}

// Item 25: discarding sections after a confirmed login mismatch must
// notify subscribers, so the UI drops the wrong account's rows
// immediately rather than only on the next unrelated event.
func TestDiscardAllSectionItems_EmitsListChanged(t *testing.T) {
	cfg := config.Default()
	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	s.sections[0].items = []model.PullRequest{pr("SOMETHING", time.Now())}

	var gotListChanged bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventListChanged {
			gotListChanged = true
		}
	})

	s.discardAllSectionItems()

	if !gotListChanged {
		t.Error("discardAllSectionItems must emit EventListChanged")
	}
	if len(s.sections[0].items) != 0 {
		t.Errorf("items = %v, want empty", s.sections[0].items)
	}
}

// Item 27: LastError reflects the most recently observed outcome, not a
// sticky "ever happened" flag: once a subsequent fetch succeeds, it must
// clear.
func TestLastError_ClearedOnSubsequentSuccess(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	wantErr := errors.New("boom")
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, wantErr
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	if s.LastError() == nil {
		t.Fatalf("expected LastError() to be set after a failed fetch")
	}

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, nil
	}
	s.Refresh()
	runUntilIdle(t, disp)

	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil after a subsequent success", s.LastError())
	}
}

// newIncrementingClock returns a Now func that advances by one second on
// every call, so tests can detect an unwanted re-assignment to the same
// nominal timestamp (a fixed, constant Now would make "unchanged" and
// "reassigned to the same value" indistinguishable).
func newIncrementingClock() func() time.Time {
	var mu sync.Mutex
	tick := 0
	base := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
}

// Item 28: LoadMore (a page-N fetch) must never update LastRefresh, which
// tracks only full, all-sections-succeeded page-1 generations.
func TestLastRefresh_NotUpdatedByLoadMore(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()

	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	s := New(Deps{
		GitHub: gitHub, Cache: cacheStore, Dispatch: disp.dispatch,
		Config: cfg, Host: "example.com", Now: newIncrementingClock(),
	})

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		if cursor == "" {
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "c2"}, nil
		}
		return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: false}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)
	afterLoad := s.LastRefresh()
	if afterLoad.IsZero() {
		t.Fatalf("expected LastRefresh to be set once every section's page 1 has succeeded")
	}

	s.LoadMore(0)
	runUntilIdle(t, disp)
	if got := s.LastRefresh(); !got.Equal(afterLoad) {
		t.Errorf("LastRefresh() = %v, want unchanged %v after LoadMore", got, afterLoad)
	}
}

// Item 28: LastRefresh must wait for every section's page-1 fetch to
// complete before updating, not just the first one to finish.
func TestLastRefresh_WaitsForEverySectionPageOne(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	targetQuery := s.sections[len(s.sections)-1].query // Involved: fetched last in startList's loop
	release := make(chan struct{})
	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		if query == targetQuery {
			<-release
		}
		return gh.SearchResult{}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	if !s.LastRefresh().IsZero() {
		t.Error("LastRefresh must not update until every section's page 1 has resolved")
	}

	close(release)
	runUntilIdle(t, disp)

	if s.LastRefresh().IsZero() {
		t.Error("LastRefresh should be set once every section has resolved")
	}
}

// Item 28: a generation where any section's page-1 fetch fails must not
// update LastRefresh, even though every other section succeeded.
func TestLastRefresh_NotUpdatedWhenAnySectionErrors(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	targetQuery := s.sections[0].query
	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		if query == targetQuery {
			return gh.SearchResult{}, errors.New("boom")
		}
		return gh.SearchResult{}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	if !s.LastRefresh().IsZero() {
		t.Error("LastRefresh must stay unset when any section's page 1 fetch fails")
	}
}

// Item 22(b): SectionStates surfaces per-section Warnings from
// gh.SearchResult, and a persistent warning is logged only once even
// though it recurs on every fetch.
func TestSectionStates_ExposesWarningsLoggedOnce(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()

	var records []slog.Record
	logger := slog.New(capturingHandler{records: &records})
	s := New(Deps{
		GitHub: gitHub, Cache: mustNewCache(t), Dispatch: disp.dispatch,
		Logger: logger, Config: cfg, Host: "example.com", Now: time.Now,
	})

	targetQuery := s.sections[0].query
	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		if query == targetQuery {
			return gh.SearchResult{Warnings: []string{"1 search result(s) omitted (could not be resolved)"}}, nil
		}
		return gh.SearchResult{}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	states := s.SectionStates()
	if len(states[0].Warnings) != 1 || states[0].Warnings[0] != "1 search result(s) omitted (could not be resolved)" {
		t.Errorf("SectionStates()[0].Warnings = %v, want the fetch's warning", states[0].Warnings)
	}

	// A second, unrelated refresh re-reports the same warning; it must
	// still only have been logged once.
	s.Refresh()
	runUntilIdle(t, disp)

	warnCount := 0
	for _, r := range records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, "search result warning") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Errorf("search-result warnings logged = %d, want exactly 1", warnCount)
	}
}

// Item 26: an incomplete state: value that is a prefix of a valid state
// (the user is presumably still typing) is silent -- no EventError, just
// the usual ListChanged/Rows update -- and does not change the state.
func TestSetFilter_IncompleteStatePrefixIsSilent(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	for _, prefix := range []string{"c", "clo", "close"} {
		var gotError bool
		var gotListChanged bool
		s.subscribers = nil
		s.Subscribe(func(e Event) {
			switch e.Kind {
			case EventError:
				gotError = true
			case EventListChanged:
				gotListChanged = true
			}
		})

		s.SetFilter("state:" + prefix)
		runUntilIdle(t, disp)

		if gotError {
			t.Errorf("SetFilter(%q) emitted EventError, want silence (incomplete prefix)", "state:"+prefix)
		}
		if !gotListChanged {
			t.Errorf("SetFilter(%q) did not emit EventListChanged", "state:"+prefix)
		}
		if s.state != cfg.List.State {
			t.Errorf("SetFilter(%q) changed state to %q, want unchanged %q", "state:"+prefix, s.state, cfg.List.State)
		}
	}
}

// Item 26: a state: value that is not a prefix of any valid state emits
// EventError, but only once while it persists unchanged; a different
// invalid value gets its own error.
func TestSetFilter_InvalidStateEmitsErrorOncePerDistinctValue(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var errCount int
	var lastErr error
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			errCount++
			lastErr = e.Err
		}
	})

	s.SetFilter("state:bogus")
	runUntilIdle(t, disp)
	if errCount != 1 {
		t.Fatalf("errCount after first invalid value = %d, want 1", errCount)
	}
	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil (an invalid state: filter is not a fetch error)", s.LastError())
	}

	s.SetFilter("state:bogus") // same value again
	runUntilIdle(t, disp)
	if errCount != 1 {
		t.Errorf("errCount after repeating the same invalid value = %d, want still 1", errCount)
	}

	s.SetFilter("state:other-bogus") // a different invalid value
	runUntilIdle(t, disp)
	if errCount != 2 {
		t.Errorf("errCount after a different invalid value = %d, want 2", errCount)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "other-bogus") {
		t.Errorf("last EventError = %v, want it to mention %q", lastErr, "other-bogus")
	}
}

func mustNewCache(t *testing.T) *cache.Store {
	t.Helper()
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	return c
}
