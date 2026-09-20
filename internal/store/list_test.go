package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// capturingHandler is a minimal slog.Handler that records every record, so
// tests can assert on exactly what was logged.
type capturingHandler struct {
	records *[]slog.Record
}

func (h capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h capturingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}

func (h capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h capturingHandler) WithGroup(string) slog.Handler      { return h }

func pr(id string, updatedAt time.Time) model.PullRequest {
	return model.PullRequest{
		ID:        id,
		Ref:       model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 1},
		UpdatedAt: updatedAt,
	}
}

func seedViewerCache(t *testing.T, s *Store, login string) {
	t.Helper()
	key, err := cache.SearchKey(s.deps.Host, viewerPlaceholderLogin, "viewer")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	body, err := json.Marshal(cachedViewer{User: model.User{Login: login}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed viewer cache: %v", err)
	}
}

func seedSectionCache(t *testing.T, s *Store, sectionIndex int, login string, res gh.SearchResult) {
	t.Helper()
	sec := s.sections[sectionIndex]
	key, err := cache.SearchKey(s.deps.Host, login, sec.query+"\x00")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed section cache: %v", err)
	}
}

func rowForItem(rows []Row, id string) *Row {
	for i := range rows {
		if rows[i].Kind == RowItem && rows[i].Item.PR != nil && rows[i].Item.PR.ID == id {
			return &rows[i]
		}
	}
	return nil
}

func containsItemID(rows []Row, id string) bool {
	return rowForItem(rows, id) != nil
}

func TestLoadList_CacheFirstThenNetworkReplacesStaleRows(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "tester")

	cachedPR := pr("PR_CACHED", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	seedSectionCache(t, s, 0, "tester", gh.SearchResult{Items: []model.PullRequest{cachedPR}})

	networkPR := pr("PR_NETWORK", time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{networkPR}}, nil
	}

	s.Start(context.Background())

	rows := s.Rows()
	if !containsItemID(rows, "PR_CACHED") {
		t.Fatalf("expected the cached item visible immediately after Start, rows = %+v", rows)
	}
	if row := rowForItem(rows, "PR_CACHED"); !row.Stale {
		t.Error("cached row should be marked Stale before the network fetch completes")
	}

	runUntilIdle(t, disp)

	rows = s.Rows()
	if containsItemID(rows, "PR_CACHED") {
		t.Error("the stale cached item should have been replaced by the network result")
	}
	if !containsItemID(rows, "PR_NETWORK") {
		t.Fatalf("expected the network item visible after the fetch completes, rows = %+v", rows)
	}
	if row := rowForItem(rows, "PR_NETWORK"); row.Stale {
		t.Error("the network-backed row should not be marked Stale")
	}
}

func TestLoadList_ForceSkipsCache(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "tester")
	s.applyCachedViewer()
	seedSectionCache(t, s, 0, "tester", gh.SearchResult{Items: []model.PullRequest{pr("PR_CACHED", time.Now())}})

	s.LoadList(true)

	rows := s.Rows()
	if containsItemID(rows, "PR_CACHED") {
		t.Error("LoadList(true) must not apply the cached page")
	}
}

func TestLoadList_GenerationDrop(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	targetQuery := s.sections[0].query

	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})

	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		if query != targetQuery {
			return gh.SearchResult{}, nil
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		if n == 1 {
			<-release
			return gh.SearchResult{Items: []model.PullRequest{pr("PR_FIRST_GEN", time.Now())}}, nil
		}
		return gh.SearchResult{Items: []model.PullRequest{pr("PR_SECOND_GEN", time.Now())}}, nil
	}

	s.LoadList(false) // generation 1: section[0]'s fetch blocks on release
	runUntilIdle(t, disp)

	s.LoadList(false) // generation 2: section[0]'s fetch is a fresh, unblocked call
	runUntilIdle(t, disp)

	close(release) // let generation 1's stale fetch finally return
	runUntilIdle(t, disp)

	rows := s.Rows()
	if containsItemID(rows, "PR_FIRST_GEN") {
		t.Error("generation 1's late result should have been dropped")
	}
	if !containsItemID(rows, "PR_SECOND_GEN") {
		t.Error("generation 2's result should have been applied")
	}
}

func TestLoadMore_InFlightDedup(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var mu sync.Mutex
	moreCalls := 0
	release := make(chan struct{})

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		if cursor == "" {
			return gh.SearchResult{HasNextPage: true, EndCursor: "cursor-2"}, nil
		}
		mu.Lock()
		moreCalls++
		mu.Unlock()
		<-release
		return gh.SearchResult{Items: []model.PullRequest{pr("PR_MORE", time.Now())}}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	s.LoadMore(0)
	s.LoadMore(0) // deduped: must not start a second network call

	close(release)
	runUntilIdle(t, disp)

	mu.Lock()
	got := moreCalls
	mu.Unlock()
	if got != 1 {
		t.Errorf("moreCalls = %d, want 1 (the second LoadMore should have been deduped)", got)
	}
	if !containsItemID(s.Rows(), "PR_MORE") {
		t.Error("expected the loaded-more item to appear")
	}
}

func TestLoadMore_AppendsAndUpdatesCursor(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	page1 := pr("PR_PAGE1", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	page2 := pr("PR_PAGE2", time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{page1}, HasNextPage: true, EndCursor: "cursor-2"}, nil
		case "cursor-2":
			return gh.SearchResult{Items: []model.PullRequest{page2}, HasNextPage: false, EndCursor: ""}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	if !s.sections[0].hasNext || s.sections[0].cursor != "cursor-2" {
		t.Fatalf("after page 1: cursor=%q hasNext=%v, want %q/true", s.sections[0].cursor, s.sections[0].hasNext, "cursor-2")
	}

	s.LoadMore(0)
	runUntilIdle(t, disp)

	if s.sections[0].hasNext {
		t.Error("hasNext should be false after the last page")
	}
	if s.sections[0].cursor != "" {
		t.Errorf("cursor = %q, want empty after the last page", s.sections[0].cursor)
	}
	if len(s.sections[0].items) != 2 {
		t.Fatalf("items = %d, want 2 (page 1 + page 2 appended)", len(s.sections[0].items))
	}
}

func TestLoadList_ErrorKeepsPreviousItemsAndEmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	existing := pr("PR_EXISTING", time.Now())
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{existing}}, nil
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	if !containsItemID(s.Rows(), "PR_EXISTING") {
		t.Fatalf("expected the initial item to be present")
	}

	var events []Event
	s.Subscribe(func(e Event) { events = append(events, e) })

	wantErr := errors.New("boom")
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, wantErr
	}
	s.Refresh()
	runUntilIdle(t, disp)

	if !containsItemID(s.Rows(), "PR_EXISTING") {
		t.Error("previous items must survive a failed refresh")
	}

	var gotErrorEvent bool
	for _, e := range events {
		if e.Kind == EventError && errors.Is(e.Err, wantErr) {
			gotErrorEvent = true
		}
	}
	if !gotErrorEvent {
		t.Error("expected an EventError carrying the fetch error")
	}
}

func TestRows_DedupeAcrossSectionsInPriorityOrder(t *testing.T) {
	cfg := config.Default()
	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())

	shared := pr("PR_SHARED", time.Now())
	s.sections[0].items = []model.PullRequest{shared} // DirectReview: highest priority
	s.sections[2].items = []model.PullRequest{shared} // Mine: should lose the claim

	var seenIn []model.SectionKind
	for _, r := range s.Rows() {
		if r.Kind == RowItem && r.Item.PR.ID == "PR_SHARED" {
			seenIn = append(seenIn, r.Section.Kind)
		}
	}
	if !reflect.DeepEqual(seenIn, []model.SectionKind{model.SectionKindDirectReview}) {
		t.Errorf("PR_SHARED appeared in %v, want exactly [DirectReview]", seenIn)
	}
}

func TestRows_SortedByUpdatedAtDesc(t *testing.T) {
	cfg := config.Default()
	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())

	older := pr("PR_OLDER", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	newer := pr("PR_NEWER", time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	s.sections[0].items = []model.PullRequest{older, newer}

	var order []string
	for _, r := range s.Rows() {
		if r.Kind == RowItem {
			order = append(order, r.Item.PR.ID)
		}
	}
	want := []string{"PR_NEWER", "PR_OLDER"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestRows_OmitsSectionsWithNoMatchingItems(t *testing.T) {
	cfg := config.Default()
	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())

	if rows := s.Rows(); len(rows) != 0 {
		t.Errorf("Rows() = %+v, want empty", rows)
	}
}

func TestStartAutoRefresh_DispatchesOnTick(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

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

	if s.LastRefresh().IsZero() {
		t.Error("expected LastRefresh to be set after an auto-refresh tick")
	}
}

func TestFetchSection_RecoversFromPanicAndEmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	existing := pr("PR_EXISTING", time.Now())
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{existing}}, nil
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	if !containsItemID(s.Rows(), "PR_EXISTING") {
		t.Fatalf("expected the initial item to be present before the panic")
	}

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		panic("boom")
	}

	var gotError bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotError = true
		}
	})

	s.Refresh()
	runUntilIdle(t, disp) // must not crash the test process

	if !gotError {
		t.Error("expected an EventError after a recovered goroutine panic")
	}
	if s.Loading() {
		t.Error("Loading() = true after a panicking fetch was recovered, want false")
	}
	if len(s.inFlight) != 0 {
		t.Errorf("inFlight = %v, want empty after a panicking fetch was recovered", s.inFlight)
	}
	if !containsItemID(s.Rows(), "PR_EXISTING") {
		t.Error("previous items must survive a panicking fetch")
	}
}

func TestFetchViewer_RecoversFromPanicAndEmitsError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		panic("boom")
	}

	var gotError bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotError = true
		}
	})

	s.fetchViewer(context.Background())
	runUntilIdle(t, disp) // must not crash the test process

	if !gotError {
		t.Error("expected an EventError after a recovered viewer-fetch panic")
	}
	if s.viewerFetchInFlight {
		t.Error("viewerFetchInFlight = true after a panicking fetch was recovered, want false")
	}
}

func TestStartList_RetriesViewerWhenLoginUnknown(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var mu sync.Mutex
	viewerCalls := 0
	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		mu.Lock()
		viewerCalls++
		n := viewerCalls
		mu.Unlock()
		if n == 1 {
			return model.User{}, model.RateLimit{}, errors.New("transient")
		}
		return model.User{Login: "tester"}, model.RateLimit{}, nil
	}

	s.Start(context.Background())
	runUntilIdle(t, disp)
	if s.viewer.Login != "" {
		t.Fatalf("viewer.Login = %q, want empty after a failed viewer fetch", s.viewer.Login)
	}

	s.Reload()
	runUntilIdle(t, disp)

	mu.Lock()
	got := viewerCalls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("viewerCalls = %d, want 2 (Reload must retry an unresolved viewer)", got)
	}
	if s.viewer.Login != "tester" {
		t.Errorf("viewer.Login = %q, want %q after the retry succeeds", s.viewer.Login, "tester")
	}
}

func TestStartList_DoesNotDuplicateViewerFetchAlreadyInFlight(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var mu sync.Mutex
	viewerCalls := 0
	release := make(chan struct{})
	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		mu.Lock()
		viewerCalls++
		mu.Unlock()
		<-release
		return model.User{Login: "tester"}, model.RateLimit{}, nil
	}

	s.Start(context.Background()) // kicks off a (blocked) viewer fetch + LoadList(false)
	s.Refresh()                   // login still unknown; must not start a second viewer fetch

	close(release)
	runUntilIdle(t, disp)

	mu.Lock()
	got := viewerCalls
	mu.Unlock()
	if got != 1 {
		t.Errorf("viewerCalls = %d, want 1 (a fetch already in flight must not be duplicated)", got)
	}
}

func TestCacheSectionPage_SkippedUntilViewerConfirmed(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "old-user")
	s.applyCachedViewer() // seeds s.viewer.Login without confirming it

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{pr("PR_1", time.Now())}}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)

	key, err := cache.SearchKey(s.deps.Host, "old-user", s.sections[0].query+"\x00")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	if _, ok, _ := s.deps.Cache.Get(key); ok {
		t.Error("a section page must not be cached before the viewer is network-confirmed")
	}
}

func TestFetchViewer_LoginMismatchDiscardsSeededRows(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "old-user")
	oldPR := pr("PR_OLD_USER", time.Now())
	seedSectionCache(t, s, 0, "old-user", gh.SearchResult{Items: []model.PullRequest{oldPR}})

	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		return model.User{Login: "new-user"}, model.RateLimit{}, nil
	}
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, nil // the real network has nothing for new-user
	}

	s.Start(context.Background())

	// Cache-first loading happens before the viewer is confirmed, so the
	// old (wrong) user's cached row is unavoidably visible for a moment.
	if !containsItemID(s.Rows(), "PR_OLD_USER") {
		t.Fatalf("expected the seeded row to be visible before the viewer resolves")
	}

	runUntilIdle(t, disp)

	if containsItemID(s.Rows(), "PR_OLD_USER") {
		t.Error("the old user's seeded row must be discarded once the real login is confirmed to differ")
	}
	if s.viewer.Login != "new-user" {
		t.Errorf("viewer.Login = %q, want %q", s.viewer.Login, "new-user")
	}
	if !s.viewerConfirmed {
		t.Error("viewerConfirmed = false after a successful viewer fetch")
	}
}

func TestCacheErrors_LoggedOncePerStore(t *testing.T) {
	dir := t.TempDir()
	cacheStore, err := cache.New(dir)
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}

	// The cache root is made read-only so that any Put, which must create
	// a fresh subdirectory tree for a not-yet-cached key, fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var records []slog.Record
	logger := slog.New(capturingHandler{records: &records})

	gitHub := newFakeGitHub()
	gitHub.viewerFunc = func(context.Context) (model.User, model.RateLimit, error) {
		return model.User{Login: "tester"}, model.RateLimit{}, nil
	}
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{pr("PR_1", time.Now())}}, nil
	}
	disp := newFakeDispatcher()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	s := New(Deps{
		GitHub: gitHub, Cache: cacheStore, Dispatch: disp.dispatch,
		Logger: logger, Config: config.Default(), Host: "example.com",
		Now: func() time.Time { return now },
	})

	s.Start(context.Background())
	runUntilIdle(t, disp)

	s.Refresh() // a second Put failure must not log a second warning
	runUntilIdle(t, disp)

	warnCount := 0
	for _, r := range records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, "cache read/write failed") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Errorf("cache-failure warnings logged = %d, want exactly 1", warnCount)
	}
}

func TestSetFilter_RevertsToConfigStateWhenNoStateQualifier(t *testing.T) {
	cfg := config.Default() // state: open
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	s.SetFilter("state:closed")
	runUntilIdle(t, disp)
	if s.state != "closed" {
		t.Fatalf("state = %q, want %q", s.state, "closed")
	}

	s.SetFilter("author:alice") // no state: qualifier this time
	runUntilIdle(t, disp)

	if s.state != "open" {
		t.Errorf("state = %q, want it reverted to the config default %q", s.state, "open")
	}
}

func TestSetFilter_UnknownStateEmitsErrorAndIsIgnored(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var events []Event
	s.Subscribe(func(e Event) { events = append(events, e) })

	initialCalls := len(gitHub.searchCalls())
	s.SetFilter("state:bogus")
	runUntilIdle(t, disp)

	if s.state != "open" {
		t.Errorf("state = %q, want unchanged (%q)", s.state, "open")
	}
	if len(gitHub.searchCalls()) != initialCalls {
		t.Error("an unknown state: value must not trigger a reload")
	}
	if s.Filter() != "state:bogus" {
		t.Errorf("Filter() = %q, want the raw text preserved", s.Filter())
	}

	var gotErr bool
	for _, e := range events {
		if e.Kind == EventError && e.Err != nil && strings.Contains(e.Err.Error(), "unknown state") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected an EventError mentioning the unknown state")
	}
}

func TestSetFilter_StateChangeMarksSectionsStaleImmediately(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	existing := pr("PR_EXISTING", time.Now())
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{existing}}, nil
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	if row := rowForItem(s.Rows(), "PR_EXISTING"); row == nil || row.Stale {
		t.Fatalf("expected a fresh, non-stale row before the state change")
	}

	s.SetFilter("state:closed")

	if row := rowForItem(s.Rows(), "PR_EXISTING"); row == nil || !row.Stale {
		t.Error("expected the existing row to be marked Stale immediately on a state change")
	}

	runUntilIdle(t, disp)
}

// TestApplyFetchResult_RefreshChainsToMatchPreviousDepth exercises the
// revised item-23 contract: a Refresh of a section that previously had
// N > 1 pages loaded replaces page 1 outright (dropping anything no longer
// present) and automatically chains fetches for pages 2..N so the section
// re-converges on the true current set at its previous depth, instead of
// grafting old, possibly-stale tail items onto the new page 1.
func TestApplyFetchResult_RefreshChainsToMatchPreviousDepth(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{
				Items: []model.PullRequest{pr("A", time.Now()), pr("B", time.Now())}, HasNextPage: true, EndCursor: "cursor-2",
			}, nil
		case "cursor-2":
			return gh.SearchResult{
				Items: []model.PullRequest{pr("C", time.Now()), pr("D", time.Now())}, HasNextPage: false,
			}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}

	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)

	if len(s.sections[0].items) != 4 {
		t.Fatalf("items after LoadMore = %d, want 4", len(s.sections[0].items))
	}

	// The refresh's own page 1 drops B (gone from the result set) and
	// adds E; its "page 2" reflects the current reality too (C and D are
	// both gone, replaced by F), reached automatically via chaining.
	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{
				Items: []model.PullRequest{pr("A", time.Now()), pr("E", time.Now())}, HasNextPage: true, EndCursor: "cursor-2b",
			}, nil
		case "cursor-2b":
			return gh.SearchResult{Items: []model.PullRequest{pr("F", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}
	s.Refresh()
	runUntilIdle(t, disp)

	var ids []string
	for _, it := range s.sections[0].items {
		ids = append(ids, it.ID)
	}
	want := []string{"A", "E", "F"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("items after refresh = %v, want %v (B/C/D must be gone, chained to the previous 2-page depth)", ids, want)
	}
}

// TestApplyFetchResult_RefreshDropsItemsNoLongerInPageOne is the
// single-page case of the same fix: a PR that disappeared from the search
// results must not linger just because it was shown before.
func TestApplyFetchResult_RefreshDropsItemsNoLongerInPageOne(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now()), pr("B", time.Now())}, HasNextPage: false}, nil
	}
	s.LoadList(false)
	runUntilIdle(t, disp)

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: false}, nil
	}
	s.Refresh()
	runUntilIdle(t, disp)

	if containsItemID(s.Rows(), "B") {
		t.Error("a PR no longer present in the refreshed page 1 must be dropped, not preserved")
	}
	if !containsItemID(s.Rows(), "A") {
		t.Error("expected A to remain")
	}
}

// TestApplyFetchResult_RefreshChainStopsOnError verifies that a failure
// partway through the automatic page-chaining leaves whatever was
// successfully re-fetched in place and does not retry indefinitely.
func TestApplyFetchResult_RefreshChainStopsOnError(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "cursor-2"}, nil
		case "cursor-2":
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)
	if len(s.sections[0].items) != 2 {
		t.Fatalf("items after LoadMore = %d, want 2", len(s.sections[0].items))
	}

	chainErr := errors.New("page 2 fetch failed")
	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "cursor-2c"}, nil
		case "cursor-2c":
			return gh.SearchResult{}, chainErr
		default:
			return gh.SearchResult{}, nil
		}
	}

	var gotErrorEvent bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			gotErrorEvent = true
		}
	})

	s.Refresh()
	runUntilIdle(t, disp)

	if !gotErrorEvent {
		t.Error("expected an EventError from the failed chained page-2 fetch")
	}
	if len(s.sections[0].items) != 1 || s.sections[0].items[0].ID != "A" {
		t.Errorf("items = %v, want just [A] (the chain must stop, not retry indefinitely)", s.sections[0].items)
	}
}

// TestLoadMore_AppendDedupesAgainstExistingItems covers the append-path
// dedupe half of item 23: a page fetched via LoadMore that happens to
// overlap with already-held items (for example after a refresh shifted
// page boundaries) must not duplicate a row.
func TestLoadMore_AppendDedupesAgainstExistingItems(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	itemA := pr("A", time.Now())
	itemB := pr("B", time.Now())
	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{itemA}, HasNextPage: true, EndCursor: "c2"}, nil
		case "c2":
			return gh.SearchResult{Items: []model.PullRequest{itemA, itemB}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}

	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)

	count := 0
	for _, it := range s.sections[0].items {
		if it.ID == "A" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("item A appears %d times, want 1 (LoadMore must dedupe against existing items)", count)
	}
	if len(s.sections[0].items) != 2 {
		t.Errorf("items = %d, want 2 (A once, B once)", len(s.sections[0].items))
	}
}

func TestFetchSection_LateGenerationResultDoesNotCorruptNewGenerationInFlight(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)
	targetQuery := s.sections[0].query

	var mu sync.Mutex
	calls := 0
	gen1Release := make(chan struct{})
	gen2Release := make(chan struct{})

	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		if query != targetQuery {
			return gh.SearchResult{}, nil
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			<-gen1Release
			return gh.SearchResult{Items: []model.PullRequest{pr("GEN1", time.Now())}}, nil
		}
		<-gen2Release
		return gh.SearchResult{Items: []model.PullRequest{pr("GEN2", time.Now())}}, nil
	}

	s.LoadList(false) // generation 1: section[0]'s fetch blocks on gen1Release
	runUntilIdle(t, disp)

	s.LoadList(false) // generation 2: section[0]'s fetch blocks on gen2Release
	runUntilIdle(t, disp)

	close(gen1Release) // let generation 1's stale fetch return and be dropped
	runUntilIdle(t, disp)

	// A duplicate fetch attempt for the same key, while generation 2's own
	// fetch is genuinely still in flight, must still be deduped.
	s.fetchSection(s.listCtx, s.generation, 0, "")

	close(gen2Release)
	runUntilIdle(t, disp)

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Errorf("search calls for the target query = %d, want exactly 2 (the duplicate attempt must have been deduped)", got)
	}
	if containsItemID(s.Rows(), "GEN1") {
		t.Error("generation 1's late result should have been dropped")
	}
	if !containsItemID(s.Rows(), "GEN2") {
		t.Error("generation 2's result should have been applied")
	}
}

func TestLoadMore_DoesNotCachePagesBeyondPageOne(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "tester")
	s.applyCachedViewer()
	s.viewerConfirmed = true

	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		if cursor == "" {
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "cursor-2"}, nil
		}
		return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: false}, nil
	}

	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)

	pageTwoKey, err := cache.SearchKey(s.deps.Host, "tester", s.sections[0].query+"\x00cursor-2")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	if _, ok, _ := s.deps.Cache.Get(pageTwoKey); ok {
		t.Error("a page fetched via LoadMore (non-empty cursor) must not be cached")
	}

	pageOneKey, err := cache.SearchKey(s.deps.Host, "tester", s.sections[0].query+"\x00")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	if _, ok, _ := s.deps.Cache.Get(pageOneKey); !ok {
		t.Error("page 1 should still be cached")
	}
}

func TestReload_InvalidatesSearchCache(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "tester")
	s.applyCachedViewer()
	s.viewerConfirmed = true
	seedSectionCache(t, s, 0, "tester", gh.SearchResult{Items: []model.PullRequest{pr("OLD", time.Now())}})

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, errors.New("network down")
	}

	key, err := cache.SearchKey(s.deps.Host, "tester", s.sections[0].query+"\x00")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}
	if _, ok, _ := s.deps.Cache.Get(key); !ok {
		t.Fatalf("expected the seeded cache entry to exist before Reload")
	}

	s.Reload()
	runUntilIdle(t, disp)

	if _, ok, _ := s.deps.Cache.Get(key); ok {
		t.Error("Reload must invalidate the search cache even though the refetch failed")
	}
}

func TestRefresh_DoesNotInvalidateSearchCache(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	seedViewerCache(t, s, "tester")
	s.applyCachedViewer()
	s.viewerConfirmed = true
	seedSectionCache(t, s, 0, "tester", gh.SearchResult{Items: []model.PullRequest{pr("OLD", time.Now())}})

	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, errors.New("network down")
	}

	key, err := cache.SearchKey(s.deps.Host, "tester", s.sections[0].query+"\x00")
	if err != nil {
		t.Fatalf("SearchKey: %v", err)
	}

	s.Refresh()
	runUntilIdle(t, disp)

	if _, ok, _ := s.deps.Cache.Get(key); !ok {
		t.Error("Refresh must not invalidate the search cache")
	}
}

func TestStop_CancelledFetchIsSilent(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	release := make(chan struct{})
	gitHub.searchFunc = func(ctx context.Context, _, _ string) (gh.SearchResult, error) {
		<-release
		<-ctx.Done()
		return gh.SearchResult{}, ctx.Err()
	}

	s.LoadList(false)

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
		t.Error("a fetch cancelled by Stop() must not emit EventError")
	}
	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil after a cancelled fetch", s.LastError())
	}
	if s.Loading() {
		t.Error("Loading() = true after a cancelled fetch, want false")
	}
	if len(s.inFlight) != 0 {
		t.Errorf("inFlight = %v, want empty after a cancelled fetch", s.inFlight)
	}
}
