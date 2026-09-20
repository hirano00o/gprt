package ui

import "github.com/rivo/tview"

// showConfirm shows a two-button (confirmLabel / "Cancel") *tview.Modal
// dialog with message, running onConfirm only if the user picks
// confirmLabel — never on "Cancel" or Escape (tview.Modal's SetDoneFunc
// reports Escape as index -1 with an empty label, which never equals
// confirmLabel). A no-op only while another confirm is already open (so
// dialogs never stack); the help/messages overlay, which shares the same
// a.overlay field, is closed first instead of also blocking this —
// Ctrl-C while a mutation is in flight must always get a confirm dialog,
// not silently do nothing merely because "?" happened to be open.
func (a *App) showConfirm(message, confirmLabel string, onConfirm func()) {
	if a.overlay == "confirm" {
		return
	}
	if a.overlay != "" {
		a.closeOverlay()
	}
	a.savedFocus = a.app.GetFocus()
	a.overlay = "confirm"

	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{confirmLabel, "Cancel"})
	modal.SetDoneFunc(func(_ int, label string) {
		a.root.RemovePage("confirm")
		a.overlay = ""
		a.restoreFocus()
		if label == confirmLabel {
			onConfirm()
		}
	})
	a.root.AddPage("confirm", modal, true, true)
	a.app.SetFocus(modal)
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
