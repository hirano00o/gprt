package widget

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func headerRow(id, text string) ListRow {
	return ListRow{ID: id, Lines: [][]Span{{{Text: text}}}}
}

func itemRow(id, line1, line2 string) ListRow {
	return ListRow{
		ID:         id,
		Selectable: true,
		Lines:      [][]Span{{{Text: line1}}, {{Text: line2}}},
	}
}

func fixtureRows() []ListRow {
	return []ListRow{
		headerRow("h1", "Section A"),
		itemRow("pr1", "#1 First", "  owner/repo"),
		itemRow("pr2", "#2 Second", "  owner/repo"),
		itemRow("pr3", "#3 Third", "  owner/repo"),
	}
}

func TestListViewRowsReturnsTheCurrentRows(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)

	if got := lv.Rows(); got != nil {
		t.Fatalf("Rows() on an empty ListView = %#v, want nil/empty", got)
	}

	rows := fixtureRows()
	lv.SetRows(rows)

	got := lv.Rows()
	if len(got) != len(rows) {
		t.Fatalf("Rows() returned %d rows, want %d", len(got), len(rows))
	}
	for i := range rows {
		if got[i].ID != rows[i].ID {
			t.Errorf("Rows()[%d].ID = %q, want %q", i, got[i].ID, rows[i].ID)
		}
	}
}

func TestListViewRendersRows(t *testing.T) {
	screen := newTestScreen(t, 40, 10)
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())

	lv.Draw(screen)

	if r := cellRune(screen, 0, 0); r != 'S' { // "Section A" header on the first line
		t.Errorf("cell (0,0) = %q, want 'S' (header text)", r)
	}
	if r := cellRune(screen, 0, 1); r != '#' { // "#1 First" on the second line
		t.Errorf("cell (0,1) = %q, want '#' (first item)", r)
	}
}

func TestListViewCursorMovement(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())

	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("initial CurrentID() = %q, want pr1", got)
	}

	lv.MoveBy(1)
	if got := lv.CurrentID(); got != "pr2" {
		t.Fatalf("after MoveBy(1), CurrentID() = %q, want pr2", got)
	}

	lv.MoveBy(-1)
	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("after MoveBy(-1), CurrentID() = %q, want pr1", got)
	}

	lv.MoveBottom()
	if got := lv.CurrentID(); got != "pr3" {
		t.Fatalf("after MoveBottom(), CurrentID() = %q, want pr3", got)
	}

	lv.MoveTop()
	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("after MoveTop(), CurrentID() = %q, want pr1", got)
	}
}

func TestListViewCursorSkipsHeaders(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows([]ListRow{
		headerRow("h1", "Section A"),
		itemRow("pr1", "#1", "line2"),
		headerRow("h2", "Section B"),
		itemRow("pr2", "#2", "line2"),
	})

	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("CurrentID() = %q, want pr1 (headers are not selectable)", got)
	}
	lv.MoveBy(1)
	if got := lv.CurrentID(); got != "pr2" {
		t.Fatalf("after MoveBy(1), CurrentID() = %q, want pr2 (h2 skipped)", got)
	}
}

func TestListViewMovementClampsAtBoundaries(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())

	lv.MoveBy(-100)
	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("MoveBy(-100) from top = %q, want pr1 (clamped)", got)
	}

	lv.MoveBy(100)
	if got := lv.CurrentID(); got != "pr3" {
		t.Fatalf("MoveBy(100) = %q, want pr3 (clamped)", got)
	}
}

func TestListViewEmptyRowsDoNotPanic(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(nil)

	lv.MoveBy(1)
	lv.MoveTop()
	lv.MoveBottom()
	lv.MoveHalfPage(1)
	lv.MoveHalfPage(-1)
	lv.Select()

	if got := lv.CurrentID(); got != "" {
		t.Errorf("CurrentID() on an empty list = %q, want \"\"", got)
	}

	screen := newTestScreen(t, 40, 10)
	lv.Draw(screen) // must not panic
}

func TestListViewKeepsCursorOnSameIDAcrossSetRows(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())
	lv.MoveBy(1) // now on pr2

	// A refresh reorders and adds rows; pr2 is still present.
	lv.SetRows([]ListRow{
		headerRow("h1", "Section A"),
		itemRow("pr0", "#0 New", "line2"),
		itemRow("pr1", "#1 First", "line2"),
		itemRow("pr2", "#2 Second", "line2"),
	})

	if got := lv.CurrentID(); got != "pr2" {
		t.Fatalf("CurrentID() after SetRows = %q, want pr2 (kept by ID)", got)
	}
}

func TestListViewFallsBackWhenIDIsGone(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())
	lv.MoveBottom() // pr3

	lv.SetRows([]ListRow{
		headerRow("h1", "Section A"),
		itemRow("pr1", "#1 First", "line2"),
	})

	if got := lv.CurrentID(); got != "pr1" {
		t.Fatalf("CurrentID() after pr3 disappeared = %q, want pr1 (fell back to first)", got)
	}
}

func TestListViewChangedFuncFiresOnMovement(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)

	var seen []string
	lv.SetChangedFunc(func(row ListRow) { seen = append(seen, row.ID) })

	lv.SetRows(fixtureRows())
	lv.MoveBy(1)
	lv.MoveBy(1)

	want := []string{"pr1", "pr2", "pr3"}
	if len(seen) != len(want) {
		t.Fatalf("changed callback fired %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("changed[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestListViewSelectInvokesSelectedFunc(t *testing.T) {
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())

	var got ListRow
	lv.SetSelectedFunc(func(row ListRow) { got = row })
	lv.MoveBy(1)
	lv.Select()

	if got.ID != "pr2" {
		t.Fatalf("Select() invoked selected func with ID %q, want pr2", got.ID)
	}
}

func TestListViewScrollKeepsCursorVisible(t *testing.T) {
	// Height 4: fits exactly two 2-line item rows (or one header + one
	// item). With 5 selectable rows, moving to the bottom must scroll.
	rows := []ListRow{
		itemRow("pr1", "#1", "l2"),
		itemRow("pr2", "#2", "l2"),
		itemRow("pr3", "#3", "l2"),
		itemRow("pr4", "#4", "l2"),
		itemRow("pr5", "#5", "l2"),
	}
	lv := NewListView()
	lv.SetRect(0, 0, 40, 4)
	lv.SetRows(rows)

	lv.MoveBottom()

	screen := newTestScreen(t, 40, 4)
	lv.Draw(screen)

	// pr5's first line ("#5") must be visible somewhere on screen.
	found := false
	for y := 0; y < 4; y++ {
		if cellRune(screen, 0, y) == '#' && cellRune(screen, 1, y) == '5' {
			found = true
		}
	}
	if !found {
		t.Errorf("after MoveBottom(), pr5's row is not visible on a 4-row-tall view")
	}
}

func TestListViewMoveHalfPage(t *testing.T) {
	rows := make([]ListRow, 0, 10)
	for i := range 10 {
		rows = append(rows, ListRow{ID: string(rune('a' + i)), Selectable: true, Lines: [][]Span{{{Text: "x"}}}})
	}
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10) // 10 visible lines, all single-line rows
	lv.SetRows(rows)

	lv.MoveHalfPage(1)
	firstID := lv.CurrentID()
	if firstID == "a" {
		t.Fatalf("MoveHalfPage(1) from the top did not move the cursor")
	}

	lv.MoveHalfPage(-1)
	if got := lv.CurrentID(); got != "a" {
		t.Fatalf("MoveHalfPage(-1) after MoveHalfPage(1) = %q, want back at a", got)
	}
}

func TestListViewMoveHalfPageCountsDisplayLinesNotRows(t *testing.T) {
	// Ten 2-line rows in a 10-line-tall view: half a page is 5 display
	// lines, which is only 2-3 rows here, not 5 rows (5 rows would be a
	// full page's worth of PR-list-style two-line items).
	rows := make([]ListRow, 0, 10)
	for i := range 10 {
		rows = append(rows, ListRow{
			ID:         fmt.Sprintf("r%d", i),
			Selectable: true,
			Lines:      [][]Span{{{Text: "line1"}}, {{Text: "line2"}}},
		})
	}
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(rows)

	lv.MoveHalfPage(1)
	if got := lv.Cursor(); got != 3 {
		t.Fatalf("MoveHalfPage(1) over 2-line rows moved to row %d, want 3 (5 display lines / 2 lines per row, rounded up to cover them)", got)
	}

	lv.MoveHalfPage(-1)
	if got := lv.Cursor(); got != 0 {
		t.Fatalf("MoveHalfPage(-1) back = row %d, want 0", got)
	}
}

func TestListViewCursorRowUsesCursorStyle(t *testing.T) {
	screen := newTestScreen(t, 40, 10)
	lv := NewListView()
	lv.SetRect(0, 0, 40, 10)
	lv.SetRows(fixtureRows())

	lv.Draw(screen)

	style := cellStyle(screen, 0, 1) // pr1's first line, the cursor row
	if _, bg, _ := style.Decompose(); bg == tcell.ColorDefault {
		t.Errorf("cursor row style has no explicit background; want the cursor highlight background")
	}
}
