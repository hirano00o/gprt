package ui

import "github.com/rivo/tview"

// showConfirm shows a two-button (confirmLabel / "Cancel") *tview.Modal
// dialog with message, running onConfirm only if the user picks
// confirmLabel — never on "Cancel" or Escape (tview.Modal's SetDoneFunc
// reports Escape as index -1 with an empty label, which never equals
// confirmLabel). A no-op only while another confirm is already open (so
// dialogs never stack); any other overlay sharing the same a.overlay
// field is closed first instead of also blocking this — Ctrl-C while a
// mutation is in flight must always get a confirm dialog, not silently do
// nothing merely because "?" happened to be open.
//
// The create/edit PR forms are the exception (stacksConfirm): they stay on
// screen underneath and become a.overlay again when the dialog closes,
// with focus back on the item it had. Closing them here removed their page
// while the form itself (and its debounce timers) stayed allocated, so
// "Cancel" on their own discard confirm dropped the form from the screen
// without discarding it. Only they stack because their teardown keys on
// their own fields (a.createForm/a.editForm); the other overlays' store
// event handlers key on a.overlay's value and would miss events while a
// confirm sat on top. The return focus is kept locally rather than in
// App.savedFocus, which is single-shot.
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
	if a.overlay == "confirm" {
		return
	}
	parent := ""
	if stacksConfirm(a.overlay) {
		parent = a.overlay
	} else if a.overlay != "" {
		a.closeOverlay()
	}
	returnFocus := a.app.GetFocus()
	a.overlay = "confirm"

	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{confirmLabel, "Cancel"})
	modal.SetFocus(1)
	modal.SetDoneFunc(func(_ int, label string) {
		a.root.RemovePage("confirm")
		a.overlay = parent
		if returnFocus != nil {
			a.app.SetFocus(returnFocus)
		} else {
			a.focusList()
		}
		if label == confirmLabel {
			onConfirm()
		}
	})
	a.root.AddPage("confirm", modal, true, true)
	a.app.SetFocus(modal)
}

// stacksConfirm reports whether overlay stays open underneath a
// showConfirm dialog rather than being closed by it (see showConfirm).
func stacksConfirm(overlay string) bool {
	return overlay == "createform" || overlay == "editform"
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
func (a *App) closeChoiceMenu(chosen bool, index int) {
	if a.overlay != "choice" {
		return
	}
	onChoose := a.choiceOnChoose
	a.choiceOnChoose = nil
	a.choiceMenu = nil
	a.root.RemovePage("choice")
	a.overlay = ""
	a.restoreFocus()
	if chosen && onChoose != nil {
		onChoose(index)
	}
}
