// diffview.go implements DiffView, the Files tab's right-hand pane: a
// unified diff (file header, hunk headers, add/context/delete lines with
// gutters and syntax highlighting) with inline, foldable review-thread
// blocks and vim-style navigation (cursor, visual line selection,
// horizontal scroll). Like ListView/DetailView, it never handles key input
// itself — internal/ui's router calls its movement/fold/selection methods
// directly.
package widget

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// DiffFile is the data DiffView renders: one changed file's status, parsed
// hunks, per-hunk syntax tokens (nil until highlighting completes), and the
// review threads anchored to it.
type DiffFile struct {
	Path         string
	PreviousPath string
	Status       model.FileStatus
	Additions    int
	Deletions    int
	// HasPatch is false for a binary or too-large file, which GitHub never
	// returns a patch for; Hunks/Tokens are meaningless in that case.
	HasPatch bool
	Hunks    []diff.Hunk
	// Tokens holds one slice of per-line tokens per hunk (hunk index -> line
	// index -> tokens), mirroring internal/store's FileEntry.Tokens. A nil
	// (or too-short) entry falls back to plain, unhighlighted text.
	Tokens [][][]highlight.Token
	// Threads are the review threads on this file: a subset of the current
	// pull request's ReviewThreads matching Path (internal/ui's Files tab
	// filters by path before calling SetFile).
	Threads []model.ReviewThread
	// Loading reports that the file's content (or its containing page) is
	// still being fetched: Hunks/Tokens are not rendered while true, even
	// if present from a previous SetFile call, since they may not reflect
	// this file at all yet.
	Loading bool
	Err     error
	// DraftLines marks which lines have a saved comment draft anchored to
	// them, keyed by their (hunk, line) index into Hunks — internal/ui
	// computes this once per file open/draft save/delete (never per Draw;
	// see docs/DESIGN.md), by parsing its own draft anchors back into a
	// location (see internal/ui's draft anchor helpers). Empty/nil means no
	// drafts for this file.
	DraftLines map[DraftAnchor]bool
	// DraftMarker is the gutter glyph drawn on a DraftLines line, from the
	// configured icon set (theme.Icons.DraftMarker). Empty disables the
	// marker column's glyph, but the column itself is always reserved so
	// every line's gutter stays the same width.
	DraftMarker string
}

// DraftAnchor identifies one diff line (by its Hunk/Line index into
// DiffFile.Hunks, the same index pair diffRow.hunk/diffRow.line use) that
// has a saved comment draft, for DiffFile.DraftLines.
type DraftAnchor struct {
	Hunk, Line int
}

// FileStatusGlyph returns the single-character glyph DiffView's file header
// and the Files tab's tree both use for a file's status, matching
// well-known git/GitHub short status codes.
func FileStatusGlyph(s model.FileStatus) string {
	switch s {
	case model.FileStatusAdded:
		return "A"
	case model.FileStatusRemoved:
		return "D"
	case model.FileStatusModified:
		return "M"
	case model.FileStatusRenamed:
		return "R"
	case model.FileStatusCopied:
		return "C"
	case model.FileStatusChanged:
		return "~"
	default:
		return " "
	}
}

// diffRowKind distinguishes the kinds of row DiffView's rebuild produces.
type diffRowKind int

const (
	rowKindFileHeader diffRowKind = iota
	rowKindOutdatedHeader
	rowKindHunkHeader
	rowKindLine
	rowKindThreadHeader
	rowKindThreadComment
)

// diffRow is one row of DiffView's rebuilt content. Only rowKindLine and
// rowKindThreadHeader are selectable. rowKindLine keeps its gutter (line
// numbers + marker, never horizontally scrolled) and content (syntax spans,
// scrolled by hOffset) separate so Draw can apply the horizontal-scroll
// offset to the content only; every other kind uses the generic,
// possibly-multi-line lines field instead.
type diffRow struct {
	kind diffRowKind

	// hunk/line identify a rowKindLine row's position in DiffFile.Hunks, and
	// are otherwise unused (zero).
	hunk, line int
	lineKind   diff.LineKind

	// threadID identifies a rowKindThreadHeader row's thread, for
	// SetThreadFolded/ToggleFoldAtCursor/CursorThread and for restoring the
	// cursor by identity across a rebuild.
	threadID string

	gutter  []Span
	content []Span

	lines [][]Span
}

func (r diffRow) selectable() bool {
	return r.kind == rowKindLine || r.kind == rowKindThreadHeader
}

// lineCount is how many physical screen lines r occupies.
func (r diffRow) lineCount() int {
	if r.kind == rowKindLine {
		return 1
	}
	return len(r.lines)
}

// diffRowKey identifies a selectable row's logical identity, stable across
// a rebuild (unlike its index into rows, which shifts whenever folds or
// thread placement change row counts) — the same role widget.ListRow.ID
// plays for ListView.
type diffRowKey struct {
	isThread bool
	hunk     int
	line     int
	threadID string
}

func keyForRow(r diffRow) diffRowKey {
	if r.kind == rowKindThreadHeader {
		return diffRowKey{isThread: true, threadID: r.threadID}
	}
	return diffRowKey{hunk: r.hunk, line: r.line}
}

// DiffView is the Files tab's diff pane: a custom tview primitive (never a
// plain TextView, since it needs a logical, re-resolvable cursor, per-line
// backgrounds, and horizontal scrolling that a TextView cannot provide).
type DiffView struct {
	*tview.Box

	file   DiffFile
	folded map[string]bool

	rows       []diffRow
	selectable []int
	cursor     int
	offset     int
	lastWidth  int

	// curLineHunk/curLineLine track the most recent rowKindLine the cursor
	// actually rested on (updated by refreshLineTracking, called after every
	// cursor movement and rebuild). Selection uses this as visual mode's
	// "current" endpoint even while the cursor has since moved onto a
	// non-line row (a thread header, most commonly reached via
	// NextThread/PrevThread while a selection is open) — see Selection's
	// own doc comment. -1 means "no line seen yet" (an empty file).
	curLineHunk, curLineLine int

	inVisual                           bool
	visualAnchorHunk, visualAnchorLine int

	hOffset    int
	maxHOffset int

	// resetCursor tells the next rebuild to skip its usual by-identity
	// cursor restore (diffRowKey) entirely, set by SetFile when the file's
	// own path changes — see its doc comment.
	resetCursor bool

	// pendingCursorSet/Side/Line name a diff-side line the next (and, while
	// the file is still loading, every following) rebuild should move the
	// cursor to once it exists — see JumpToLine.
	pendingCursorSet  bool
	pendingCursorSide model.DiffSide
	pendingCursorLine int
}

// JumpToLine arranges for the cursor to move to the line anchored at
// (side, line) — the same anchor a diff.Anchor/diff.RangeAnchor call, or a
// review thread's own Side/Line, would report — as soon as the current
// file's hunks contain one. Unlike the identity-based cursor restore
// rebuild otherwise performs, the target here survives across rebuilds
// while DiffFile.Loading stays true (the file's data has not arrived yet:
// used by the "p" pending-list dialog to resolve a line/range draft's
// anchor once its file has loaded), and is dropped once loading finishes —
// found or not, so a target that never resolves (an outdated draft whose
// line no longer exists) does not keep retrying on every future,
// unrelated rebuild.
func (dv *DiffView) JumpToLine(side model.DiffSide, line int) {
	dv.pendingCursorSet = true
	dv.pendingCursorSide = side
	dv.pendingCursorLine = line
	dv.lastWidth = -1
}

// NewDiffView creates an empty DiffView.
func NewDiffView() *DiffView {
	return &DiffView{
		Box:         tview.NewBox(),
		folded:      map[string]bool{},
		lastWidth:   -1,
		curLineHunk: -1,
		curLineLine: -1,
	}
}

// SetFile replaces the displayed file and forces the next Draw to rebuild
// its rows. Folded state is kept per thread ID across calls (see the
// folded field): a thread ID seen for the first time defaults to folded
// when already resolved, open otherwise; a thread ID already present in
// the map (from an earlier SetFile, possibly for the same file re-rendered
// after highlighting completed) keeps whatever the user last chose.
func (dv *DiffView) SetFile(f DiffFile) {
	if f.Path != dv.file.Path {
		// A cursor/selection position is only ever meaningful relative to
		// the file it was set on: diffRowKey is positional (hunk/line
		// indices, or a thread ID), so restoring it by identity against a
		// *different* file could land on an arbitrary, unrelated row, and
		// a leftover Selection() would silently report the new file's own
		// lines as still selected. resetCursor tells rebuild to skip that
		// identity-based restore entirely for this rebuild, in addition to
		// resetting everything else here that rebuild does not own.
		dv.EndVisual()
		dv.cursor, dv.offset, dv.hOffset = 0, 0, 0
		dv.curLineHunk, dv.curLineLine = -1, -1
		dv.resetCursor = true
	}
	dv.file = f
	for _, t := range f.Threads {
		if _, ok := dv.folded[t.ID]; !ok {
			dv.folded[t.ID] = t.IsResolved
		}
	}
	dv.lastWidth = -1
}

// SetThreadFolded sets id's folded state directly (used by the Files tab
// after opening the thread comment URL, for example, though today only
// ToggleFoldAtCursor/FoldAll drive it).
func (dv *DiffView) SetThreadFolded(id string, folded bool) {
	dv.folded[id] = folded
	dv.lastWidth = -1
}

// FoldAll sets every known thread's folded state to folded (zM folds all,
// zR unfolds all).
func (dv *DiffView) FoldAll(folded bool) {
	for _, t := range dv.file.Threads {
		dv.folded[t.ID] = folded
	}
	dv.lastWidth = -1
}

// ToggleFoldAtCursor toggles the folded state of the thread under the
// cursor (za); a no-op when the cursor is not on a thread header row.
func (dv *DiffView) ToggleFoldAtCursor() {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindThreadHeader {
		return
	}
	dv.folded[row.threadID] = !dv.folded[row.threadID]
	dv.lastWidth = -1
}

// currentRow returns the row under the cursor, if any.
func (dv *DiffView) currentRow() (diffRow, bool) {
	if len(dv.selectable) == 0 {
		return diffRow{}, false
	}
	return dv.rows[dv.selectable[dv.cursor]], true
}

// setCursor moves the cursor to the i-th selectable row, clamping i, and
// refreshes curLineHunk/curLineLine.
func (dv *DiffView) setCursor(i int) {
	if len(dv.selectable) == 0 {
		return
	}
	dv.cursor = clamp(i, 0, len(dv.selectable)-1)
	dv.ensureVisible()
	dv.refreshLineTracking()
}

func (dv *DiffView) refreshLineTracking() {
	if row, ok := dv.currentRow(); ok && row.kind == rowKindLine {
		dv.curLineHunk, dv.curLineLine = row.hunk, row.line
	}
}

// MoveBy moves the cursor by n selectable rows (negative moves up).
func (dv *DiffView) MoveBy(n int) { dv.setCursor(dv.cursor + n) }

// MoveTop moves the cursor to the first selectable row.
func (dv *DiffView) MoveTop() { dv.setCursor(0) }

// MoveBottom moves the cursor to the last selectable row.
func (dv *DiffView) MoveBottom() { dv.setCursor(len(dv.selectable) - 1) }

// MoveHalfPage moves the cursor by however many selectable rows together
// span roughly half of the view's visible height in display lines,
// matching widget.ListView.MoveHalfPage's own semantics.
func (dv *DiffView) MoveHalfPage(dir int) {
	if len(dv.selectable) == 0 {
		return
	}
	_, _, _, height := dv.GetInnerRect()
	target := height / 2
	if target < 1 {
		target = 1
	}

	startRow := dv.selectable[dv.cursor]
	lines, steps := 0, 0
	for i := 1; lines < target; i++ {
		var idx int
		if dir >= 0 {
			idx = startRow + i
		} else {
			idx = startRow - i
		}
		if idx < 0 || idx >= len(dv.rows) {
			break
		}
		lines += dv.rows[idx].lineCount()
		if dv.rows[idx].selectable() {
			steps++
		}
	}
	if steps == 0 {
		steps = 1
	}
	if dir < 0 {
		steps = -steps
	}
	dv.MoveBy(steps)
}

// NextThread moves the cursor to the next thread-header row after the
// current position, if any (]c); a no-op at the last one.
func (dv *DiffView) NextThread() {
	for i := dv.cursor + 1; i < len(dv.selectable); i++ {
		if dv.rows[dv.selectable[i]].kind == rowKindThreadHeader {
			dv.setCursor(i)
			return
		}
	}
}

// PrevThread moves the cursor to the previous thread-header row before the
// current position, if any ([c); a no-op at the first one.
func (dv *DiffView) PrevThread() {
	for i := dv.cursor - 1; i >= 0; i-- {
		if dv.rows[dv.selectable[i]].kind == rowKindThreadHeader {
			dv.setCursor(i)
			return
		}
	}
}

// StartVisual begins a visual line selection anchored at the cursor's
// current line; a no-op when the cursor is not on a diff line (for example
// a thread header), per the "V on a thread row is a no-op" rule.
func (dv *DiffView) StartVisual() {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindLine {
		return
	}
	dv.inVisual = true
	dv.visualAnchorHunk, dv.visualAnchorLine = row.hunk, row.line
}

// EndVisual cancels an active visual selection (Esc).
func (dv *DiffView) EndVisual() { dv.inVisual = false }

// InVisual reports whether a visual selection is currently active.
func (dv *DiffView) InVisual() bool { return dv.inVisual }

// Selection returns the contiguous run of diff.Lines currently selected in
// visual mode, from the anchor to curLineHunk/curLineLine (the most recent
// line the cursor actually rested on — see those fields' doc comment for
// why this, rather than the literal current row, is used: it lets the
// selection keep behaving sensibly even if the cursor has since moved onto
// a non-line row). A selection that would cross into a different hunk than
// the anchor's is clamped to that hunk's own first or last line instead
// (GitHub range comments cannot span hunks). ok is false when no visual
// selection is active.
func (dv *DiffView) Selection() ([]diff.Line, bool) {
	hunk, lo, hi, ok := dv.selectionRange()
	if !ok {
		return nil, false
	}
	return dv.file.Hunks[hunk].Lines[lo : hi+1], true
}

// selectionRange computes the active visual selection's anchor hunk index
// and its clamped, inclusive [lo, hi] *line-index* range (into that hunk's
// own Lines slice) — the shared logic behind both Selection() (which slices
// Lines with it) and Draw's per-row highlighting (which compares a row's
// own index-based line field against it): both must derive the range the
// same way, since diff.Line.OldNo/NewNo are file line numbers, not Lines
// indices, and comparing a row index against a line number would silently
// misalign whenever OldStart != 1 or the range's first line has no old
// number (an Add line). ok is false when no visual selection is active.
func (dv *DiffView) selectionRange() (hunk, lo, hi int, ok bool) {
	if !dv.inVisual || dv.curLineHunk < 0 {
		return 0, 0, 0, false
	}
	anchorHunk, anchorLine := dv.visualAnchorHunk, dv.visualAnchorLine
	curHunk, curLine := dv.curLineHunk, dv.curLineLine

	if anchorHunk < 0 || anchorHunk >= len(dv.file.Hunks) {
		return 0, 0, 0, false
	}
	hunkLines := dv.file.Hunks[anchorHunk].Lines

	switch {
	case curHunk > anchorHunk:
		curLine = len(hunkLines) - 1
	case curHunk < anchorHunk:
		curLine = 0
	}

	lo, hi = anchorLine, curLine
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 || hi >= len(hunkLines) {
		return 0, 0, 0, false
	}
	return anchorHunk, lo, hi, true
}

// CursorLine returns the diff.Line under the cursor, if the cursor is
// currently on a line row.
func (dv *DiffView) CursorLine() (diff.Line, bool) {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindLine {
		return diff.Line{}, false
	}
	if row.hunk < 0 || row.hunk >= len(dv.file.Hunks) {
		return diff.Line{}, false
	}
	h := dv.file.Hunks[row.hunk]
	if row.line < 0 || row.line >= len(h.Lines) {
		return diff.Line{}, false
	}
	return h.Lines[row.line], true
}

// CursorThread returns the review thread under the cursor, if the cursor is
// currently on a thread header row.
func (dv *DiffView) CursorThread() (model.ReviewThread, bool) {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindThreadHeader {
		return model.ReviewThread{}, false
	}
	for _, t := range dv.file.Threads {
		if t.ID == row.threadID {
			return t, true
		}
	}
	return model.ReviewThread{}, false
}

// ScrollHorizontal adjusts the diff content's horizontal scroll position by
// delta cells (zh/zl), clamped to [0, the longest line's overflow past the
// last-drawn content width].
func (dv *DiffView) ScrollHorizontal(delta int) {
	dv.hOffset = clamp(dv.hOffset+delta, 0, dv.maxHOffset)
}

// HOffset returns the diff content's current horizontal scroll position, in
// cells (0 when not scrolled).
func (dv *DiffView) HOffset() int { return dv.hOffset }

// ensureVisible adjusts offset so the cursor's row is fully within the
// view's current height, matching widget.ListView.ensureVisible.
func (dv *DiffView) ensureVisible() {
	if len(dv.selectable) == 0 {
		dv.offset = 0
		return
	}
	cursorRow := dv.selectable[dv.cursor]
	if cursorRow < dv.offset {
		dv.offset = cursorRow
		return
	}

	_, _, _, height := dv.GetInnerRect()
	if height <= 0 {
		return
	}
	for dv.offset < cursorRow {
		lines := 0
		for i := dv.offset; i <= cursorRow; i++ {
			lines += dv.rows[i].lineCount()
		}
		if lines <= height {
			return
		}
		dv.offset++
	}
}

// Draw rebuilds rows (when the inner width has changed since the last
// rebuild — see rebuild) and renders the visible rows.
func (dv *DiffView) Draw(screen tcell.Screen) {
	dv.DrawForSubclass(screen, dv)
	x, y, width, height := dv.GetInnerRect()
	if width != dv.lastWidth {
		dv.rebuild(width)
		dv.lastWidth = width
	}
	if width <= 0 || height <= 0 {
		return
	}

	cursorRowIdx := -1
	if len(dv.selectable) > 0 {
		cursorRowIdx = dv.selectable[dv.cursor]
	}
	selHunk, selLo, selHi, hasSel := dv.selectionRange()

	line := y
	for i := dv.offset; i < len(dv.rows) && line < y+height; i++ {
		row := dv.rows[i]
		isCursorRow := i == cursorRowIdx
		isSelectedRow := hasSel && row.kind == rowKindLine && row.hunk == selHunk && row.line >= selLo && row.line <= selHi

		bgStyle := theme.DiffLineStyle(row.lineKind, isCursorRow, isSelectedRow)
		_, bg, _ := bgStyle.Decompose()

		n := row.lineCount()
		for pl := 0; pl < n && line < y+height; pl++ {
			for cx := x; cx < x+width; cx++ {
				screen.SetContent(cx, line, ' ', nil, bgStyle)
			}
			if row.kind == rowKindLine {
				dv.drawLineRow(screen, x, line, width, row, bg)
			} else if pl < len(row.lines) {
				DrawSpans(screen, x, line, width, withBackground(row.lines[pl], bg))
			}
			line++
		}
	}
}

// drawLineRow draws one rowKindLine row: its gutter (never scrolled), then
// its content with the horizontal-scroll offset applied, with a "‹" marker
// when scrolled.
func (dv *DiffView) drawLineRow(screen tcell.Screen, x, y, width int, row diffRow, bg tcell.Color) {
	gutterSpans := withBackground(row.gutter, bg)
	gw := SpanWidth(row.gutter)
	if gw > width {
		gw = width
	}
	DrawSpans(screen, x, y, gw, gutterSpans)
	if gw >= width {
		return
	}

	contentX := x + gw
	contentWidth := width - gw
	if dv.hOffset > 0 {
		markerStyle := theme.Muted.Background(bg)
		screen.SetContent(contentX, y, '‹', nil, markerStyle)
		contentX++
		contentWidth--
	}
	if contentWidth <= 0 {
		return
	}
	DrawSpansOffset(screen, contentX, y, contentWidth, dv.hOffset, withBackground(row.content, bg))
}

// rebuild recomputes every row of the current file at width, preserving the
// cursor by logical identity (see diffRowKey) the same way
// widget.ListView.SetRows preserves it by ListRow.ID.
func (dv *DiffView) rebuild(width int) {
	if width < 1 {
		width = 1
	}

	var oldKey diffRowKey
	hadSelection := len(dv.selectable) > 0 && !dv.resetCursor
	if hadSelection {
		oldKey = keyForRow(dv.rows[dv.selectable[dv.cursor]])
	}
	oldCursor := dv.cursor
	dv.resetCursor = false

	b := &diffBuilder{dv: dv, width: width}
	b.build()

	dv.rows = b.rows
	dv.selectable = dv.selectable[:0]
	for i, r := range dv.rows {
		if r.selectable() {
			dv.selectable = append(dv.selectable, i)
		}
	}
	dv.maxHOffset = maxHOffsetFor(b.longestContent, width, b.gutterWidth)
	dv.hOffset = clamp(dv.hOffset, 0, dv.maxHOffset)

	dv.cursor = clamp(oldCursor, 0, len(dv.selectable)-1)
	if hadSelection {
		for i, idx := range dv.selectable {
			if keyForRow(dv.rows[idx]) == oldKey {
				dv.cursor = i
				break
			}
		}
	}
	dv.applyPendingCursor()
	dv.ensureVisible()
	dv.refreshLineTracking()
}

// applyPendingCursor resolves a JumpToLine target against the just-rebuilt
// rows, if one is pending, taking priority over rebuild's own by-identity
// restore above (a caller requesting a jump wants it honoured even when a
// selectable row of the same identity as the old cursor also still exists).
// See JumpToLine's own doc comment for why the target survives across
// rebuilds while the file is still loading, and is dropped once it is not,
// regardless of whether it was ever found.
func (dv *DiffView) applyPendingCursor() {
	if !dv.pendingCursorSet {
		return
	}
	if hi, li, ok := diff.LocateLine(dv.file.Hunks, dv.pendingCursorSide, dv.pendingCursorLine); ok {
		for i, idx := range dv.selectable {
			r := dv.rows[idx]
			if r.kind == rowKindLine && r.hunk == hi && r.line == li {
				dv.cursor = i
				dv.pendingCursorSet = false
				break
			}
		}
	}
	if !dv.file.Loading {
		dv.pendingCursorSet = false
	}
}

// contentWidthFor returns the display width available for a diff line's
// content once its fixed-width gutter (line numbers + marker + the draft
// marker column + one separating space) is subtracted, never less than 1.
func contentWidthFor(width, gutterWidth int) int {
	gutterTotal := gutterWidth*2 + 5 // "%*d %*d %s %s " layout, see gutterSpansFor
	w := width - gutterTotal
	if w < 1 {
		w = 1
	}
	return w
}

// maxHOffsetFor returns the furthest hOffset that still leaves at least one
// content column on screen: drawLineRow only ever spends a cell on the "‹"
// marker once hOffset > 0, so the content actually visible while scrolled is
// contentWidthFor(width, gutterWidth)-1 cells wide, one narrower than the
// unscrolled (hOffset == 0) case contentWidthFor itself describes. Without
// the "+1" below, the longest line's very last column would never scroll
// into view — hOffset would already be clamped one cell short of it.
func maxHOffsetFor(longestContent, width, gutterWidth int) int {
	overflow := longestContent - contentWidthFor(width, gutterWidth)
	if overflow <= 0 {
		return 0
	}
	return overflow + 1
}

// diffBuilder accumulates rows for one rebuild pass.
type diffBuilder struct {
	dv    *DiffView
	width int

	rows           []diffRow
	gutterWidth    int
	longestContent int
}

func (b *diffBuilder) build() {
	f := b.dv.file
	b.gutterWidth = gutterDigitWidth(f.Hunks)

	b.rows = append(b.rows, b.fileHeaderRow(f))

	outdated, fileThreads, inline := bucketThreads(f)

	if len(outdated) > 0 {
		b.rows = append(b.rows, diffRow{kind: rowKindOutdatedHeader, lines: [][]Span{{{Text: "Outdated threads", Style: theme.Muted}}}})
		for _, t := range outdated {
			b.appendThread(t)
		}
	}
	for _, t := range fileThreads {
		b.appendThread(t)
	}

	if !f.Loading && f.Err == nil && f.HasPatch {
		for hi, h := range f.Hunks {
			b.rows = append(b.rows, b.hunkHeaderRow(h))
			for li, l := range h.Lines {
				b.rows = append(b.rows, b.lineRow(f, hi, li, l))
				for _, t := range inline[hunkLineKey{hi, li}] {
					b.appendThread(t)
				}
			}
		}
	}
}

type hunkLineKey struct{ hunk, line int }

// bucketThreads splits f.Threads into: outdated/unlocatable (rendered in
// the "Outdated threads" block), file-level (rendered right after the file
// header), and inline (keyed by the hunk/line they anchor to).
func bucketThreads(f DiffFile) (outdated, fileThreads []model.ReviewThread, inline map[hunkLineKey][]model.ReviewThread) {
	inline = map[hunkLineKey][]model.ReviewThread{}
	for _, t := range f.Threads {
		switch t.SubjectType {
		case model.ThreadSubjectFile:
			fileThreads = append(fileThreads, t)
		default:
			hi, li, ok := diff.LocateThread(f.Hunks, t)
			if !ok {
				outdated = append(outdated, t)
				continue
			}
			key := hunkLineKey{hi, li}
			inline[key] = append(inline[key], t)
		}
	}
	return outdated, fileThreads, inline
}

// fileHeaderRow renders the status glyph, path (with a rename note), diff
// stats, and — when applicable — a loading/error/no-patch line.
func (b *diffBuilder) fileHeaderRow(f DiffFile) diffRow {
	if f.Path == "" {
		// Nothing selected at all (no file open, possibly no pull request
		// either): the usual glyph/stats header and its !HasPatch fallback
		// ("Patch not available") would both misrepresent a non-existent
		// file as a real, patchless one.
		msg := "No file selected"
		switch {
		case f.Err != nil:
			msg = f.Err.Error()
		case f.Loading:
			msg = "Loading…"
		}
		return diffRow{kind: rowKindFileHeader, lines: [][]Span{{{Text: msg, Style: theme.Muted}}}}
	}

	title := fmt.Sprintf("%s %s", FileStatusGlyph(f.Status), f.Path)
	if f.Status == model.FileStatusRenamed && f.PreviousPath != "" && f.PreviousPath != f.Path {
		title += fmt.Sprintf(" (renamed from %s)", f.PreviousPath)
	}
	title += fmt.Sprintf("  +%d −%d", f.Additions, f.Deletions)

	lines := [][]Span{{{Text: title, Style: theme.Base.Bold(true)}}}
	switch {
	case f.Err != nil:
		lines = append(lines, []Span{{Text: "Error: " + f.Err.Error(), Style: theme.Error}})
	case f.Loading:
		lines = append(lines, []Span{{Text: "Loading…", Style: theme.Muted}})
	case !f.HasPatch:
		lines = append(lines, []Span{{Text: "Patch not available (binary or too large)", Style: theme.Muted}})
	}
	return diffRow{kind: rowKindFileHeader, lines: lines}
}

func (b *diffBuilder) hunkHeaderRow(h diff.Hunk) diffRow {
	text := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		text += " " + h.Section
	}
	return diffRow{kind: rowKindHunkHeader, lines: [][]Span{{{Text: text, Style: theme.Muted}}}}
}

func (b *diffBuilder) lineRow(f DiffFile, hunkIdx, lineIdx int, l diff.Line) diffRow {
	row := diffRow{kind: rowKindLine, hunk: hunkIdx, line: lineIdx, lineKind: l.Kind}
	draftMark := ""
	if f.DraftLines[DraftAnchor{Hunk: hunkIdx, Line: lineIdx}] {
		draftMark = f.DraftMarker
	}
	row.gutter = gutterSpansFor(l, b.gutterWidth, draftMark)
	row.content = contentSpansFor(f, hunkIdx, lineIdx, l)
	if w := SpanWidth(row.content); w > b.longestContent {
		b.longestContent = w
	}
	return row
}

// gutterSpansFor renders one line's "old-no new-no marker draft-mark "
// gutter, each number right-aligned to width digits and blank on the side
// the line has no number for (an Add line has no old number; a Del line has
// no new one). draftMark is the draft-gutter glyph for this line ("" for
// none — see DiffFile.DraftLines/DraftMarker); the column itself is always
// reserved, blank otherwise, so every line's gutter stays the same width.
func gutterSpansFor(l diff.Line, width int, draftMark string) []Span {
	oldStr := blankOrNumber(l.Kind != diff.Add, l.OldNo, width)
	newStr := blankOrNumber(l.Kind != diff.Del, l.NewNo, width)

	marker, markerStyle := " ", theme.Base
	switch l.Kind {
	case diff.Add:
		marker, markerStyle = "+", theme.Success
	case diff.Del:
		marker, markerStyle = "-", theme.Error
	}

	draftCol := " "
	if draftMark != "" {
		draftCol = draftMark
	}

	return []Span{
		{Text: oldStr, Style: theme.Muted},
		{Text: " ", Style: theme.Muted},
		{Text: newStr, Style: theme.Muted},
		{Text: " ", Style: theme.Muted},
		{Text: marker, Style: markerStyle},
		{Text: draftCol, Style: theme.Accent},
		{Text: " ", Style: theme.Base},
	}
}

func blankOrNumber(present bool, n, width int) string {
	if !present {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, n)
}

// contentSpansFor renders one line's content: syntax-highlighted spans from
// DiffFile.Tokens when present for (hunkIdx, lineIdx), otherwise the raw
// line text in the plain base style.
func contentSpansFor(f DiffFile, hunkIdx, lineIdx int, l diff.Line) []Span {
	if hunkIdx < len(f.Tokens) && lineIdx < len(f.Tokens[hunkIdx]) {
		tokens := f.Tokens[hunkIdx][lineIdx]
		if tokens != nil {
			spans := make([]Span, len(tokens))
			for i, t := range tokens {
				spans[i] = Span{Text: t.Text, Style: theme.TokenStyle(t.Type)}
			}
			return spans
		}
	}
	return []Span{{Text: l.Text, Style: theme.Base}}
}

// gutterDigitWidth returns the number of digits needed to right-align the
// largest old/new line number across every hunk, at least 1.
func gutterDigitWidth(hunks []diff.Hunk) int {
	max := 0
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.OldNo > max {
				max = l.OldNo
			}
			if l.NewNo > max {
				max = l.NewNo
			}
		}
	}
	if max == 0 {
		return 1
	}
	return len(strconv.Itoa(max))
}

// appendThread appends t's header row (selectable) and, when unfolded, one
// row per comment.
func (b *diffBuilder) appendThread(t model.ReviewThread) {
	b.rows = append(b.rows, diffRow{
		kind:     rowKindThreadHeader,
		threadID: t.ID,
		lines:    [][]Span{threadHeaderSpans(t, b.dv.folded[t.ID])},
	})
	if b.dv.folded[t.ID] {
		return
	}
	for _, c := range t.Comments {
		b.rows = append(b.rows, diffRow{kind: rowKindThreadComment, lines: commentLines(c, b.width)})
	}
}

// threadHeaderSpans renders "▸/▾ @author · <relative> · status · N replies".
func threadHeaderSpans(t model.ReviewThread, folded bool) []Span {
	glyph := "▾"
	if folded {
		glyph = "▸"
	}
	author := "unknown"
	var created time.Time
	if len(t.Comments) > 0 {
		if t.Comments[0].Author.Login != "" {
			author = t.Comments[0].Author.Login
		}
		created = t.Comments[0].CreatedAt
	}

	var status []string
	if t.IsResolved {
		status = append(status, "resolved")
	}
	if t.IsOutdated {
		status = append(status, "outdated")
	}
	if len(status) == 0 {
		status = append(status, "open")
	}

	replies := len(t.Comments) - 1
	if replies < 0 {
		replies = 0
	}

	text := fmt.Sprintf("%s @%s · %s · %s · %d replies", glyph, author, relativeTimeShort(created), strings.Join(status, ", "), replies)
	return []Span{{Text: text, Style: theme.Base}}
}

// commentBorder is the left-border glyph indenting every line of a rendered
// review comment, matching diffview.nvim-style inline thread blocks.
const commentBorder = "│ "

// commentLines renders one review comment: an "@login · <relative>" header,
// its body wrapped to width (minus the border's own width), and a reactions
// summary line when it has any — all indented with commentBorder.
func commentLines(c model.ReviewComment, width int) [][]Span {
	borderWidth := SpanWidth([]Span{{Text: commentBorder}})
	bodyWidth := width - borderWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}

	login := c.Author.Login
	if login == "" {
		login = "unknown"
	}
	header := fmt.Sprintf("@%s · %s", login, relativeTimeShort(c.CreatedAt))
	headerLine := []Span{{Text: commentBorder, Style: theme.Muted}, {Text: header, Style: theme.Base}}
	if c.State == model.ReviewCommentStatePending {
		// Reuses theme.Pending, the style every other "not final yet"
		// marker in gprt already uses (ReviewerStyle/RollupStyle's own
		// pending states), rather than introducing a separate badge style.
		headerLine = append(headerLine, Span{Text: " PENDING", Style: theme.Pending})
	}
	lines := [][]Span{headerLine}

	for _, l := range WrapText(c.Body, bodyWidth) {
		lines = append(lines, []Span{{Text: commentBorder, Style: theme.Muted}, {Text: l, Style: theme.Base}})
	}

	if r := reactionSummary(c.ReactionGroups); len(r) > 0 {
		lines = append(lines, append([]Span{{Text: commentBorder, Style: theme.Muted}}, r...))
	}
	return lines
}

// reactionSummary renders groups as a line of Spans (for example "👍 2  🎉
// 1"), bolding any group the viewer has themselves reacted with
// (ViewerHasReacted) so it is distinguishable from one they have not, or
// nil when there is nothing to show. Kept local to this package (rather
// than shared with internal/ui's near-identical reactionLine) since widget
// must not depend on internal/ui, and the two renderers are small enough
// that the duplication is cheaper than introducing a shared package for it.
func reactionSummary(groups []model.ReactionGroup) []Span {
	var spans []Span
	for _, g := range groups {
		if g.Count <= 0 {
			continue
		}
		if len(spans) > 0 {
			spans = append(spans, Span{Text: "  ", Style: theme.Muted})
		}
		style := theme.Muted
		if g.ViewerHasReacted {
			style = style.Bold(true)
		}
		spans = append(spans, Span{Text: fmt.Sprintf("%s %d", g.Content.Emoji(), g.Count), Style: style})
	}
	return spans
}

// relativeTimeShort renders t as a short "Ns"/"Nm"/"Nh"/"Nd" duration
// relative to time.Now(), or "" for a zero t. Kept local to this package
// for the same reason as reactionSummary — see its doc comment.
func relativeTimeShort(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	const day = 24 * time.Hour
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d/day))
	}
}
