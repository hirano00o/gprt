package widget

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/kitty"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// twoHunkPatch has two hunks: the first with a context/del/add/context
// sequence (4 lines), the second with a single added line between two
// context lines (3 lines).
const twoHunkPatch = `@@ -1,3 +1,3 @@
 line1
-old2
+new2
 line3
@@ -10,2 +10,3 @@
 line10
+newline
 line11
`

func twoHunkFile(t *testing.T) DiffFile {
	t.Helper()
	hunks, err := diff.Parse(twoHunkPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return DiffFile{
		Path: "pkg/example.go", Status: model.FileStatusModified, Additions: 2, Deletions: 1, HasPatch: true, Hunks: hunks,
		// The second hunk ends at new-side line 12: HeadKnown/HeadLineCount
		// are set to exactly that so no trailing gap row (an unrelated
		// concern for most of this file's tests) is appended after it. The
		// hunks themselves are still deliberately non-contiguous (lines 1-3,
		// then 10-12): a genuine, unconditional gap row between them is
		// still expected (see TestDiffViewCursorMovementSkipsNonSelectableRows).
		HeadKnown: true, HeadLineCount: 12,
	}
}

func rowText(r diffRow, li int) string {
	var sb []rune
	var spans []Span
	switch {
	case r.kind == rowKindLine && li == 0:
		spans = append(append([]Span{}, r.primary().gutter...), r.primary().content[0]...)
	case r.kind == rowKindLine && li < len(r.primary().content):
		spans = r.primary().content[li]
	case li < len(r.lines):
		spans = r.lines[li]
	}
	for _, s := range spans {
		sb = append(sb, []rune(s.Text)...)
	}
	return string(sb)
}

func drawn(t *testing.T, dv *DiffView, w, h int) {
	t.Helper()
	dv.SetRect(0, 0, w, h)
	screen := newTestScreen(t, w, h)
	dv.Draw(screen)
}

func TestDiffViewRendersFileHeaderHunkHeadersGuttersAndMarkers(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	drawn(t, dv, 60, 20)

	if len(dv.rows) == 0 {
		t.Fatal("no rows built")
	}
	if dv.rows[0].kind != rowKindFileHeader || !containsAll(rowText(dv.rows[0], 0), "pkg/example.go", "+2", "−1") {
		t.Errorf("file header row = %q, want it to mention the path and stats", rowText(dv.rows[0], 0))
	}

	var sawHunkHeader1, sawHunkHeader2, sawPlus, sawMinus bool
	for _, r := range dv.rows {
		switch r.kind {
		case rowKindHunkHeader:
			text := rowText(r, 0)
			if containsAll(text, "@@ -1,3 +1,3 @@") {
				sawHunkHeader1 = true
			}
			if containsAll(text, "@@ -10,2 +10,3 @@") {
				sawHunkHeader2 = true
			}
		case rowKindLine:
			for _, s := range r.primary().gutter {
				if s.Text == "+" {
					sawPlus = true
				}
				if s.Text == "-" {
					sawMinus = true
				}
			}
		}
	}
	if !sawHunkHeader1 || !sawHunkHeader2 {
		t.Errorf("hunk headers not both rendered: h1=%v h2=%v", sawHunkHeader1, sawHunkHeader2)
	}
	if !sawPlus || !sawMinus {
		t.Errorf("+/- markers not both rendered: plus=%v minus=%v", sawPlus, sawMinus)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !containsSubstr(s, sub) {
			return false
		}
	}
	return true
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDiffViewCursorMovementSkipsNonSelectableRows(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	drawn(t, dv, 60, 20)

	dv.MoveTop()
	first, ok := dv.CursorLine()
	if !ok || first.Text != "line1" {
		t.Fatalf("MoveTop() cursor line = %+v, ok=%v, want the first hunk's first line", first, ok)
	}

	// The first hunk has 4 lines (line1, old2, new2, line3); moving past all
	// of them must land on the gap row between the two hunks (new-side
	// lines 4-9, hidden since the hunks are non-contiguous) - itself
	// selectable, unlike the second hunk's own header row, which is skipped
	// entirely.
	for range 3 {
		dv.MoveBy(1)
	}
	last1, ok := dv.CursorLine()
	if !ok || last1.Text != "line3" {
		t.Fatalf("cursor after 3 moves = %+v, ok=%v, want the first hunk's last line (line3)", last1, ok)
	}

	dv.MoveBy(1)
	gapStart, ok := dv.CursorGap()
	if !ok || gapStart != 4 {
		t.Fatalf("cursor after moving past the first hunk = gapStart=%d, ok=%v, want the gap row starting at line 4", gapStart, ok)
	}

	dv.MoveBy(1)
	next, ok := dv.CursorLine()
	if !ok || next.Text != "line10" {
		t.Fatalf("cursor after moving past the gap row = %+v, ok=%v, want the second hunk's first line (line10), not its header", next, ok)
	}
}

func TestDiffViewMoveTopAndBottom(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	drawn(t, dv, 60, 20)

	dv.MoveBottom()
	last, ok := dv.CursorLine()
	if !ok || last.Text != "line11" {
		t.Fatalf("MoveBottom() cursor line = %+v, ok=%v, want the last line (line11)", last, ok)
	}
}

func TestDiffViewMoveHalfPageByDisplayLines(t *testing.T) {
	// A patch with a single hunk of 10 context lines, so half a 10-line-tall
	// view (5 display lines) is unambiguous.
	var patch string
	patch = "@@ -1,10 +1,10 @@\n"
	for i := 1; i <= 10; i++ {
		patch += " line\n"
	}
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}

	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	drawn(t, dv, 60, 12) // header(1) + hunk header(1) + 10 lines = 12 rows tall exactly

	dv.MoveTop()
	dv.MoveHalfPage(1)
	movedDownLine, ok := dv.CursorLine()
	if !ok || movedDownLine.NewNo == 1 {
		t.Fatalf("MoveHalfPage(1) from the top did not move the cursor down")
	}

	dv.MoveHalfPage(-1)
	backAtTop, ok := dv.CursorLine()
	if !ok || backAtTop.NewNo != 1 {
		t.Fatalf("MoveHalfPage(-1) after MoveHalfPage(1) = %+v, want back at the first line", backAtTop)
	}
}

// TestDiffViewMovePageByDisplayLines mirrors
// TestDiffViewMoveHalfPageByDisplayLines, but for a full page (MovePage)
// instead of half a page.
func TestDiffViewMovePageByDisplayLines(t *testing.T) {
	// A patch with a single hunk of 10 context lines, so a full 10-line-tall
	// view (10 display lines) reaches the last line exactly.
	var patch string
	patch = "@@ -1,10 +1,10 @@\n"
	for i := 1; i <= 10; i++ {
		patch += " line\n"
	}
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}

	dv := NewDiffView()
	// HeadKnown/HeadLineCount matching the hunk's own last line (10)
	// suppresses the trailing gap row that would otherwise follow it,
	// which is not what this test is about.
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks, HeadKnown: true, HeadLineCount: 10})
	drawn(t, dv, 60, 12) // header(1) + hunk header(1) + 10 lines = 12 rows tall exactly

	dv.MoveTop()
	dv.MovePage(1)
	movedDownLine, ok := dv.CursorLine()
	if !ok || movedDownLine.NewNo != 10 {
		t.Fatalf("MovePage(1) from the top landed on %+v, ok=%v, want the last line (NewNo 10)", movedDownLine, ok)
	}

	dv.MovePage(-1)
	backAtTop, ok := dv.CursorLine()
	if !ok || backAtTop.NewNo != 1 {
		t.Fatalf("MovePage(-1) after MovePage(1) = %+v, want back at the first line", backAtTop)
	}
}

func TestDiffViewVisualSelectionWithinAHunkAndClampAtBoundary(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	drawn(t, dv, 60, 20)

	dv.MoveTop() // line1
	dv.StartVisual()
	dv.MoveBy(2) // line1 -> old2 -> new2: a 3-line selection

	sel, ok := dv.Selection()
	if !ok || len(sel) != 3 {
		t.Fatalf("Selection() = %+v, ok=%v, want 3 lines", sel, ok)
	}
	if sel[0].Text != "line1" || sel[2].Text != "new2" {
		t.Errorf("Selection() = %+v, want line1..new2", sel)
	}

	// One more move crosses into the second hunk (line3, across the gap
	// row, into line10): the selection must clamp to the anchor hunk's own
	// last line rather than spanning hunks.
	dv.MoveBy(3) // line3, the gap row, then across the hunk boundary into line10
	clamped, ok := dv.Selection()
	if !ok {
		t.Fatal("Selection() lost after crossing a hunk boundary, want it clamped instead")
	}
	if last := clamped[len(clamped)-1]; last.Text != "line3" {
		t.Errorf("Selection() after crossing hunks ends at %+v, want it clamped to the anchor hunk's last line (line3)", last)
	}

	dv.EndVisual()
	if _, ok := dv.Selection(); ok {
		t.Error("Selection() still reports a selection after EndVisual")
	}
}

// TestDiffViewVisualSelectionAppliesSelectedBackgroundOnScreen guards
// against a regression where Draw's per-row selection-highlight check
// compared a row's line *index* against the selected diff.Lines' *OldNo*
// field values instead of against index bounds. The hunk here starts at
// old line 10 (not 1), so the two numbering schemes diverge enough (by 10)
// that the anchor row — selected, but not the cursor, so its background
// is not masked by the cursor's own — would be wrongly left unselected
// under the old, buggy comparison.
func TestDiffViewVisualSelectionAppliesSelectedBackgroundOnScreen(t *testing.T) {
	patch := "@@ -10,10 +10,10 @@\n" + strings.Repeat(" line\n", 10)
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 40, 20)
	screen := newTestScreen(t, 40, 20)
	dv.Draw(screen)

	dv.MoveTop()     // the gap row before the hunk (it starts at line 10, not 1)
	dv.MoveBy(1)     // hunk index 0 (old line 10)
	dv.StartVisual() // anchor = index 0
	dv.MoveBy(5)     // cursor -> index 5 (old line 15); selects indices 0..5
	dv.Draw(screen)

	x, y, _, _ := dv.GetRect()
	// Row layout: y+0 file header, y+1 the gap row before the hunk (it
	// starts at old/new line 10, not 1), y+2 hunk header, y+3 is hunk
	// index 0 (the anchor: selected, but not the cursor).
	_, gotBG, _ := cellStyle(screen, x+1, y+3).Decompose()
	_, wantBG, _ := theme.DiffLineStyle(diff.Context, true).Decompose()
	if gotBG != wantBG {
		t.Errorf("anchor row (hunk index 0, old line 10) background = %v, want the selected tint %v", gotBG, wantBG)
	}
}

func TestDiffViewVisualOnThreadRowIsANoOp(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Author: model.User{Login: "alice"}, Body: "hi"}}},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	dv.MoveTop()
	dv.NextThread()
	if _, ok := dv.CursorThread(); !ok {
		t.Fatal("NextThread() did not land on the thread header")
	}
	dv.StartVisual()
	if dv.InVisual() {
		t.Error("StartVisual() on a thread header row must be a no-op")
	}
}

func TestDiffViewFoldAndUnfoldThread(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{
			ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight,
			Comments: []model.ReviewComment{{Author: model.User{Login: "alice"}, Body: "please fix this", CreatedAt: time.Now()}},
		},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	if !hasCommentRow(dv, "please fix this") {
		t.Fatal("an unresolved thread must default to unfolded (open)")
	}

	dv.MoveTop()
	dv.NextThread()
	dv.ToggleFoldAtCursor()
	drawn(t, dv, 60, 30)
	if hasCommentRow(dv, "please fix this") {
		t.Error("ToggleFoldAtCursor did not fold the thread's comments")
	}

	dv.ToggleFoldAtCursor()
	drawn(t, dv, 60, 30)
	if !hasCommentRow(dv, "please fix this") {
		t.Error("ToggleFoldAtCursor did not unfold the thread's comments again")
	}
}

func TestDiffViewResolvedThreadDefaultsFolded(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{
			ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight, IsResolved: true,
			Comments: []model.ReviewComment{{Author: model.User{Login: "alice"}, Body: "looks good now"}},
		},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	dv.MoveTop()
	dv.NextThread()
	if th, ok := dv.CursorThread(); !ok || th.ID != "t1" {
		// Without this, a bug dropping the thread entirely (never placed at
		// all, rather than placed-and-folded) would vacuously pass the
		// fold check below: no comment row rendered either way.
		t.Fatalf("setup: CursorThread() = %+v, ok=%v, want t1 (the thread header itself must exist even though its body is folded)", th, ok)
	}

	if hasCommentRow(dv, "looks good now") {
		t.Error("a resolved thread must default to folded")
	}
}

func TestDiffViewFoldAllAndUnfoldAll(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "a"}}},
		{ID: "t2", Path: f.Path, Line: 10, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "b"}}},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	dv.FoldAll(true)
	drawn(t, dv, 60, 30)
	if hasCommentRow(dv, "a") || hasCommentRow(dv, "b") {
		t.Error("FoldAll(true) did not fold every thread")
	}

	dv.FoldAll(false)
	drawn(t, dv, 60, 30)
	if !hasCommentRow(dv, "a") || !hasCommentRow(dv, "b") {
		t.Error("FoldAll(false) did not unfold every thread")
	}
}

func hasCommentRow(dv *DiffView, substr string) bool {
	for _, r := range dv.rows {
		if r.kind != rowKindThreadComment {
			continue
		}
		for i := range r.lines {
			if containsSubstr(rowText(r, i), substr) {
				return true
			}
		}
	}
	return false
}

func TestDiffViewNextThreadAndPrevThread(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "a"}}},
		{ID: "t2", Path: f.Path, Line: 10, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "b"}}},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 40)

	dv.MoveTop()
	if moved := dv.NextThread(); !moved {
		t.Fatal("NextThread() from the top returned false, want true (t1 exists)")
	}
	first, ok := dv.CursorThread()
	if !ok || first.ID != "t1" {
		t.Fatalf("NextThread() from the top landed on %+v, ok=%v, want t1", first, ok)
	}

	if moved := dv.NextThread(); !moved {
		t.Fatal("second NextThread() returned false, want true (t2 exists)")
	}
	second, ok := dv.CursorThread()
	if !ok || second.ID != "t2" {
		t.Fatalf("second NextThread() landed on %+v, ok=%v, want t2", second, ok)
	}

	// No third thread: NextThread must not move (clamp, no wrap) and must
	// report false, so the router's own dispatch knows to fall forward into
	// App.stepThread instead.
	if moved := dv.NextThread(); moved {
		t.Error("NextThread() past the last thread returned true, want false")
	}
	if id, _ := dv.CursorThread(); id.ID != "t2" {
		t.Errorf("NextThread() past the last thread moved to %+v, want it to stay on t2", id)
	}

	if moved := dv.PrevThread(); !moved {
		t.Fatal("PrevThread() returned false, want true (t1 exists)")
	}
	back, ok := dv.CursorThread()
	if !ok || back.ID != "t1" {
		t.Fatalf("PrevThread() = %+v, ok=%v, want t1", back, ok)
	}

	// No thread before t1: PrevThread must not move and must report false.
	if moved := dv.PrevThread(); moved {
		t.Error("PrevThread() before the first thread returned true, want false")
	}
}

func TestDiffViewHorizontalScroll(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n+" + repeatChar('x', 100) + "\n"
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 30, 10)
	screen := newTestScreen(t, 30, 10)
	dv.Draw(screen)

	dv.MoveTop()
	dv.MoveBy(1) // move onto the added line (row 0 is the file header/hunk header before it)

	before := dv.hOffset
	dv.ScrollHorizontal(10)
	if dv.hOffset == before {
		t.Fatal("ScrollHorizontal(10) did not change hOffset")
	}
	if dv.hOffset > dv.maxHOffset {
		t.Errorf("hOffset %d exceeds maxHOffset %d", dv.hOffset, dv.maxHOffset)
	}

	dv.ScrollHorizontal(-1000)
	if dv.hOffset != 0 {
		t.Errorf("ScrollHorizontal clamped to %d, want 0 (cannot scroll left of the start)", dv.hOffset)
	}
}

// TestDiffViewRebuildClampsHOffsetWhenAWiderRedrawNeedsLessScroll guards
// against rebuild's own "dv.hOffset = clamp(...)" line specifically (M2
// review round 3, item 22): every other hOffset-clamping test drives it
// through SetFile switching to a different path first, which already
// resets hOffset to 0 on its own (see SetFile's own doc comment) before
// rebuild's clamp would ever have anything to do — so none of them can
// actually tell whether that clamp line does anything at all. This test
// instead keeps the same file and widens the pane itself: scrolled right
// on a narrow rect, then redrawn at a rect wide enough that the whole line
// now fits without scrolling (the new maxHOffset is 0), the stale hOffset
// must be pulled back down to 0 too, not left pointing past content that
// no longer needs any offset to show in full.
func TestDiffViewRebuildClampsHOffsetWhenAWiderRedrawNeedsLessScroll(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n+" + repeatChar('x', 50) + "\n"
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 20, 10)
	screen := newTestScreen(t, 200, 10)
	dv.Draw(screen)

	dv.MoveTop()
	dv.MoveBy(1)
	dv.ScrollHorizontal(1000) // clamps to the narrow rect's own maxHOffset
	if dv.hOffset == 0 {
		t.Fatal("setup: ScrollHorizontal did not move hOffset")
	}

	dv.SetRect(0, 0, 200, 10) // wide enough that the 50-character line fits without any scroll
	dv.Draw(screen)

	if dv.hOffset != 0 {
		t.Errorf("hOffset = %d after widening the pane enough to fit the whole line, want 0 (rebuild's own clamp)", dv.hOffset)
	}
}

// TestDiffViewSetFileToADifferentPathResetsCursorSelectionAndScroll guards
// against a SetFile switch to a *different* file (]f/[f in the Files tab)
// keeping the previous file's cursor position, an active visual selection,
// or a horizontal scroll offset: the by-identity cursor restore
// (diffRowKey) is positional (hunk/line indices, or a thread ID), so it can
// land on an arbitrary, unrelated row in the new file, and a leftover
// Selection() would report the *new* file's lines as selected — dangerous
// once M3 turns that into a comment range.
func TestDiffViewSetFileToADifferentPathResetsCursorSelectionAndScroll(t *testing.T) {
	longPatch := "@@ -1,1 +1,1 @@\n+" + repeatChar('x', 200) + "\n"
	longHunks, err := diff.Parse(longPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "a.go", HasPatch: true, Hunks: longHunks})
	dv.SetRect(0, 0, 30, 10)
	screen := newTestScreen(t, 30, 10)
	dv.Draw(screen)

	dv.MoveTop()
	dv.StartVisual()
	dv.MoveBy(0)
	dv.ScrollHorizontal(50)
	if dv.hOffset == 0 {
		t.Fatal("setup: ScrollHorizontal did not move hOffset")
	}
	if !dv.InVisual() {
		t.Fatal("setup: StartVisual did not enter visual mode")
	}

	otherHunks, err := diff.Parse(twoHunkPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv.SetFile(DiffFile{Path: "b.go", HasPatch: true, Hunks: otherHunks})
	dv.Draw(screen)

	if dv.InVisual() {
		t.Error("SetFile to a different path did not end an active visual selection")
	}
	if _, ok := dv.Selection(); ok {
		t.Error("Selection() still reports a selection after SetFile to a different path")
	}
	if dv.HOffset() != 0 {
		t.Errorf("HOffset() = %d after SetFile to a different path, want 0", dv.HOffset())
	}
	line, ok := dv.CursorLine()
	if !ok || line.Text != "line1" {
		t.Errorf("cursor after SetFile to a different path = %+v, ok=%v, want the new file's first line (line1)", line, ok)
	}
}

// TestDiffViewClampsHOffsetAfterSetFileToAShorterFile guards against a
// stale hOffset surviving a SetFile to a file whose longest line is
// shorter than the scroll position reached on the previous file: without
// re-clamping hOffset inside rebuild (ScrollHorizontal is the only other
// place that clamps it, and it is not called by SetFile), drawLineRow
// would skip past all of the new, shorter content and render every
// content cell blank.
func TestDiffViewClampsHOffsetAfterSetFileToAShorterFile(t *testing.T) {
	longPatch := "@@ -1,1 +1,1 @@\n+" + repeatChar('x', 200) + "\n"
	longHunks, err := diff.Parse(longPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "long.go", HasPatch: true, Hunks: longHunks})
	dv.SetRect(0, 0, 30, 10)
	screen := newTestScreen(t, 30, 10)
	dv.Draw(screen)

	dv.ScrollHorizontal(1000) // clamps to the long file's own maxHOffset
	if dv.hOffset == 0 {
		t.Fatal("ScrollHorizontal did not move hOffset on the long file")
	}

	shortPatch := "@@ -1,1 +1,1 @@\n+short\n"
	shortHunks, err := diff.Parse(shortPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv.SetFile(DiffFile{Path: "short.go", HasPatch: true, Hunks: shortHunks})
	dv.Draw(screen)

	if dv.hOffset > dv.maxHOffset {
		t.Fatalf("hOffset %d exceeds the shorter file's own maxHOffset %d after SetFile", dv.hOffset, dv.maxHOffset)
	}

	dv.MoveTop()
	dv.MoveBy(1) // the added "short" line
	x, y, w, _ := dv.GetRect()
	found := false
	for cx := x; cx < x+w; cx++ {
		if cellRune(screen, cx, y+2) == 's' {
			found = true
			break
		}
	}
	if !found {
		t.Error(`content "short" not found on screen; a stale hOffset scrolled it entirely out of view`)
	}
}

// TestDiffViewMaxHOffsetReachesTheLastColumnOfTheLongestLine guards against
// an off-by-one in rebuild's maxHOffset computation: the content width (pane less gutter)
// is the content width available when hOffset == 0 (no "‹"
// marker drawn yet), but drawLineRow spends one of those cells on the "‹"
// marker as soon as hOffset > 0, so the content actually visible while
// scrolled is one cell narrower than that content width.
// Without accounting for that, scrolling all the way to maxHOffset always
// leaves the longest line's very last column just out of view.
func TestDiffViewMaxHOffsetReachesTheLastColumnOfTheLongestLine(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n+" + repeatChar('x', 49) + "Z\n"
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "long.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 16, 10)
	screen := newTestScreen(t, 16, 10)
	dv.Draw(screen)

	dv.ScrollHorizontal(1000) // clamps to maxHOffset
	if dv.hOffset == 0 {
		t.Fatal("setup: ScrollHorizontal did not move hOffset")
	}

	dv.MoveTop()
	dv.MoveBy(1) // the added line
	dv.Draw(screen)
	x, y, w, _ := dv.GetRect()
	found := false
	for cx := x; cx < x+w; cx++ {
		if cellRune(screen, cx, y+2) == 'Z' {
			found = true
			break
		}
	}
	if !found {
		t.Error(`the longest line's last column ('Z') never appears on screen at maxHOffset; maxHOffset is one short of accounting for the "‹" marker cell drawn once hOffset > 0`)
	}
}

func repeatChar(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

func TestDiffViewSetFileKeepsCursorPosition(t *testing.T) {
	f := twoHunkFile(t)
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 20)

	dv.MoveBottom()
	before, ok := dv.CursorLine()
	if !ok {
		t.Fatal("no cursor line before re-render")
	}

	// A re-render of the same file (e.g. a highlight result landing) must
	// not reset the cursor back to the top.
	f2 := f
	f2.Tokens = [][][]highlight.Token{
		{{{Text: "line1", Type: 0}}, nil, nil, nil},
		{nil, nil, nil},
	}
	dv.SetFile(f2)
	drawn(t, dv, 60, 20)

	after, ok := dv.CursorLine()
	if !ok || after.OldNo != before.OldNo || after.NewNo != before.NewNo {
		t.Errorf("cursor moved after SetFile re-rendered the same file: before=%+v after=%+v", before, after)
	}
}

func TestDiffViewEmptyZeroWidthAndNoPatchAreSafe(t *testing.T) {
	dv := NewDiffView()
	// Never given a file at all.
	drawn(t, dv, 20, 10)
	dv.MoveBy(1)
	dv.MoveTop()
	dv.MoveBottom()
	dv.MoveHalfPage(1)
	dv.NextThread()
	dv.PrevThread()
	dv.ToggleFoldAtCursor()
	dv.ScrollHorizontal(1)
	if _, ok := dv.CursorLine(); ok {
		t.Error("CursorLine() on an empty DiffView should report ok=false")
	}

	// Zero width.
	dv2 := NewDiffView()
	dv2.SetFile(twoHunkFile(t))
	dv2.SetRect(0, 0, 0, 10)
	screen := newTestScreen(t, 1, 10)
	dv2.Draw(screen) // must not panic

	// No patch (binary/too-large file).
	dv3 := NewDiffView()
	dv3.SetFile(DiffFile{Path: "image.png", Status: model.FileStatusModified, HasPatch: false})
	drawn(t, dv3, 40, 10)
	if len(dv3.rows) != 1 {
		t.Fatalf("no-patch file produced %d rows, want just the file header", len(dv3.rows))
	}
	if !containsSubstr(rowText(dv3.rows[0], 1), "Patch not available") {
		t.Errorf("no-patch file header second line = %q, want it to mention unavailability", rowText(dv3.rows[0], 1))
	}
}

// TestDiffViewEmptyPathDoesNotClaimAPatchIsUnavailable guards against a
// zero-value/empty-path DiffFile (nothing selected yet) being rendered as
// if it *were* a real, patchless file: the file-header row's own
// !f.HasPatch fallback ("Patch not available (binary or too large)") plus
// a "+0 −0" stats line are both misleading when there is no file at all.
func TestDiffViewEmptyPathDoesNotClaimAPatchIsUnavailable(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(DiffFile{})
	drawn(t, dv, 40, 10)

	if len(dv.rows) != 1 {
		t.Fatalf("empty DiffFile produced %d rows, want just one placeholder row", len(dv.rows))
	}
	text := rowText(dv.rows[0], 0)
	if containsSubstr(text, "Patch not available") {
		t.Errorf("empty DiffFile rendered %q, want it not to claim a patch is unavailable", text)
	}
	if containsSubstr(text, "+0") || containsSubstr(text, "−0") {
		t.Errorf("empty DiffFile rendered %q, want no +0/−0 stats for a non-existent file", text)
	}
}

// TestDiffViewEmptyPathWithErrShowsTheError guards against the same
// zero-path placeholder swallowing a caller-supplied Err (internal/ui's
// Files tab sets one to distinguish "no pull request open" from "no file
// selected").
func TestDiffViewEmptyPathWithErrShowsTheError(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(DiffFile{Err: errors.New("no pull request open")})
	drawn(t, dv, 40, 10)

	text := rowText(dv.rows[0], 0)
	if !containsSubstr(text, "no pull request open") {
		t.Errorf("empty DiffFile with Err rendered %q, want the error message", text)
	}
}

// TestDiffViewPendingCommentShowsBadge covers the PENDING badge next to a
// review comment's author, distinguishing an unpublished (pending review)
// comment from an already-submitted one on the same thread.
func TestDiffViewPendingCommentShowsBadge(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{
			ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight,
			Comments: []model.ReviewComment{
				{Author: model.User{Login: "alice"}, Body: "submitted", State: model.ReviewCommentStateSubmitted},
				{Author: model.User{Login: "alice"}, Body: "still pending", State: model.ReviewCommentStatePending},
			},
		},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	var sawPendingBadge, sawSubmittedRow bool
	for _, r := range dv.rows {
		if r.kind != rowKindThreadComment {
			continue
		}
		text := rowText(r, 0)
		if containsSubstr(text, "still pending") && !containsSubstr(text, "PENDING") {
			t.Fatalf("comment row header %q for a PENDING comment must include the PENDING badge", rowText(r, 0))
		}
		if containsSubstr(rowText(r, 0), "PENDING") {
			sawPendingBadge = true
		}
		if containsSubstr(rowText(r, 0), "@alice") && !containsSubstr(rowText(r, 0), "PENDING") {
			sawSubmittedRow = true
		}
	}
	if !sawPendingBadge {
		t.Error("no comment row carried a PENDING badge")
	}
	if !sawSubmittedRow {
		t.Error("the already-submitted comment's own header row must not carry a PENDING badge")
	}
}

// TestDiffViewCommentReactionLineBoldsViewerReaction covers commentLines'
// own reaction summary line: a group the viewer has reacted with
// (ViewerHasReacted) renders bold, one they have not does not.
func TestDiffViewCommentReactionLineBoldsViewerReaction(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{
			ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight,
			Comments: []model.ReviewComment{
				{
					Author: model.User{Login: "alice"}, Body: "nice", State: model.ReviewCommentStateSubmitted,
					ReactionGroups: []model.ReactionGroup{
						{Content: model.ReactionThumbsUp, Count: 2, ViewerHasReacted: true},
						{Content: model.ReactionHooray, Count: 1},
					},
				},
			},
		},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	var gotThumbsUp, gotHooray bool
	for _, r := range dv.rows {
		if r.kind != rowKindThreadComment {
			continue
		}
		for _, line := range r.lines {
			for _, s := range line {
				switch {
				case strings.Contains(s.Text, model.ReactionThumbsUp.Emoji()):
					gotThumbsUp = true
					if s.Style != theme.Muted.Bold(true) {
						t.Errorf("thumbs-up reaction span style = %+v, want theme.Muted.Bold(true)", s.Style)
					}
				case strings.Contains(s.Text, model.ReactionHooray.Emoji()):
					gotHooray = true
					if s.Style != theme.Muted {
						t.Errorf("hooray reaction span style = %+v, want plain theme.Muted", s.Style)
					}
				}
			}
		}
	}
	if !gotThumbsUp || !gotHooray {
		t.Fatal("did not find both reaction spans in the comment's rendered lines")
	}
}

// TestDiffViewDraftMarkerRendersInGutter covers the "✎"-style gutter marker
// on a line with a saved draft anchored to it (DiffFile.DraftLines), and its
// absence on every other line.
func TestDiffViewDraftMarkerRendersInGutter(t *testing.T) {
	f := twoHunkFile(t)
	f.DraftMarker = "M"
	f.DraftLines = map[DraftAnchor]bool{{Hunk: 0, Line: 1}: true} // the "-old2" line
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	var markedRows int
	for _, r := range dv.rows {
		if r.kind != rowKindLine {
			continue
		}
		marked := false
		for _, s := range r.primary().gutter {
			if s.Text == "M" {
				marked = true
			}
		}
		if marked {
			markedRows++
			if r.hunk != 0 || r.primary().line != 1 {
				t.Errorf("draft marker rendered on row (hunk=%d, line=%d), want only (0, 1)", r.hunk, r.primary().line)
			}
		}
	}
	if markedRows != 1 {
		t.Errorf("draft marker rendered on %d rows, want exactly 1", markedRows)
	}
}

// TestDiffViewJumpToLineMovesCursorOnceTheLineExists covers the pending-list
// dialog's use of JumpToLine to resolve a draft's saved anchor once the
// file's hunks have arrived: the target survives across a rebuild that
// happens before the data does (Loading true) and is applied on the
// rebuild after it lands.
func TestDiffViewJumpToLineMovesCursorOnceTheLineExists(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "pkg/example.go", Loading: true})
	dv.JumpToLine(model.DiffSideRight, 2)
	drawn(t, dv, 60, 20) // rebuilds while still loading: nothing to land on yet

	f := twoHunkFile(t)
	dv.SetFile(f)
	drawn(t, dv, 60, 20)

	line, ok := dv.CursorLine()
	if !ok {
		t.Fatal("cursor is not on a line row after JumpToLine's target arrived")
	}
	if line.NewNo != 2 {
		t.Errorf("cursor line NewNo = %d, want 2 (the anchor JumpToLine was given)", line.NewNo)
	}
}

// TestDiffViewJumpToLineScrollsTheTargetOnScreen guards that a jump resolved
// inside rebuild (the "t"/"p" dialogs, search n/N) also scrolls the view to
// it: Draw, not rebuild, is what brings the cursor row into view.
func TestDiffViewJumpToLineScrollsTheTargetOnScreen(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("@@ -1,0 +1,50 @@\n")
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&sb, "+added line %d\n", i)
	}
	hunks, err := diff.Parse(sb.String())
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "pkg/long.go", Status: model.FileStatusAdded, HasPatch: true, Hunks: hunks})
	dv.JumpToLine(model.DiffSideRight, 45)

	dv.SetRect(0, 0, 60, 10)
	screen := newTestScreen(t, 60, 10)
	dv.Draw(screen)

	var text strings.Builder
	for y := range 10 {
		for x := range 60 {
			text.WriteRune(cellRune(screen, x, y))
		}
		text.WriteByte('\n')
	}
	if !strings.Contains(text.String(), "added line 45") {
		t.Errorf("JumpToLine target is not on screen after Draw; screen:\n%s", text.String())
	}
}

// TestDiffViewJumpToLineGivesUpOnceLoadingFinishesWithoutAMatch guards
// against an anchor that never resolves (an outdated draft whose line no
// longer exists) retrying forever on every future, unrelated rebuild.
func TestDiffViewJumpToLineGivesUpOnceLoadingFinishesWithoutAMatch(t *testing.T) {
	dv := NewDiffView()
	f := twoHunkFile(t)
	dv.JumpToLine(model.DiffSideRight, 9999) // no such line in f
	dv.SetFile(f)                            // f.Loading is false: data has "arrived"
	drawn(t, dv, 60, 20)

	if dv.pendingCursorSet {
		t.Error("pendingCursorSet still true after a rebuild with Loading false; an unresolved anchor must be dropped, not retried forever")
	}
}

// TestDiffViewJumpToThreadMovesCursorOnceTheThreadExists mirrors
// TestDiffViewJumpToLineMovesCursorOnceTheLineExists for JumpToThread: the
// "t" review-threads dialog's own Enter uses it to resolve a chosen thread
// once its file's data has arrived.
func TestDiffViewJumpToThreadMovesCursorOnceTheThreadExists(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(DiffFile{Path: "pkg/example.go", Loading: true})
	dv.JumpToThread("t2")
	drawn(t, dv, 60, 40) // rebuilds while still loading: nothing to land on yet

	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "a"}}},
		{ID: "t2", Path: f.Path, Line: 10, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "b"}}},
	}
	dv.SetFile(f)
	drawn(t, dv, 60, 40)

	th, ok := dv.CursorThread()
	if !ok {
		t.Fatal("cursor is not on a thread row after JumpToThread's target arrived")
	}
	if th.ID != "t2" {
		t.Errorf("cursor thread ID = %q, want %q (the target JumpToThread was given)", th.ID, "t2")
	}
}

// TestDiffViewJumpToThreadGivesUpOnceLoadingFinishesWithoutAMatch mirrors
// TestDiffViewJumpToLineGivesUpOnceLoadingFinishesWithoutAMatch for
// JumpToThread: an ID that never resolves (the thread was deleted/resolved
// away between listing it and opening this file) must not keep retrying on
// every future, unrelated rebuild.
func TestDiffViewJumpToThreadGivesUpOnceLoadingFinishesWithoutAMatch(t *testing.T) {
	dv := NewDiffView()
	f := twoHunkFile(t)
	dv.JumpToThread("no-such-thread")
	dv.SetFile(f) // f.Loading is false: data has "arrived"
	drawn(t, dv, 60, 20)

	if dv.pendingThreadSet {
		t.Error("pendingThreadSet still true after a rebuild with Loading false; an unresolved thread ID must be dropped, not retried forever")
	}
}

// TestDiffViewJumpToThreadEdgeUsesDisplayOrder covers JumpToThreadEdge
// landing on the first/last thread-header row in *display* order
// (bucketThreads/build: outdated threads first, then file-level, then
// inline in hunk order) rather than by a raw model.ReviewThread.Line
// comparison, which would get an outdated thread's own placement wrong —
// see JumpToThreadEdge's own doc comment.
func TestDiffViewJumpToThreadEdgeUsesDisplayOrder(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{ID: "inline", Path: f.Path, Line: 1, Side: model.DiffSideRight, Comments: []model.ReviewComment{{Body: "inline"}}},
		{ID: "outdated", Path: f.Path, Line: 999, Side: model.DiffSideRight, IsOutdated: true, Comments: []model.ReviewComment{{Body: "outdated"}}},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 40)

	dv.JumpToThreadEdge(1)
	drawn(t, dv, 60, 40)
	first, ok := dv.CursorThread()
	if !ok || first.ID != "outdated" {
		t.Fatalf("JumpToThreadEdge(1) landed on %+v, ok=%v, want the outdated thread (rendered first)", first, ok)
	}

	dv.JumpToThreadEdge(-1)
	drawn(t, dv, 60, 40)
	last, ok := dv.CursorThread()
	if !ok || last.ID != "inline" {
		t.Fatalf("JumpToThreadEdge(-1) landed on %+v, ok=%v, want the inline thread (rendered last)", last, ok)
	}
}

// TestDiffViewSearchHighlightsMatchedCellsOnly guards SetSearch/ClearSearch
// end to end: only the cells covered by an actual regexp match are drawn
// Reverse (theme.SearchMatch), never a neighbouring cell on the same line or
// content on a different line, and ClearSearch removes it again.
func TestDiffViewSearchHighlightsMatchedCellsOnly(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	dv.SetSearch(regexp.MustCompile("new2"))
	dv.SetRect(0, 0, 60, 20)
	screen := newTestScreen(t, 60, 20)
	dv.Draw(screen)

	// Row layout for twoHunkFile: file header(0), hunk header(1),
	// " line1"(2), "-old2"(3), "+new2"(4) — the only line "new2" matches.
	row := dv.rows[4]
	if row.kind != rowKindLine || row.primary().line != 2 {
		t.Fatalf("rows[4] = %+v, want the +new2 line (hunk 0, line index 2)", row)
	}
	gw := SpanWidth(row.primary().gutter)

	x, y, _, _ := dv.GetRect()
	for i := range 4 { // "new2" is 4 runes wide
		_, _, attr := cellStyle(screen, x+1+gw+i, y+4).Decompose()
		if attr&tcell.AttrReverse == 0 {
			t.Errorf("match cell %d not drawn Reverse", i)
		}
	}
	if _, _, attr := cellStyle(screen, x+1+gw+4, y+4).Decompose(); attr&tcell.AttrReverse != 0 {
		t.Error("cell right after the match must not be drawn Reverse")
	}
	if _, _, attr := cellStyle(screen, x+1+gw, y+2).Decompose(); attr&tcell.AttrReverse != 0 {
		t.Error("a neighbouring, non-matching line must not be drawn Reverse")
	}

	dv.ClearSearch()
	dv.Draw(screen)
	for i := range 4 {
		if _, _, attr := cellStyle(screen, x+1+gw+i, y+4).Decompose(); attr&tcell.AttrReverse != 0 {
			t.Errorf("match cell %d still Reverse after ClearSearch", i)
		}
	}
}

// repeatedMatchPatch has one hunk with a single added line containing the
// same short string twice ("foo" at byte [0,3) and [8,11)), for
// TestDiffViewSearchCurrentHighlightsOnlyTheCurrentMatch.
const repeatedMatchPatch = `@@ -1,1 +1,1 @@
+foo bar foo
`

func repeatedMatchFile(t *testing.T) DiffFile {
	t.Helper()
	hunks, err := diff.Parse(repeatedMatchPatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return DiffFile{Path: "pkg/repeated.go", Status: model.FileStatusModified, Additions: 1, HasPatch: true, Hunks: hunks}
}

// TestDiffViewSearchCurrentHighlightsOnlyTheCurrentMatch guards
// SetSearchCurrent: when a line has more than one match for the active
// search, only the one SetSearchCurrent names is drawn theme.SearchCurrent;
// every other match on the same line stays theme.SearchMatch.
func TestDiffViewSearchCurrentHighlightsOnlyTheCurrentMatch(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(repeatedMatchFile(t))
	dv.SetSearch(regexp.MustCompile("foo"))
	dv.SetSearchCurrent("pkg/repeated.go", 0, 0, 8, 11) // the second "foo"
	dv.SetRect(0, 0, 60, 20)
	screen := newTestScreen(t, 60, 20)
	dv.Draw(screen)

	// Row layout for repeatedMatchFile: file header(0), hunk header(1),
	// "+foo bar foo"(2).
	row := dv.rows[2]
	if row.kind != rowKindLine || row.hunk != 0 || row.primary().line != 0 {
		t.Fatalf("rows[2] = %+v, want the +foo bar foo line (hunk 0, line index 0)", row)
	}
	gw := SpanWidth(row.primary().gutter)
	x, y, _, _ := dv.GetRect()

	matchFg, _, _ := theme.SearchMatch.Decompose()
	currentFg, _, _ := theme.SearchCurrent.Decompose()

	for i := range 3 { // first "foo" (bytes 0-2): the other match
		fg, _, attr := cellStyle(screen, x+1+gw+i, y+2).Decompose()
		if attr&tcell.AttrReverse == 0 {
			t.Errorf("other-match cell %d not drawn Reverse", i)
		}
		if attr&tcell.AttrBold != 0 {
			t.Errorf("other-match cell %d drawn Bold, want only the current match to be", i)
		}
		if fg != matchFg {
			t.Errorf("other-match cell %d foreground = %v, want theme.SearchMatch's %v", i, fg, matchFg)
		}
	}
	for i := 8; i < 11; i++ { // second "foo" (bytes 8-10): the current match
		fg, _, attr := cellStyle(screen, x+1+gw+i, y+2).Decompose()
		if attr&tcell.AttrReverse == 0 {
			t.Errorf("current-match cell %d not drawn Reverse", i)
		}
		if attr&tcell.AttrBold == 0 {
			t.Errorf("current-match cell %d not drawn Bold", i)
		}
		if fg != currentFg {
			t.Errorf("current-match cell %d foreground = %v, want theme.SearchCurrent's %v", i, fg, currentFg)
		}
	}
}

// gapFixturePatch has two hunks: the first starts at new-side line 5 (a
// 4-line gap before it, lines 1-4), the second at new-side line 10 (a
// 3-line gap between them, lines 7-9).
const gapFixturePatch = `@@ -5,2 +5,2 @@
 context5
 context6
@@ -10,1 +10,1 @@
 context10
`

// gapFixtureFile builds a DiffFile from gapFixturePatch with the given
// status and head-content knowledge, for the gap-row tests below.
func gapFixtureFile(t *testing.T, status model.FileStatus, headKnown bool, headLineCount int) DiffFile {
	t.Helper()
	hunks, err := diff.Parse(gapFixturePatch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return DiffFile{
		Path: "gap.go", Status: status, HasPatch: true, Hunks: hunks,
		HeadKnown: headKnown, HeadLineCount: headLineCount,
	}
}

func TestDiffViewGapRowsRenderedBeforeBetweenAndAfterWithCounts(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(gapFixtureFile(t, model.FileStatusModified, true, 15))
	drawn(t, dv, 60, 30)

	var gaps []diffRow
	for _, r := range dv.rows {
		if r.kind == rowKindGap {
			gaps = append(gaps, r)
		}
	}
	if len(gaps) != 3 {
		t.Fatalf("gap rows = %d, want 3 (before, between, trailing)", len(gaps))
	}
	if gaps[0].gapStart != 1 || gaps[0].gapCount != 4 {
		t.Errorf("before gap = %+v, want gapStart=1 gapCount=4", gaps[0])
	}
	if gaps[1].gapStart != 7 || gaps[1].gapCount != 3 {
		t.Errorf("between gap = %+v, want gapStart=7 gapCount=3", gaps[1])
	}
	if gaps[2].gapStart != 11 || gaps[2].gapCount != 5 {
		t.Errorf("trailing gap = %+v, want gapStart=11 gapCount=5 (HeadLineCount 15 - last visible line 10)", gaps[2])
	}
	if !containsAll(rowText(gaps[1], 0), "3", "lines hidden") {
		t.Errorf("between gap text = %q, want it to mention the hidden line count", rowText(gaps[1], 0))
	}
}

func TestDiffViewGapRowsNoneForAddedRemovedNoPatchOrLoading(t *testing.T) {
	tests := []struct {
		name string
		f    func(t *testing.T) DiffFile
	}{
		{"added status", func(t *testing.T) DiffFile { return gapFixtureFile(t, model.FileStatusAdded, true, 15) }},
		{"removed status", func(t *testing.T) DiffFile { return gapFixtureFile(t, model.FileStatusRemoved, true, 15) }},
		{"no patch", func(t *testing.T) DiffFile {
			return DiffFile{Path: "x.go", Status: model.FileStatusModified, HasPatch: false}
		}},
		{"loading", func(t *testing.T) DiffFile {
			f := gapFixtureFile(t, model.FileStatusModified, true, 15)
			f.Loading = true
			return f
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dv := NewDiffView()
			dv.SetFile(tc.f(t))
			drawn(t, dv, 60, 30)
			for _, r := range dv.rows {
				if r.kind == rowKindGap {
					t.Fatalf("gap row present = %+v, want none", r)
				}
			}
		})
	}
}

func TestDiffViewTrailingGapHiddenWhenHeadLineCountMatchesLastLineShownWhenLarger(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(gapFixtureFile(t, model.FileStatusModified, true, 10)) // matches the last visible line exactly
	drawn(t, dv, 60, 30)
	for _, r := range dv.rows {
		if r.kind == rowKindGap && r.gapStart == 11 {
			t.Fatalf("trailing gap row present = %+v, want none when HeadLineCount == last visible line", r)
		}
	}

	dv2 := NewDiffView()
	dv2.SetFile(gapFixtureFile(t, model.FileStatusModified, true, 12))
	drawn(t, dv2, 60, 30)
	var found bool
	for _, r := range dv2.rows {
		if r.kind == rowKindGap && r.gapStart == 11 {
			found = true
			if r.gapCount != 2 {
				t.Errorf("trailing gap count = %d, want 2", r.gapCount)
			}
		}
	}
	if !found {
		t.Error("trailing gap row missing when HeadLineCount > last visible line")
	}
}

func TestDiffViewCursorGap(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(gapFixtureFile(t, model.FileStatusModified, true, 15))
	drawn(t, dv, 60, 30)

	dv.MoveTop() // the before gap row (gapStart=1)
	gapStart, ok := dv.CursorGap()
	if !ok || gapStart != 1 {
		t.Fatalf("CursorGap() = (%d, %v), want (1, true) on the before gap row", gapStart, ok)
	}

	dv.MoveBy(1) // context5, a real line - not a gap
	if _, ok := dv.CursorGap(); ok {
		t.Error("CursorGap() = ok on a non-gap row, want false")
	}
}

func TestDiffViewVisualOnGapRowIsANoOp(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(gapFixtureFile(t, model.FileStatusModified, true, 15))
	drawn(t, dv, 60, 30)

	dv.MoveTop() // the before gap row
	dv.StartVisual()
	if dv.InVisual() {
		t.Error("StartVisual() on a gap row must be a no-op")
	}
}

// gapFixtureHeadLines builds a 15-line synthetic head file for the
// expansion tests below.
func gapFixtureHeadLines() []string {
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = fmt.Sprintf("headline%d", i+1)
	}
	return lines
}

// TestDiffViewCursorSurvivesGapExpansionLandingOnFirstRevealedLine covers
// the decided cursor-placement design: a gap row's identity (diffRowKey's
// isGap/gapStart) can never match any row in a rebuild once its gap has
// been expanded (there is no longer a gap at that start line at all), so
// rebuild's by-identity restore falls through to its index-based
// clamp(oldCursor) fallback - which lands correctly on the first revealed
// line, since the new lines are inserted exactly where the gap row used to
// sit in selectable order.
func TestDiffViewCursorSurvivesGapExpansionLandingOnFirstRevealedLine(t *testing.T) {
	dv := NewDiffView()
	f := gapFixtureFile(t, model.FileStatusModified, true, 15)
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	dv.MoveTop()
	dv.MoveBy(3) // context5, context6, then the between gap row
	gapStart, ok := dv.CursorGap()
	if !ok || gapStart != 7 {
		t.Fatalf("cursor gapStart = %d, ok=%v, want (7, true) on the between gap row", gapStart, ok)
	}

	expanded, err := diff.ExpandGap(f.Hunks, gapStart, gapFixtureHeadLines())
	if err != nil {
		t.Fatalf("diff.ExpandGap() error = %v", err)
	}

	f2 := f
	f2.Hunks = expanded
	dv.SetFile(f2)
	drawn(t, dv, 60, 30)

	line, ok := dv.CursorLine()
	if !ok || line.NewNo != 7 {
		t.Fatalf("CursorLine() after expansion = %+v, ok=%v, want the first revealed line (NewNo 7)", line, ok)
	}
}

// TestDiffViewCursorGapIdentitySurvivesExpandingADifferentGap covers a
// cursor left on one gap row while a *different* gap in the same file gets
// expanded: expanding the between-gap merges the file's two hunks into
// one, which would shift a hunk-index-based identity for "the last hunk"
// - but gap identity is by start line, so the cursor must still be found
// on the (unmoved) trailing gap.
func TestDiffViewCursorGapIdentitySurvivesExpandingADifferentGap(t *testing.T) {
	dv := NewDiffView()
	f := gapFixtureFile(t, model.FileStatusModified, true, 15)
	dv.SetFile(f)
	drawn(t, dv, 60, 30)

	dv.MoveBottom() // the trailing gap row (gapStart=11)
	gapStart, ok := dv.CursorGap()
	if !ok || gapStart != 11 {
		t.Fatalf("cursor gapStart = %d, ok=%v, want (11, true) on the trailing gap row", gapStart, ok)
	}

	expanded, err := diff.ExpandGap(f.Hunks, 7, gapFixtureHeadLines())
	if err != nil {
		t.Fatalf("diff.ExpandGap() error = %v", err)
	}
	if len(expanded) != 1 {
		t.Fatalf("expanding the between gap produced %d hunks, want 1 (merged)", len(expanded))
	}

	f2 := f
	f2.Hunks = expanded
	dv.SetFile(f2)
	drawn(t, dv, 60, 30)

	gapStart, ok = dv.CursorGap()
	if !ok || gapStart != 11 {
		t.Fatalf("cursor after expanding a different gap = gapStart=%d ok=%v, want still (11, true)", gapStart, ok)
	}
}

// TestDiffViewOutdatedThreadWithLineRendersInlineWithOutdatedStatus covers
// bucketThreads no longer sending every outdated thread to the "Outdated
// threads" block: one that carries a (possibly relocated) valid line is
// rendered at that line, still flagged "outdated".
func TestDiffViewOutdatedThreadWithLineRendersInlineWithOutdatedStatus(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{
		{
			ID: "relocated", Path: f.Path, Line: 1, Side: model.DiffSideRight, IsOutdated: true,
			Comments: []model.ReviewComment{{Body: "relocated body"}},
		},
	}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 80, 40)

	var sawHeader, sawStatus, sawBody bool
	for _, r := range dv.rows {
		switch r.kind {
		case rowKindOutdatedHeader:
			sawHeader = true
		case rowKindThreadHeader:
			sawStatus = sawStatus || containsAll(rowText(r, 0), "outdated")
		case rowKindThreadComment:
			for li := range r.lines {
				sawBody = sawBody || containsAll(rowText(r, li), "relocated body")
			}
		}
	}
	if sawHeader {
		t.Error("an outdated thread with a valid line went to the Outdated threads block, want inline")
	}
	if !sawStatus {
		t.Error("inline outdated thread's header lacks the \"outdated\" status")
	}
	if !sawBody {
		t.Error("inline outdated thread's comment body was not rendered")
	}
}

// TestCommentLinesRenderMarkdownBodyInsideBorder covers a review comment's
// body being rendered as Markdown, with every body line (wrapped or not)
// still prefixed by the border and no wider than the requested width.
func TestCommentLinesRenderMarkdownBodyInsideBorder(t *testing.T) {
	c := model.ReviewComment{
		Author: model.User{Login: "alice"},
		Body:   "# Plan\n- first step is rather long indeed\n- `code`",
		State:  model.ReviewCommentStateSubmitted,
	}
	lines := commentLines(c, 24, nil)

	var text []string
	for _, line := range lines {
		if w := SpanWidth(line); w > 24 {
			t.Errorf("line %q has width %d, want <= 24", spansText(line), w)
		}
		text = append(text, spansText(line))
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"│ Plan", "│ • first step is rather", "│   long indeed", "│ • code"} {
		if !strings.Contains(joined, want) {
			t.Errorf("comment lines =\n%s\nwant a line containing %q", joined, want)
		}
	}
	if strings.Contains(joined, "# Plan") {
		t.Errorf("comment lines =\n%s\nwant the heading marker removed", joined)
	}
}

// TestDiffViewCommentImagesComeFromTheCommentsOwnSource covers
// SetImageSource: a standalone image in a review comment's body is drawn
// from the source the factory returns for that comment's node ID.
func TestDiffViewCommentImagesComeFromTheCommentsOwnSource(t *testing.T) {
	f := twoHunkFile(t)
	f.Threads = []model.ReviewThread{{
		ID: "t1", Path: f.Path, Line: 1, Side: model.DiffSideRight,
		Comments: []model.ReviewComment{
			{ID: "RC_1", Author: model.User{Login: "alice"}, Body: "![shot](https://e.com/a.png)", CreatedAt: time.Now()},
		},
	}}
	dv := NewDiffView()
	dv.SetFile(f)
	drawn(t, dv, 60, 30)
	if !hasCommentRow(dv, "[image: shot]") {
		t.Fatal("without an image source the image must stay a text placeholder")
	}

	var asked []string
	fake := &fakeImages{result: readyImage}
	dv.SetImageSource(func(id string) ImageSource {
		asked = append(asked, id)
		return fake
	})
	drawn(t, dv, 60, 30)

	if len(asked) != 1 || asked[0] != "RC_1" {
		t.Errorf("factory asked for subjects %v, want [RC_1]", asked)
	}
	if len(fake.calls) != 1 || fake.calls[0].src != "https://e.com/a.png" || fake.calls[0].maxCols != 56 {
		t.Errorf("source calls = %+v, want the comment's image with the border subtracted from the width", fake.calls)
	}
	if !hasCommentRow(dv, kitty.Rows(3, 2)[1]) {
		t.Error("the comment's rows do not contain the image placeholder cells")
	}
	if hasCommentRow(dv, "[image: shot]") {
		t.Error("the text placeholder is still shown beside a ready image")
	}
}

// TestDiffViewCursorRowIsFramedAndKeepsKindTint guards the frame-only cursor:
// the cursor line of an added row has bars at both edges, and the whole
// row — frame columns included — keeps the Add tint.
func TestDiffViewCursorRowIsFramedAndKeepsKindTint(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	dv.SetRect(0, 0, 60, 20)
	screen := newTestScreen(t, 60, 20)
	dv.Draw(screen)
	// Cursor starts on the first selectable row; move to the "+new2" line.
	for dv.rows[dv.selectable[dv.cursor]].primary().kind != diff.Add {
		dv.MoveBy(1)
	}
	dv.Draw(screen)

	y := 0
	for i := 0; i < dv.selectable[dv.cursor]; i++ {
		y += dv.rows[i].lineCount()
	}
	gw := SpanWidth(dv.rows[dv.selectable[dv.cursor]].primary().gutter)
	_, addBG, _ := theme.DiffLineStyle(diff.Add, false).Decompose()

	for _, x := range []int{0, 59} {
		if got := cellRune(screen, x, y); got != '┃' {
			t.Errorf("cell (%d,%d) = %q, want '┃'", x, y, got)
		}
		if _, bg, _ := cellStyle(screen, x, y).Decompose(); bg != addBG {
			t.Errorf("frame cell (%d,%d) background = %v, want the Add tint %v", x, y, bg, addBG)
		}
	}
	if _, bg, _ := cellStyle(screen, 1+gw+2, y).Decompose(); bg != addBG {
		t.Errorf("cursor content background = %v, want the Add tint %v", bg, addBG)
	}
	if got := cellRune(screen, 0, y-1); got == '┃' {
		t.Errorf("row above the cursor has a frame bar")
	}
}

// TestDiffViewTintSpansReservedColumns covers a tinted row away from the
// cursor: its kind tint reaches both reserved frame columns, so the band
// is not cut short at the edges.
func TestDiffViewTintSpansReservedColumns(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(twoHunkFile(t))
	dv.SetRect(0, 0, 60, 20)
	screen := newTestScreen(t, 60, 20)
	dv.Draw(screen)

	y, found := 0, false
	for i, row := range dv.rows {
		if row.kind == rowKindLine && row.primary().kind == diff.Add && i != dv.selectable[dv.cursor] {
			found = true
			break
		}
		y += row.lineCount()
	}
	if !found {
		t.Fatal("fixture has no added line away from the cursor")
	}
	_, addBG, _ := theme.DiffLineStyle(diff.Add, false).Decompose()
	for _, x := range []int{0, 59} {
		if got := cellRune(screen, x, y); got == '┃' {
			t.Errorf("non-cursor row has a frame bar at (%d,%d)", x, y)
		}
		if _, bg, _ := cellStyle(screen, x, y).Decompose(); bg != addBG {
			t.Errorf("edge cell (%d,%d) background = %v, want the Add tint %v", x, y, bg, addBG)
		}
	}
}

// wrapDiffView returns a DiffView showing one added line holding text,
// drawn at w x h with wrapping on, with the cursor on that line. Its row is
// rows[2] (file header, hunk header, line).
func wrapDiffView(t *testing.T, text string, w, h int) (*DiffView, tcell.SimulationScreen) {
	t.Helper()
	hunks, err := diff.Parse("@@ -1,1 +1,1 @@\n+" + text + "\n")
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, w, h)
	screen := newTestScreen(t, w, h)
	dv.Draw(screen)
	return dv, screen
}

// screenLine returns the runes of screen row y.
func screenLine(screen tcell.Screen, w, y int) string {
	rs := make([]rune, 0, w)
	for x := range w {
		rs = append(rs, cellRune(screen, x, y))
	}
	return string(rs)
}

func wrapLongText() string {
	var sb strings.Builder
	for i := range 100 {
		sb.WriteByte(byte('a' + i%26))
	}
	return sb.String()
}

func TestDiffViewWrapChopsLongLineIntoContinuationRows(t *testing.T) {
	text := wrapLongText()
	dv, screen := wrapDiffView(t, text, 30, 12)
	row := dv.rows[2]
	if row.kind != rowKindLine || row.lineCount() < 2 {
		t.Fatalf("row = kind %v lineCount %d, want a line row spanning several display lines", row.kind, row.lineCount())
	}
	gw := SpanWidth(row.primary().gutter)
	var joined strings.Builder
	for i, ln := range row.primary().content {
		for _, s := range ln {
			joined.WriteString(s.Text)
		}
		if i == 0 {
			continue
		}
		got := []rune(screenLine(screen, 30, 2+i))
		blank := string(got[1 : 1+gw])
		if strings.TrimSpace(blank) != "" {
			t.Errorf("continuation %d gutter = %q, want blank", i, blank)
		}
		want := ""
		for _, s := range ln {
			want += s.Text
		}
		if !strings.HasPrefix(string(got[1+gw:]), want) {
			t.Errorf("continuation %d = %q, want it to start with %q", i, string(got[1+gw:]), want)
		}
	}
	if joined.String() != text {
		t.Errorf("content across lines = %q, want %q", joined.String(), text)
	}
}

func TestDiffViewWrapDisablesHorizontalScroll(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	if dv.ScrollHorizontal(10) {
		t.Error("ScrollHorizontal reported success while wrapping")
	}
	dv.Draw(screen)
	if dv.HOffset() != 0 {
		t.Errorf("HOffset = %d, want 0 while wrapping", dv.HOffset())
	}
	for y := range 12 {
		if strings.ContainsRune(screenLine(screen, 30, y), '‹') {
			t.Errorf("line %d shows the scroll marker while wrapping", y)
		}
	}
}

func TestDiffViewWrapOffStillEllipsisAndScrolls(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	dv.ToggleWrap()
	if dv.Wrap() {
		t.Fatal("ToggleWrap left wrap on")
	}
	dv.Draw(screen)
	if got := dv.rows[2].lineCount(); got != 1 {
		t.Errorf("lineCount = %d, want 1 with wrap off", got)
	}
	if !strings.ContainsRune(screenLine(screen, 30, 2), '…') {
		t.Error("truncated line shows no ellipsis with wrap off")
	}
	dv.ScrollHorizontal(10)
	if dv.HOffset() == 0 {
		t.Error("ScrollHorizontal did nothing with wrap off")
	}
}

func TestDiffViewWrapFrameOnEveryDisplayLine(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	for dv.rows[dv.selectable[dv.cursor]].kind != rowKindLine {
		dv.MoveBy(1)
	}
	dv.Draw(screen)
	n := dv.rows[2].lineCount()
	if n < 2 {
		t.Fatalf("lineCount = %d, want >= 2", n)
	}
	for i := range n {
		if got := cellRune(screen, 0, 2+i); got != '┃' {
			t.Errorf("display line %d frame = %q, want '┃'", i, got)
		}
	}
}

// TestDiffViewWrapSelectionTintsEveryDisplayLine guards that a V selection
// on a wrapped line tints every display line of it, not just the first.
func TestDiffViewWrapSelectionTintsEveryDisplayLine(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	for dv.rows[dv.selectable[dv.cursor]].kind != rowKindLine {
		dv.MoveBy(1)
	}
	dv.StartVisual()
	dv.Draw(screen)
	n := dv.rows[2].lineCount()
	if n < 2 {
		t.Fatalf("lineCount = %d, want >= 2", n)
	}
	_, want, _ := theme.DiffLineStyle(diff.Add, true).Decompose()
	for i := range n {
		if _, bg, _ := cellStyle(screen, 15, 2+i).Decompose(); bg != want {
			t.Errorf("display line %d background = %v, want the selection tint %v", i, bg, want)
		}
	}
}

func TestDiffViewWrapSearchMatchAcrossBoundary(t *testing.T) {
	text := wrapLongText()
	hunks, err := diff.Parse("@@ -1,1 +1,1 @@\n+" + text + "\n")
	if err != nil {
		t.Fatal(err)
	}
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 30, 12)
	screen := newTestScreen(t, 30, 12)
	dv.Draw(screen) // learn the content width
	cw := len(dv.rows[2].primary().content[0][0].Text)
	// Match 2 cells on each side of the first wrap point.
	dv.SetSearch(regexp.MustCompile(regexp.QuoteMeta(text[cw-2 : cw+2])))
	dv.Draw(screen)

	gw := SpanWidth(dv.rows[2].primary().gutter)
	for _, c := range []struct{ y, x int }{{2, 1 + gw + cw - 2}, {2, 1 + gw + cw - 1}, {3, 1 + gw}, {3, 1 + gw + 1}} {
		if _, _, attr := cellStyle(screen, c.x, c.y).Decompose(); attr&tcell.AttrReverse == 0 {
			t.Errorf("cell (%d,%d) not Reverse", c.x, c.y)
		}
	}
	if _, _, attr := cellStyle(screen, 1+gw+2, 3).Decompose(); attr&tcell.AttrReverse != 0 {
		t.Error("cell after the match is Reverse")
	}
}

func TestDiffViewWrapWideRuneAtBoundary(t *testing.T) {
	dv, _ := wrapDiffView(t, "x", 30, 12)
	contentWidth := dv.lastWidth - SpanWidth(dv.rows[2].primary().gutter)

	text := repeatChar('a', contentWidth-1) + "あ" + repeatChar('b', 5)
	dv2, _ := wrapDiffView(t, text, 30, 12)
	lines := dv2.rows[2].primary().content
	if len(lines) != 2 {
		t.Fatalf("got %d display lines, want 2", len(lines))
	}
	for i, ln := range lines {
		if w := SpanWidth(ln); w > contentWidth {
			t.Errorf("line %d width %d exceeds content width %d", i, w, contentWidth)
		}
	}
	if !strings.HasPrefix(spansText(lines[1]), "あ") {
		t.Errorf("second line = %q, want it to start with the wide rune", spansText(lines[1]))
	}
}

func TestDiffViewToggleWrapKeepsCursor(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	for dv.rows[dv.selectable[dv.cursor]].kind != rowKindLine {
		dv.MoveBy(1)
	}
	before, _ := dv.CursorLine()
	dv.ToggleWrap()
	dv.Draw(screen)
	after, ok := dv.CursorLine()
	if !ok || after != before {
		t.Errorf("cursor line = %+v, want %+v", after, before)
	}
	dv.ToggleWrap()
	dv.Draw(screen)
	if again, _ := dv.CursorLine(); again != before {
		t.Errorf("cursor line = %+v after toggling back, want %+v", again, before)
	}
}

func TestDiffViewMoveHalfPageCountsWrappedLines(t *testing.T) {
	patch := "@@ -1,6 +1,6 @@\n"
	for range 6 {
		patch += " " + repeatChar('q', 100) + "\n"
	}
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatal(err)
	}
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	drawn(t, dv, 30, 12) // half a page = 6 display lines; each line is ~4 tall
	dv.MoveTop()
	dv.MoveHalfPage(1)
	l, ok := dv.CursorLine()
	if !ok || l.NewNo > 3 {
		t.Errorf("MoveHalfPage(1) landed on line %d, want within the first 3 (wrapped rows are tall)", l.NewNo)
	}
	if l.NewNo == 1 {
		t.Errorf("MoveHalfPage(1) did not move")
	}
}

func TestDiffViewWrapNarrowPaneDoesNotWrap(t *testing.T) {
	// The gutter alone fills the pane, leaving no cell for content.
	dv, _ := wrapDiffView(t, wrapLongText(), 9, 12)
	if n := dv.rows[2].lineCount(); n != 1 {
		t.Errorf("lineCount = %d, want 1 when no content cell is left", n)
	}
}

func TestDiffViewWrapUsesEachLinesOwnGutterWidth(t *testing.T) {
	hunks, err := diff.Parse("@@ -1,1 +1,1 @@\n+" + repeatChar('m', 100) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetFile(DiffFile{
		Path: "f.go", HasPatch: true, Hunks: hunks,
		DraftMarker: "😀", DraftLines: map[DraftAnchor]bool{{Hunk: 0, Line: 0}: true},
	})
	dv.SetRect(0, 0, 40, 30)
	dv.Draw(newTestScreen(t, 40, 30))
	row := dv.rows[2]
	cw := 38 - SpanWidth(row.primary().gutter)
	var joined strings.Builder
	for i, l := range row.primary().content {
		if SpanWidth(l) > cw {
			t.Errorf("line %d width %d exceeds %d", i, SpanWidth(l), cw)
		}
		joined.WriteString(spansText(l))
	}
	if joined.String() != repeatChar('m', 100) {
		t.Errorf("wrapped text %q lost or gained cells (stray ellipsis?)", joined.String())
	}
}

func TestDiffViewWrapToggleKeepsHorizontalScroll(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	dv.SetWrap(false)
	dv.Draw(screen)
	dv.ScrollHorizontal(7)
	if dv.HOffset() != 7 {
		t.Fatalf("HOffset = %d, want 7", dv.HOffset())
	}
	dv.SetWrap(true)
	dv.Draw(screen)
	if dv.HOffset() != 0 {
		t.Errorf("HOffset = %d while wrapping, want 0", dv.HOffset())
	}
	dv.SetWrap(false)
	dv.Draw(screen)
	if dv.HOffset() != 7 {
		t.Errorf("HOffset = %d after wrap off, want the earlier 7", dv.HOffset())
	}
}

// rowsPtr identifies the current rows slice: rebuild replaces it.
func rowsPtr(dv *DiffView) *diffRow { return &dv.rows[0] }

func TestDiffViewWrapKeepsHorizontalScrollAcrossAResize(t *testing.T) {
	dv, screen := wrapDiffView(t, wrapLongText(), 30, 12)
	dv.SetWrap(false)
	dv.Draw(screen)
	dv.ScrollHorizontal(7)
	dv.SetWrap(true)
	dv.SetRect(0, 0, 200, 12) // maxHOffset would be 0 here
	dv.Draw(newTestScreen(t, 200, 12))
	dv.SetRect(0, 0, 30, 12)
	dv.Draw(screen)
	dv.SetWrap(false)
	dv.Draw(screen)
	if dv.HOffset() != 7 {
		t.Errorf("HOffset = %d, want the saved 7", dv.HOffset())
	}
}

func TestDiffViewWrapDoesNotWrapAOneCellContentWidth(t *testing.T) {
	probe, _ := wrapDiffView(t, "x", 30, 12)
	gw := SpanWidth(probe.rows[2].primary().gutter)
	dv, _ := wrapDiffView(t, strings.Repeat("あ", 10), gw+3, 12) // inner width = pane - 2
	if n := dv.rows[2].lineCount(); n != 1 {
		t.Errorf("lineCount = %d, want 1: a wide rune cannot fit a 1-cell line", n)
	}
}

func TestDiffViewHeightShrinkKeepsTheCursorRowInView(t *testing.T) {
	patch := "@@ -1,31 +1,31 @@\n"
	for range 30 {
		patch += " c\n"
	}
	patch += " " + repeatChar('z', 60) + "\n" // wraps to 3 display lines at width 30
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatal(err)
	}
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
	dv.SetRect(0, 0, 30, 20)
	dv.Draw(newTestScreen(t, 30, 20))
	dv.MoveBottom()
	dv.Draw(newTestScreen(t, 30, 20))

	dv.SetRect(0, 0, 30, 10)
	screen := newTestScreen(t, 30, 10)
	dv.Draw(screen)
	zs := 0
	for y := range 10 {
		zs += strings.Count(screenLine(screen, 30, y), "z")
	}
	if zs != 60 {
		t.Errorf("%d of 60 cells of the cursor row are on screen after the pane shrank", zs)
	}
}

func TestDiffViewWrapRowCappedAtPaneHeight(t *testing.T) {
	dv, screen := wrapDiffView(t, repeatChar('z', 5000), 30, 12)
	if got := dv.rows[2].displayLines(12); got != 12 {
		t.Fatalf("displayLines = %d, want 12 (the pane height)", got)
	}
	x, y := 28, 11 // last content cell of the last display line; the capped row sits at the top
	if got := cellRune(screen, x, y); got != '…' {
		t.Errorf("last visible content cell = %q, want '…'", got)
	}
	if _, _, attr := cellStyle(screen, x, y).Decompose(); attr&tcell.AttrReverse != 0 {
		t.Error("ellipsis drawn Reverse")
	}
}

func TestDiffViewWrapCapFollowsPaneHeightWithoutARebuild(t *testing.T) {
	text := repeatChar('k', 12*20)
	dv, screen := wrapDiffView(t, text, 30, 5)
	if got := dv.rows[2].displayLines(5); got != 5 {
		t.Fatalf("displayLines = %d, want 5", got)
	}
	if got := cellRune(screen, 28, 4); got != '…' {
		t.Errorf("last visible content cell = %q, want '…'", got)
	}

	before := rowsPtr(dv)
	dv.SetRect(0, 0, 30, 40)
	screen = newTestScreen(t, 30, 40)
	dv.Draw(screen)
	if rowsPtr(dv) != before {
		t.Error("a height change rebuilt the rows")
	}
	ks := 0
	for y := range 40 {
		ks += strings.Count(screenLine(screen, 30, y), "k")
	}
	if ks != len(text) {
		t.Errorf("%d of %d cells on screen after growing the pane", ks, len(text))
	}
}

func TestDiffViewWrapCapEllipsisNeverTakesASearchStyle(t *testing.T) {
	tests := []struct {
		name, text, pattern string
	}{
		{"match in the hidden tail", strings.Repeat("あ", 40) + "MATCH", "MATCH"},
		{"last visible cell is a match", strings.Repeat("あ", 40) + "MATCH", "あ"},
		{"ascii match", repeatChar('x', 200), "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hunks, err := diff.Parse("@@ -1,1 +1,1 @@\n+" + tc.text + "\n")
			if err != nil {
				t.Fatal(err)
			}
			dv := NewDiffView()
			dv.SetWrap(true)
			dv.SetSearch(regexp.MustCompile(tc.pattern))
			dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
			dv.SetRect(0, 0, 30, 2)
			screen := newTestScreen(t, 30, 2)
			dv.Draw(screen)
			if got := cellRune(screen, 28, 1); got != '…' {
				t.Fatalf("last visible content cell = %q, want '…'", got)
			}
			fg, _, attr := cellStyle(screen, 28, 1).Decompose()
			wantFg, _, _ := theme.Muted.Decompose()
			if attr&tcell.AttrReverse != 0 || fg != wantFg {
				t.Errorf("ellipsis style fg %v attr %v, want theme.Muted without Reverse", fg, attr)
			}
		})
	}
}

func TestDiffViewWrapEllipsisOverAWideRuneBlanksItsLeftHalf(t *testing.T) {
	// Pane width 30 gives an odd content width (the last cell is spare) and
	// 31 an even one (the last cell is a wide rune's right half).
	for _, w := range []int{30, 31} {
		_, screen := wrapDiffView(t, strings.Repeat("あ", 300), w, 2)
		if got := cellRune(screen, w-2, 1); got != '…' {
			t.Errorf("w=%d: last content cell = %q, want '…'", w, got)
		}
		if w == 31 {
			if got := cellRune(screen, w-3, 1); got != ' ' {
				t.Errorf("w=%d: cell left of the ellipsis = %q, want a blank (no half rune)", w, got)
			}
		}
	}
}

func TestDiffViewDisplayLinesClampsOnlyLineRows(t *testing.T) {
	dv, _ := wrapDiffView(t, "abc", 30, 12)
	if got := dv.rows[2].displayLines(1); got != 1 {
		t.Errorf("displayLines = %d, want 1", got)
	}
	if got := dv.rows[0].displayLines(1); got != dv.rows[0].lineCount() {
		t.Errorf("a non-line row must not be clamped: %d vs %d", got, dv.rows[0].lineCount())
	}
}

func TestDiffViewWrapTruncatedAtMaxWrapLinesEndsInAMutedEllipsis(t *testing.T) {
	// Wide enough that 250 display lines of 20+ cells need ~5000+ cells; the
	// pane is taller than maxWrapLines so only the rebuild-time cut applies.
	h := maxWrapLines + 10
	dv, screen := wrapDiffView(t, repeatChar('q', 20*(maxWrapLines+50)), 30, h)
	half := dv.rows[2].primary()
	if len(half.content) != maxWrapLines || !half.truncated {
		t.Fatalf("got %d lines, truncated %v; want %d, true", len(half.content), half.truncated, maxWrapLines)
	}
	y := 2 + maxWrapLines - 1 // the row starts below the file and hunk headers
	if got := cellRune(screen, 28, y); got != '…' {
		t.Errorf("last content cell of line %d = %q, want '…'", maxWrapLines, got)
	}
	if got := cellRune(screen, 28, y+1); got == '…' {
		t.Error("an ellipsis was drawn past the last kept line")
	}
	fg, _, _ := cellStyle(screen, 28, y).Decompose()
	if wantFg, _, _ := theme.Muted.Decompose(); fg != wantFg {
		t.Errorf("ellipsis fg %v, want Muted %v", fg, wantFg)
	}
}
