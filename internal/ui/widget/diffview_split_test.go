package widget

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// splitPatch pairs as: ctx1|ctx1, del2a|add2a, del2b|-, ctx3|ctx3, -|tail.
const splitPatch = `@@ -1,4 +1,4 @@
 ctx1
-del2a
-del2b
+add2a
 ctx3
+tail
`

// At a 60-wide pane the layout is: reserved column 0, left half x 1..28,
// separator x 29, right half x 30..58, reserved column 59. Rows: 0 file
// header, 1 hunk header, 2..6 the five pairs (screen y equals row index).
const (
	splitW   = 60
	splitSep = 29
)

func splitFileOf(t *testing.T, patch string) DiffFile {
	t.Helper()
	hunks, err := diff.Parse(patch)
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return DiffFile{Path: "f.go", Status: model.FileStatusModified, HasPatch: true, Hunks: hunks, HeadKnown: true, HeadLineCount: 100}
}

func splitView(t *testing.T) (*DiffView, tcell.SimulationScreen) {
	t.Helper()
	return splitViewOf(t, splitFileOf(t, splitPatch), splitW, 20)
}

func splitViewOf(t *testing.T, f DiffFile, w, h int) (*DiffView, tcell.SimulationScreen) {
	t.Helper()
	dv := NewDiffView()
	dv.SetSplit(true)
	dv.SetFile(f)
	dv.SetRect(0, 0, w, h)
	screen := newTestScreen(t, w, h)
	dv.Draw(screen)
	return dv, screen
}

// moveToRow puts the cursor on rows[idx].
func moveToRow(t *testing.T, dv *DiffView, idx int) {
	t.Helper()
	for i, r := range dv.selectable {
		if r == idx {
			dv.setCursor(i)
			return
		}
	}
	t.Fatalf("row %d is not selectable", idx)
}

func cursorText(t *testing.T, dv *DiffView) string {
	t.Helper()
	l, ok := dv.CursorLine()
	if !ok {
		t.Fatal("cursor is not on a line")
	}
	return l.Text
}

func textBetween(screen tcell.Screen, x0, x1, y int) string {
	var sb strings.Builder
	for x := x0; x < x1; x++ {
		sb.WriteRune(cellRune(screen, x, y))
	}
	return sb.String()
}

func TestDiffViewSplitPairsLinesIntoTwoHalves(t *testing.T) {
	dv, screen := splitView(t)
	want := [][2]int{{0, 0}, {1, 3}, {2, -1}, {4, 4}, {-1, 5}}
	for i, w := range want {
		r := dv.rows[2+i]
		if r.kind != rowKindLine || len(r.halves) != 2 {
			t.Fatalf("row %d: kind %v with %d halves, want a two-half line row", 2+i, r.kind, len(r.halves))
		}
		if r.halves[0].line != w[0] || r.halves[1].line != w[1] {
			t.Errorf("row %d lines = (%d,%d), want (%d,%d)", 2+i, r.halves[0].line, r.halves[1].line, w[0], w[1])
		}
	}
	if got := textBetween(screen, 1, splitSep, 3); !strings.Contains(got, "del2a") {
		t.Errorf("left half of the modified pair = %q, want del2a", got)
	}
	if got := textBetween(screen, splitSep+1, splitW-1, 3); !strings.Contains(got, "add2a") {
		t.Errorf("right half of the modified pair = %q, want add2a", got)
	}
}

func TestDiffViewSplitGutterShowsOldNumberLeftAndNewNumberRight(t *testing.T) {
	_, screen := splitView(t)
	// ctx3 is old line 4 and new line 3; each half carries only its own number.
	if got := cellRune(screen, 1, 5); got != '4' {
		t.Errorf("left gutter number = %q, want '4'", got)
	}
	if got := cellRune(screen, splitSep+1, 5); got != '3' {
		t.Errorf("right gutter number = %q, want '3'", got)
	}
}

func TestDiffViewSplitBlankHalfUsesFillerBackground(t *testing.T) {
	_, screen := splitView(t)
	if got := cellBG(screen, 40, 4); got != theme.DiffFillerBg {
		t.Errorf("blank right half bg = %v, want the filler %v", got, theme.DiffFillerBg)
	}
	if got := cellBG(screen, 5, 6); got != theme.DiffFillerBg {
		t.Errorf("blank left half bg = %v, want the filler %v", got, theme.DiffFillerBg)
	}
	_, delBG, _ := theme.DiffLineStyle(diff.Del, false).Decompose()
	if got := cellBG(screen, 5, 4); got != delBG {
		t.Errorf("left Del half bg = %v, want the Del tint %v", got, delBG)
	}
}

func TestDiffViewSplitSeparatorColumn(t *testing.T) {
	dv, screen := splitView(t)
	dv.MoveBottom() // the cursor row shows a bar instead of the separator
	dv.Draw(screen)
	for y := 2; y <= 5; y++ {
		if got := cellRune(screen, splitSep, y); got != '│' {
			t.Errorf("separator at y=%d = %q, want '│'", y, got)
		}
	}
}

func TestDiffViewSplitCursorFallsBackToTheOtherHalf(t *testing.T) {
	dv, _ := splitView(t)
	moveToRow(t, dv, 4) // del2b | blank, preference is right
	if got := cursorText(t, dv); got != "del2b" {
		t.Errorf("cursor line = %q, want del2b (right half is blank)", got)
	}
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 6) // blank | tail
	if got := cursorText(t, dv); got != "tail" {
		t.Errorf("cursor line = %q, want tail (left half is blank)", got)
	}
}

func TestDiffViewSplitSideSwitchKeepsTheRow(t *testing.T) {
	dv, _ := splitView(t)
	moveToRow(t, dv, 3)
	if got := cursorText(t, dv); got != "add2a" {
		t.Errorf("default side line = %q, want add2a", got)
	}
	cur := dv.cursor
	dv.SetCursorSide(model.DiffSideLeft)
	if got := cursorText(t, dv); got != "del2a" {
		t.Errorf("after left = %q, want del2a", got)
	}
	if dv.cursor != cur {
		t.Errorf("cursor row moved from %d to %d", cur, dv.cursor)
	}
}

func TestDiffViewSplitCursorFrameSurroundsOnlyTheActiveHalf(t *testing.T) {
	dv, screen := splitView(t)
	moveToRow(t, dv, 3)
	dv.Draw(screen)
	bars := func() (l, s, r bool) {
		return cellRune(screen, 0, 3) == '┃', cellRune(screen, splitSep, 3) == '┃', cellRune(screen, splitW-1, 3) == '┃'
	}
	if l, s, r := bars(); l || !s || !r {
		t.Errorf("right active: bars left/sep/right = %v/%v/%v, want false/true/true", l, s, r)
	}
	dv.SetCursorSide(model.DiffSideLeft)
	dv.Draw(screen)
	if l, s, r := bars(); !l || !s || r {
		t.Errorf("left active: bars left/sep/right = %v/%v/%v, want true/true/false", l, s, r)
	}
}

func TestDiffViewSplitSelectionIsSideFiltered(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 3)
	dv.StartVisual()
	dv.MoveBy(1)
	dv.Draw(screen)

	sel, ok := dv.Selection()
	if !ok || len(sel) == 0 {
		t.Fatalf("Selection() = %v, %v", sel, ok)
	}
	for _, l := range sel {
		if l.Kind != diff.Del {
			t.Errorf("selected %q of kind %v, want only Del lines", l.Text, l.Kind)
		}
	}
	_, selBG, _ := theme.DiffLineStyle(diff.Context, true).Decompose()
	_, addBG, _ := theme.DiffLineStyle(diff.Add, false).Decompose()
	if got := cellBG(screen, 5, 3); got != selBG {
		t.Errorf("selected left half bg = %v, want %v", got, selBG)
	}
	if got := cellBG(screen, 40, 3); got != addBG {
		t.Errorf("unselected right half bg = %v, want the Add tint %v", got, addBG)
	}
	if got := cellBG(screen, 5, 4); got != selBG {
		t.Errorf("second selected left half bg = %v, want %v", got, selBG)
	}
}

func TestDiffViewSplitSelectionKeepsContextAndDropsTheOtherSide(t *testing.T) {
	dv, _ := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 2)
	dv.StartVisual()
	moveToRow(t, dv, 5)
	sel, _ := dv.Selection()
	var got []string
	for _, l := range sel {
		got = append(got, l.Text)
	}
	if want := "ctx1 del2a del2b ctx3"; strings.Join(got, " ") != want {
		t.Errorf("Selection() = %v, want %q (the Add line is on the other side)", got, want)
	}
}

func TestDiffViewSplitSkipsRowsWithoutTheSelectedSideWhileSelecting(t *testing.T) {
	dv, _ := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 4)
	dv.StartVisual()
	moveToRow(t, dv, 6) // blank | tail: nothing on the left
	sel, _ := dv.Selection()
	if len(sel) != 1 || sel[0].Text != "del2b" {
		t.Errorf("Selection() = %v, want only del2b", sel)
	}
}

func TestDiffViewSplitHAndLIgnoredInVisual(t *testing.T) {
	dv, _ := splitView(t)
	moveToRow(t, dv, 3)
	dv.StartVisual()
	dv.SetCursorSide(model.DiffSideLeft)
	if got := cursorText(t, dv); got != "add2a" {
		t.Errorf("cursor line = %q after h in visual, want add2a (side pinned)", got)
	}
}

func TestDiffViewSplitJumpToLineSideHandling(t *testing.T) {
	dv, _ := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	dv.JumpToLine(model.DiffSideRight, 1) // ctx1
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "ctx1" || dv.cursorSide != model.DiffSideLeft {
		t.Errorf("context jump: line %q side %v, want ctx1 and the kept left side", got, dv.cursorSide)
	}
	dv.SetCursorSide(model.DiffSideRight)
	dv.JumpToLine(model.DiffSideLeft, 3) // del2b
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "del2b" || dv.cursorSide != model.DiffSideLeft {
		t.Errorf("Del jump: line %q side %v, want del2b on the left", got, dv.cursorSide)
	}
	dv.SetCursorSide(model.DiffSideLeft)
	dv.JumpToLine(model.DiffSideRight, 2) // add2a
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "add2a" || dv.cursorSide != model.DiffSideRight {
		t.Errorf("Add jump: line %q side %v, want add2a on the right", got, dv.cursorSide)
	}
}

func TestDiffViewSplitThreadsFollowTheirRowLeftThenRight(t *testing.T) {
	f := splitFileOf(t, splitPatch)
	mk := func(id string, side model.DiffSide, line int) model.ReviewThread {
		return model.ReviewThread{
			ID: id, Path: f.Path, Line: line, Side: side,
			Comments: []model.ReviewComment{{Author: model.User{Login: "alice"}, Body: "body " + id, CreatedAt: time.Now()}},
		}
	}
	f.Threads = []model.ReviewThread{
		mk("r", model.DiffSideRight, 2), // add2a
		mk("l", model.DiffSideLeft, 2),  // del2a
		mk("c", model.DiffSideRight, 1), // ctx1: both halves, listed once
	}
	dv, _ := splitViewOf(t, f, splitW, 40)

	var order []string
	for _, r := range dv.rows {
		switch r.kind {
		case rowKindLine:
			order = append(order, fmt.Sprintf("line%d", r.halves[0].line))
		case rowKindThreadHeader:
			order = append(order, r.threadID)
		}
	}
	if got, want := strings.Join(order, " "), "line0 c line1 l r line2 line4 line-1"; got != want {
		t.Errorf("row order = %q, want %q", got, want)
	}
}

func TestDiffViewSplitDraftMarkerPerHalf(t *testing.T) {
	f := splitFileOf(t, splitPatch)
	f.DraftMarker = "M"
	f.DraftLines = map[DraftAnchor]bool{{Hunk: 0, Line: 1, Side: model.DiffSideLeft}: true} // del2a
	dv, _ := splitViewOf(t, f, splitW, 20)
	has := func(h diffHalf) bool {
		for _, s := range h.gutter {
			if s.Text == "M" {
				return true
			}
		}
		return false
	}
	r := dv.rows[3]
	if !has(r.halves[0]) || has(r.halves[1]) {
		t.Errorf("draft marker left/right = %v/%v, want true/false", has(r.halves[0]), has(r.halves[1]))
	}
}

func TestDiffViewSplitFallsBackToUnifiedWhenHalvesAreTooNarrow(t *testing.T) {
	for _, tc := range []struct {
		width     int
		wantHalve int
	}{{36, 1}, {37, 2}} {
		dv, _ := splitViewOf(t, splitFileOf(t, splitPatch), tc.width, 20)
		if !dv.Split() {
			t.Errorf("width %d: Split() = false, want the requested true", tc.width)
		}
		if got := len(dv.rows[2].halves); got != tc.wantHalve {
			t.Errorf("width %d: %d halves, want %d", tc.width, got, tc.wantHalve)
		}
	}
}

func longPairFile(t *testing.T, n int) DiffFile {
	t.Helper()
	long := strings.Repeat("abcdefghij", 4)
	return splitFileOf(t, "@@ -1,1 +1,1 @@\n-"+long+"\n+"+long[:n]+"\n")
}

func TestDiffViewSplitHOffsetScrollsBothHalves(t *testing.T) {
	dv, screen := splitViewOf(t, longPairFile(t, 40), splitW, 20)
	// Each half's content is 23 wide (28 less a 5-column gutter).
	if want := 40 - 23 + 1; dv.maxHOffset != want {
		t.Errorf("maxHOffset = %d, want %d", dv.maxHOffset, want)
	}
	dv.ScrollHorizontal(3)
	dv.Draw(screen)
	for _, x := range []int{1 + 5, splitSep + 1 + 5} {
		if got := cellRune(screen, x, 2); got != '‹' {
			t.Errorf("scroll marker at x=%d = %q, want '‹'", x, got)
		}
	}
}

func TestDiffViewSplitWrapRowHeightIsTheTallerHalf(t *testing.T) {
	f := splitFileOf(t, "@@ -1,1 +1,1 @@\n-"+strings.Repeat("a", 100)+"\n+short\n")
	dv := NewDiffView()
	dv.SetWrap(true)
	dv.SetSplit(true)
	dv.SetFile(f)
	dv.SetRect(0, 0, splitW, 20)
	screen := newTestScreen(t, splitW, 20)
	dv.Draw(screen)

	// 100 cells at 23 per line: 5 lines; the short right half is 1.
	if got := dv.rows[2].lineCount(); got != 5 {
		t.Fatalf("lineCount = %d, want 5", got)
	}
	_, addBG, _ := theme.DiffLineStyle(diff.Add, false).Decompose()
	if got := cellBG(screen, 40, 2+3); got != addBG {
		t.Errorf("right half's extra line bg = %v, want the Add tint %v", got, addBG)
	}
}

func TestDiffViewSplitWordEmphasisOnBothHalves(t *testing.T) {
	dv, screen := splitViewOf(t, emphasisFile(t), splitW, 20)
	_ = dv
	count := func(x0, x1 int) int {
		n := 0
		for x := x0; x < x1; x++ {
			if theme.IsDiffWordBg(cellBG(screen, x, 2)) {
				n++
			}
		}
		return n
	}
	if l, r := count(1, splitSep), count(splitSep+1, splitW-1); l == 0 || r == 0 {
		t.Errorf("emphasised cells left/right = %d/%d, want both > 0", l, r)
	}
}

func TestDiffViewSplitToggleKeepsTheCursorLineAndKeepsItInView(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("@@ -1,20 +1,21 @@\n")
	for i := range 20 {
		fmt.Fprintf(&sb, "-old%d\n", i)
	}
	for i := range 20 {
		fmt.Fprintf(&sb, "+new%d\n", i)
	}
	sb.WriteString("+last\n")
	dv := NewDiffView()
	dv.SetFile(splitFileOf(t, sb.String()))
	dv.SetCursorSide(model.DiffSideLeft)
	dv.SetRect(0, 0, splitW, 10)
	screen := newTestScreen(t, splitW, 10)
	dv.Draw(screen)
	dv.MoveBottom() // the trailing gap row
	dv.MoveBy(-1)
	dv.Draw(screen)

	dv.ToggleSplit()
	dv.Draw(screen)
	if got := cursorText(t, dv); got != "last" {
		t.Errorf("cursor line after toggling to split = %q, want last", got)
	}
	if dv.cursorSide != model.DiffSideRight {
		t.Errorf("cursor side = %v, want right (the Add line's side)", dv.cursorSide)
	}
	if !strings.Contains(screenText(screen, splitW, 10), "last") {
		t.Errorf("cursor line is off screen in split:\n%s", screenText(screen, splitW, 10))
	}

	dv.ToggleSplit()
	dv.Draw(screen)
	if got := cursorText(t, dv); got != "last" {
		t.Errorf("cursor line after toggling back = %q, want last", got)
	}
	if !strings.Contains(screenText(screen, splitW, 10), "last") {
		t.Errorf("cursor line is off screen in unified:\n%s", screenText(screen, splitW, 10))
	}
}

func TestDiffViewToggleSplitEndsVisualMode(t *testing.T) {
	dv, _ := splitView(t)
	moveToRow(t, dv, 3)
	dv.StartVisual()
	dv.ToggleSplit()
	drawn(t, dv, splitW, 20)
	if dv.InVisual() {
		t.Error("toggling the layout left visual mode on")
	}
	if dv.Split() {
		t.Error("ToggleSplit did not switch split off")
	}
}

func screenText(screen tcell.Screen, w, h int) string {
	var sb strings.Builder
	for y := range h {
		sb.WriteString(screenLine(screen, w, y))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestDiffViewCommentSide(t *testing.T) {
	t.Run("unified is not explicit", func(t *testing.T) {
		dv, _ := splitViewOf(t, splitFileOf(t, splitPatch), 30, 20)
		moveToRow(t, dv, 3)
		if _, explicit := dv.CommentSide(); explicit {
			t.Error("explicit = true in the unified layout")
		}
	})
	t.Run("follows the column", func(t *testing.T) {
		dv, _ := splitView(t)
		moveToRow(t, dv, 3)
		if side, explicit := dv.CommentSide(); !explicit || side != model.DiffSideRight {
			t.Errorf("CommentSide = %v, %v, want RIGHT, true", side, explicit)
		}
		dv.SetCursorSide(model.DiffSideLeft)
		moveToRow(t, dv, 5) // a context row: the side is the column, not the kind
		if side, explicit := dv.CommentSide(); !explicit || side != model.DiffSideLeft {
			t.Errorf("CommentSide = %v, %v, want LEFT, true", side, explicit)
		}
		moveToRow(t, dv, 6) // blank | tail falls back to the right half
		if side, _ := dv.CommentSide(); side != model.DiffSideRight {
			t.Errorf("CommentSide on a row with a blank left = %v, want RIGHT", side)
		}
	})
	t.Run("pinned while selecting", func(t *testing.T) {
		dv, _ := splitView(t)
		dv.SetCursorSide(model.DiffSideLeft)
		moveToRow(t, dv, 4)
		dv.StartVisual()
		moveToRow(t, dv, 6) // nothing on the left
		if side, explicit := dv.CommentSide(); !explicit || side != model.DiffSideLeft {
			t.Errorf("CommentSide = %v, %v, want LEFT, true", side, explicit)
		}
	})
}

func TestDiffViewSplitVisualFramesThePinnedSideEvenWhenBlank(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 4)
	dv.StartVisual()
	moveToRow(t, dv, 6) // blank | tail, reached by a jump: movement keys skip it
	dv.Draw(screen)
	if cellRune(screen, 0, 6) != '┃' || cellRune(screen, splitSep, 6) != '┃' || cellRune(screen, splitW-1, 6) == '┃' {
		t.Errorf("bars on the blank pinned row: left %q sep %q right %q, want the left half framed",
			cellRune(screen, 0, 6), cellRune(screen, splitSep, 6), cellRune(screen, splitW-1, 6))
	}
}

func TestDiffViewSplitTooNarrowKeepsVisualAndViewport(t *testing.T) {
	dv, screen := splitViewOf(t, splitFileOf(t, splitPatch), 30, 20)
	if dv.SplitShown() {
		t.Error("SplitShown() = true on a pane too narrow for split")
	}
	moveToRow(t, dv, 3)
	dv.StartVisual()
	dv.ToggleSplit() // off
	dv.ToggleSplit() // on again, still unshown
	dv.Draw(screen)
	if !dv.InVisual() {
		t.Error("toggling a split that is never shown ended visual mode")
	}
	if !dv.Split() || dv.SplitShown() {
		t.Errorf("Split/SplitShown = %v/%v, want true/false", dv.Split(), dv.SplitShown())
	}
}

func TestDiffViewSplitShownFollowsTheLayoutAtOnce(t *testing.T) {
	dv, _ := splitView(t)
	if !dv.SplitShown() {
		t.Error("SplitShown() = false on a wide pane")
	}
	dv.ToggleSplit()
	if dv.SplitShown() {
		t.Error("SplitShown() = true right after toggling split off")
	}
	dv.ToggleSplit()
	if !dv.SplitShown() {
		t.Error("SplitShown() = false right after toggling split on")
	}
}

func TestDiffViewResizeAcrossTheFallbackEndsAPinnedSelectionAndAdoptsTheSide(t *testing.T) {
	t.Run("split to unified", func(t *testing.T) {
		dv, screen := splitView(t)
		moveToRow(t, dv, 4) // del2b | blank, preference stays right
		dv.StartVisual()
		dv.SetRect(0, 0, 30, 20)
		dv.Draw(screen)
		if dv.InVisual() {
			t.Error("selection survived the layout change")
		}
		if dv.cursorSide != model.DiffSideLeft {
			t.Errorf("cursorSide = %v, want LEFT (the Del line's side)", dv.cursorSide)
		}
	})
	t.Run("unified to split", func(t *testing.T) {
		dv, screen := splitViewOf(t, splitFileOf(t, splitPatch), 30, 20)
		dv.SetCursorSide(model.DiffSideLeft)
		moveToRow(t, dv, 7) // tail (unified rows: one per line)
		dv.StartVisual()
		dv.SetRect(0, 0, splitW, 20)
		dv.Draw(screen)
		if dv.InVisual() {
			t.Error("selection survived the layout change")
		}
		if dv.cursorSide != model.DiffSideRight {
			t.Errorf("cursorSide = %v, want RIGHT (the Add line's side)", dv.cursorSide)
		}
		if got := cursorText(t, dv); got != "tail" {
			t.Errorf("cursor line = %q, want tail", got)
		}
	})
}

func hasMark(h diffHalf, mark string) bool {
	for _, sp := range h.gutter {
		if sp.Text == mark {
			return true
		}
	}
	return false
}

func TestDiffViewDraftMarkerFollowsItsSideInSplit(t *testing.T) {
	for _, side := range []model.DiffSide{model.DiffSideLeft, model.DiffSideRight} {
		f := splitFileOf(t, splitPatch)
		f.DraftMarker = "M"
		f.DraftLines = map[DraftAnchor]bool{{Hunk: 0, Line: 0, Side: side}: true} // ctx1, on both sides
		dv, _ := splitViewOf(t, f, splitW, 20)
		l, r := hasMark(dv.rows[2].halves[0], "M"), hasMark(dv.rows[2].halves[1], "M")
		if wantL := side == model.DiffSideLeft; l != wantL || r == wantL {
			t.Errorf("%v draft on a context row: marks left/right = %v/%v", side, l, r)
		}
	}
}

// In unified a line is marked only for the side "c" would anchor it to
// (diff.Anchor: Del LEFT, otherwise RIGHT): a LEFT draft on a context line
// could not be reopened there, so it shows nothing.
func TestDiffViewDraftMarkerInUnifiedFollowsTheKindDerivedSide(t *testing.T) {
	for _, tc := range []struct {
		name string
		line int // index into the hunk
		side model.DiffSide
		want bool
	}{
		{"context RIGHT", 0, model.DiffSideRight, true},
		{"context LEFT", 0, model.DiffSideLeft, false},
		{"del LEFT", 1, model.DiffSideLeft, true},
		{"del RIGHT", 1, model.DiffSideRight, false},
		{"add RIGHT", 3, model.DiffSideRight, true},
	} {
		f := splitFileOf(t, splitPatch)
		f.DraftMarker = "M"
		f.DraftLines = map[DraftAnchor]bool{{Hunk: 0, Line: tc.line, Side: tc.side}: true}
		dv, _ := splitViewOf(t, f, 30, 20) // unified
		var got bool
		for _, r := range dv.rows {
			if r.kind == rowKindLine && r.halves[0].line == tc.line {
				got = hasMark(r.halves[0], "M")
			}
		}
		if got != tc.want {
			t.Errorf("%s: marked = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDiffViewJumpToAnchorAdoptsTheSideOfAContextLine(t *testing.T) {
	dv, _ := splitView(t)
	dv.JumpToAnchor(model.DiffSideLeft, 1) // ctx1, preference is right
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "ctx1" || dv.cursorSide != model.DiffSideLeft {
		t.Errorf("line %q side %v, want ctx1 on the left", got, dv.cursorSide)
	}
	dv.JumpToAnchor(model.DiffSideRight, 3) // ctx3 (new number 3)
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "ctx3" || dv.cursorSide != model.DiffSideRight {
		t.Errorf("line %q side %v, want ctx3 on the right", got, dv.cursorSide)
	}
}

func TestDiffViewSplitVisualMovementSkipsRowsBlankOnThePinnedSide(t *testing.T) {
	dv, _ := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 4) // del2b | blank
	dv.StartVisual()
	dv.MoveBy(1) // ctx3
	if got := dv.selectable[dv.cursor]; got != 5 {
		t.Fatalf("MoveBy(1) landed on row %d, want 5", got)
	}
	dv.MoveBy(1) // row 6 has nothing on the left: on to the gap row
	if got := dv.selectable[dv.cursor]; got != 7 {
		t.Errorf("MoveBy(1) landed on row %d, want 7 (row 6 skipped)", got)
	}

	dv, _ = splitView(t) // pinned right: row 4 has nothing on the right
	moveToRow(t, dv, 6)
	dv.StartVisual()
	dv.MoveBy(-2)
	if got := dv.selectable[dv.cursor]; got != 3 {
		t.Errorf("MoveBy(-2) landed on row %d, want 3 (row 4 skipped)", got)
	}
}

func TestDiffViewSplitVisualMoveTopSkipsLeadingBlankRows(t *testing.T) {
	f := splitFileOf(t, "@@ -1,2 +1,2 @@\n+new\n ctx\n-d\n")
	dv, _ := splitViewOf(t, f, splitW, 20)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 3) // ctx
	dv.StartVisual()
	dv.MoveTop() // row 2 is Add-only: nothing on the left
	if got := dv.selectable[dv.cursor]; got != 3 {
		t.Errorf("MoveTop landed on row %d, want 3", got)
	}
}

func TestDiffViewSplitSearchCurrentOnAContextRowStylesTheRightHalfOnly(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetSearch(regexp.MustCompile("ctx1"))
	dv.SetSearchCurrent("f.go", 0, 0, 0, 4)
	dv.Draw(screen)
	matchFg, _, _ := theme.SearchMatch.Decompose()
	currentFg, _, _ := theme.SearchCurrent.Decompose()
	// Content starts after the 1+5-column left gutter / 5-column right one.
	lfg, _, lattr := cellStyle(screen, 1+5, 2).Decompose()
	rfg, _, rattr := cellStyle(screen, splitSep+1+5, 2).Decompose()
	if lfg != matchFg || lattr&tcell.AttrBold != 0 {
		t.Errorf("left half style fg %v bold %v, want the plain SearchMatch", lfg, lattr&tcell.AttrBold != 0)
	}
	if rfg != currentFg || rattr&tcell.AttrBold == 0 {
		t.Errorf("right half style fg %v bold %v, want SearchCurrent", rfg, rattr&tcell.AttrBold != 0)
	}
}

func TestDiffViewSplitVisualPagingCountsOnlyRowsItCanStopOn(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("@@ -1,61 +1,31 @@\n ctx\n")
	for i := range 30 {
		fmt.Fprintf(&sb, "-del%d\n", i)
	}
	for i := range 30 {
		fmt.Fprintf(&sb, " tail%d\n", i)
	}
	dv, _ := splitViewOf(t, splitFileOf(t, sb.String()), splitW, 10)
	moveToRow(t, dv, 2) // ctx; pinned right, so the 30 Del-only rows are blank
	dv.StartVisual()
	dv.MovePage(1)
	if l, _ := dv.CursorLine(); l.Text != "tail0" {
		t.Errorf("MovePage landed on %q, want tail0 (the first row it can stop on past the block)", l.Text)
	}
}

func TestDiffViewSplitVisualMoveBottomAndHalfPageStopOnValidRows(t *testing.T) {
	f := splitFileOf(t, splitPatch)
	f.HeadLineCount = 4 // no trailing gap row: the last row is blank | tail
	dv, _ := splitViewOf(t, f, splitW, 10)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 2)
	dv.StartVisual()
	dv.MoveBottom()
	if got := dv.selectable[dv.cursor]; got != 5 {
		t.Errorf("MoveBottom landed on row %d, want 5 (row 6 has nothing on the left)", got)
	}
	moveToRow(t, dv, 2)
	dv.MoveHalfPage(1)
	if got := dv.selectable[dv.cursor]; dv.pinnedBlank(dv.rows[got]) {
		t.Errorf("MoveHalfPage landed on row %d, blank on the pinned side", got)
	}
}

func TestDiffViewSplitSelectionClampsToTheAnchorHunkAndStaysOneSided(t *testing.T) {
	f := splitFileOf(t, "@@ -1,3 +1,3 @@\n c1\n-d1\n+a1\n c2\n@@ -20,1 +20,1 @@\n-x\n+y\n")
	f.HeadLineCount = 20 // no trailing gap row
	dv, _ := splitViewOf(t, f, splitW, 30)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 3) // d1 | a1
	dv.StartVisual()
	dv.MoveBottom()
	if dv.curLineHunk != 1 {
		t.Fatalf("cursor ended in hunk %d, want 1", dv.curLineHunk)
	}
	sel, ok := dv.Selection()
	var got []string
	for _, l := range sel {
		got = append(got, l.Text)
	}
	if !ok || strings.Join(got, " ") != "d1 c2" {
		t.Errorf("Selection() = %v, %v, want d1 c2 (clamped to hunk 0, no Add)", got, ok)
	}
}

func TestDiffViewSplitSelectionTintsOnlyThePinnedHalfOfContextRows(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 2) // ctx1
	dv.StartVisual()
	dv.MoveBy(1)
	dv.Draw(screen)
	_, selBG, _ := theme.DiffLineStyle(diff.Context, true).Decompose()
	_, ctxBG, _ := theme.DiffLineStyle(diff.Context, false).Decompose()
	if got := cellBG(screen, 8, 2); got != selBG {
		t.Errorf("pinned (left) half of the context row bg = %v, want %v", got, selBG)
	}
	if got := cellBG(screen, 40, 2); got != ctxBG {
		t.Errorf("other (right) half of the context row bg = %v, want the plain %v", got, ctxBG)
	}
}

func TestDiffViewRelayoutKeepsTheCursorScreenLine(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("@@ -1,20 +1,21 @@\n")
	for i := range 20 {
		fmt.Fprintf(&sb, "-old%d\n", i)
	}
	for i := range 20 {
		fmt.Fprintf(&sb, "+new%d\n", i)
	}
	sb.WriteString("+last\n")
	f := splitFileOf(t, sb.String())
	lineOf := func(screen tcell.Screen, w int) int {
		for y := range 10 {
			if strings.Contains(screenLine(screen, w, y), "last") {
				return y
			}
		}
		return -1
	}
	dv, screen := splitViewOf(t, f, 30, 10) // requested split, too narrow
	dv.MoveBottom()
	dv.MoveBy(-1)
	dv.Draw(screen)
	before := lineOf(screen, 30)
	dv.SetRect(0, 0, splitW, 10)
	screen = newTestScreen(t, splitW, 10)
	dv.Draw(screen)
	if got := lineOf(screen, splitW); got != before {
		t.Errorf("cursor line moved from screen line %d to %d when split became shown", before, got)
	}
	dv.SetRect(0, 0, 30, 10)
	screen = newTestScreen(t, 30, 10)
	dv.Draw(screen)
	if got := lineOf(screen, 30); got != before {
		t.Errorf("cursor line moved from screen line %d to %d when split stopped being shown", before, got)
	}
}

func TestDiffViewSplitJumpDuringPinnedSelectionLandsOnARowWithThePinnedSide(t *testing.T) {
	f := splitFileOf(t, splitPatch)
	f.HeadLineCount = 4 // no trailing gap row
	dv, _ := splitViewOf(t, f, splitW, 20)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 3) // del2a | add2a
	dv.StartVisual()
	dv.JumpToLine(model.DiffSideRight, 4) // "tail": nothing on its left
	drawn(t, dv, splitW, 20)
	if got := cursorText(t, dv); got != "ctx3" {
		t.Errorf("CursorLine after the jump = %q, want ctx3 (the nearest row with a left line)", got)
	}
	sel, _ := dv.Selection()
	if len(sel) == 0 || sel[len(sel)-1].Text != "ctx3" {
		t.Errorf("Selection() = %v, want it to end at ctx3", sel)
	}
	if dv.cursorSide != model.DiffSideLeft {
		t.Errorf("cursorSide = %v, want the locked LEFT", dv.cursorSide)
	}
}

func TestDiffViewGutterDigitsSurviveALoadingPlaceholder(t *testing.T) {
	f := splitFileOf(t, "@@ -100,1 +100,1 @@\n-a\n+b\n")
	// 3-digit numbers need a wider pane for split (40 columns: unified).
	dv, screen := splitViewOf(t, f, 40, 20)
	if dv.SplitShown() {
		t.Fatal("fixture: split should not fit with 3-digit numbers at width 40")
	}
	for range 5 {
		if _, ok := dv.CursorLine(); ok {
			break
		}
		dv.MoveBy(1) // past the leading gap row
	}
	dv.StartVisual()
	placeholder := DiffFile{Path: f.Path, Loading: true}
	dv.SetFile(placeholder)
	dv.Draw(screen)
	if dv.SplitShown() {
		t.Error("a Loading placeholder made the split layout fit")
	}
	if !dv.InVisual() {
		t.Error("a Loading placeholder ended visual mode")
	}
}

func TestDiffViewSplitCurrentSearchMatchOnAContextRowFollowsTheCursorColumn(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetSearch(regexp.MustCompile("ctx1"))
	dv.SetSearchCurrent("f.go", 0, 0, 0, 4)
	dv.SetCursorSide(model.DiffSideLeft)
	dv.Draw(screen)
	currentFg, _, _ := theme.SearchCurrent.Decompose()
	lfg, _, lattr := cellStyle(screen, 1+5, 2).Decompose()
	rfg, _, _ := cellStyle(screen, splitSep+1+5, 2).Decompose()
	if lfg != currentFg || lattr&tcell.AttrBold == 0 {
		t.Errorf("left half fg %v, want SearchCurrent after h", lfg)
	}
	if rfg == currentFg {
		t.Error("right half is styled SearchCurrent too")
	}
}

func TestDiffViewSetFileToAnotherPathResetsTheGutterDigits(t *testing.T) {
	dv := NewDiffView()
	dv.SetFile(splitFileOf(t, "@@ -10000,1 +10000,1 @@\n-a\n+b\n"))
	if dv.gutterDigits != 5 {
		t.Fatalf("gutterDigits = %d, want 5", dv.gutterDigits)
	}
	dv.SetFile(DiffFile{Path: "other.go", Loading: true})
	if dv.gutterDigits != 1 {
		t.Errorf("gutterDigits after another path's placeholder = %d, want 1", dv.gutterDigits)
	}
}

// currentMatchColumn reports which half of screen row y (an "ctx" context
// row) the current search match is styled in: 'L', 'R' or '?'.
func currentMatchColumn(screen tcell.Screen, y int) rune {
	currentFg, _, _ := theme.SearchCurrent.Decompose()
	l, _, _ := cellStyle(screen, 1+5, y).Decompose()
	r, _, _ := cellStyle(screen, splitSep+1+5, y).Decompose()
	switch {
	case l == currentFg && r != currentFg:
		return 'L'
	case r == currentFg && l != currentFg:
		return 'R'
	}
	return '?'
}

func TestDiffViewSplitCurrentMatchColumnIsTheCursorsEffectiveColumn(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetCursorSide(model.DiffSideLeft)
	moveToRow(t, dv, 6) // blank | tail: the cursor is drawn in the right half
	dv.SetSearch(regexp.MustCompile("ctx3"))
	dv.SetSearchCurrent("f.go", 0, 4, 0, 4)
	dv.Draw(screen)
	if got := currentMatchColumn(screen, 5); got != 'R' {
		t.Errorf("current match column = %c, want R (the cursor's drawn column)", got)
	}
}

func TestDiffViewSplitCurrentMatchColumnFollowsASideFlippingJump(t *testing.T) {
	dv, screen := splitView(t)
	dv.SetSearch(regexp.MustCompile("ctx1"))
	dv.SetSearchCurrent("f.go", 0, 0, 0, 4)
	dv.Draw(screen)
	if got := currentMatchColumn(screen, 2); got != 'R' {
		t.Fatalf("before the jump: column = %c, want R", got)
	}
	dv.JumpToLine(model.DiffSideLeft, 3) // del2b flips the cursor to the left
	dv.Draw(screen)
	if got := currentMatchColumn(screen, 2); got != 'L' {
		t.Errorf("after the jump: column = %c, want L (the cursor's column)", got)
	}
}
