package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// fakeDispatcher is a channel-driven fake event loop for tests. Unlike a
// fake that calls f() synchronously (forbidden: it would hide deadlocks a
// real app.QueueUpdateDraw could hit), goroutines started by the Store
// enqueue onto ch and the test drains it explicitly via runUntilIdle,
// mirroring how the real UI goroutine only ever runs dispatched callbacks
// one at a time.
type fakeDispatcher struct {
	ch chan func()
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{ch: make(chan func(), 256)}
}

func (d *fakeDispatcher) dispatch(f func()) {
	d.ch <- f
}

// runUntilIdle drains d, running every dispatched function on the calling
// (test) goroutine, until no further function arrives within idleWindow.
func runUntilIdle(t *testing.T, d *fakeDispatcher) {
	t.Helper()
	const idleWindow = 100 * time.Millisecond
	timer := time.NewTimer(idleWindow)
	defer timer.Stop()
	for {
		select {
		case f := <-d.ch:
			if !timer.Stop() {
				<-timer.C
			}
			f()
			timer.Reset(idleWindow)
		case <-timer.C:
			return
		}
	}
}

// searchCall records one SearchPullRequests invocation for assertions.
type searchCall struct {
	query  string
	cursor string
}

// detailCall records one PullRequest invocation for assertions.
type detailCall struct {
	ref         model.PRRef
	viewerLogin string
}

// filesCall records one ChangedFiles invocation for assertions.
type filesCall struct {
	ref  model.PRRef
	page int
	etag string
}

// fakeGitHub is a test double for the GitHub interface. viewerFunc,
// searchFunc, detailFunc, and filesFunc default to returning zero values
// with no error; tests override any of them to control timing and results.
type fakeGitHub struct {
	mu sync.Mutex

	viewerFunc func(ctx context.Context) (model.User, model.RateLimit, error)
	searchFunc func(ctx context.Context, query, cursor string) (gh.SearchResult, error)
	detailFunc func(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error)
	filesFunc  func(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error)

	calls       []searchCall
	detailCalls []detailCall
	filesCalls  []filesCall
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		viewerFunc: func(context.Context) (model.User, model.RateLimit, error) {
			return model.User{}, model.RateLimit{}, nil
		},
		searchFunc: func(context.Context, string, string) (gh.SearchResult, error) {
			return gh.SearchResult{}, nil
		},
		detailFunc: func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
			return gh.DetailResult{}, nil
		},
		filesFunc: func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
			return gh.FilesResult{}, nil
		},
	}
}

func (f *fakeGitHub) Viewer(ctx context.Context) (model.User, model.RateLimit, error) {
	// The func value is copied out while holding the lock, then called
	// after releasing it: fn can itself block for an arbitrary time (some
	// tests deliberately do this to control fetch ordering), and holding
	// the lock across that call would deadlock a test goroutine trying to
	// reassign viewerFunc/searchFunc/detailFunc in the meantime.
	f.mu.Lock()
	fn := f.viewerFunc
	f.mu.Unlock()
	return fn(ctx)
}

func (f *fakeGitHub) SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, searchCall{query: query, cursor: cursor})
	fn := f.searchFunc
	f.mu.Unlock()
	return fn(ctx, query, cursor)
}

func (f *fakeGitHub) PullRequest(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error) {
	f.mu.Lock()
	f.detailCalls = append(f.detailCalls, detailCall{ref: ref, viewerLogin: viewerLogin})
	fn := f.detailFunc
	f.mu.Unlock()
	return fn(ctx, ref, viewerLogin)
}

func (f *fakeGitHub) ChangedFiles(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error) {
	f.mu.Lock()
	f.filesCalls = append(f.filesCalls, filesCall{ref: ref, page: page, etag: etag})
	fn := f.filesFunc
	f.mu.Unlock()
	return fn(ctx, ref, page, etag)
}

func (f *fakeGitHub) searchCalls() []searchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]searchCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeGitHub) detailCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.detailCalls)
}

// setSearchFunc reassigns searchFunc under the lock. Most tests reassign
// the field directly (safe in practice: they fully drain via
// runUntilIdle, which gives any goroutine spawned as a side effect of the
// drained callbacks time to have already read the old value, before
// reassigning); this is for the rare test that reassigns searchFunc while
// a fetch goroutine spawned moments earlier (as a side effect of the very
// dispatch callback runUntilIdle just ran) may not have started executing
// yet, which a plain field write would race with the read in
// SearchPullRequests.
func (f *fakeGitHub) setSearchFunc(fn func(ctx context.Context, query, cursor string) (gh.SearchResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchFunc = fn
}

// setDetailFunc reassigns detailFunc under the lock, for the same reason
// setSearchFunc does.
func (f *fakeGitHub) setDetailFunc(fn func(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detailFunc = fn
}

// setFilesFunc reassigns filesFunc under the lock, for the same reason
// setSearchFunc does.
func (f *fakeGitHub) setFilesFunc(fn func(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filesFunc = fn
}

// filesCallsSnapshot returns a copy of every ChangedFiles call recorded so
// far, for assertions.
func (f *fakeGitHub) filesCallsSnapshot() []filesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]filesCall, len(f.filesCalls))
	copy(out, f.filesCalls)
	return out
}

// newTestStore builds a Store wired to a fakeGitHub and fakeDispatcher over
// an in-memory-backed cache.Store rooted at t.TempDir(), along with a fixed
// Now so tests can assert exact timestamps.
func newTestStore(t *testing.T, cfg config.Config, gitHub *fakeGitHub, disp *fakeDispatcher) *Store {
	t.Helper()
	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	return New(Deps{
		GitHub:   gitHub,
		Cache:    cacheStore,
		Dispatch: disp.dispatch,
		Config:   cfg,
		Host:     "example.com",
		Now:      func() time.Time { return now },
	})
}

func TestSections_BuiltinsThenCustom(t *testing.T) {
	cfg := config.Default()
	cfg.List.Sections = []config.Section{{Name: "Backend", Query: "org:acme label:backend"}}

	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	sections := s.Sections()

	wantKinds := []model.SectionKind{
		model.SectionKindDirectReview,
		model.SectionKindTeamReview,
		model.SectionKindMine,
		model.SectionKindInvolved,
		model.SectionKindCustom,
	}
	if len(sections) != len(wantKinds) {
		t.Fatalf("Sections() has %d entries, want %d", len(sections), len(wantKinds))
	}
	for i, want := range wantKinds {
		if sections[i].Kind != want {
			t.Errorf("Sections()[%d].Kind = %v, want %v", i, sections[i].Kind, want)
		}
	}
	last := sections[len(sections)-1]
	if last.Name != "Backend" {
		t.Errorf("custom section Name = %q, want %q", last.Name, "Backend")
	}
	if last.Query != gh.BuildSearchQuery(model.SectionKindCustom, "org:acme label:backend", "open") {
		t.Errorf("custom section Query = %q, want the built query", last.Query)
	}
}

func TestSections_QueriesUseConfiguredState(t *testing.T) {
	cfg := config.Default()
	cfg.List.State = "closed"

	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	sections := s.Sections()

	want := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "closed")
	if sections[0].Query != want {
		t.Errorf("Sections()[0].Query = %q, want %q", sections[0].Query, want)
	}
}

func TestSetFilter_StateTriggersReloadWithNewQuery(t *testing.T) {
	cfg := config.Default() // state: open

	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var mu sync.Mutex
	var queriesSeen []string
	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		mu.Lock()
		queriesSeen = append(queriesSeen, query)
		mu.Unlock()
		return gh.SearchResult{}, nil
	}

	s.SetFilter("state:closed")
	runUntilIdle(t, disp)

	wantQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "closed")
	if s.Sections()[0].Query != wantQuery {
		t.Errorf("Sections()[0].Query = %q, want %q", s.Sections()[0].Query, wantQuery)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawNewQuery bool
	for _, q := range queriesSeen {
		if q == wantQuery {
			sawNewQuery = true
		}
	}
	if !sawNewQuery {
		t.Errorf("SearchPullRequests was never called with the reloaded query %q; calls = %v", wantQuery, queriesSeen)
	}
	if s.Filter() != "state:closed" {
		t.Errorf("Filter() = %q, want %q", s.Filter(), "state:closed")
	}
}

func TestSetFilter_NonStateQualifierDoesNotReload(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	s.SetFilter("author:alice")
	runUntilIdle(t, disp)

	if len(gitHub.searchCalls()) != 0 {
		t.Errorf("SetFilter with no state: qualifier must not trigger a reload; calls = %v", gitHub.searchCalls())
	}
}
