package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/logging"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// fakeGitHub is a minimal test double for store.GitHub: Viewer returns a
// fixed user, SearchPullRequests returns whatever was registered for the
// exact query text (built with gh.BuildSearchQuery so it matches what
// Store actually sends), and an empty result for anything else (including
// every LoadMore page, since these tests never scroll past one page). A
// test that needs to observe an in-flight fetch (for example the loading
// marker a section's header row should show) can arm block via SetBlock so
// SearchPullRequests waits until the test closes it.
type fakeGitHub struct {
	mu      sync.Mutex
	viewer  model.User
	results map[string]gh.SearchResult
	errs    map[string]error
	block   chan struct{}
}

func (f *fakeGitHub) Viewer(context.Context) (model.User, model.RateLimit, error) {
	return f.viewer, model.RateLimit{}, nil
}

// PullRequest is a minimal stub: no test in this package (M1a scope)
// exercises PR detail fetching yet.
func (f *fakeGitHub) PullRequest(context.Context, model.PRRef, string) (gh.DetailResult, error) {
	return gh.DetailResult{}, nil
}

func (f *fakeGitHub) SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error) {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return gh.SearchResult{}, ctx.Err()
		}
	}
	if cursor != "" {
		return gh.SearchResult{}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[query]; ok {
		return gh.SearchResult{}, err
	}
	return f.results[query], nil
}

// SetBlock arms (or, passed nil, disarms) a gate every subsequent
// SearchPullRequests call waits on before returning.
func (f *fakeGitHub) SetBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = ch
}

// SetResult changes what SearchPullRequests returns for query, clearing any
// error previously armed for it via SetError.
func (f *fakeGitHub) SetResult(query string, res gh.SearchResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		f.results = map[string]gh.SearchResult{}
	}
	f.results[query] = res
	delete(f.errs, query)
}

// SetError makes SearchPullRequests fail with err for query.
func (f *fakeGitHub) SetError(query string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[query] = err
}

func fixtureRef(number int) model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}, Number: number}
}

func fixturePR(number int, title string) model.PullRequest {
	ref := fixtureRef(number)
	return model.PullRequest{
		ID:        fmt.Sprintf("PR_fixture_%d", number),
		Ref:       ref,
		Title:     title,
		Author:    model.User{Login: "alice"},
		State:     model.PRStateOpen,
		UpdatedAt: time.Now(),
		URL:       fmt.Sprintf("https://github.com/%s/pull/%d", ref.Repo.NameWithOwner(), ref.Number),
	}
}

// newTestApp builds an App wired to a fake GitHub with two fixture pull
// requests in two different sections (direct review requests, and the
// viewer's own), running against a tcell.SimulationScreen. It blocks until
// both fixtures have loaded and registers a cleanup that stops the app and
// waits for Run to return.
func newTestApp(t *testing.T, overrides map[string]string) (*App, <-chan struct{}, *fakeGitHub) {
	t.Helper()

	km, err := keys.Merge(keys.Defaults(), overrides)
	if err != nil {
		t.Fatalf("keys.Merge: %v", err)
	}

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	mineQuery := gh.BuildSearchQuery(model.SectionKindMine, "", "open")
	fake := &fakeGitHub{
		viewer: model.User{Login: "octocat"},
		results: map[string]gh.SearchResult{
			directQuery: {Items: []model.PullRequest{fixturePR(1, "First PR")}},
			mineQuery:   {Items: []model.PullRequest{fixturePR(2, "Second PR")}},
		},
	}

	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}

	// A real logging.Setup (not just slog.DiscardHandler) so its Recorder
	// keeps error-and-above entries: tests can then check app.deps.Recent()
	// the same way the ":messages" overlay does, for anything the UI
	// itself logs (browser launcher failures, for instance).
	logger, closeLog, err := logging.Setup(logging.Options{Debug: false, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = closeLog() })
	recorder, _ := logger.Handler().(*logging.Recorder)

	cfg := config.Default()
	cfg.RefreshInterval = time.Hour // long enough to never tick during a test

	var app *App
	st := store.New(store.Deps{
		GitHub:   fake,
		Cache:    cacheStore,
		Dispatch: func(f func()) { app.Dispatch(f) },
		Logger:   logger,
		Config:   cfg,
		Host:     "github.com",
	})

	app = New(Deps{
		Store:  st,
		Config: cfg,
		Keymap: km,
		Icons:  theme.Unicode(),
		Browser: &browser.Opener{
			Env:      func(string) string { return "" },
			Fallback: func(string) error { return nil },
		},
		Logger:  logger,
		Recent:  recorder.Recent,
		Version: "test",
	})

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(100, 30)
	app.app.SetScreen(screen)

	// done is closed, never sent-on, so both the test and this cleanup can
	// safely wait on it — closing broadcasts to every receiver, whereas a
	// single buffered send would only ever satisfy the first one to read
	// it, leaving the other blocked for the full timeout below.
	done := make(chan struct{})
	go func() {
		_ = app.Run()
		close(done)
	}()
	t.Cleanup(func() {
		app.app.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("app.Run() did not return after Stop()")
		}
	})

	// Both fixtures live in different sections (DirectReview, Mine), whose
	// fetches complete on independent goroutines: waiting for only one of
	// them would let this return as soon as whichever section happens to
	// finish first, leaving the cursor on an unpredictable row depending
	// on fetch-completion order rather than the sections' priority order.
	firstKey, secondKey := fixtureRef(1).Key(), fixtureRef(2).Key()
	waitFor(t, app.app, func() bool {
		return app.rowIndex[firstKey] != nil && app.rowIndex[secondKey] != nil
	})

	// The two sections' fetches complete on independent goroutines, so
	// whichever cursor position ListView picked while only one of them
	// had loaded (it keeps the cursor on whatever was selected, by ID, as
	// rows are added — the right behaviour for a real refresh, but
	// nondeterministic here) is not a stable starting point for tests.
	// Move to the top now that both fixtures are present: by then, row
	// order is fully determined by fixed section priority, never by
	// fetch-arrival order.
	act(app.app, func() { app.listView.MoveTop() })
	return app, done, fake
}

// act runs f on the UI goroutine (via app.QueueUpdate) and waits for it to
// finish, without needing a return value.
func act(app *tview.Application, f func()) {
	app.QueueUpdate(f)
}

// query runs f on the UI goroutine (via app.QueueUpdate) and returns its
// result, so a test can read App- or widget-owned state without racing the
// event loop goroutine the way a direct, unsynchronized field read would.
func query[T any](app *tview.Application, f func() T) T {
	var v T
	app.QueueUpdate(func() { v = f() })
	return v
}

// waitFor polls cond, evaluated on the UI goroutine via app.QueueUpdate,
// until it returns true or a two-second deadline passes.
func waitFor(t *testing.T, app *tview.Application, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := make(chan bool, 1)
		app.QueueUpdate(func() { result <- cond() })
		if <-result {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition was never met before the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sendKey delivers ev exactly as tview's own event loop would: through
// app.GetInputCapture(), then (if the capture returned a non-nil event) to
// whatever primitive currently has focus. It runs on the UI goroutine via
// app.QueueUpdate and does not return until fully processed, so tests never
// race the event loop the way driving input through the SimulationScreen's
// own InjectKey/PollEvent path would (the update and event channels have no
// ordering guarantee relative to each other).
func sendKey(app *tview.Application, ev *tcell.EventKey) {
	app.QueueUpdate(func() {
		result := ev
		if capture := app.GetInputCapture(); capture != nil {
			result = capture(ev)
		}
		if result == nil {
			return
		}
		if focused := app.GetFocus(); focused != nil {
			if h := focused.InputHandler(); h != nil {
				h(result, func(p tview.Primitive) { app.SetFocus(p) })
			}
		}
	})
}

func sendRune(app *tview.Application, r rune) {
	sendKey(app, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

func sendSpecial(app *tview.Application, key tcell.Key) {
	sendKey(app, tcell.NewEventKey(key, 0, tcell.ModNone))
}

func TestAppInitialRenderShowsSectionsAndRows(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	title := query(app.app, func() string {
		id := app.listView.CurrentID()
		if info, ok := app.rowIndex[id]; ok {
			return info.item.PR.Title
		}
		return ""
	})
	if title != "First PR" {
		t.Errorf("initial cursor row's title = %q, want the direct-review fixture (First PR)", title)
	}

	rows := query(app.app, app.buildRows)
	var sawHeader, sawFirst, sawSecond bool
	for _, r := range rows {
		if !r.Selectable {
			sawHeader = true
		}
		for _, line := range r.Lines {
			for _, span := range line {
				if span.Text != "" {
					switch {
					case containsSubstring(span.Text, "First PR"):
						sawFirst = true
					case containsSubstring(span.Text, "Second PR"):
						sawSecond = true
					}
				}
			}
		}
	}
	if !sawHeader {
		t.Error("no section header row was rendered")
	}
	if !sawFirst || !sawSecond {
		t.Errorf("expected both fixture PRs to render as rows (First PR seen=%v, Second PR seen=%v)", sawFirst, sawSecond)
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestAppJKMoveCursor(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	first := query(app.app, app.listView.CurrentID)
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })
	second := query(app.app, app.listView.CurrentID)
	if second == first {
		t.Fatalf("j did not move the cursor")
	}

	sendRune(app.app, 'k')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == first })
}

func TestAppStalePreviewCallbackIsIgnoredAfterSelectionMoves(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	firstID := query(app.app, app.listView.CurrentID)
	act(app.app, func() { app.listView.MoveBy(1) })
	secondID := query(app.app, app.listView.CurrentID)
	if secondID == firstID {
		t.Fatal("MoveBy(1) did not move the cursor; the fixture needs at least two rows")
	}

	// A legitimate preview of the row the cursor is now on.
	act(app.app, func() { app.showPreview(secondID) })
	wantTitle := query(app.app, func() string {
		if app.currentPR == nil {
			return ""
		}
		return app.currentPR.item.PR.Title
	})
	if wantTitle == "" {
		t.Fatal("showPreview(secondID) did not set currentPR")
	}

	// A stale debounce callback for the FIRST row fires late — as if its
	// timer.Stop() call had raced the timer already starting, which
	// Timer.Stop's own documentation says it cannot prevent — after the
	// cursor has already moved on. It must not override the still-current
	// preview.
	act(app.app, func() { app.showPreview(firstID) })

	gotTitle := query(app.app, func() string {
		if app.currentPR == nil {
			return ""
		}
		return app.currentPR.item.PR.Title
	})
	if gotTitle != wantTitle {
		t.Fatalf("a stale preview callback overrode the current selection: currentPR.Title = %q, want %q", gotTitle, wantTitle)
	}
}

func TestAppGgAndG(t *testing.T) {
	app, _, _ := newTestApp(t, nil)
	first := query(app.app, app.listView.CurrentID)

	sendRune(app.app, 'G')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })
	last := query(app.app, app.listView.CurrentID)

	sendRune(app.app, 'g')
	sendRune(app.app, 'g')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == first })

	sendRune(app.app, 'G')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == last })
}

func TestAppFilterShowsAndNarrowsRows(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.filterInput })

	for _, r := range "second" {
		sendRune(app.app, r)
	}
	waitFor(t, app.app, func() bool { return app.deps.Store.Filter() == "second" })

	waitFor(t, app.app, func() bool {
		rows := app.buildRows()
		for _, row := range rows {
			for _, line := range row.Lines {
				for _, span := range line {
					if containsSubstring(span.Text, "First PR") {
						return false
					}
				}
			}
		}
		return true
	})
}

func TestAppEscClearsFilter(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.filterInput })
	sendRune(app.app, 'x')
	waitFor(t, app.app, func() bool { return app.deps.Store.Filter() == "x" })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool {
		return app.deps.Store.Filter() == "" && app.app.GetFocus() == app.listView
	})
}

func TestAppLoadingChangedRefreshesListRowsImmediately(t *testing.T) {
	app, _, fake := newTestApp(t, nil)

	// Block every fetch this reload starts so the test can observe the
	// list's rows while a section is still loading, before any result
	// arrives (which would fire EventListChanged on its own regardless of
	// this fix).
	block := make(chan struct{})
	fake.SetBlock(block)
	defer close(block)

	act(app.app, func() { app.reload() })

	// app.listView.Rows() reflects only what a real SetRows call last
	// applied — unlike calling app.buildRows() directly, which would
	// reflect live Store state regardless of whether the event that is
	// supposed to trigger a refresh actually did.
	loadingMarker := theme.Unicode().Loading
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if containsSubstring(span.Text, loadingMarker) {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestAppCommandLineQuits(t *testing.T) {
	app, done, _ := newTestApp(t, nil)

	sendRune(app.app, ':')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.cmdLine })

	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)

	select {
	case <-time.After(2 * time.Second):
		t.Fatal(":q did not stop the app")
	case <-done:
	}
}

func TestAppToastTimerRaceDoesNotClearANewerToast(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	var firstSeq int
	act(app.app, func() {
		app.showToast("first", theme.Error)
		firstSeq = app.toastSeq
		app.showToast("second", theme.Error)
		// Simulate the first toast's timer callback firing late, as if
		// its Stop() call had raced its own expiry (time.Timer.Stop
		// returning false does not guarantee the callback has not
		// already started) — it must not clear the newer toast.
		app.clearToastIfCurrent(firstSeq)
	})

	if got := query(app.app, func() string { return app.statusBar.toast }); got != "second" {
		t.Fatalf("a stale toast callback cleared the newer toast; status bar toast = %q, want %q", got, "second")
	}
}

func TestAppToastClearsAfterItsDuration(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	old := toastDuration
	toastDuration = 20 * time.Millisecond
	t.Cleanup(func() { toastDuration = old })

	act(app.app, func() { app.showToast("hello", theme.Error) })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "" })
}

func TestAppHelpOpensAndCloses(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestAppHelpAlsoClosesOnQuestionMark(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestAppOverlayForwardsUnboundKeysToItsTextView(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	// "j" is not a close key: the overlay's scrollable TextView must
	// receive it (so j/k/gg/G scroll the help/messages text), which means
	// the router must return the original event instead of swallowing it.
	var result *tcell.EventKey
	act(app.app, func() {
		result = app.handleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	})
	if result == nil {
		t.Fatal("handleKey swallowed an unbound key while an overlay was open; its TextView cannot scroll")
	}
}

func TestAppDetailPaneForwardsUnboundKeysForNativeScrolling(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	// "j" is not bound in ContextDetail (only ContextList/Diff/Files):
	// the router must return the original event so the focused TextView's
	// own InputHandler can scroll with it.
	var result *tcell.EventKey
	act(app.app, func() {
		result = app.handleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	})
	if result == nil {
		t.Fatal("handleKey swallowed an unbound key while a detail TextView was focused; it cannot scroll natively")
	}

	// End-to-end: pumping enough long content and "j" presses through the
	// real router should move the TextView's own scroll offset.
	act(app.app, func() {
		var b strings.Builder
		for i := range 100 {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		app.prView.SetText(b.String())
	})
	for range 5 {
		sendRune(app.app, 'j')
	}
	waitFor(t, app.app, func() bool {
		row, _ := app.prView.GetScrollOffset()
		return row > 0
	})
}

func TestAppDetailPaneBoundKeysStillDispatch(t *testing.T) {
	// A regression guard for the fix above: an actually-bound key in
	// ContextDetail (gt) must still be consumed by the router, not
	// forwarded to the TextView as if it were unbound.
	app, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
}

func TestAppCtrlWMovesFocus(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.listView })
}

func TestAppCtrlWoCollapsesList(t *testing.T) {
	app, _, _ := newTestApp(t, nil)
	if !query(app.app, func() bool { return app.listExpanded }) {
		t.Fatal("list must start expanded")
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return !app.listExpanded })
}

func TestAppCtrlWhReExpandsACollapsedList(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	// Collapse the list and move focus to the detail column, matching
	// what Ctrl-w o itself already does when the list has focus.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return !app.listExpanded && app.isDetailFocused() })

	// Ctrl-w h ("focus the previous column") must never focus a
	// zero-width list column: it should re-expand the list first.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool {
		return app.listExpanded && app.app.GetFocus() == app.listView
	})
}

func TestAppGtSwitchesTab(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
}

func TestAppRemappedKeyFromConfig(t *testing.T) {
	app, _, _ := newTestApp(t, map[string]string{"list.down": "<C-n>"})
	first := query(app.app, app.listView.CurrentID)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlN, 0, tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })

	// The old default ("j") must no longer trigger the action.
	before := query(app.app, app.listView.CurrentID)
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == before })
}

func TestAppCtrlHAmbiguityBothSwitchTabs(t *testing.T) {
	t.Run("legacy terminal reports KeyBackspace", func(t *testing.T) {
		app, _, _ := newTestApp(t, nil)
		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
		sendRune(app.app, 'l')
		waitFor(t, app.app, func() bool { return app.isDetailFocused() })
		sendRune(app.app, 'g')
		sendRune(app.app, 't') // move off "pr" first so tab_prev is observable
		waitFor(t, app.app, func() bool { return app.currentTab == "files" })

		sendKey(app.app, tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))
		waitFor(t, app.app, func() bool { return app.currentTab == "pr" })
	})

	t.Run("CSI-u terminal reports KeyCtrlH", func(t *testing.T) {
		app, _, _ := newTestApp(t, nil)
		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
		sendRune(app.app, 'l')
		waitFor(t, app.app, func() bool { return app.isDetailFocused() })
		sendRune(app.app, 'g')
		sendRune(app.app, 't')
		waitFor(t, app.app, func() bool { return app.currentTab == "files" })

		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlH, 0, tcell.ModCtrl))
		waitFor(t, app.app, func() bool { return app.currentTab == "pr" })
	})
}

func TestAppShowErrorFromABackgroundGoroutine(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.ShowError("browser: launcher exited with status 1")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ShowError did not return; it must be safe to call from any goroutine")
	}

	waitFor(t, app.app, func() bool {
		return app.statusBar.toast == "browser: launcher exited with status 1"
	})

	entries := query(app.app, func() []logging.Entry { return app.deps.Recent() })
	found := false
	for _, e := range entries {
		if containsSubstring(e.String(), "browser: launcher exited with status 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("ShowError's message did not reach :messages; entries = %+v", entries)
	}
}

func TestAppOpenBrowserFailureShowsAToast(t *testing.T) {
	app, _, _ := newTestApp(t, nil)

	act(app.app, func() {
		app.deps.Browser = &browser.Opener{
			Env: func(string) string { return "" },
			Fallback: func(string) error {
				return fmt.Errorf("no browser found")
			},
		}
	})

	sendRune(app.app, 'o')

	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "no browser found")
	})

	entries := query(app.app, func() []logging.Entry { return app.deps.Recent() })
	found := false
	for _, e := range entries {
		if containsSubstring(e.String(), "no browser found") {
			found = true
		}
	}
	if !found {
		t.Errorf("Browser.Open's error did not reach :messages; entries = %+v", entries)
	}
}

func TestAppPersistentErrorMarkerOnStatusBarAndHeaderRow(t *testing.T) {
	app, _, fake := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetError(directQuery, errors.New("network unreachable"))

	act(app.app, func() { app.reload() })

	waitFor(t, app.app, func() bool { return app.deps.Store.LastError() != nil })

	// The status bar's right side must show a persistent error marker
	// while Store.LastError() stands, not just a five-second toast.
	waitFor(t, app.app, func() bool { return app.statusBar.errorMarker != "" })

	// DirectReview keeps its previous items despite the failed refresh
	// (see internal/store's own docs), so its header row still renders —
	// and must now carry an error marker.
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if span.Style == theme.Error {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestAppSectionWarningsShowHeaderMarkerAndToastOnce(t *testing.T) {
	app, _, fake := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetResult(directQuery, gh.SearchResult{
		Items:    []model.PullRequest{fixturePR(1, "First PR")},
		Warnings: []string{"1 reviewer could not be resolved"},
	})

	act(app.app, func() { app.reload() })

	// Header row gets a theme.Warning span mentioning the warning count.
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if span.Style == theme.Warning && containsSubstring(span.Text, "1") {
						return true
					}
				}
			}
		}
		return false
	})

	// The first time a section reports warnings, it toasts one line
	// naming the section and the first warning.
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "reviewer could not be resolved")
	})
}

func TestAppMessagesOverlayListsSectionWarnings(t *testing.T) {
	app, _, fake := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetResult(directQuery, gh.SearchResult{
		Items:    []model.PullRequest{fixturePR(1, "First PR")},
		Warnings: []string{"1 reviewer could not be resolved"},
	})
	act(app.app, func() { app.reload() })
	waitFor(t, app.app, func() bool { return app.deps.Store.SectionStates()[0].Warnings != nil })

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })
	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	sendRune(app.app, ':')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.cmdLine })
	for _, r := range "messages" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "messages" })

	if !app.root.HasPage("messages") {
		t.Fatal("messages overlay page not found")
	}
	text := query(app.app, func() string {
		return app.root.GetPage("messages").(*tview.TextView).GetText(true)
	})
	if !containsSubstring(text, "Warnings") {
		t.Errorf(":messages text = %q, want it to include a \"Warnings\" heading", text)
	}
	if !containsSubstring(text, "reviewer could not be resolved") {
		t.Errorf(":messages text = %q, want it to list the section's current warning", text)
	}
}

func TestAppStatusBarHintFollowsFocus(t *testing.T) {
	app, _, _ := newTestApp(t, nil)
	listHint := query(app.app, func() string { return app.statusBar.hint })
	if !strings.Contains(listHint, "filter") {
		t.Fatalf("initial hint should describe the list pane, got %q", listHint)
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	detailHint := query(app.app, func() string { return app.statusBar.hint })
	if detailHint == listHint || !strings.Contains(detailHint, "tab") {
		t.Fatalf("hint did not follow focus to the detail pane: got %q", detailHint)
	}
}

func TestReviewerSpansBoldTheViewer(t *testing.T) {
	pr := &model.PullRequest{
		ReviewRequests: []model.Reviewer{{Login: "alice", Kind: model.ReviewerKindUser}, {Login: "bob", Kind: model.ReviewerKindUser}},
	}
	bold := map[string]bool{}
	for _, s := range reviewerSpans(pr, "bob") {
		_, _, attrs := s.Style.Decompose()
		bold[s.Text] = attrs&tcell.AttrBold != 0
	}
	if !bold["bob"] || bold["alice"] {
		t.Fatalf("expected only the viewer's login in bold, got %v", bold)
	}
}
