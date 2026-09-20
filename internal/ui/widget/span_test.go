package widget

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func newTestScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init() failed: %v", err)
	}
	screen.SetSize(w, h)
	return screen
}

// cellRune returns the first rune drawn at (x, y), or 0 if the cell is
// empty. tcell.Screen.Get returns the cell's content as a string (which may
// contain a base rune plus combining marks); tests here only ever draw
// single, non-combining runes, so the first rune is always the whole story.
func cellRune(screen tcell.Screen, x, y int) rune {
	s, _, _ := screen.Get(x, y)
	for _, r := range s {
		return r
	}
	return 0
}

// cellStyle returns the style drawn at (x, y).
func cellStyle(screen tcell.Screen, x, y int) tcell.Style {
	_, style, _ := screen.Get(x, y)
	return style
}

func TestSpanWidth(t *testing.T) {
	tests := []struct {
		name  string
		spans []Span
		want  int
	}{
		{"empty", nil, 0},
		{"ascii", []Span{{Text: "abc"}}, 3},
		{"multiple spans", []Span{{Text: "ab"}, {Text: "cde"}}, 5},
		{"wide characters count as two cells", []Span{{Text: "日本語"}}, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpanWidth(tc.spans); got != tc.want {
				t.Errorf("SpanWidth(%+v) = %d, want %d", tc.spans, got, tc.want)
			}
		})
	}
}

// TestSpanWidthMatchesTerminalDisplayWidths pins SpanWidth's values for a
// set of characters where displaywidth (used internally, replacing the
// unmaintained uniseg dependency — see decisis #169) must agree with what
// tcell (which still uses uniseg itself) actually draws each cell as; a
// mismatch here would desynchronize gprt's own cursor/gutter math from the
// terminal's real layout.
func TestSpanWidthMatchesTerminalDisplayWidths(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"CJK ideographs are double-width", "日本語", 6},
		{"a ZWJ family emoji sequence is one double-width cluster", "👨‍👩‍👧", 2},
		{"a regional-indicator flag sequence is one double-width cluster", "🇯🇵", 2},
		{"a base letter plus a combining mark is one single-width cluster", "é", 1},
		{"narrow punctuation (ellipsis, middle dot, box drawing) is single-width", "…·─│", 4},
		{"halfwidth katakana is single-width per character", "ｱｲ", 2},
		{"a tab is zero-width", "\t", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpanWidth([]Span{{Text: tc.text}}); got != tc.want {
				t.Errorf("SpanWidth(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

func TestDrawSpansFitsWithinWidth(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	style := tcell.StyleDefault.Foreground(tcell.ColorRed)

	got := DrawSpans(screen, 2, 0, 10, []Span{{Text: "hi", Style: style}})
	if got != 2 {
		t.Fatalf("DrawSpans() returned %d, want 2", got)
	}

	if r := cellRune(screen, 2, 0); r != 'h' {
		t.Errorf("cell (2,0) rune = %q, want 'h'", r)
	}
	if gotStyle := cellStyle(screen, 2, 0); gotStyle != style {
		t.Errorf("cell (2,0) style = %v, want %v", gotStyle, style)
	}
	if r := cellRune(screen, 3, 0); r != 'i' {
		t.Errorf("cell (3,0) rune = %q, want 'i'", r)
	}
}

func TestDrawSpansMultipleSpans(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	styleA := tcell.StyleDefault.Foreground(tcell.ColorRed)
	styleB := tcell.StyleDefault.Foreground(tcell.ColorBlue)

	DrawSpans(screen, 0, 0, 20, []Span{
		{Text: "ab", Style: styleA},
		{Text: "cd", Style: styleB},
	})

	if r, s := cellRune(screen, 0, 0), cellStyle(screen, 0, 0); r != 'a' || s != styleA {
		t.Errorf("cell (0,0) = %q/%v, want 'a'/%v", r, s, styleA)
	}
	if r, s := cellRune(screen, 2, 0), cellStyle(screen, 2, 0); r != 'c' || s != styleB {
		t.Errorf("cell (2,0) = %q/%v, want 'c'/%v", r, s, styleB)
	}
}

func TestDrawSpansTruncatesWithEllipsis(t *testing.T) {
	screen := newTestScreen(t, 20, 3)

	got := DrawSpans(screen, 0, 0, 5, []Span{{Text: "hello world"}})
	if got != 5 {
		t.Fatalf("DrawSpans() returned %d, want 5 (== maxWidth)", got)
	}

	want := "hell…"
	for i, wantRune := range []rune(want) {
		if r := cellRune(screen, i, 0); r != wantRune {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, wantRune)
		}
	}
}

func TestDrawSpansNeverSplitsAWideRuneAtTheBoundary(t *testing.T) {
	screen := newTestScreen(t, 20, 3)

	// "ab" + a wide rune ("日") would need 4 cells; with a maxWidth of 3
	// the wide rune cannot fit alongside the reserved ellipsis cell, so
	// drawing must stop after "ab" and place the ellipsis at cell 2,
	// never splitting "日" across the boundary.
	got := DrawSpans(screen, 0, 0, 3, []Span{{Text: "ab日"}})
	if got != 3 {
		t.Fatalf("DrawSpans() returned %d, want 3", got)
	}
	want := []rune{'a', 'b', '…'}
	for i, wantRune := range want {
		if r := cellRune(screen, i, 0); r != wantRune {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, wantRune)
		}
	}
}

func TestDrawSpansZeroWidthIsANoOp(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	if got := DrawSpans(screen, 0, 0, 0, []Span{{Text: "hi"}}); got != 0 {
		t.Errorf("DrawSpans() with maxWidth 0 returned %d, want 0", got)
	}
}
