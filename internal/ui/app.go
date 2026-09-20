// Package ui composes gprt's tview application: the root layout (PR list,
// detail column, status bar), the single input-capture router that turns
// key events into actions via internal/ui/keys, and the glue that renders
// internal/store's state and dispatches its mutating calls. Widgets
// (internal/ui/widget) never handle input themselves, and internal/store
// never imports tview: App.Dispatch is the one bridge between them.
package ui

import (
	"context"
	"log/slog"
	"time"

	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/logging"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// Deps are the App's dependencies, supplied once at construction. Store is
// built by the caller (main) before the App exists — see App.Dispatch's
// doc comment for how the two are wired together despite that ordering.
type Deps struct {
	// Store owns gprt's PR list state and talks to GitHub; App only reads
	// it and calls its mutating methods, never touching its internals.
	Store *store.Store
	// Config is the loaded, validated user configuration.
	Config config.Config
	// Keymap is the merged (defaults + user overrides) keymap the router
	// resolves every keypress against.
	Keymap keys.Keymap
	// Icons is the selected glyph set (Unicode or Nerd Font).
	Icons theme.Icons
	// Browser opens a pull request's URL in the user's browser.
	Browser *browser.Opener
	// Logger receives structured logs for recovered panics and similar
	// operational events.
	Logger *slog.Logger
	// Recent returns the most recent error-and-above log entries, shown
	// by the ":messages" command.
	Recent func() []logging.Entry
	// Version is gprt's version string, shown by the help overlay.
	Version string
}

// App is gprt's tview application: the root layout, the router, and the
// glue between internal/store's events and the widgets that render them.
type App struct {
	deps Deps
	app  *tview.Application

	root      *tview.Pages
	statusBar *statusBarView

	listView    *widget.ListView
	filterInput *tview.InputField
	listFlex    *tview.Flex

	detailColumn *tview.Flex
	tabBar       *tabBarView
	detailPages  *tview.Pages
	prView       *tview.TextView
	filesView    *tview.TextView
	currentTab   string

	row          *tview.Flex
	listColumn   *tview.Flex
	listExpanded bool

	bottomPages *tview.Pages
	cmdLine     *tview.InputField
	cmdHistory  []string
	cmdHistIdx  int

	seq *keys.Sequencer

	overlay    string // "" | "help" | "messages"
	savedFocus tview.Primitive

	currentPR *previewState
	// rowIndex maps a ListRow.ID (a PR's PRRef.Key()) to the data needed
	// to preview it and to decide whether reaching it should trigger
	// Store.LoadMore, rebuilt every time buildRows runs.
	rowIndex map[string]*previewState
	// warnedSections tracks which sections' current warnings have already
	// been toasted, so a section's warnings are announced once, not on
	// every refresh. Cleared for a section once its warnings go away, so
	// a later, different batch of warnings is announced again.
	warnedSections map[string]bool

	previewTimer *time.Timer
	toastTimer   *time.Timer
	// toastSeq increments on every showToast call; a scheduled clear only
	// takes effect if it still matches, so a stale timer racing its own
	// Stop() call can never clear a newer toast. See clearToastIfCurrent.
	toastSeq    int
	spinnerStop chan struct{}
	// spinnerFrame is the last frame the spinner goroutine rendered, kept
	// so status-bar re-renders triggered by key events (see handleKey) do
	// not snap the spinner back to its first frame.
	spinnerFrame int
}

// previewState is the currently previewed/opened pull request, kept
// alongside the row index information needed for the "reached the last row
// of a section" LoadMore trigger.
type previewState struct {
	item          model.ListItem
	sectionIndex  int
	lastInSection bool
}

// New builds an App from deps. It constructs the underlying
// tview.Application and the whole widget tree immediately (so App.Dispatch
// is usable — and, in tests, a tcell.SimulationScreen can be installed —
// before Run is ever called), but starts no goroutines and makes no store
// calls until Run.
func New(deps Deps) *App {
	a := &App{
		deps:       deps,
		app:        tview.NewApplication(),
		currentTab: "pr",
		seq:        keys.NewSequencer(deps.Keymap),
		cmdHistIdx: -1,
	}
	a.build()
	return a
}

// Dispatch runs f on the UI goroutine, via the underlying
// tview.Application's QueueUpdateDraw. It exists so main can wire it into
// store.Deps.Dispatch despite the Store having to be built before the App:
// main declares a *App variable, builds the Store with a Dispatch closure
// that calls through that variable, then assigns the variable once New
// returns — safe because the Store makes no Dispatch call until Start runs,
// which App.Run only does after the App (and therefore the closure's
// target) already exists.
func (a *App) Dispatch(f func()) {
	a.app.QueueUpdateDraw(f)
}

// Run starts gprt's event loop: it starts the Store (loading the viewer and
// the first page of every section) and its auto-refresh ticker, sets the
// root primitive, and blocks in tview's event loop until the user quits or
// an unrecoverable error occurs. On return — however it happens — the
// Store's context is cancelled immediately; Run never waits for any
// goroutine the Store or App started (see docs/DESIGN.md's concurrency
// rule 9).
func (a *App) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.subscribeStore()
	a.deps.Store.Start(ctx)
	a.deps.Store.StartAutoRefresh(ctx, a.deps.Config.RefreshInterval)

	return a.app.SetRoot(a.root, true).Run()
}
