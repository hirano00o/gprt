package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// openHelp opens the "?" / ":help" overlay: a scrollable list of every
// effective binding (defaults merged with the user's config) for the
// contexts this milestone's UI actually uses.
func (a *App) openHelp() {
	if a.overlay != "" {
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "help"
	view := a.buildHelpView()
	a.root.AddPage("help", view, true, true)
	a.app.SetFocus(view)
}

// openMessages opens the ":messages" overlay: the ring buffer of recent
// error-and-above log entries (see internal/logging).
func (a *App) openMessages() {
	if a.overlay != "" {
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "messages"
	view := a.buildMessagesView()
	a.root.AddPage("messages", view, true, true)
	a.app.SetFocus(view)
}

// closeOverlay closes whichever overlay is open, if any, and restores
// focus to whatever had it before the overlay opened.
func (a *App) closeOverlay() {
	if a.overlay == "" {
		return
	}
	a.root.RemovePage(a.overlay)
	a.overlay = ""
	a.restoreFocus()
}

// helpContexts are the (label, Context) pairs the help overlay lists, in
// display order. Only the contexts the router actually resolves keys
// against today are shown; the rest (thread, comment, pr, composer) arrive
// with the milestones that give them a pane.
var helpContexts = []struct {
	label string
	ctx   keys.Context
}{
	{"Global", keys.ContextGlobal},
	{"List", keys.ContextList},
	{"Detail", keys.ContextDetail},
	{"Files (tree)", keys.ContextFiles},
	{"Diff", keys.ContextDiff},
}

func (a *App) buildHelpView() *tview.TextView {
	view := tview.NewTextView().SetScrollable(true)
	view.SetBorder(true).SetTitle(" gprt help (q or Esc to close) ")

	var b strings.Builder
	fmt.Fprintf(&b, "gprt %s\n\n", a.deps.Version)
	for _, hc := range helpContexts {
		bindings := a.deps.Keymap.Bindings(hc.ctx)
		if len(bindings) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", hc.label)
		for _, bd := range bindings {
			fmt.Fprintf(&b, "  %-10s %s\n", bd.Sequence, bd.Action)
		}
		b.WriteString("\n")
	}
	fmt.Fprint(&b, ": commands: q, quit, help, messages, reload\n")
	view.SetText(b.String())
	return view
}

// buildMessagesView lists the ring buffer of recent error-and-above log
// entries, followed by every section's *current* warnings (see
// gh.SearchResult.Warnings) and the current pull request's own
// DetailState().Warnings under a "Warnings" heading. Warnings are logged by
// internal/store at Warn level, which the ":messages" ring buffer does not
// keep (it only keeps Error and above) — rather than re-logging them at
// Error from here (which would duplicate the store's own logging and could
// drift out of sync with whether a warning is still current), this reads
// them fresh from Store.SectionStates/DetailState every time the overlay
// is built, which is simpler and always accurate.
func (a *App) buildMessagesView() *tview.TextView {
	view := tview.NewTextView().SetScrollable(true)
	view.SetBorder(true).SetTitle(" Messages (q or Esc to close) ")

	var b strings.Builder
	entries := a.deps.Recent()
	if len(entries) == 0 {
		b.WriteString("No messages yet.\n")
	}
	for _, e := range entries {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}

	var warned []store.SectionState
	for _, s := range a.deps.Store.SectionStates() {
		if len(s.Warnings) > 0 {
			warned = append(warned, s)
		}
	}
	detailWarnings := a.deps.Store.DetailState().Warnings
	filesWarnings := a.deps.Store.FilesState().Warnings

	if len(warned) > 0 || len(detailWarnings) > 0 || len(filesWarnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, s := range warned {
			for _, w := range s.Warnings {
				fmt.Fprintf(&b, "  %s: %s\n", s.Section.Name, w)
			}
		}
		for _, w := range detailWarnings {
			fmt.Fprintf(&b, "  Pull request: %s\n", w)
		}
		for _, w := range filesWarnings {
			fmt.Fprintf(&b, "  Files: %s\n", w)
		}
	}

	view.SetText(b.String())
	return view
}

// submitCommand runs whatever was typed into the command line, then closes
// it. Recognized commands: q/quit, help, messages, reload; anything else
// shows an error toast instead of doing nothing silently.
func (a *App) submitCommand() {
	text := strings.TrimSpace(a.cmdLine.GetText())
	a.closeCommandLine()
	if text == "" {
		return
	}

	a.cmdHistory = append(a.cmdHistory, text)
	a.cmdHistIdx = len(a.cmdHistory)

	switch text {
	case "q", "quit":
		a.quitWithConfirmIfMutating()
	case "help":
		a.openHelp()
	case "messages":
		a.openMessages()
	case "reload":
		a.reload()
	default:
		a.showToast("unknown command: "+text, theme.Error)
	}
}

// historyPrev recalls the previous command line entry (Up).
func (a *App) historyPrev() {
	if len(a.cmdHistory) == 0 || a.cmdHistIdx == 0 {
		return
	}
	a.cmdHistIdx--
	a.cmdLine.SetText(a.cmdHistory[a.cmdHistIdx])
}

// historyNext recalls the next command line entry, or clears the line once
// history is exhausted (Down).
func (a *App) historyNext() {
	if a.cmdHistIdx >= len(a.cmdHistory)-1 {
		a.cmdHistIdx = len(a.cmdHistory)
		a.cmdLine.SetText("")
		return
	}
	a.cmdHistIdx++
	a.cmdLine.SetText(a.cmdHistory[a.cmdHistIdx])
}
