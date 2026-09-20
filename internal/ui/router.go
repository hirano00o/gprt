package ui

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// handleKey is the App's single app.SetInputCapture function: every key
// event is normalized and routed here before any primitive sees it.
// Ctrl-C always quits immediately, regardless of what has focus (gprt has
// no in-flight mutations yet to warrant the confirmation docs/DESIGN.md's
// concurrency rule 9 describes; that confirmation is added once M3a's
// mutation queue exists). Everywhere else, a focused text-entry field
// (filter or command line) gets everything except Esc/Enter/history keys
// forwarded unchanged; an open overlay only responds to q/Esc; otherwise
// every key feeds the Sequencer and resolved actions dispatch.
func (a *App) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	normalized := keys.Normalize(ev)
	if containsCtrlC(normalized) {
		a.quit()
		return nil
	}
	// Every focus, tab, filter, command-line and overlay change originates
	// from a key event, so re-rendering the status bar once per event is
	// what keeps its hint text in step with the focused pane.
	defer a.renderStatusBar(a.spinnerFrame)

	switch a.app.GetFocus() {
	case a.filterInput:
		return a.routeFilterKey(ev, normalized)
	case a.cmdLine:
		return a.routeCommandKey(ev, normalized)
	}

	if a.overlay != "" {
		return a.routeOverlayKey(ev, normalized)
	}

	ctxs := a.currentContexts()
	anyConsumed := false
	for _, k := range normalized {
		res := a.seq.Feed(k, ctxs)
		if res.Consumed {
			anyConsumed = true
			if res.Action != "" {
				a.dispatch(res.Action, res.Count)
			}
		}
	}
	if !anyConsumed {
		// No context bound (or is mid-sequence toward) this key: let it
		// fall through to whatever has focus. For the PR/Files TextViews
		// this is what makes their own native scrolling (j/k/g/G,
		// PgUp/PgDn, ...) work; for ListView, which does not act on
		// forwarded keys at all, this is harmless.
		return ev
	}
	return nil
}

func containsCtrlC(normalized []keys.Key) bool {
	for _, k := range normalized {
		if k.Kind == keys.KindRune && k.Rune == 'c' && k.Mod == tcell.ModCtrl {
			return true
		}
	}
	return false
}

func isEscKey(k keys.Key) bool {
	return k.Kind == keys.KindSpecial && k.Special == tcell.KeyEsc
}

func isEnterKey(k keys.Key) bool {
	return k.Kind == keys.KindSpecial && k.Special == tcell.KeyEnter
}

// routeFilterKey handles the filter InputField: Esc clears and closes it,
// Enter keeps the typed filter and closes it, everything else is left to
// the InputField's own InputHandler.
func (a *App) routeFilterKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k):
			a.closeFilter(false)
			return nil
		case isEnterKey(k):
			a.closeFilter(true)
			return nil
		}
	}
	return ev
}

// routeCommandKey handles the ":" command InputField: Esc cancels, Enter
// submits, Up/Down cycle through history; everything else is left to the
// InputField's own InputHandler.
func (a *App) routeCommandKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k):
			a.closeCommandLine()
			return nil
		case isEnterKey(k):
			a.submitCommand()
			return nil
		case k.Kind == keys.KindSpecial && k.Special == tcell.KeyUp:
			a.historyPrev()
			return nil
		case k.Kind == keys.KindSpecial && k.Special == tcell.KeyDown:
			a.historyNext()
			return nil
		}
	}
	return ev
}

// routeOverlayKey handles a help/messages overlay: q or Esc close it
// (pressing "?" again also closes the help overlay specifically, toggling
// it like the key that opened it); every other key is returned unchanged
// so the overlay's own scrollable TextView can handle it (j/k/gg/G,
// PgUp/PgDn, ...).
func (a *App) routeOverlayKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		isRune := k.Kind == keys.KindRune && k.Mod == tcell.ModNone
		closes := isEscKey(k) ||
			(isRune && k.Rune == 'q') ||
			(a.overlay == "help" && isRune && k.Rune == '?')
		if closes {
			a.closeOverlay()
			return nil
		}
	}
	return ev
}

// currentContexts returns the context list Sequencer.Feed resolves the
// current keypress against: whatever pane has focus, falling back to
// ContextGlobal.
func (a *App) currentContexts() []keys.Context {
	switch a.app.GetFocus() {
	case a.listView:
		return []keys.Context{keys.ContextList, keys.ContextGlobal}
	case a.prView, a.filesView:
		return []keys.Context{keys.ContextDetail, keys.ContextGlobal}
	default:
		return []keys.Context{keys.ContextGlobal}
	}
}

// dispatch runs the effect of a resolved Action. Actions not yet
// implemented in M1a (files/diff/thread/comment/pr actions, list.new_pr)
// are silently ignored rather than surfacing a toast for every
// exploratory keypress — docs/REQUIREMENTS.md tracks their milestone.
func (a *App) dispatch(action keys.Action, count int) {
	switch action {
	case keys.ActionListDown:
		a.listView.MoveBy(count)
	case keys.ActionListUp:
		a.listView.MoveBy(-count)
	case keys.ActionListTop:
		a.listView.MoveTop()
	case keys.ActionListBottom:
		a.listView.MoveBottom()
	case keys.ActionListHalfDown:
		a.listView.MoveHalfPage(1)
	case keys.ActionListHalfUp:
		a.listView.MoveHalfPage(-1)
	case keys.ActionListFilter:
		a.openFilter()
	case keys.ActionListOpen:
		a.listView.Select()
	case keys.ActionGlobalFocusLeft:
		a.focusList()
	case keys.ActionGlobalFocusRight:
		a.focusDetail()
	case keys.ActionGlobalToggleList:
		a.toggleListColumn()
	case keys.ActionGlobalReload:
		a.reload()
	case keys.ActionGlobalOpenBrowser:
		a.openCurrentInBrowser()
	case keys.ActionGlobalHelp:
		a.openHelp()
	case keys.ActionGlobalQuit:
		a.quit()
	case keys.ActionGlobalCommand:
		a.openCommandLine()
	case keys.ActionDetailTabNext:
		a.nextTab()
	case keys.ActionDetailTabPrev:
		a.prevTab()
	}
}
