// formnav.go adds Up/Down as synonyms for Tab/Backtab in the create-PR/
// edit-PR forms (createform.go/editform.go's own routeCreateFormKey/
// routeEditFormKey), without colliding with an open InputField
// autocomplete drop-down's own scheme (Up/Down navigate its candidates,
// Tab/Backtab/Enter select one) — see rewriteFormNavKey's own doc comment
// for the exact rewriting rules and wrapFormAutocomplete's for how a
// field's own "is its drop-down currently shown" state is tracked, since
// InputField exposes no getter for it at all.
package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// rewriteFormNavKey rewrites ev before it reaches a tview.Form's currently
// focused item, so a router can let Up/Down move between form items
// exactly like Tab/Backtab already do — swapped the other way while an
// autocomplete drop-down is shown for the focused field (suggesting),
// so Tab/Backtab instead navigate its candidates:
//
//   - not suggesting: Up -> Backtab, Down -> Tab. This applies regardless
//     of which item type has focus — InputField without a drop-down open,
//     Checkbox, Button, and TextArea-backed items all already move focus
//     on a literal Tab/Backtab (see form.go's own item.SetFinishedFunc),
//     so rewriting Up/Down into those is all that is needed to add them
//     as synonyms everywhere at once.
//   - suggesting: Tab -> Down, Backtab -> Up, so they navigate the
//     drop-down instead of moving focus away from it (previously, tview's
//     own InputField.InputHandler treated a literal Tab exactly like
//     Enter — "select the highlighted candidate" — which this
//     deliberately overrides only for the direction key, not Enter
//     itself, so the two selection paths remain: Enter always selects,
//     Up/Down (now including Tab/Backtab) always navigate).
//
// Every other key (Enter, Escape, Up/Down while not suggesting, Tab/
// Backtab while suggesting is false and there is nothing to swap, runes,
// ...) is returned unchanged — including, notably, Up/Down while
// suggesting: tview's own InputField.InputHandler already navigates the
// drop-down with them, so there is nothing for this function to rewrite.
func rewriteFormNavKey(ev *tcell.EventKey, suggesting bool) *tcell.EventKey {
	var key tcell.Key
	switch {
	case !suggesting && ev.Key() == tcell.KeyUp:
		key = tcell.KeyBacktab
	case !suggesting && ev.Key() == tcell.KeyDown:
		key = tcell.KeyTab
	case suggesting && ev.Key() == tcell.KeyTab:
		key = tcell.KeyDown
	case suggesting && ev.Key() == tcell.KeyBacktab:
		key = tcell.KeyUp
	default:
		return ev
	}
	return tcell.NewEventKey(key, 0, tcell.ModNone)
}

// wrapFormAutocomplete wraps an InputField's own SetAutocompleteFunc
// callback so *shown always mirrors tview's own, otherwise unexported,
// "is the drop-down list currently populated" state (inputfield.go's
// InputField.autocompleteList != nil): Autocomplete() creates/keeps that
// list exactly when this same callback's own returned entries are
// non-empty, so recomputing that condition here, from the very callback
// that decides it, is the only signal that can never drift out of step
// with it — restoring a genuine getter tview does not expose.
//
// This alone does not cover every way the real drop-down can close
// without the callback running again — a literal Escape while it is
// shown, focus simply leaving the field, and a candidate being selected
// (Enter, or, before rewriteFormNavKey's own remapping ever reaches
// InputField, a literal Tab) all close it directly
// (InputField.autocompleteList = nil) without ever calling this callback
// again. routeCreateFormKey/routeEditFormKey account for the first two
// explicitly (forcing *shown false whenever the field does not currently
// have focus — recomputed fresh on every routed key event, so it is never
// stale by more than one keystroke regardless of *how* focus left — and
// resetting it explicitly the moment an Escape is routed while suggesting
// was true); wireFormAutocomplete below accounts for the third.
func wrapFormAutocomplete(shown *bool, fn func(text string) []string) func(string) []string {
	return func(text string) []string {
		entries := fn(text)
		*shown = len(entries) > 0
		return entries
	}
}

// wireFormAutocomplete registers fn as field's own SetAutocompleteFunc
// (wrapped by wrapFormAutocomplete so *shown tracks it) and pairs it with
// a SetAutocompletedFunc callback, the one hook that can tell a genuine
// candidate selection (source AutocompletedEnter/AutocompletedTab) apart
// from mere arrow-key navigation (AutocompletedNavigate) — needed only to
// reset *shown at the exact moment a selection closes the drop-down for
// good, since (see wrapFormAutocomplete's own doc comment) nothing else
// ever calls back into fn for that case. A selection sets the field's text
// and returns true (close the drop-down), clearing *shown.
// AutocompletedNavigate returns false (keep it open) and deliberately does
// not live-preview the highlighted entry in the field the way tview's
// default does: tview only resynchronises its "text changed" check when
// the callback returns true, so a SetText here would make InputHandler
// re-run fn with the highlighted candidate as the query, narrowing the
// list to that one entry (and restarting a branch field's debounced
// search with it). The drop-down's own highlight already shows which
// candidate Enter will pick.
func wireFormAutocomplete(field *tview.InputField, shown *bool, fn func(text string) []string) {
	field.SetAutocompleteFunc(wrapFormAutocomplete(shown, fn))
	field.SetAutocompletedFunc(func(text string, index, source int) bool {
		if source == tview.AutocompletedNavigate {
			return false
		}
		field.SetText(text)
		*shown = false
		return true
	})
}
