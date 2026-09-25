package widget

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/highlight"
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
	return DiffFile{Path: "pkg/example.go", Status: model.FileStatusModified, Additions: 2, Deletions: 1, HasPatch: true, Hunks: hunks}
}

func rowText(r diffRow, li int) string {
	var sb []rune
	var spans []Span
	switch {
	case r.kind == rowKindLine:
		spans = append(append([]Span{}, r.gutter...), r.content...)
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
			for _, s := range r.gutter {
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
	// of them must land directly on the second hunk's first line
	// ("line10"), skipping the second hunk's header row entirely.
	for range 3 {
		dv.MoveBy(1)
	}
	last1, ok := dv.CursorLine()
	if !ok || last1.Text != "line3" {
		t.Fatalf("cursor after 3 moves = %+v, ok=%v, want the first hunk's last line (line3)", last1, ok)
	}

	dv.MoveBy(1)
	next, ok := dv.CursorLine()
	if !ok || next.Text != "line10" {
		t.Fatalf("cursor after moving past the first hunk = %+v, ok=%v, want the second hunk's first line (line10), not its header", next, ok)
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
	dv.SetFile(DiffFile{Path: "f.go", HasPatch: true, Hunks: hunks})
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

	// One more step crosses into the second hunk (line3 -> line10): the
	// selection must clamp to the anchor hunk's own last line rather than
	// spanning hunks.
	dv.MoveBy(2) // line3, then across the hunk boundary into line10
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

	dv.MoveTop()     // hunk index 0 (old line 10)
	dv.StartVisual() // anchor = index 0
	dv.MoveBy(5)     // cursor -> index 5 (old line 15); selects indices 0..5
	dv.Draw(screen)

	x, y, _, _ := dv.GetRect()
	// Row layout: y+0 file header, y+1 hunk header, y+2 is hunk index 0
	// (the anchor: selected, but not the cursor).
	_, gotBG, _ := cellStyle(screen, x, y+2).Decompose()
	_, wantBG, _ := theme.DiffLineStyle(diff.Context, false, true).Decompose()
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
	dv.NextThread()
	first, ok := dv.CursorThread()
	if !ok || first.ID != "t1" {
		t.Fatalf("NextThread() from the top landed on %+v, ok=%v, want t1", first, ok)
	}

	dv.NextThread()
	second, ok := dv.CursorThread()
	if !ok || second.ID != "t2" {
		t.Fatalf("second NextThread() landed on %+v, ok=%v, want t2", second, ok)
	}

	// No third thread: NextThread must not move (clamp, no wrap).
	dv.NextThread()
	if id, _ := dv.CursorThread(); id.ID != "t2" {
		t.Errorf("NextThread() past the last thread moved to %+v, want it to stay on t2", id)
	}

	dv.PrevThread()
	back, ok := dv.CursorThread()
	if !ok || back.ID != "t1" {
		t.Fatalf("PrevThread() = %+v, ok=%v, want t1", back, ok)
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
// an off-by-one in rebuild's maxHOffset computation: contentWidthFor(width,
// gutterWidth) is the content width available when hOffset == 0 (no "‹"
// marker drawn yet), but drawLineRow spends one of those cells on the "‹"
// marker as soon as hOffset > 0, so the content actually visible while
// scrolled is one cell narrower than contentWidthFor's own return value.
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
		for _, s := range r.gutter {
			if s.Text == "M" {
				marked = true
			}
		}
		if marked {
			markedRows++
			if r.hunk != 0 || r.line != 1 {
				t.Errorf("draft marker rendered on row (hunk=%d, line=%d), want only (0, 1)", r.hunk, r.line)
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
