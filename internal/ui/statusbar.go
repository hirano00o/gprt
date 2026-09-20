package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// spinnerFrames cycles while the Store has a fetch in flight. A fixed,
// self-contained animation independent of the configured icon set (Unicode
// vs. Nerd Font), since it is a status-bar detail rather than a per-item
// glyph.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// statusBarView is gprt's one-line status bar: a left-aligned hint (or an
// error toast, while one is active) and a right-aligned summary (spinner,
// rate limit, last refresh, viewer login).
type statusBarView struct {
	*tview.Box

	hint  string
	right string
	// errorMarker, when non-empty, is drawn in theme.Error immediately
	// before the muted right-hand summary — a persistent indicator, shown
	// for as long as Store.LastError() stands, distinct from a toast
	// (which always clears itself after toastDuration regardless of
	// whether the underlying problem is still there).
	errorMarker string

	toast      string
	toastStyle tcell.Style
}

func newStatusBarView() *statusBarView {
	return &statusBarView{Box: tview.NewBox()}
}

// SetHint sets the left-aligned key-hint text.
func (sb *statusBarView) SetHint(hint string) { sb.hint = hint }

// SetRight sets the right-aligned summary text (spinner, rate limit, last
// refresh, viewer login).
func (sb *statusBarView) SetRight(right string) { sb.right = right }

// SetErrorMarker sets (or, given "", clears) the persistent error marker
// drawn in theme.Error before the right-hand summary.
func (sb *statusBarView) SetErrorMarker(marker string) { sb.errorMarker = marker }

// SetToast replaces the left side with message, styled style, until
// ClearToast is called (App.showToast schedules that after five seconds).
func (sb *statusBarView) SetToast(message string, style tcell.Style) {
	sb.toast = message
	sb.toastStyle = style
}

// ClearToast removes any toast, reverting the left side to the hint text.
func (sb *statusBarView) ClearToast() { sb.toast = "" }

// Draw renders the status bar.
func (sb *statusBarView) Draw(screen tcell.Screen) {
	sb.DrawForSubclass(screen, sb)
	x, y, width, height := sb.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	left := []widget.Span{{Text: sb.hint, Style: theme.Base}}
	if sb.toast != "" {
		left = []widget.Span{{Text: sb.toast, Style: sb.toastStyle}}
	}
	widget.DrawSpans(screen, x, y, width, left)

	var right []widget.Span
	if sb.errorMarker != "" {
		right = append(right, widget.Span{Text: sb.errorMarker + "  ", Style: theme.Error})
	}
	if sb.right != "" {
		right = append(right, widget.Span{Text: sb.right, Style: theme.Muted})
	}
	if len(right) == 0 {
		return
	}
	rightWidth := widget.SpanWidth(right)
	if rightWidth > width {
		return
	}
	widget.DrawSpans(screen, x+width-rightWidth, y, rightWidth, right)
}

// hintForFocus returns the left-hand key hint text for whatever pane
// currently has focus.
func (a *App) hintForFocus() string {
	// Checked before the switch, not as one of its cases: GetFocus()
	// never equals a.composerEditor itself — see Editor.HasFocus's own
	// doc comment.
	if a.composerEditor != nil && a.composerEditor.HasFocus() {
		return "Ctrl-w j/k switch focus  Ctrl-w h/l list/detail  Ctrl-c quit"
	}
	switch a.app.GetFocus() {
	case a.listView:
		return "j/k move  gg/G top/bottom  / filter  Enter open  o browser  R reload  ? help  q quit"
	case a.prView:
		return "j/k move  gg/G top/bottom  c comment  e/d edit/delete own  o browser  gt/Ctrl-l next tab  gT/Ctrl-h prev tab  Ctrl-w h back  ? help  q quit"
	case a.treeView:
		return "j/k move  h/l collapse/expand  Enter/l open  Ctrl-w t hide tree  Ctrl-w l diff  gt/Ctrl-l next tab  ? help  q quit"
	case a.diffView:
		if a.treeExpanded {
			return "j/k move  V select  za fold  ]c/[c thread  ]f/[f file  zh/zl scroll  o browser  Ctrl-w h tree  Ctrl-w t hide tree  gt/Ctrl-l next tab  ? help  q quit"
		}
		return "j/k move  V select  za fold  ]c/[c thread  ]f/[f file  zh/zl scroll  o browser  Ctrl-w t show tree  Ctrl-w h list  gt/Ctrl-l next tab  ? help  q quit"
	case a.filterInput:
		return "type to filter  Enter keep  Esc clear"
	case a.cmdLine:
		return "type a command  Enter run  Esc cancel"
	default:
		return "? help  q quit"
	}
}

// renderStatusBar recomputes and applies the status bar's text from the
// App's current state. It never redraws by itself: callers already run
// either inside tview's own post-input redraw or inside a
// QueueUpdateDraw callback.
func (a *App) renderStatusBar(spinnerFrame int) {
	a.statusBar.SetHint(a.hintForFocus())

	st := a.deps.Store
	if err := st.LastError(); err != nil {
		a.statusBar.SetErrorMarker("! " + err.Error())
	} else {
		a.statusBar.SetErrorMarker("")
	}

	var right string
	if st.Loading() {
		right += string(spinnerFrames[spinnerFrame%len(spinnerFrames)]) + "  "
	}
	if a.currentTab == "files" {
		right += a.filesStatusSummary()
	}
	if n := a.draftCount(); n > 0 {
		right += fmt.Sprintf("✎ %d  ", n)
	}
	if rl := st.RateLimit(); rl.Known {
		right += fmt.Sprintf("rate %d  ", rl.Remaining)
	}
	if lr := st.LastRefresh(); !lr.IsZero() {
		right += "refreshed " + relativeTime(lr) + "  "
	}
	if login := st.Viewer().Login; login != "" {
		right += "@" + login
	}
	a.statusBar.SetRight(right)
}

// relativeTime renders t as a short "Ns ago" / "Nm ago" / "Nh ago" /
// "Nd ago" / "Nmo ago" / "Ny ago" string relative to time.Now(), matching
// the compact style of the rest of the status bar (months and years are
// rough 30-/365-day approximations, not calendar-aware — good enough for
// a "how long ago" reading, which is all this is for; the PR tab's
// "opened"/"updated" lines are what actually need the day/month/year
// buckets, since a pull request can be arbitrarily old, unlike the status
// bar's own "last refresh"). A zero t (never set — for example a PENDING
// review's SubmittedAt, which the timeline should not normally carry but a
// defensive caller may still hand in) has no reasonable relative time to
// show, so it returns "" rather than the enormous, nonsensical duration
// time.Since would otherwise compute against Go's zero time. A t in the
// future (clock skew, or a rounding edge right around "now") reads as
// "just now" rather than a negative duration.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	const day = 24 * time.Hour
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*day:
		return fmt.Sprintf("%dd ago", int(d/day))
	case d < 365*day:
		return fmt.Sprintf("%dmo ago", int(d/(30*day)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*day)))
	}
}

// withRelativeTime appends " " + relativeTime(t) to prefix, or returns
// prefix unchanged when t is zero (see relativeTime's doc comment) — used
// by the PR tab's header/comment/review/event lines so a missing
// timestamp (a PENDING review's SubmittedAt, for instance) never leaves a
// dangling trailing space, let alone relativeTime's own zero-time
// nonsense.
func withRelativeTime(prefix string, t time.Time) string {
	rel := relativeTime(t)
	if rel == "" {
		return prefix
	}
	return prefix + " " + rel
}

// onLoadingChanged starts or stops the spinner ticker to match the Store's
// current Loading state. The ticker goroutine's only action is to dispatch
// a redraw — it never touches Store state itself, per docs/DESIGN.md's
// concurrency rules (a background goroutine may not read Store fields
// directly; only code already running on the UI goroutine, such as this
// method, may call Store methods like Loading()).
func (a *App) onLoadingChanged() {
	loading := a.deps.Store.Loading()
	switch {
	case loading && a.spinnerStop == nil:
		stop := make(chan struct{})
		a.spinnerStop = stop
		go a.runSpinner(stop)
	case !loading && a.spinnerStop != nil:
		close(a.spinnerStop)
		a.spinnerStop = nil
		a.renderStatusBar(0)
	}
}

func (a *App) runSpinner(stop chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			a.deps.Logger.Error("recovered panic in spinner goroutine", "panic", r)
		}
	}()
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			frame++
			a.app.QueueUpdateDraw(func() {
				a.spinnerFrame = frame
				a.renderStatusBar(frame)
			})
		}
	}
}

// toastDuration is how long a toast stays visible before clearing itself.
// A var, not a const, so tests can shrink it rather than waiting for real
// time to pass.
var toastDuration = 5 * time.Second

// ShowError displays message as an error toast and logs it at Error level
// so it also lands in the ":messages" ring buffer (see internal/logging).
// Safe to call from any goroutine — for example browser.Opener's OnExit
// callback, which runs on a background goroutine when a launched browser
// process later exits non-zero — since it dispatches to the UI goroutine
// via QueueUpdateDraw rather than touching App/widget state directly. Do
// not call it from a key handler or any other code already running on the
// UI goroutine: nesting QueueUpdateDraw inside the event loop deadlocks
// (see showErrorToast for that case).
func (a *App) ShowError(message string) {
	a.app.QueueUpdateDraw(func() {
		a.showErrorToast(message)
	})
}

// showErrorToast shows message as a theme.Error toast and logs it at Error
// level (so it also lands in ":messages"), for use from code that already
// runs on the UI goroutine — for instance a key handler. It must never be
// called from any other goroutine, since it touches App/widget state with
// no synchronization of its own; see ShowError for that case.
func (a *App) showErrorToast(message string) {
	a.deps.Logger.Error(message)
	a.showToast(message, theme.Error)
}

// showToast displays message in the status bar, styled style, for
// toastDuration. A new toast replaces any toast already showing and
// restarts the timer.
func (a *App) showToast(message string, style tcell.Style) {
	a.statusBar.SetToast(message, style)

	a.toastSeq++
	seq := a.toastSeq
	if a.toastTimer != nil {
		a.toastTimer.Stop()
	}
	a.toastTimer = time.AfterFunc(toastDuration, func() {
		a.app.QueueUpdateDraw(func() {
			a.clearToastIfCurrent(seq)
		})
	})
}

// clearToastIfCurrent clears the status bar's toast only if seq is still
// the most recently shown toast's sequence number. time.Timer.Stop
// returning false does not guarantee its callback has not already started
// running, so showToast's Stop call alone cannot prevent an old timer from
// clearing a toast shown after it — this sequence check is what actually
// prevents that race.
func (a *App) clearToastIfCurrent(seq int) {
	if a.toastSeq == seq {
		a.statusBar.ClearToast()
	}
}
