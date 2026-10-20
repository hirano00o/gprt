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
	// lineOff is how many leading display lines of the cursor's row are
	// scrolled off above the view. It is only ever nonzero when the cursor
	// row is the first drawn row (offset == cursorRow) and taller than the
	// inner height: rows are otherwise scrolled whole, but a single row
	// taller than the view (a long PR description is one block) would never
	// have its tail reachable without it.
	lineOff int

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
	keptRow := false
	if hadSelection {
		for i, rowIdx := range lv.selectable {
			if rows[rowIdx].ID == keepID {
				lv.cursor = i
				keptRow = true
				break
			}
		}
	}
	// A refresh or width-change rebuild of the same row keeps the reading
	// position (ensureVisible clamps it if the row shrank); landing on a
	// different row starts that row from its top.
	if !keptRow {
		lv.lineOff = 0
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
	lv.lineOff = 0
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

// MoveBy moves the cursor by n selectable rows (negative moves up). While
// the cursor row is taller than the view and not yet scrolled to its end in
// that direction, it scrolls the row by n display lines instead.
func (lv *ListView) MoveBy(n int) {
	if lv.scrollWithinRow(n) {
		return
	}
	lv.moveRows(n)
}

// moveRows moves the cursor by n selectable rows. Moving up onto a tall row
// shows its end, since that is the edge the reader arrives at.
func (lv *ListView) moveRows(n int) {
	before := lv.cursor
	lv.SetCursor(lv.cursor + n)
	if n < 0 && lv.cursor != before {
		lv.showRowEnd()
	}
}

// rowHeights returns the cursor row's display line count and the view's
// inner height; ok is false when there is no cursor row or the row fits in
// the view (height <= 0, before the first layout, counts as fitting).
func (lv *ListView) rowHeights() (rowLines, height int, ok bool) {
	if len(lv.selectable) == 0 {
		return 0, 0, false
	}
	_, _, _, height = lv.GetInnerRect()
	rowLines = len(lv.rows[lv.selectable[lv.cursor]].Lines)
	return rowLines, height, height > 0 && rowLines > height
}

// scrollWithinRow scrolls the cursor row by up to n display lines (negative
// scrolls up) when the row is taller than the view, and reports whether it
// moved. A partial scroll consumes the whole count: the leftover is not
// carried into row movement, so one keypress never both scrolls and jumps.
func (lv *ListView) scrollWithinRow(n int) bool {
	lv.ensureVisible() // the view may have been resized since the last move
	rowLines, height, tall := lv.rowHeights()
	if !tall {
		return false
	}
	next := clamp(lv.lineOff+n, 0, rowLines-height)
	if next == lv.lineOff {
		return false
	}
	lv.lineOff = next
	return true
}

// showRowEnd scrolls a tall cursor row so its last line is the view's last
// line; rows that fit are left alone.
func (lv *ListView) showRowEnd() {
	if rowLines, height, tall := lv.rowHeights(); tall {
		lv.lineOff = rowLines - height
	}
}

// MoveTop moves the cursor to the first selectable row and shows its top
// even when the cursor was already there.
func (lv *ListView) MoveTop() {
	lv.SetCursor(0)
	lv.lineOff = 0
}

// MoveBottom moves the cursor to the last selectable row and shows its end.
func (lv *ListView) MoveBottom() {
	lv.SetCursor(len(lv.selectable) - 1)
	lv.showRowEnd()
}

// MoveHalfPage moves the cursor by however many selectable rows together
// span roughly half of the view's visible height in display lines (not a
// row count directly: a row can be 1 or 2 lines, so counting rows alone
// would move a full page's worth of 2-line PR rows for what should be a
// half page). dir < 0 moves up, dir > 0 moves down; always moves at least
// one selectable row when one exists in that direction.
func (lv *ListView) MoveHalfPage(dir int) {
	_, _, _, height := lv.GetInnerRect()
	lv.moveByDisplayLines(dir, height/2)
}

// MovePage moves the cursor by a full page instead of half; otherwise
// identical to MoveHalfPage.
func (lv *ListView) MovePage(dir int) {
	_, _, _, height := lv.GetInnerRect()
	lv.moveByDisplayLines(dir, height)
}

// moveByDisplayLines is MoveHalfPage/MovePage's shared walk: it moves the
// cursor by however many selectable rows together span roughly target
// display lines. dir < 0 moves up, dir > 0 moves down; always moves at
// least one selectable row when one exists in that direction. A tall cursor
// row is scrolled by target lines first, like MoveBy.
func (lv *ListView) moveByDisplayLines(dir, target int) {
	if len(lv.selectable) == 0 {
		return
	}
	if target < 1 {
		target = 1
	}
	if dir < 0 {
		if lv.scrollWithinRow(-target) {
			return
		}
	} else if lv.scrollWithinRow(target) {
		return
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
	lv.moveRows(steps)
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
// view's current height, scrolling the minimum amount necessary, then
// normalizes lineOff: it survives only for a tall cursor row that is the
// first drawn row, clamped so the view never scrolls past the row's end.
func (lv *ListView) ensureVisible() {
	if len(lv.selectable) == 0 {
		lv.offset = 0
		lv.lineOff = 0
		return
	}
	cursorRow := lv.selectable[lv.cursor]
	if cursorRow < lv.offset {
		lv.offset = cursorRow
	} else if _, _, _, height := lv.GetInnerRect(); height > 0 {
		for lv.offset < cursorRow {
			lines := 0
			for i := lv.offset; i <= cursorRow; i++ {
				lines += len(lv.rows[i].Lines)
			}
			if lines <= height {
				break
			}
			lv.offset++
		}
	}

	rowLines, height, tall := lv.rowHeights()
	if tall && lv.offset == cursorRow {
		lv.lineOff = clamp(lv.lineOff, 0, rowLines-height)
	} else {
		lv.lineOff = 0
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
		lines := row.Lines
		if onCursor && i == lv.offset {
			lines = lines[min(lv.lineOff, len(lines)):]
		}
		for _, spans := range lines {
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
