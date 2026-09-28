package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// helpTitleDefault is the help overlay's title with no search active;
// submitHelpSearch overwrites it with match feedback (the overlay covers
// the status bar, so it has nowhere else to show a toast) and an empty
// search submission restores it.
const helpTitleDefault = " gprt help (q or Esc to close) "

// openHelp opens the "?" / ":help" overlay: a scrollable list of every
// effective binding (defaults merged with the user's config) for the
// contexts this milestone's UI actually uses, plus a hidden "/" search row
// (see openHelpSearch) toggled the same way the list's own filter is.
func (a *App) openHelp() {
	if a.overlay != "" {
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "help"

	// No wrapping: buildHelpText's returned line number counts raw lines,
	// which ScrollTo only lands on exactly while one raw line is one row.
	a.helpView = tview.NewTextView().SetScrollable(true).SetDynamicColors(true).SetWrap(false)
	a.helpView.SetBorder(true).SetTitle(helpTitleDefault)
	text, _, _, _ := a.buildHelpText(nil, 0)
	a.helpView.SetText(text)
	a.helpSearchRE = nil
	a.helpMatchCount, a.helpMatchIdx = 0, 0

	a.helpFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	a.helpFlex.AddItem(a.helpView, 0, 1, true)
	a.helpFlex.AddItem(a.helpSearchInput, 0, 0, false) // hidden until "/" (openHelpSearch)

	a.root.AddPage("help", a.helpFlex, true, true)
	a.app.SetFocus(a.helpView)
}

// openHelpSearch shows the help overlay's own "/" search row and focuses
// it. Unlike App.savedFocus (the outer overlay's own entry/exit point),
// this only moves focus within the still-open overlay — see
// closeHelpSearch's own doc comment for why restoreFocus() must not be
// reused here.
func (a *App) openHelpSearch() {
	a.helpFlex.ResizeItem(a.helpSearchInput, 1, 0)
	a.helpSearchInput.SetText("")
	a.app.SetFocus(a.helpSearchInput)
}

// closeHelpSearch hides the help overlay's search row and returns focus to
// the help TextView — not restoreFocus(): App.savedFocus is single-shot
// and already holds whatever pane had focus before the help overlay itself
// opened (see restoreFocus's own doc comment); calling it here would
// consume that early, so the "q"/Esc that later actually closes the help
// overlay would fall back to the generic list-focused default instead of
// the real pane the overlay was opened from.
func (a *App) closeHelpSearch() {
	a.helpFlex.ResizeItem(a.helpSearchInput, 0, 0)
	a.app.SetFocus(a.helpView)
}

// submitHelpSearch runs the pattern currently typed into helpSearchInput
// (Enter): recompiles the help text with its first match marked and
// scrolls to it (see buildHelpText's own doc comment for why a style tag
// rather than a tview region/Highlight()). Feedback (match count, "no
// match", or an invalid pattern) is shown in the help TextView's own
// title, since the overlay covers the status bar.
func (a *App) submitHelpSearch() {
	pattern := a.helpSearchInput.GetText()
	a.closeHelpSearch()

	if pattern == "" {
		text, _, _, _ := a.buildHelpText(nil, 0)
		a.helpView.SetText(text)
		a.helpSearchRE = nil
		a.helpMatchCount, a.helpMatchIdx = 0, 0
		a.helpView.SetTitle(helpTitleDefault)
		return
	}

	re, err := regexp.Compile(smartcase(pattern))
	if err != nil {
		a.helpView.SetTitle(fmt.Sprintf(" gprt help — invalid pattern: %s ", err.Error()))
		return
	}

	text, n, line, endCol := a.buildHelpText(re, 0)
	a.helpView.SetText(text)
	a.helpSearchRE = re
	a.helpMatchCount = n
	if n == 0 {
		a.helpMatchIdx = 0
		a.helpView.SetTitle(fmt.Sprintf(" gprt help — no match: %s ", pattern))
		return
	}
	a.helpMatchIdx = 0
	a.scrollHelpTo(line, endCol)
	a.helpView.SetTitle(fmt.Sprintf(" gprt help — %d matches (n/N) ", n))
}

// helpSearchStep cycles the help overlay's search to the next (dir > 0) or
// previous (dir < 0) match, wrapping around, rebuilding the text so it
// marks (and scrolling to) the newly current one. A no-op with no active
// search.
func (a *App) helpSearchStep(dir int) {
	if a.helpMatchCount == 0 {
		return
	}
	a.helpMatchIdx = (a.helpMatchIdx + dir + a.helpMatchCount) % a.helpMatchCount
	text, _, line, endCol := a.buildHelpText(a.helpSearchRE, a.helpMatchIdx)
	a.helpView.SetText(text)
	a.scrollHelpTo(line, endCol)
}

// scrollHelpTo scrolls the help view so row line is at the top and a match
// ending at column endCol is inside the view horizontally (the view does
// not wrap, so a match far right on a long line is otherwise off-screen on
// a narrow terminal).
func (a *App) scrollHelpTo(line, endCol int) {
	_, _, width, _ := a.helpView.GetInnerRect()
	a.helpView.ScrollTo(line, max(0, endCol-width))
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

// helpCommandsLine lists every ":" command submitCommand recognizes; kept
// in sync with its switch by hand (a mismatch only ever makes the help
// text wrong, never the command itself unusable, so a generated list is
// not worth the extra indirection for one line).
const helpCommandsLine = ": commands: q, quit, help, messages, reload, close, reopen, merge"

// buildHelpText renders the help overlay's full text: gprt's version, then
// every effective binding (defaults merged with the user's config) for
// each of helpContexts, then helpCommandsLine. Every line is passed
// through tview.Escape: helpView enables dynamic colours
// (SetDynamicColors(true), for the style tag below), which would
// otherwise misparse a literal "[" in a binding sequence like "[c"/"]f" as
// the start of a colour tag.
//
// re == nil renders plain text with no match marked (n, line and endCol
// are all 0). re != nil counts every regexp match across the whole text (n) and
// marks the current-th one (0-based, wrapping is the caller's job) with
// "[::r]"/"[::-]" (reverse video) rather than a tview region tag: an
// earlier version wrapped every match in a ["m<N>"]...[""] region and drove
// TextView.Highlight("mN")/ScrollToHighlight to jump to it, but calling
// Highlight() outside of Draw lazily builds tview's line index only as far
// as the highlighted region — for a match near the top of a buffer taller
// than the view, ScrollToHighlight's own (negative, pre-clamp) scroll
// offset then made Draw's "index has enough lines" step under-build it,
// leaving the view's lower rows showing stale content for a frame (see
// TestAppHelpSearchFillsViewportOnFirstDraw). Marking only the current
// match and returning its own line number (0-based) and end column (for
// the caller's own scrollHelpTo) sidesteps that lazy index entirely. Each line is built
// piecewise (escaped prefix, then an escaped match inside the style tags,
// ...) rather than searching the already-escaped line, since tview.Escape
// can change a line's length and would otherwise invalidate the match's
// own byte offsets.
func (a *App) buildHelpText(re *regexp.Regexp, current int) (text string, n, line, endCol int) {
	var raw []string
	raw = append(raw, fmt.Sprintf("gprt %s", a.deps.Version), "")
	for _, hc := range helpContexts {
		bindings := a.deps.Keymap.Bindings(hc.ctx)
		if len(bindings) == 0 {
			continue
		}
		raw = append(raw, hc.label+":")
		for _, bd := range bindings {
			raw = append(raw, fmt.Sprintf("  %-10s %s", bd.Sequence, bd.Action))
		}
		raw = append(raw, "")
	}
	raw = append(raw, helpCommandsLine)

	var b strings.Builder
	for lineNo, rawLine := range raw {
		if re == nil {
			b.WriteString(tview.Escape(rawLine))
			b.WriteByte('\n')
			continue
		}
		last := 0
		for _, r := range re.FindAllStringIndex(rawLine, -1) {
			b.WriteString(tview.Escape(rawLine[last:r[0]]))
			if n == current {
				line = lineNo
				endCol = utf8.RuneCountInString(rawLine[:r[1]])
				b.WriteString("[::r]")
				b.WriteString(tview.Escape(rawLine[r[0]:r[1]]))
				b.WriteString("[::-]")
			} else {
				b.WriteString(tview.Escape(rawLine[r[0]:r[1]]))
			}
			n++
			last = r[1]
		}
		b.WriteString(tview.Escape(rawLine[last:]))
		b.WriteByte('\n')
	}
	return b.String(), n, line, endCol
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
// it. Recognized commands: q/quit, help, messages, reload, close, reopen,
// merge (see prcommands.go/mergedialog.go); anything else shows an error
// toast instead of doing nothing silently.
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
	case "close":
		a.cmdClose()
	case "reopen":
		a.cmdReopen()
	case "merge":
		a.cmdMerge()
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
