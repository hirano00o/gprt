package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/editor"
	"github.com/hirano00o/gprt/internal/ui/keys"
)

// handleKey is the App's single app.SetInputCapture function: every key
// event is normalized and routed here before any primitive sees it.
// Ctrl-C quits immediately, unless a mutation is in flight, in which case
// it asks for confirmation first (see quitWithConfirmIfMutating) —
// regardless of what has focus, including the composer, so a composer's
// own routing never needs to special-case it itself. Everywhere else, a
// focused text-entry field (filter or command line) gets everything
// except Esc/Enter/history keys forwarded unchanged; the composer (when
// open and focused) gets everything except its own Ctrl-w chords (see
// routeComposerKey); an open overlay only responds to q/Esc; otherwise
// every key feeds the Sequencer and resolved actions dispatch.
func (a *App) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	normalized := keys.Normalize(ev)
	if containsCtrlC(normalized) {
		// Ctrl-C bypasses routeComposerKey entirely (it is handled here,
		// before the composer-focus check below, regardless of what has
		// focus), so a Ctrl-w chord left pending from a keystroke just
		// before it must be cleared here too — left set, the next
		// ordinary key typed into the composer afterward (once any
		// confirm dialog this shows is dismissed) would otherwise be
		// misread as that chord's own continuation.
		a.composerCtrlWPending = false
		a.quitWithConfirmIfMutating()
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
	// Not a switch case: tview.Flex.Focus (which Editor inherits, like
	// tview.Form) delegates keyboard focus straight down to its TextArea
	// child, so Application.GetFocus() itself never equals a.composerEditor
	// — see Editor.HasFocus's own doc comment.
	if a.composerEditor != nil && a.composerEditor.HasFocus() {
		return a.routeComposerKey(ev, normalized)
	}

	if a.overlay != "" {
		switch a.overlay {
		case "confirm", "pendingConfirm":
			// A confirm dialog is a real focused *tview.Modal, not a
			// scrollable TextView like help/messages: returning ev lets
			// tview's own event loop hand it straight to the Modal's
			// native InputHandler (arrow keys/Enter to pick a button,
			// Escape to cancel), rather than routeOverlayKey's
			// q/Esc-only handling, which would otherwise swallow every
			// key the Modal itself needs. pendingConfirm is the same
			// shape, stacked on top of the "pending" list overlay for
			// its own d/D confirmations — see pendinglist.go's
			// confirmWithinPendingList.
			return ev
		case "choice":
			return a.routeChoiceKey(ev, normalized)
		case "pending":
			return a.routePendingKey(ev, normalized)
		default:
			return a.routeOverlayKey(ev, normalized)
		}
	}

	// Ending an active visual selection on the diff needs its own, earlier
	// check: Esc is not a dispatchable Action, so it would never otherwise
	// reach diffView.EndVisual(). It still falls through to the Sequencer
	// feed below (rather than returning immediately) so Esc keeps doing
	// its usual job there too — resetting any pending multi-key prefix
	// (see keys.validateSequenceStart) — since skipping that would let a
	// half-typed sequence like "z" (a prefix of za/zM/zR/zh/zl) survive
	// across the Esc keypress and combine with whatever key comes next.
	if a.app.GetFocus() == a.diffView && a.diffView.InVisual() {
		for _, k := range normalized {
			if isEscKey(k) {
				a.diffView.EndVisual()
				break
			}
		}
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
		// No context bound (or mid-sequence toward) this key at all: let it
		// fall through to whatever has focus. list.down/up/top/bottom/
		// half_down/half_up (j/k/gg/G/Ctrl-d/Ctrl-u) are bound in every
		// movable context, including ContextFiles (see keys.defaultTable's
		// own doc comment), so they are consumed above and reach the tree
		// via focusedListPane's treeMovablePane rather than this fallthrough
		// at all. A key genuinely unbound in every context (arrows, Home/End,
		// mouse-adjacent keys, ...) still falls through here to whatever has
		// focus; for the ListView-backed list, the PR tab, the tree, and the
		// DiffView, none of which act on a forwarded key they do not already
		// bind themselves, this is harmless either way.
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

// routeChoiceKey handles the "choice" overlay (dialogs.go's
// showChoiceMenu): j/k move the selection, Enter chooses the current item,
// q/Esc cancel. Every key is consumed (nil returned) while the menu is
// open, matching routeOverlayKey's own confirm-dialog-adjacent shape.
//
// A close (Enter/q/Esc) returns immediately instead of continuing the loop:
// keys.Normalize can expand one raw event into more than one Key (Alt+rune
// -> [Esc, rune], and tcell itself reports a printable key typed within
// ~50ms of Esc as Alt+that-key on some terminals) — continuing after
// closeChoiceMenu has nilled a.choiceMenu would feed the remaining key(s)
// to moveListSelection/GetCurrentItem against a nil *tview.List.
func (a *App) routeChoiceKey(_ *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k), isPlainRune(k, 'q'):
			a.closeChoiceMenu(false, -1)
			return nil
		case isEnterKey(k):
			a.closeChoiceMenu(true, a.choiceMenu.GetCurrentItem())
			return nil
		case isPlainRune(k, 'j'):
			moveListSelection(a.choiceMenu, 1)
		case isPlainRune(k, 'k'):
			moveListSelection(a.choiceMenu, -1)
		}
	}
	return nil
}

// moveListSelection moves list's current item by delta (+1/-1), clamped to
// its item range — j/k navigation for the small tview.List-backed overlays
// (the choice menu, the pending list) that do not otherwise bind j/k
// themselves.
func moveListSelection(list *tview.List, delta int) {
	count := list.GetItemCount()
	if count == 0 {
		return
	}
	next := list.GetCurrentItem() + delta
	if next < 0 {
		next = 0
	}
	if next >= count {
		next = count - 1
	}
	list.SetCurrentItem(next)
}

// currentContexts returns the context list Sequencer.Feed resolves the
// current keypress against: whatever pane has focus, falling back to
// ContextGlobal.
func (a *App) currentContexts() []keys.Context {
	switch a.app.GetFocus() {
	case a.listView:
		return []keys.Context{keys.ContextList, keys.ContextGlobal}
	case a.prView:
		// ContextPR first: it and ContextComment carry identical
		// bindings for comment/thread actions (diff.comment,
		// comment.edit, comment.delete, comment.react — see
		// keys.defaultTable), so resolving through ContextPR alone
		// covers both without needing to first determine whether the
		// cursor is actually on a comment block.
		return []keys.Context{keys.ContextPR, keys.ContextDetail, keys.ContextGlobal}
	case a.treeView:
		return []keys.Context{keys.ContextFiles, keys.ContextGlobal}
	case a.diffView:
		// ContextDiff first, so "c" (bound in both ContextDiff, as
		// diff.comment, and ContextThread, as thread.reply) always
		// resolves to diff.comment here — dispatch's own ActionDiffComment
		// case then decides between "new comment"/"reply" from the
		// cursor's actual row (see diffCommentOrReply), matching
		// docs/KEYBINDINGS.md footnote 2 ("one physical key ... does not
		// introduce a third action ID"). ContextThread/ContextComment are
		// always included (not only when the cursor happens to be on a
		// thread row) so "r"/"x"/"e"/"d" — none of which ContextDiff binds
		// at all — resolve to their thread/comment actions regardless of
		// cursor position; each handler itself toasts when the cursor is
		// not actually on a thread, rather than the key falling through
		// unconsumed with no feedback at all.
		return []keys.Context{keys.ContextDiff, keys.ContextThread, keys.ContextComment, keys.ContextGlobal}
	default:
		return []keys.Context{keys.ContextGlobal}
	}
}

// movablePane is the subset of widget.ListView's/widget.DiffView's movement
// API (plus treeMovablePane's native-key-synthesising adaptation of it) that
// list.down/up/top/bottom/half_down/half_up dispatch to, so those actions
// route to whichever movable pane currently has focus (the PR list, the PR
// tab's DetailView, the Files tab's tree, or its DiffView) rather than
// always moving the list.
type movablePane interface {
	MoveBy(n int)
	MoveTop()
	MoveBottom()
	MoveHalfPage(dir int)
}

// noopMovablePane discards every movement: the default focusedListPane
// result when no movable pane has focus at all, so a list movement key
// bound in ContextDetail/ContextDiff does nothing rather than silently
// moving the PR list in the background.
type noopMovablePane struct{}

func (noopMovablePane) MoveBy(int)       {}
func (noopMovablePane) MoveTop()         {}
func (noopMovablePane) MoveBottom()      {}
func (noopMovablePane) MoveHalfPage(int) {}

// focusedListPane returns the movable pane that currently has focus (the PR
// list, the PR tab's DetailView, the Files tab's tree — wrapped in a
// treeMovablePane, see files.go — or its DiffView), or a no-op pane when
// none does.
func (a *App) focusedListPane() movablePane {
	switch a.app.GetFocus() {
	case a.listView:
		return a.listView
	case a.prView:
		return a.prView
	case a.treeView:
		return treeMovablePane{tree: a.treeView}
	case a.diffView:
		return a.diffView
	default:
		return noopMovablePane{}
	}
}

// dispatch runs the effect of a resolved Action. Actions not yet
// implemented (comment.react, pr.submit, pr.edit, list.new_pr) are silently
// ignored rather than surfacing a toast for every exploratory keypress —
// docs/REQUIREMENTS.md tracks their milestone.
func (a *App) dispatch(action keys.Action, count int) {
	switch action {
	case keys.ActionListDown:
		a.focusedListPane().MoveBy(count)
	case keys.ActionListUp:
		a.focusedListPane().MoveBy(-count)
	case keys.ActionListTop:
		a.focusedListPane().MoveTop()
	case keys.ActionListBottom:
		a.focusedListPane().MoveBottom()
	case keys.ActionListHalfDown:
		a.focusedListPane().MoveHalfPage(1)
	case keys.ActionListHalfUp:
		a.focusedListPane().MoveHalfPage(-1)
	case keys.ActionListFilter:
		a.openFilter()
	case keys.ActionListOpen:
		if a.app.GetFocus() == a.treeView {
			a.treeOpen()
		} else {
			a.listView.Select()
		}
	case keys.ActionGlobalFocusLeft:
		a.focusPrevPane()
	case keys.ActionGlobalFocusRight:
		a.focusNextPane()
	case keys.ActionGlobalToggleList:
		a.toggleListColumn()
	case keys.ActionGlobalReload:
		a.reload()
	case keys.ActionGlobalOpenBrowser:
		a.openCurrentInBrowser()
	case keys.ActionGlobalHelp:
		a.openHelp()
	case keys.ActionGlobalQuit:
		a.quitWithConfirmIfMutating()
	case keys.ActionGlobalCommand:
		a.openCommandLine()
	case keys.ActionDetailTabNext:
		a.nextTab()
	case keys.ActionDetailTabPrev:
		a.prevTab()
	case keys.ActionFilesToggleTree:
		a.toggleTree()
	case keys.ActionDiffVisual:
		a.diffView.StartVisual()
	case keys.ActionDiffFold:
		a.diffView.ToggleFoldAtCursor()
	case keys.ActionDiffUnfoldAll:
		a.diffView.FoldAll(false)
	case keys.ActionDiffFoldAll:
		a.diffView.FoldAll(true)
	case keys.ActionDiffScrollLeft:
		a.diffView.ScrollHorizontal(-diffScrollStep)
	case keys.ActionDiffScrollRight:
		a.diffView.ScrollHorizontal(diffScrollStep)
	case keys.ActionDiffNextThread:
		a.diffView.NextThread()
	case keys.ActionDiffPrevThread:
		a.diffView.PrevThread()
	case keys.ActionDiffNextFile:
		a.stepFile(1)
	case keys.ActionDiffPrevFile:
		a.stepFile(-1)
	case keys.ActionGlobalFocusDown, keys.ActionGlobalFocusUp:
		a.toggleComposerFocus()
	case keys.ActionDiffComment:
		switch a.app.GetFocus() {
		case a.prView:
			a.openGeneralCommentComposer()
		case a.diffView:
			a.diffCommentOrReply()
		}
	case keys.ActionDiffCommentFile:
		if a.app.GetFocus() == a.diffView {
			a.diffCommentFile()
		}
	case keys.ActionThreadReply:
		if a.app.GetFocus() == a.diffView {
			a.replyToCurrentThread()
		}
	case keys.ActionThreadToggleResolved:
		if a.app.GetFocus() == a.diffView {
			a.toggleCurrentThreadResolved()
		}
	case keys.ActionCommentEdit:
		switch a.app.GetFocus() {
		case a.prView:
			a.editCurrentComment()
		case a.diffView:
			a.editCurrentThreadComment()
		}
	case keys.ActionCommentDelete:
		switch a.app.GetFocus() {
		case a.prView:
			a.deleteCurrentComment()
		case a.diffView:
			a.deleteCurrentThreadComment()
		}
	case keys.ActionPRPending:
		a.openPendingList()
	}
}

// quitWithConfirmIfMutating quits immediately, unless a mutation is
// currently in flight (a comment being sent, edited, or deleted), in
// which case it asks for confirmation first so Ctrl-C can never silently
// discard a mutation the user cannot tell has not landed yet.
func (a *App) quitWithConfirmIfMutating() {
	if !a.deps.Store.Mutating() {
		a.quit()
		return
	}
	a.showConfirm("A comment is still being sent. Quit anyway?", "Quit", a.quit)
}

// isPlainRune reports whether k is r typed with no modifier.
func isPlainRune(k keys.Key, r rune) bool {
	return k.Kind == keys.KindRune && k.Rune == r && k.Mod == 0
}

// isCtrlW reports whether k is Ctrl-w.
func isCtrlW(k keys.Key) bool {
	return k.Kind == keys.KindRune && k.Rune == 'w' && k.Mod == tcell.ModCtrl
}

// routeComposerKey handles every key while the composer's editor has
// focus: a pending Ctrl-w (composerCtrlWPending) resolves its own
// h/l/j/k continuation exactly like the rest of the app's Ctrl-w chords
// (focusPrevPane/focusNextPane/toggleComposerFocus) and drops any other
// continuation; a fresh Ctrl-w arms that pending state; everything else —
// including keys that are otherwise global bindings elsewhere, such as
// "q" or "?", which are plain vim keys inside the editor (:q is how the
// composer itself closes) — goes straight to Editor.Handle. Ctrl-C is
// handled earlier, in handleKey, before focus is even considered.
func (a *App) routeComposerKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		if a.composerCtrlWPending {
			a.composerCtrlWPending = false
			switch {
			case isPlainRune(k, 'h'):
				a.focusPrevPane()
			case isPlainRune(k, 'l'):
				a.focusNextPane()
			case isPlainRune(k, 'j'), isPlainRune(k, 'k'):
				a.toggleComposerFocus()
			}
			continue
		}
		// The focus chord only applies outside insert mode: vim's own
		// Ctrl-w there deletes the word before the cursor (already in
		// Vim's insert whitelist, forwarded straight to TextArea, which
		// implements it natively), and every composer test that types
		// through insert mode would otherwise never reach it at all.
		if isCtrlW(k) && a.composerEditor.Vim().Mode() != editor.ModeInsert {
			a.composerCtrlWPending = true
			continue
		}
		// HandleKey, not Handle: ev may itself normalize to more than one
		// key (Alt+rune -> [Esc, rune]), and normalized already reflects
		// that full expansion — calling Handle(ev) again per key here
		// would re-normalize and reprocess the same event once per key
		// already produced from it. Stop as soon as an earlier key in
		// that sequence closes the composer (":q"/Ctrl-s/...), since
		// there would be nothing left to feed the rest to.
		a.composerEditor.HandleKey(k, ev)
		if a.composerEditor == nil {
			break
		}
	}
	return nil
}

// toggleComposerFocus implements Ctrl-w j/Ctrl-w k: it moves focus into
// the composer's editor from wherever composerReturnFocus was captured
// when it opened, or back out to that pane when the editor already has
// focus. A no-op when no composer is open.
func (a *App) toggleComposerFocus() {
	if a.composerEditor == nil {
		return
	}
	if a.composerEditor.HasFocus() {
		if a.composerReturnFocus != nil {
			a.app.SetFocus(a.composerReturnFocus)
		}
		return
	}
	a.app.SetFocus(a.composerEditor)
}
