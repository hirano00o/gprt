// formnav_test.go covers rewriteFormNavKey/wrapFormAutocomplete in
// isolation (see formnav.go's own doc comment) — the create/edit form's
// own end-to-end Up/Down-as-navigation and autocomplete-drop-down
// interactions are covered by createform_test.go/editform_test.go
// instead, driven through the real *tview.Application.
package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestRewriteFormNavKeyWithoutSuggestingRewritesUpDownToBacktabTab(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		want tcell.Key
	}{
		{"up becomes backtab", tcell.KeyUp, tcell.KeyBacktab},
		{"down becomes tab", tcell.KeyDown, tcell.KeyTab},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteFormNavKey(tcell.NewEventKey(tc.key, 0, tcell.ModNone), false)
			if got.Key() != tc.want {
				t.Errorf("rewriteFormNavKey(%v, false).Key() = %v, want %v", tc.key, got.Key(), tc.want)
			}
		})
	}
}

func TestRewriteFormNavKeyWhileSuggestingRewritesTabBacktabToDownUp(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		want tcell.Key
	}{
		{"tab becomes down", tcell.KeyTab, tcell.KeyDown},
		{"backtab becomes up", tcell.KeyBacktab, tcell.KeyUp},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteFormNavKey(tcell.NewEventKey(tc.key, 0, tcell.ModNone), true)
			if got.Key() != tc.want {
				t.Errorf("rewriteFormNavKey(%v, true).Key() = %v, want %v", tc.key, got.Key(), tc.want)
			}
		})
	}
}

// TestRewriteFormNavKeyLeavesOtherKeysUnchanged covers every combination
// rewriteFormNavKey must leave alone: Up/Down while suggesting (tview's
// own InputField.InputHandler already navigates the drop-down with them),
// Tab/Backtab while not suggesting (the Form's own native "next/previous
// item" keys), and Enter/Escape always (selection/close are handled by
// tview and the App's own router respectively, never rewritten here). The
// original *tcell.EventKey is returned as-is (same pointer), not merely an
// equal one, so callers can rely on "unchanged" meaning exactly that.
func TestRewriteFormNavKeyLeavesOtherKeysUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		key        tcell.Key
		suggesting bool
	}{
		{"up while suggesting", tcell.KeyUp, true},
		{"down while suggesting", tcell.KeyDown, true},
		{"tab while not suggesting", tcell.KeyTab, false},
		{"backtab while not suggesting", tcell.KeyBacktab, false},
		{"enter while suggesting", tcell.KeyEnter, true},
		{"enter while not suggesting", tcell.KeyEnter, false},
		{"escape while suggesting", tcell.KeyEsc, true},
		{"rune while not suggesting", tcell.KeyRune, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tc.key, 0, tcell.ModNone)
			got := rewriteFormNavKey(ev, tc.suggesting)
			if got != ev {
				t.Errorf("rewriteFormNavKey(%v, suggesting=%v) returned a new event, want the original unchanged", tc.key, tc.suggesting)
			}
		})
	}
}

// TestWrapFormAutocompleteTracksWhetherEntriesWereReturned covers
// wrapFormAutocomplete's own tracking: *shown mirrors the wrapped
// callback's own "did it return any entries" outcome, the same condition
// tview's InputField.Autocomplete() itself uses to decide whether to show
// a drop-down at all (inputfield.go).
func TestWrapFormAutocompleteTracksWhetherEntriesWereReturned(t *testing.T) {
	var shown bool
	wrapped := wrapFormAutocomplete(&shown, func(text string) []string {
		if text == "" {
			return nil
		}
		return []string{"a", "b"}
	})

	wrapped("")
	if shown {
		t.Error("shown = true after zero entries, want false")
	}

	wrapped("x")
	if !shown {
		t.Error("shown = false after non-zero entries, want true")
	}

	wrapped("")
	if shown {
		t.Error("shown = true after a later zero-entries call, want false")
	}
}

// TestWireFormAutocompleteSelectionClosesAndResetsShown covers the gap
// wrapFormAutocomplete alone leaves open (see its own doc comment): a
// candidate selection (Enter) closes tview's own drop-down directly,
// without ever calling the wrapped autocomplete callback again — only
// wireFormAutocomplete's own SetAutocompletedFunc hook notices that and
// resets *shown.
func TestWireFormAutocompleteSelectionClosesAndResetsShown(t *testing.T) {
	field := tview.NewInputField()
	var shown bool
	wireFormAutocomplete(field, &shown, func(text string) []string { return []string{"alpha", "beta"} })
	if !shown {
		t.Fatal("shown must be true right after SetAutocompleteFunc's own eager call (2 entries)")
	}

	field.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})

	if shown {
		t.Error("shown must be false after Enter selects a candidate")
	}
	if got := field.GetText(); got != "alpha" {
		t.Errorf("field text after Enter = %q, want the highlighted candidate %q", got, "alpha")
	}
}

// TestWireFormAutocompleteNavigationKeepsQueryAndCandidates covers the
// other branch: arrow-key navigation must keep the drop-down open with the
// candidates of the typed query, never re-query with the highlighted
// candidate's text (which would narrow the list to that one candidate, and
// restart a branch field's debounced search with it).
func TestWireFormAutocompleteNavigationKeepsQueryAndCandidates(t *testing.T) {
	field := tview.NewInputField()
	var shown bool
	var queries []string
	wireFormAutocomplete(field, &shown, func(text string) []string {
		queries = append(queries, text)
		var out []string
		for _, c := range []string{"alpha", "alpine", "beta"} {
			if strings.HasPrefix(c, text) {
				out = append(out, c)
			}
		}
		return out
	})
	field.SetText("al")
	field.Autocomplete()
	queries = nil

	field.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})

	if !shown {
		t.Error("shown must stay true after navigating (the drop-down remains open)")
	}
	if len(queries) != 0 {
		t.Errorf("navigating re-queried the autocomplete callback with %q, want no query", queries)
	}
	if got := field.GetText(); got != "al" {
		t.Errorf("field text after navigating Down = %q, want the typed query %q kept", got, "al")
	}

	field.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if got := field.GetText(); got != "alpine" {
		t.Errorf("field text after Enter = %q, want the navigated-to candidate %q", got, "alpine")
	}
}
