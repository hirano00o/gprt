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
