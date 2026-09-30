package ui

import (
	"fmt"

	"github.com/rivo/tview"
)

// confirmFrame is one open showConfirm dialog's own state, kept on the
// App.confirms stack. Usually the stack holds just one frame; Ctrl-C while
// a mutation is in flight can show its own "Quit anyway?" confirm on top of
// whichever confirm dialog was already open (including pendinglist.go's own
// delete/discard confirm, now that it calls showConfirm directly rather
// than building a separate Modal) — see showConfirm's own doc comment for
// why that never needs to nest deeper than one extra frame.
type confirmFrame struct {
	// page is this frame's own a.root page name: "confirm" for the bottom
	// frame (the common, non-nested case, so every existing single-confirm
	// caller/test keeps working unchanged), "confirm-N" for one stacked at
	// depth N.
	page string
	// parent is the overlay this frame stacked over: the literal "confirm"
	// for a frame stacked over another confirm (see overlayStillOpen's own
	// "confirm" case), or whatever a.overlay held before this frame's own
	// showConfirm call otherwise.
	parent string
	// returnFocus is a.app.GetFocus() captured when this frame opened —
	// for a nested frame, that is the Modal stacked underneath it.
	returnFocus tview.Primitive
	// fromComposer records whether the composer's editor had focus when
	// this frame opened (see showConfirm's own done-func handling below).
	fromComposer bool
	// message is this frame's own Modal text, compared against a new
	// showConfirm call's message to reject an identical duplicate already
	// on top — e.g. a second Ctrl-C while "Quit anyway?" is already
	// showing must not stack another copy of itself.
	message string
}

// showConfirm shows a two-button (confirmLabel / "Cancel") *tview.Modal
// dialog with message, running onConfirm only if the user picks
// confirmLabel — never on "Cancel" or Escape (tview.Modal's SetDoneFunc
// reports Escape as index -1 with an empty label, which never equals
// confirmLabel). It stacks on top of whatever overlay (including another
// confirm dialog) is already open, pushing a new frame onto App.confirms —
// a no-op only when an identical message is already the top frame (the
// simplest way to stop a second Ctrl-C from stacking a duplicate "Quit
// anyway?" on top of itself; a caller showing two genuinely different
// confirms back to back is unaffected).
//
// Every overlay stays open underneath, becoming a.overlay again (with
// focus back on whatever had it, captured on the frame rather than in
// App.savedFocus, which is single-shot) once the dialog closes normally —
// for a nested frame, that "whatever had it" is the confirm Modal
// underneath, so Cancel on the top frame returns to it still focused on
// whichever button it had (tview.Modal/Form keep their own focus index
// across SetFocus calls, since neither is rebuilt). a.overlay reads
// "confirm" for as long as App.confirms is non-empty, regardless of depth.
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
	if n := len(a.confirms); n > 0 && a.confirms[n-1].message == message {
		return
	}

	parent := a.overlay
	page := "confirm"
	if depth := len(a.confirms); depth > 0 {
		parent = "confirm"
		page = fmt.Sprintf("confirm-%d", depth+1)
	}
	frame := &confirmFrame{
		page:         page,
		parent:       parent,
		returnFocus:  a.app.GetFocus(),
		fromComposer: a.composerEditor != nil && a.composerEditor.HasFocus(),
		message:      message,
	}
	a.confirms = append(a.confirms, frame)
	a.overlay = "confirm"

	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{confirmLabel, "Cancel"})
	modal.SetFocus(1)
	modal.SetDoneFunc(func(_ int, label string) {
		a.confirms = a.confirms[:len(a.confirms)-1]
		a.root.RemovePage(frame.page)
		if frame.fromComposer && a.composerEditor == nil {
			// The composer this dialog was opened from was closed while it
			// showed (closeComposer deferred its own refocus): returnFocus
			// is its detached text area, so go where the composer itself
			// would have returned. The edit form's own body composer can
			// close together with its form (a pull request switch), so the
			// parent overlay is checked rather than assumed.
			if a.overlayStillOpen(frame.parent) {
				a.restoreOverlayAfterConfirm(frame.parent)
			} else {
				a.overlay = ""
			}
			if a.composerReturnFocus != nil {
				a.app.SetFocus(a.composerReturnFocus)
				a.composerReturnFocus = nil
			} else {
				a.focusDetail()
			}
		} else if a.overlayStillOpen(frame.parent) {
			a.restoreOverlayAfterConfirm(frame.parent)
			if frame.returnFocus != nil {
				a.app.SetFocus(frame.returnFocus)
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
	a.root.AddPage(frame.page, modal, true, true)
	a.app.SetFocus(modal)
}

// restoreOverlayAfterConfirm sets a.overlay back to parent once a confirm
// frame closes, unless the stack still holds at least one frame below it
// (a.overlay stays "confirm" for as long as App.confirms is non-empty,
// regardless of depth) — called from showConfirm's own done func once it
// has already popped the closing frame off the stack.
func (a *App) restoreOverlayAfterConfirm(parent string) {
	if len(a.confirms) > 0 {
		a.overlay = "confirm"
		return
	}
	a.overlay = parent
}

// retargetConfirmReturn updates the App.confirms frame whose captured
// parent overlay is parent to return focus to p instead of whatever
// a.app.GetFocus() held when it opened — used by openMergeForm when it
// replaces its own loading placeholder with the real form while a confirm
// dialog (or a nested stack of them) sits on top of the merge dialog. There
// is never more than one frame with a non-"confirm" parent at a time (only
// the bottom frame of the stack ever captures a real overlay's name — see
// confirmFrame's own parent field), so the first match is the only one.
func (a *App) retargetConfirmReturn(parent string, p tview.Primitive) {
	for _, f := range a.confirms {
		if f.parent == parent {
			f.returnFocus = p
			return
		}
	}
}

// raiseConfirms re-sends every open App.confirms page to the front, bottom
// frame first, so the top of the stack ends up drawn — and, once refocused,
// receiving input — above whatever a caller just redrew underneath it. Used
// by openMergeForm alongside retargetConfirmReturn, for the same reason.
func (a *App) raiseConfirms() {
	for _, f := range a.confirms {
		a.root.SendToFront(f.page)
	}
}

// overlayStillOpen reports whether the overlay last named by parent (an
// App.overlay value captured before showConfirm stacked "confirm" over it)
// is still open by the time the confirm dialog itself closes — keyed on
// each overlay's own field(s), not a.overlay's string (which reads
// "confirm" for as long as this matters), exactly like closeEditForm's own
// a.editForm == nil guard. parent == "" (no overlay was open when the
// confirm appeared) is trivially "still open": there is nothing that could
// have closed it. parent == "confirm" (a nested confirm frame's own
// parent) is "still open" whenever the stack still holds a frame below the
// one that just closed — the only way a confirm frame closes at all is
// through its own done func above, which always pops the top of the stack
// first, so this is never asked about anything but the frame directly
// beneath the one that just popped.
func (a *App) overlayStillOpen(parent string) bool {
	switch parent {
	case "":
		return true
	case "confirm":
		return len(a.confirms) > 0
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
