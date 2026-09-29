package ui

import "github.com/rivo/tview"

// showConfirm shows a two-button (confirmLabel / "Cancel") *tview.Modal
// dialog with message, running onConfirm only if the user picks
// confirmLabel — never on "Cancel" or Escape (tview.Modal's SetDoneFunc
// reports Escape as index -1 with an empty label, which never equals
// confirmLabel). A no-op while another confirm is already open, or while
// pendingConfirm (pendinglist.go's own nested delete/discard confirm) is —
// stacking a third modal on top of a second would only ever complicate an
// already-nested overlay, with no caller that needs it: Ctrl-C reaching
// here while pendingConfirm is up (Store.Mutating() from an unrelated,
// still in-flight mutation) is simply ignored, exactly like it already is
// while "confirm" itself is up.
//
// Every other overlay stays open underneath, becoming a.overlay again
// (with focus back on whatever had it, captured locally rather than in
// App.savedFocus, which is single-shot) once the dialog closes normally.
// This used to close whatever overlay was already open first (except the
// create/edit PR forms, kept open as the one exception, "stacksConfirm"),
// since most overlays' own store-event handlers keyed on a.overlay's own
// value and would otherwise silently miss events while a confirm sat on
// top of them — see overlayStillOpen's own doc comment for how every such
// check is now keyed on each overlay's own field(s) instead, which is what
// makes stacking safe everywhere, not just for the two forms.
//
// "Cancel" (button index 1) is the default focus (SetFocus(1)): tview's
// own Form (which Modal wraps its buttons in) otherwise defaults to
// focusIndex 0 — the *first* AddButtons argument, confirmLabel, the
// destructive/executing choice — so a bare Enter right after the dialog
// appears, with no navigation at all, would immediately run onConfirm. A
// confirmation dialog whose own default action is "confirm" defeats the
// point of asking; every caller (delete/discard, :merge/:close/:reopen,
// Ctrl-C-while-mutating) relies on this shared default, not its own.
func (a *App) showConfirm(message, confirmLabel string, onConfirm func()) {
	if a.overlay == "confirm" || a.overlay == "pendingConfirm" {
		return
	}
	parent := a.overlay
	a.confirmReturnFocus = a.app.GetFocus()
	fromComposer := a.composerEditor != nil && a.composerEditor.HasFocus()
	a.overlay = "confirm"

	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{confirmLabel, "Cancel"})
	modal.SetFocus(1)
	modal.SetDoneFunc(func(_ int, label string) {
		a.root.RemovePage("confirm")
		returnFocus := a.confirmReturnFocus
		a.confirmReturnFocus = nil
		if fromComposer && a.composerEditor == nil {
			// The composer this dialog was opened from was closed while it
			// showed (closeComposer deferred its own refocus): returnFocus
			// is its detached text area, so go where the composer itself
			// would have returned.
			a.overlay = parent
			if a.composerReturnFocus != nil {
				a.app.SetFocus(a.composerReturnFocus)
				a.composerReturnFocus = nil
			} else {
				a.focusDetail()
			}
		} else if a.overlayStillOpen(parent) {
			a.overlay = parent
			if returnFocus != nil {
				a.app.SetFocus(returnFocus)
			} else {
				a.focusList()
			}
		} else {
			// A store event closed parent out from under this dialog
			// while it was showing (e.g. closeMergeDialogIfWrongPR on a
			// PR switch): that close already tore its own page/fields
			// down, deferring only the focus/overlay reset that would
			// otherwise have stolen focus from this still-visible Modal —
			// see those close functions' own "a.overlay == confirm" guard.
			// restoreFocus() picks up wherever App.savedFocus still points
			// (left untouched by that deferred close); for editform/
			// createform, which use their own dedicated return-focus field
			// instead (see closeEditForm's own doc comment for why),
			// App.savedFocus was never touched in the first place, so this
			// falls back to the list, same as returnFocus being nil above.
			a.overlay = ""
			a.restoreFocus()
		}
		if label == confirmLabel {
			onConfirm()
		}
	})
	a.root.AddPage("confirm", modal, true, true)
	a.app.SetFocus(modal)
}

// overlayStillOpen reports whether the overlay last named by parent (an
// App.overlay value captured before showConfirm stacked "confirm" over it)
// is still open by the time the confirm dialog itself closes — keyed on
// each overlay's own field(s), not a.overlay's string (which reads
// "confirm" for as long as this matters), exactly like closeEditForm's own
// a.editForm == nil guard. parent == "" (no overlay was open when the
// confirm appeared) is trivially "still open": there is nothing that could
// have closed it.
func (a *App) overlayStillOpen(parent string) bool {
	switch parent {
	case "":
		return true
	case "help":
		return a.helpView != nil
	case "messages":
		return a.messagesView != nil
	case "choice":
		return a.choiceMenu != nil
	case "pending":
		return a.pendingListView != nil
	case "threads":
		return a.threadListView != nil
	case "reaction":
		return a.reactionPickerView != nil
	case "merge":
		return a.mergeDialogOpen()
	case "editform":
		return a.editForm != nil
	case "editlabels":
		return a.editLabelsView != nil
	case "editreviewers":
		return a.editReviewersView != nil
	case "createform":
		return a.createForm != nil
	default:
		return false
	}
}

// showChoiceMenu shows a small overlay list of items (labels only, no
// secondary text) titled title; choosing one (Enter) calls onChoose with
// its index. Cancelling (Esc/q, via routeChoiceKey) calls nothing and
// restores focus to whatever had it before — the caller's own state (for
// example a still-open composer's typed text) is untouched either way,
// since onChoose is the only thing that acts on the choice. A no-op while
// another overlay is already open, so choice menus never stack.
func (a *App) showChoiceMenu(title string, items []string, onChoose func(index int)) {
	if a.overlay != "" {
		return
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "choice"
	a.choiceOnChoose = onChoose

	list := tview.NewList().ShowSecondaryText(false)
	for _, item := range items {
		list.AddItem(item, "", 0, nil)
	}
	list.SetBorder(true).SetTitle(" " + title + " ")
	a.choiceMenu = list

	a.root.AddPage("choice", list, true, true)
	a.app.SetFocus(list)
}

// closeChoiceMenu closes the choice menu opened by showChoiceMenu. It
// invokes the stored callback with index only when chosen is true (Enter);
// Esc/q (chosen == false) close it without invoking anything at all.
//
// The guard is a.choiceMenu == nil, not a.overlay != "choice": a confirm
// dialog can be stacked on top of this one (a.overlay == "confirm") when a
// store event closes it out from under that confirm — see
// closeComposerIfWrongPR's own call to this — so keying on a.overlay would
// silently miss it, mirroring closeEditForm's own a.editForm-based guard.
// When that happens, a.overlay/focus are left alone (the confirm stays the
// visible, focused overlay); its own done func notices via
// overlayStillOpen("choice") and falls back once it closes.
func (a *App) closeChoiceMenu(chosen bool, index int) {
	if a.choiceMenu == nil {
		return
	}
	onChoose := a.choiceOnChoose
	a.choiceOnChoose = nil
	a.choiceMenu = nil
	a.root.RemovePage("choice")
	if a.overlay != "confirm" {
		a.overlay = ""
		a.restoreFocus()
	}
	if chosen && onChoose != nil {
		onChoose(index)
	}
}
