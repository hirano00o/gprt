// editlabels.go implements the edit-PR form's "Labels" button: a stacked
// multi-select *tview.List overlay (App.overlay == "editlabels") over the
// repository's full label catalog (Store.Labels(repo)) — Space toggles,
// Enter confirms (writing the result back to App.editFormSelectedLabelIDs
// and the form's own button text), Esc/q cancel (discarding whatever was
// toggled within this overlay only).
package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
)

// openEditLabelsOverlay opens the "editlabels" overlay: a no-op unless the
// edit form is the overlay currently open.
func (a *App) openEditLabelsOverlay() {
	if a.overlay != "editform" {
		return
	}
	a.overlay = "editlabels"
	a.editLabelsWorking = copyBoolSet(a.editFormSelectedLabelIDs)
	a.editLabelsView = tview.NewList().ShowSecondaryText(false)
	a.editLabelsView.SetBorder(true).SetTitle(" Labels (Space toggles, Enter confirms) ")
	a.rebuildEditLabelsList()

	a.root.AddPage("editlabels", a.editLabelsView, true, true)
	a.app.SetFocus(a.editLabelsView)
}

// rebuildEditLabelsList re-renders the list from Store.Labels(repo) and
// editLabelsWorking: a "(loading labels…)" placeholder while the
// repository's metadata has not resolved yet (Store.Labels' own second
// return value), "(no labels in this repository)" once it has and there
// are none.
func (a *App) rebuildEditLabelsList() {
	labels, ok := a.deps.Store.Labels(a.editFormRepo)
	a.editLabelsView.Clear()
	switch {
	case !ok:
		a.editLabelsView.AddItem("(loading labels…)", "", 0, nil)
	case len(labels) == 0:
		a.editLabelsView.AddItem("(no labels in this repository)", "", 0, nil)
	default:
		for _, l := range labels {
			a.editLabelsView.AddItem(editLabelItemText(l, a.editLabelsWorking[l.ID]), "", 0, nil)
		}
	}
}

// editLabelItemText renders one label's row: a "[x]"/"[ ]" marker plus its
// name.
func editLabelItemText(l model.Label, selected bool) string {
	marker := "[ ]"
	if selected {
		marker = "[x]"
	}
	return marker + " " + l.Name
}

// toggleCurrentEditLabel implements Space: toggles the label under the
// cursor in editLabelsWorking, then rebuilds the list, restoring the
// cursor to the same row. A no-op while labels have not resolved yet or
// the list is showing its placeholder row.
func (a *App) toggleCurrentEditLabel() {
	labels, ok := a.deps.Store.Labels(a.editFormRepo)
	if !ok || len(labels) == 0 {
		return
	}
	idx := a.editLabelsView.GetCurrentItem()
	if idx < 0 || idx >= len(labels) {
		return
	}
	id := labels[idx].ID
	if a.editLabelsWorking[id] {
		delete(a.editLabelsWorking, id)
	} else {
		a.editLabelsWorking[id] = true
	}
	a.rebuildEditLabelsList()
	a.editLabelsView.SetCurrentItem(idx)
}

// closeEditLabelsOverlay closes the "editlabels" overlay. apply writes
// editLabelsWorking back to App.editFormSelectedLabelIDs and refreshes the
// edit form's own "Labels (N selected)" button text (Enter); false
// discards it (Esc/q).
func (a *App) closeEditLabelsOverlay(apply bool) {
	if a.overlay != "editlabels" {
		return
	}
	if apply {
		a.editFormSelectedLabelIDs = a.editLabelsWorking
		if a.editForm != nil {
			a.editForm.GetButton(0).SetLabel(a.editFormLabelsButtonText())
		}
	}
	a.editLabelsWorking = nil
	a.editLabelsView = nil
	a.root.RemovePage("editlabels")
	a.overlay = "editform"
	if a.editForm != nil {
		a.app.SetFocus(a.editForm)
	}
}

// routeEditLabelsKey handles the "editlabels" overlay: j/k move, Space
// toggles, Enter confirms, Esc/q cancel — the same j/k/Enter/q/Esc router
// shape pendinglist.go's "p" dialog and reactionpicker.go's own picker
// already use. A close (Enter/q/Esc) returns immediately instead of
// continuing the loop, for the same reason routeChoiceKey/routePendingKey
// do (see their own doc comments): a raw event can normalize into more
// than one Key, and feeding a later one to moveListSelection against a
// *tview.List the close already nilled out would panic.
func (a *App) routeEditLabelsKey(_ *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		switch {
		case isEscKey(k), isPlainRune(k, 'q'):
			a.closeEditLabelsOverlay(false)
			return nil
		case isEnterKey(k):
			a.closeEditLabelsOverlay(true)
			return nil
		case isPlainRune(k, 'j'):
			moveListSelection(a.editLabelsView, 1)
		case isPlainRune(k, 'k'):
			moveListSelection(a.editLabelsView, -1)
		case isPlainRune(k, ' '):
			a.toggleCurrentEditLabel()
		}
	}
	return nil
}
