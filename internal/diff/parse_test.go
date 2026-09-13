package diff

import (
	"errors"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

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
