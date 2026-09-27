package diff

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

// fixtureHeadLines returns a synthetic head file of n lines: "line1" ...
// "lineN", so a Context line inserted for new-side line N always has the
// predictable text "lineN".
func fixtureHeadLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i+1)
	}
	return lines
}

// assertHunksEqual compares got against want field-by-field via
// reflect.DeepEqual, which also verifies every pre-existing line's OldNo/
// NewNo/Text/Kind is preserved unchanged (they are part of want's literal
// Lines).
func assertHunksEqual(t *testing.T, got, want []Hunk) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hunks = %+v, want %+v", got, want)
	}
}

// assertHeadersConsistent checks that every hunk's OldLines/NewLines match
// the actual count of lines present on each side of its Lines slice.
func assertHeadersConsistent(t *testing.T, hunks []Hunk) {
	t.Helper()
	for i, h := range hunks {
		var oldCount, newCount int
		for _, l := range h.Lines {
			if l.Kind != Add {
				oldCount++
			}
			if l.Kind != Del {
				newCount++
			}
		}
		if h.OldLines != oldCount {
			t.Errorf("hunks[%d].OldLines = %d, want %d (count of non-Add lines)", i, h.OldLines, oldCount)
		}
		if h.NewLines != newCount {
			t.Errorf("hunks[%d].NewLines = %d, want %d (count of non-Del lines)", i, h.NewLines, newCount)
		}
	}
}

func TestParse_EmptyPatchReturnsNil(t *testing.T) {
	hunks, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\") error = %v, want nil", err)
	}
	if hunks != nil {
		t.Fatalf("Parse(\"\") = %v, want nil", hunks)
	}
}

func TestParse_OmittedCountsDefaultToOne(t *testing.T) {
	patch := "@@ -1 +1 @@\n-a\n+b"
	hunks, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("Parse() returned %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 1 || h.OldLines != 1 || h.NewStart != 1 || h.NewLines != 1 {
		t.Errorf("hunk header = %+v, want OldStart=1 OldLines=1 NewStart=1 NewLines=1", h)
	}
	if len(h.Lines) != 2 {
		t.Fatalf("hunk has %d lines, want 2", len(h.Lines))
	}
	if h.Lines[0].Kind != Del || h.Lines[0].OldNo != 1 || h.Lines[0].NewNo != 0 || h.Lines[0].Text != "a" {
		t.Errorf("Lines[0] = %+v, want Del OldNo=1 NewNo=0 Text=a", h.Lines[0])
	}
	if h.Lines[1].Kind != Add || h.Lines[1].NewNo != 1 || h.Lines[1].OldNo != 0 || h.Lines[1].Text != "b" {
		t.Errorf("Lines[1] = %+v, want Add NewNo=1 OldNo=0 Text=b", h.Lines[1])
	}
}

func TestParse_ExplicitCountsAndSection(t *testing.T) {
	patch := "@@ -10,3 +12,4 @@ func Foo() {\n context1\n-old\n+new1\n+new2\n context2"
	hunks, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("Parse() returned %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 10 || h.OldLines != 3 || h.NewStart != 12 || h.NewLines != 4 {
		t.Errorf("hunk header = %+v, want OldStart=10 OldLines=3 NewStart=12 NewLines=4", h)
	}
	if h.Section != "func Foo() {" {
		t.Errorf("Section = %q, want %q", h.Section, "func Foo() {")
	}

	wantKinds := []LineKind{Context, Del, Add, Add, Context}
	if len(h.Lines) != len(wantKinds) {
		t.Fatalf("hunk has %d lines, want %d", len(h.Lines), len(wantKinds))
	}
	for i, want := range wantKinds {
		if h.Lines[i].Kind != want {
			t.Errorf("Lines[%d].Kind = %v, want %v", i, h.Lines[i].Kind, want)
		}
	}
	// context1 is old line 10 / new line 12; old advances to 11 (consumed
	// by "old"); new advances to 13 (consumed by "new1"), 14 ("new2");
	// context2 is old line 11 / new line 15.
	if h.Lines[0].OldNo != 10 || h.Lines[0].NewNo != 12 {
		t.Errorf("context1 numbers = old:%d new:%d, want old:10 new:12", h.Lines[0].OldNo, h.Lines[0].NewNo)
	}
	if h.Lines[1].OldNo != 11 {
		t.Errorf("del numbers = old:%d, want old:11", h.Lines[1].OldNo)
	}
	if h.Lines[2].NewNo != 13 || h.Lines[3].NewNo != 14 {
		t.Errorf("add numbers = %d,%d, want 13,14", h.Lines[2].NewNo, h.Lines[3].NewNo)
	}
	if h.Lines[4].OldNo != 12 || h.Lines[4].NewNo != 15 {
		t.Errorf("context2 numbers = old:%d new:%d, want old:12 new:15", h.Lines[4].OldNo, h.Lines[4].NewNo)
	}
}

func TestParse_NoNewlineMarkerOnBothSides(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -1 +1 @@",
		"-old",
		`\ No newline at end of file`,
		"+new",
		`\ No newline at end of file`,
	}, "\n")
	hunks, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("Parse() returned %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if len(h.Lines) != 2 {
		t.Fatalf("hunk has %d lines, want 2 (marker lines must not become Lines)", len(h.Lines))
	}
	if !h.Lines[0].NoNewline {
		t.Errorf("Lines[0].NoNewline = false, want true")
	}
	if !h.Lines[1].NoNewline {
		t.Errorf("Lines[1].NoNewline = false, want true")
	}
}

func TestParse_TrailingNewlineInPatchIgnored(t *testing.T) {
	patch := "@@ -1 +1 @@\n context\n"
	hunks, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(hunks) != 1 || len(hunks[0].Lines) != 1 {
		t.Fatalf("Parse() = %+v, want exactly one hunk with one line", hunks)
	}
}

func TestParse_MultipleHunks(t *testing.T) {
	patch := "@@ -1 +1 @@\n-a\n+b\n@@ -5,2 +5,2 @@\n context\n-x\n+y"
	hunks, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("Parse() returned %d hunks, want 2", len(hunks))
	}
	if hunks[1].OldStart != 5 || hunks[1].NewStart != 5 {
		t.Errorf("second hunk header = %+v, want OldStart=5 NewStart=5", hunks[1])
	}
}

func TestParse_InvalidLineShapeErrorsWithLineNumber(t *testing.T) {
	patch := "@@ -1 +1 @@\n-a\n*bogus"
	_, err := Parse(patch)
	if err == nil {
		t.Fatal("Parse() error = nil, want an error for the bogus line")
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error %q does not name line 3", err.Error())
	}
}

func TestParse_LineBeforeAnyHunkHeaderErrors(t *testing.T) {
	_, err := Parse(" context\n@@ -1 +1 @@\n context")
	if err == nil {
		t.Fatal("Parse() error = nil, want an error for content before any hunk header")
	}
}

func TestParse_NoNewlineMarkerWithNoPrecedingLineErrors(t *testing.T) {
	patch := "@@ -1 +1 @@\n" + `\ No newline at end of file`
	_, err := Parse(patch)
	if err == nil {
		t.Fatal("Parse() error = nil, want an error for a no-newline marker with nothing before it")
	}
}

func TestExpandTabs_DefaultWidthWhenNonPositive(t *testing.T) {
	got := ExpandTabs("a\tb", 0)
	want := ExpandTabs("a\tb", 1)
	if got != want {
		t.Errorf("ExpandTabs(width=0) = %q, want same as width=1 (%q)", got, want)
	}
}

func TestExpandTabs_ColumnAware(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{"tab at start", "\ta", 4, "    a"},
		{"tab mid-column", "ab\tc", 4, "ab  c"},
		{"tab exactly at stop", "abcd\te", 4, "abcd    e"},
		{"multiple tabs", "\t\ta", 4, "        a"},
		{"wide rune before tab", "あ\tb", 4, "あ  b"}, // "あ" occupies 2 columns
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandTabs(tt.s, tt.width)
			if got != tt.want {
				t.Errorf("ExpandTabs(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
			}
		})
	}
}

func TestAnchor(t *testing.T) {
	tests := []struct {
		name     string
		line     Line
		wantSide model.DiffSide
		wantNo   int
	}{
		{"del uses left/old", Line{Kind: Del, OldNo: 5, NewNo: 0}, model.DiffSideLeft, 5},
		{"add uses right/new", Line{Kind: Add, OldNo: 0, NewNo: 7}, model.DiffSideRight, 7},
		{"context uses right/new", Line{Kind: Context, OldNo: 3, NewNo: 4}, model.DiffSideRight, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			side, no := Anchor(tt.line)
			if side != tt.wantSide || no != tt.wantNo {
				t.Errorf("Anchor(%+v) = (%v, %d), want (%v, %d)", tt.line, side, no, tt.wantSide, tt.wantNo)
			}
		})
	}
}

func TestRangeAnchor_OnlyDelLinesUsesLeft(t *testing.T) {
	lines := []Line{
		{Kind: Del, OldNo: 10},
		{Kind: Del, OldNo: 11},
	}
	r, err := RangeAnchor(lines)
	if err != nil {
		t.Fatalf("RangeAnchor() error = %v", err)
	}
	if r.Side != model.DiffSideLeft || r.StartSide != model.DiffSideLeft || r.StartLine != 10 || r.Line != 11 {
		t.Errorf("RangeAnchor() = %+v, want LEFT 10..11", r)
	}
}

func TestRangeAnchor_NoDelLinesUsesRight(t *testing.T) {
	lines := []Line{
		{Kind: Context, NewNo: 20},
		{Kind: Add, NewNo: 21},
	}
	r, err := RangeAnchor(lines)
	if err != nil {
		t.Fatalf("RangeAnchor() error = %v", err)
	}
	if r.Side != model.DiffSideRight || r.StartSide != model.DiffSideRight || r.StartLine != 20 || r.Line != 21 {
		t.Errorf("RangeAnchor() = %+v, want RIGHT 20..21", r)
	}
}

func TestRangeAnchor_MixedSidesIsError(t *testing.T) {
	lines := []Line{
		{Kind: Del, OldNo: 10},
		{Kind: Add, NewNo: 21},
	}
	_, err := RangeAnchor(lines)
	if !errors.Is(err, ErrMixedSides) {
		t.Errorf("RangeAnchor() error = %v, want ErrMixedSides", err)
	}
}

func TestRangeAnchor_RequiresAtLeastTwoLines(t *testing.T) {
	_, err := RangeAnchor([]Line{{Kind: Del, OldNo: 1}})
	if err == nil {
		t.Fatal("RangeAnchor() error = nil, want an error for a single line")
	}
}

func testHunks() []Hunk {
	return []Hunk{
		{
			OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "ctx"},
				{Kind: Del, OldNo: 2, NewNo: 0, Text: "removed"},
				{Kind: Add, OldNo: 0, NewNo: 2, Text: "added1"},
				{Kind: Add, OldNo: 0, NewNo: 3, Text: "added2"},
			},
		},
	}
}

func TestLocateThread_LeftMatchesDelOrContextByOldNo(t *testing.T) {
	hunk, line, ok := LocateThread(testHunks(), model.ReviewThread{
		Line: 2, Side: model.DiffSideLeft, SubjectType: model.ThreadSubjectLine,
	})
	if !ok || hunk != 0 || line != 1 {
		t.Errorf("LocateThread(LEFT,2) = (%d,%d,%v), want (0,1,true)", hunk, line, ok)
	}

	hunk, line, ok = LocateThread(testHunks(), model.ReviewThread{
		Line: 1, Side: model.DiffSideLeft, SubjectType: model.ThreadSubjectLine,
	})
	if !ok || hunk != 0 || line != 0 {
		t.Errorf("LocateThread(LEFT,1) (context) = (%d,%d,%v), want (0,0,true)", hunk, line, ok)
	}
}

func TestLocateThread_RightMatchesAddOrContextByNewNo(t *testing.T) {
	hunk, line, ok := LocateThread(testHunks(), model.ReviewThread{
		Line: 3, Side: model.DiffSideRight, SubjectType: model.ThreadSubjectLine,
	})
	if !ok || hunk != 0 || line != 3 {
		t.Errorf("LocateThread(RIGHT,3) = (%d,%d,%v), want (0,3,true)", hunk, line, ok)
	}
}

func TestLocateThread_OutdatedNeverLocates(t *testing.T) {
	_, _, ok := LocateThread(testHunks(), model.ReviewThread{
		Line: 2, Side: model.DiffSideLeft, SubjectType: model.ThreadSubjectLine, IsOutdated: true,
	})
	if ok {
		t.Error("LocateThread() with IsOutdated = true, want false")
	}
}

func TestLocateThread_FileSubjectNeverLocates(t *testing.T) {
	_, _, ok := LocateThread(testHunks(), model.ReviewThread{
		Line: 2, Side: model.DiffSideLeft, SubjectType: model.ThreadSubjectFile,
	})
	if ok {
		t.Error("LocateThread() with SubjectType FILE, want false")
	}
}

func TestLocateLine_LeftMatchesDelOrContextByOldNo(t *testing.T) {
	hunk, line, ok := LocateLine(testHunks(), model.DiffSideLeft, 2)
	if !ok || hunk != 0 || line != 1 {
		t.Errorf("LocateLine(LEFT,2) = (%d,%d,%v), want (0,1,true)", hunk, line, ok)
	}
}

func TestLocateLine_RightMatchesAddOrContextByNewNo(t *testing.T) {
	hunk, line, ok := LocateLine(testHunks(), model.DiffSideRight, 3)
	if !ok || hunk != 0 || line != 3 {
		t.Errorf("LocateLine(RIGHT,3) = (%d,%d,%v), want (0,3,true)", hunk, line, ok)
	}
}

func TestLocateLine_NoMatchReturnsFalse(t *testing.T) {
	_, _, ok := LocateLine(testHunks(), model.DiffSideRight, 99)
	if ok {
		t.Error("LocateLine() with no matching line, want false")
	}
}

func TestLocateThread_NoMatchReturnsFalse(t *testing.T) {
	_, _, ok := LocateThread(testHunks(), model.ReviewThread{
		Line: 99, Side: model.DiffSideRight, SubjectType: model.ThreadSubjectLine,
	})
	if ok {
		t.Error("LocateThread() with no matching line, want false")
	}
}

func TestNewRange(t *testing.T) {
	tests := []struct {
		name      string
		hunk      Hunk
		wantFirst int
		wantLast  int
	}{
		{"non-empty side", Hunk{NewStart: 5, NewLines: 3}, 5, 7},
		{"single line side (count 1)", Hunk{NewStart: 5, NewLines: 1}, 5, 5},
		{
			"zero-count side positioned after Start",
			Hunk{NewStart: 5, NewLines: 0},
			6, 5,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first, last := NewRange(tc.hunk)
			if first != tc.wantFirst || last != tc.wantLast {
				t.Errorf("NewRange(%+v) = (%d,%d), want (%d,%d)", tc.hunk, first, last, tc.wantFirst, tc.wantLast)
			}
		})
	}
}

func TestExpandGap_BeforeFirstHunk(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1, Section: "func A",
			Lines: []Line{{Kind: Context, OldNo: 5, NewNo: 5, Text: "line5"}},
		},
	}
	got, err := ExpandGap(hunks, 1, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	want := []Hunk{
		{
			OldStart: 1, OldLines: 5, NewStart: 1, NewLines: 5, Section: "func A",
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"},
				{Kind: Context, OldNo: 2, NewNo: 2, Text: "line2"},
				{Kind: Context, OldNo: 3, NewNo: 3, Text: "line3"},
				{Kind: Context, OldNo: 4, NewNo: 4, Text: "line4"},
				{Kind: Context, OldNo: 5, NewNo: 5, Text: "line5"},
			},
		},
	}
	assertHunksEqual(t, got, want)
	assertHeadersConsistent(t, got)
}

func TestExpandGap_BetweenHunksNoDelta(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"},
				{Kind: Context, OldNo: 2, NewNo: 2, Text: "line2"},
				{Kind: Context, OldNo: 3, NewNo: 3, Text: "line3"},
			},
		},
		{
			OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 7, NewNo: 7, Text: "line7"}},
		},
	}
	got, err := ExpandGap(hunks, 4, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (hunks merged)", len(got))
	}
	want := []Hunk{
		{
			OldStart: 1, OldLines: 7, NewStart: 1, NewLines: 7,
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"},
				{Kind: Context, OldNo: 2, NewNo: 2, Text: "line2"},
				{Kind: Context, OldNo: 3, NewNo: 3, Text: "line3"},
				{Kind: Context, OldNo: 4, NewNo: 4, Text: "line4"},
				{Kind: Context, OldNo: 5, NewNo: 5, Text: "line5"},
				{Kind: Context, OldNo: 6, NewNo: 6, Text: "line6"},
				{Kind: Context, OldNo: 7, NewNo: 7, Text: "line7"},
			},
		},
	}
	assertHunksEqual(t, got, want)
	assertHeadersConsistent(t, got)
}

func TestExpandGap_BetweenHunksPositiveDelta(t *testing.T) {
	// h0 deletes 1 old line and adds 2 new lines (more adds than deletes),
	// so new-side numbering runs ahead of old-side numbering after it:
	// delta = newRange.last(2) - oldRange.last(1) = 1.
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
			Lines: []Line{
				{Kind: Del, OldNo: 1, Text: "old1"},
				{Kind: Add, NewNo: 1, Text: "new1"},
				{Kind: Add, NewNo: 2, Text: "new2"},
			},
		},
		{
			OldStart: 4, OldLines: 1, NewStart: 5, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 4, NewNo: 5, Text: "line5"}},
		},
	}
	got, err := ExpandGap(hunks, 3, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	gap := got[0].Lines[3:5]
	want := []Line{
		{Kind: Context, OldNo: 2, NewNo: 3, Text: "line3"},
		{Kind: Context, OldNo: 3, NewNo: 4, Text: "line4"},
	}
	if !reflect.DeepEqual(gap, want) {
		t.Errorf("gap lines = %+v, want %+v", gap, want)
	}
	assertHeadersConsistent(t, got)
}

func TestExpandGap_BetweenHunksNegativeDelta(t *testing.T) {
	// h0 deletes 2 old lines and adds 1 new line (more deletes than adds),
	// so old-side numbering runs ahead of new-side numbering after it:
	// delta = newRange.last(1) - oldRange.last(2) = -1.
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 1,
			Lines: []Line{
				{Kind: Del, OldNo: 1, Text: "old1"},
				{Kind: Del, OldNo: 2, Text: "old2"},
				{Kind: Add, NewNo: 1, Text: "new1"},
			},
		},
		{
			OldStart: 4, OldLines: 1, NewStart: 3, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 4, NewNo: 3, Text: "line3"}},
		},
	}
	got, err := ExpandGap(hunks, 2, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	gap := got[0].Lines[3:4]
	want := []Line{{Kind: Context, OldNo: 3, NewNo: 2, Text: "line2"}}
	if !reflect.DeepEqual(gap, want) {
		t.Errorf("gap lines = %+v, want %+v", gap, want)
	}
	assertHeadersConsistent(t, got)
}

func TestExpandGap_BetweenHunksAfterPureDeletion(t *testing.T) {
	// h0 is a pure deletion (NewLines == 0): it has no new-side lines of
	// its own, so its (empty) new range sits right after NewStart, and the
	// gap after it starts at NewStart+1.
	hunks := []Hunk{
		{
			OldStart: 5, OldLines: 2, NewStart: 4, NewLines: 0,
			Lines: []Line{
				{Kind: Del, OldNo: 5, Text: "old5"},
				{Kind: Del, OldNo: 6, Text: "old6"},
			},
		},
		{
			OldStart: 8, OldLines: 1, NewStart: 6, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 8, NewNo: 6, Text: "line6"}},
		},
	}
	got, err := ExpandGap(hunks, 5, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	gap := got[0].Lines[2:3]
	want := []Line{{Kind: Context, OldNo: 7, NewNo: 5, Text: "line5"}}
	if !reflect.DeepEqual(gap, want) {
		t.Errorf("gap lines = %+v, want %+v", gap, want)
	}
	assertHeadersConsistent(t, got)
}

func TestExpandGap_TrailingGap(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 8, NewNo: 8, Text: "line8"}},
		},
	}
	got, err := ExpandGap(hunks, 9, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	want := []Hunk{
		{
			OldStart: 8, OldLines: 3, NewStart: 8, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 8, NewNo: 8, Text: "line8"},
				{Kind: Context, OldNo: 9, NewNo: 9, Text: "line9"},
				{Kind: Context, OldNo: 10, NewNo: 10, Text: "line10"},
			},
		},
	}
	assertHunksEqual(t, got, want)
	assertHeadersConsistent(t, got)
}

// TestExpandGap_TrailingGapAfterPureDeletionLastHunk covers a last hunk
// whose new side is itself empty (a pure deletion, NewLines == 0): its own
// NewStart is only the position the empty side sits after (see NewRange's
// doc comment), not a line actually present in it, so the merged hunk's
// header must be derived from NewRange/oldRange's own first line, not
// last.OldStart/NewStart directly - the regression this guards against.
func TestExpandGap_TrailingGapAfterPureDeletionLastHunk(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 5, OldLines: 2, NewStart: 4, NewLines: 0,
			Lines: []Line{
				{Kind: Del, OldNo: 5, Text: "old5"},
				{Kind: Del, OldNo: 6, Text: "old6"},
			},
		},
	}
	got, err := ExpandGap(hunks, 5, fixtureHeadLines(7))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	want := []Hunk{
		{
			OldStart: 5, OldLines: 5, NewStart: 5, NewLines: 3,
			Lines: []Line{
				{Kind: Del, OldNo: 5, Text: "old5"},
				{Kind: Del, OldNo: 6, Text: "old6"},
				{Kind: Context, OldNo: 7, NewNo: 5, Text: "line5"},
				{Kind: Context, OldNo: 8, NewNo: 6, Text: "line6"},
				{Kind: Context, OldNo: 9, NewNo: 7, Text: "line7"},
			},
		},
	}
	assertHunksEqual(t, got, want)
	assertHeadersConsistent(t, got)
}

func TestExpandGap_TrailingGapEmptyReturnsHunksUnchanged(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 8, OldLines: 3, NewStart: 8, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 8, NewNo: 8, Text: "line8"},
				{Kind: Context, OldNo: 9, NewNo: 9, Text: "line9"},
				{Kind: Context, OldNo: 10, NewNo: 10, Text: "line10"},
			},
		},
	}
	got, err := ExpandGap(hunks, 11, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	assertHunksEqual(t, got, hunks)
}

func TestExpandGap_SingleHunkBothEdges(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 5, NewNo: 5, Text: "line5"}},
		},
	}

	before, err := ExpandGap(hunks, 1, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap(gapStart=1) error = %v", err)
	}
	if len(before) != 1 || len(before[0].Lines) != 5 {
		t.Fatalf("before-edge expansion = %+v, want a single hunk with 5 lines", before)
	}
	if before[0].OldStart != 1 || before[0].NewStart != 1 {
		t.Errorf("before-edge OldStart/NewStart = %d/%d, want 1/1", before[0].OldStart, before[0].NewStart)
	}

	after, err := ExpandGap(hunks, 6, fixtureHeadLines(10))
	if err != nil {
		t.Fatalf("ExpandGap(gapStart=6) error = %v", err)
	}
	if len(after) != 1 || len(after[0].Lines) != 6 {
		t.Fatalf("trailing-edge expansion = %+v, want a single hunk with 6 lines", after)
	}
	assertHeadersConsistent(t, before)
	assertHeadersConsistent(t, after)
}

func TestExpandGap_FirstHunkAlreadyAtLineOneErrors(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"}},
		},
	}
	_, err := ExpandGap(hunks, 1, fixtureHeadLines(10))
	if err == nil {
		t.Fatal("ExpandGap(gapStart=1) error = nil, want an error (no before-gap when the first hunk already starts at line 1)")
	}
}

func TestExpandGap_UnknownGapStartErrors(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"}},
		},
	}
	_, err := ExpandGap(hunks, 99, fixtureHeadLines(10))
	if err == nil {
		t.Fatal("ExpandGap(gapStart=99) error = nil, want an error")
	}
}

// TestExpandGap_BeforeFirstHunkShortHeadLinesErrors covers a headLines
// slice too short to cover the before-first gap: without the bounds check,
// contextLines would index past the end of headLines and panic (on the UI
// goroutine, via internal/store.ExpandGap - this must be an error instead).
func TestExpandGap_BeforeFirstHunkShortHeadLinesErrors(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 5, NewNo: 5, Text: "line5"}},
		},
	}
	// The gap [1,4] needs headLines[3] (line 4); only 2 lines are given.
	_, err := ExpandGap(hunks, 1, fixtureHeadLines(2))
	if err == nil {
		t.Fatal("ExpandGap() with headLines shorter than the gap needs, error = nil, want an error")
	}
}

// TestExpandGap_BetweenHunksShortHeadLinesErrors is
// TestExpandGap_BeforeFirstHunkShortHeadLinesErrors' between-gap
// counterpart.
func TestExpandGap_BetweenHunksShortHeadLinesErrors(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"},
				{Kind: Context, OldNo: 2, NewNo: 2, Text: "line2"},
				{Kind: Context, OldNo: 3, NewNo: 3, Text: "line3"},
			},
		},
		{
			OldStart: 9, OldLines: 1, NewStart: 9, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 9, NewNo: 9, Text: "line9"}},
		},
	}
	// The gap [4,8] needs headLines[7] (line 8); only 5 lines are given.
	_, err := ExpandGap(hunks, 4, fixtureHeadLines(5))
	if err == nil {
		t.Fatal("ExpandGap() with headLines shorter than the gap needs, error = nil, want an error")
	}
}

func TestExpandGap_DoesNotMutateInput(t *testing.T) {
	hunks := []Hunk{
		{
			OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3,
			Lines: []Line{
				{Kind: Context, OldNo: 1, NewNo: 1, Text: "line1"},
				{Kind: Context, OldNo: 2, NewNo: 2, Text: "line2"},
				{Kind: Context, OldNo: 3, NewNo: 3, Text: "line3"},
			},
		},
		{
			OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1,
			Lines: []Line{{Kind: Context, OldNo: 7, NewNo: 7, Text: "line7"}},
		},
	}
	want := make([]Hunk, len(hunks))
	for i, h := range hunks {
		lines := make([]Line, len(h.Lines))
		copy(lines, h.Lines)
		h.Lines = lines
		want[i] = h
	}

	if _, err := ExpandGap(hunks, 4, fixtureHeadLines(10)); err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	assertHunksEqual(t, hunks, want)
}
