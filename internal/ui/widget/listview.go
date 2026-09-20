package widget

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/theme"
)

// ListRow is one row of a ListView: either a non-selectable heading (for
// example a PR list section header) or a selectable item spanning one or
// two lines, identified by a stable ID used to keep the cursor on the same
// logical row across a SetRows refresh.
type ListRow struct {
	Lines      [][]Span
	Selectable bool
	ID         string
}

// ListView is a scrollable list of ListRows with a single-row cursor over
// the selectable rows only. It embeds *tview.Box for the usual Primitive
// plumbing (rect, border, focus) but never handles key input itself: gprt's
// router calls MoveBy/MoveTop/MoveBottom/MoveHalfPage/Select directly, since
// multi-key sequences and remapping live in internal/ui/keys, not in the
// widget layer.
type ListView struct {
	*tview.Box

	rows []ListRow
	// selectable holds the indices into rows of every selectable row, in
	// display order; cursor indexes into this slice, never into rows
	// directly.
	selectable []int
	cursor     int
	// offset is the index into rows of the first row Draw starts from
	// (the scroll position), kept so the cursor's row always stays fully
	// visible.
	offset int

	changed  func(row ListRow)
	selected func(row ListRow)
}

// NewListView creates an empty ListView.
func NewListView() *ListView {
	return &ListView{Box: tview.NewBox()}
}

// SetRows replaces the displayed rows. If a selectable row shares its ID
// with the previously selected row, the cursor stays on it (by ID, not by
// position); otherwise the cursor clamps to the nearest valid position
// (the row that took its old position, or the last row if the list
// shrank).
func (lv *ListView) SetRows(rows []ListRow) {
	var keepID string
	hadSelection := len(lv.selectable) > 0
	if hadSelection {
		keepID = lv.rows[lv.selectable[lv.cursor]].ID
	}
	oldCursor := lv.cursor

	lv.rows = rows
	lv.selectable = lv.selectable[:0]
	for i, r := range rows {
		if r.Selectable {
			lv.selectable = append(lv.selectable, i)
		}
	}

	lv.cursor = clamp(oldCursor, 0, len(lv.selectable)-1)
	if hadSelection {
		for i, rowIdx := range lv.selectable {
			if rows[rowIdx].ID == keepID {
				lv.cursor = i
				break
			}
		}
	}

	lv.ensureVisible()
	lv.notifyChanged()
}

// Cursor returns the index, among selectable rows only, of the current
// selection.
func (lv *ListView) Cursor() int {
	return lv.cursor
}

// Rows returns the rows most recently passed to SetRows, in display order.
// Callers must not mutate the returned slice or its elements.
func (lv *ListView) Rows() []ListRow {
	return lv.rows
}

// SetCursor moves the cursor to the i-th selectable row, clamping i to the
// valid range.
func (lv *ListView) SetCursor(i int) {
	if len(lv.selectable) == 0 {
		return
	}
	next := clamp(i, 0, len(lv.selectable)-1)
	if next == lv.cursor {
		return
	}
	lv.cursor = next
	lv.ensureVisible()
	lv.notifyChanged()
}

// CurrentID returns the ID of the currently selected row, or "" when the
// list has no selectable rows.
func (lv *ListView) CurrentID() string {
	row, ok := lv.currentRow()
	if !ok {
		return ""
	}
	return row.ID
}

// MoveBy moves the cursor by n selectable rows (negative moves up).
func (lv *ListView) MoveBy(n int) {
	lv.SetCursor(lv.cursor + n)
}

// MoveTop moves the cursor to the first selectable row.
func (lv *ListView) MoveTop() {
	lv.SetCursor(0)
}

// MoveBottom moves the cursor to the last selectable row.
func (lv *ListView) MoveBottom() {
	lv.SetCursor(len(lv.selectable) - 1)
}

// MoveHalfPage moves the cursor by however many selectable rows together
// span roughly half of the view's visible height in display lines (not a
// row count directly: a row can be 1 or 2 lines, so counting rows alone
// would move a full page's worth of 2-line PR rows for what should be a
// half page). dir < 0 moves up, dir > 0 moves down; always moves at least
// one selectable row when one exists in that direction.
func (lv *ListView) MoveHalfPage(dir int) {
	if len(lv.selectable) == 0 {
		return
	}
	_, _, _, height := lv.GetInnerRect()
	target := height / 2
	if target < 1 {
		target = 1
	}

	startRow := lv.selectable[lv.cursor]
	lines, steps := 0, 0
	for i := 1; lines < target; i++ {
		var idx int
		if dir >= 0 {
			idx = startRow + i
		} else {
			idx = startRow - i
		}
		if idx < 0 || idx >= len(lv.rows) {
			break
		}
		lines += len(lv.rows[idx].Lines)
		if lv.rows[idx].Selectable {
			steps++
		}
	}
	if steps == 0 {
		steps = 1
	}
	if dir < 0 {
		steps = -steps
	}
	lv.MoveBy(steps)
}

// SetChangedFunc sets the function invoked whenever the cursor lands on a
// (possibly new) row: on every cursor movement and on SetRows, whenever a
// selectable row exists. Typical use: previewing the highlighted row after
// a short debounce.
func (lv *ListView) SetChangedFunc(fn func(row ListRow)) {
	lv.changed = fn
}

// SetSelectedFunc sets the function Select invokes.
func (lv *ListView) SetSelectedFunc(fn func(row ListRow)) {
	lv.selected = fn
}

// Select invokes the selected callback for the current row. ListView does
// not call this itself — the router calls it in response to whatever key
// is bound to "open" (Enter, l, ...).
func (lv *ListView) Select() {
	if lv.selected == nil {
		return
	}
	if row, ok := lv.currentRow(); ok {
		lv.selected(row)
	}
}

func (lv *ListView) currentRow() (ListRow, bool) {
	if len(lv.selectable) == 0 {
		return ListRow{}, false
	}
	return lv.rows[lv.selectable[lv.cursor]], true
}

func (lv *ListView) notifyChanged() {
	if lv.changed == nil {
		return
	}
	if row, ok := lv.currentRow(); ok {
		lv.changed(row)
	}
}

// ensureVisible adjusts offset so the cursor's row is fully within the
// view's current height, scrolling the minimum amount necessary.
func (lv *ListView) ensureVisible() {
	if len(lv.selectable) == 0 {
		lv.offset = 0
		return
	}
	cursorRow := lv.selectable[lv.cursor]
	if cursorRow < lv.offset {
		lv.offset = cursorRow
		return
	}

	_, _, _, height := lv.GetInnerRect()
	if height <= 0 {
		return
	}
	for lv.offset < cursorRow {
		lines := 0
		for i := lv.offset; i <= cursorRow; i++ {
			lines += len(lv.rows[i].Lines)
		}
		if lines <= height {
			return
		}
		lv.offset++
	}
}

// Draw renders the visible rows, painting the cursor row's background with
// theme.Cursor before drawing its spans (with each span's own background
// overridden to match, so per-span foreground colours remain visible on
// the highlighted bar).
func (lv *ListView) Draw(screen tcell.Screen) {
	lv.DrawForSubclass(screen, lv)
	x, y, width, height := lv.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	cursorRow := -1
	if len(lv.selectable) > 0 {
		cursorRow = lv.selectable[lv.cursor]
	}
	_, cursorBG, _ := theme.Cursor.Decompose()

	line := y
	for i := lv.offset; i < len(lv.rows) && line < y+height; i++ {
		row := lv.rows[i]
		onCursor := i == cursorRow
		for _, spans := range row.Lines {
			if line >= y+height {
				break
			}
			if onCursor {
				for cx := x; cx < x+width; cx++ {
					screen.SetContent(cx, line, ' ', nil, theme.Cursor)
				}
				spans = withBackground(spans, cursorBG)
			}
			DrawSpans(screen, x, line, width, spans)
			line++
		}
	}
}

func withBackground(spans []Span, bg tcell.Color) []Span {
	out := make([]Span, len(spans))
	for i, s := range spans {
		out[i] = Span{Text: s.Text, Style: s.Style.Background(bg)}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
