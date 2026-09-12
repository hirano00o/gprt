package widget

import "testing"

func TestDrawSpansOffsetZeroOffsetMatchesDrawSpans(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 0, []Span{{Text: "hello"}})
	if got != 5 {
		t.Fatalf("DrawSpansOffset with offset 0 returned %d, want 5", got)
	}
	for i, want := range []rune("hello") {
		if r := cellRune(screen, i, 0); r != want {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, want)
		}
	}
}

func TestDrawSpansOffsetSkipsLeadingCells(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 3, []Span{{Text: "hello world"}})
	// "hello world" with the first 3 cells ("hel") skipped leaves "lo world"
	// (8 cells), which fits within maxWidth 10 with no truncation.
	if got != 8 {
		t.Fatalf("DrawSpansOffset returned %d, want 8", got)
	}
	for i, want := range []rune("lo world") {
		if r := cellRune(screen, i, 0); r != want {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, want)
		}
	}
}

func TestDrawSpansOffsetSkipsAcrossMultipleSpans(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 3, []Span{{Text: "ab"}, {Text: "cdef"}})
	if got != 3 {
		t.Fatalf("DrawSpansOffset returned %d, want 3", got)
	}
	for i, want := range []rune("def") {
		if r := cellRune(screen, i, 0); r != want {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, want)
		}
	}
}

func TestDrawSpansOffsetTruncatesRemainderWithEllipsis(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 5, 3, []Span{{Text: "hello world"}})
	// After skipping "hel", "lo world" (8 cells) remains, truncated to 5.
	if got != 5 {
		t.Fatalf("DrawSpansOffset returned %d, want 5", got)
	}
	want := "lo w…"
	for i, wantRune := range []rune(want) {
		if r := cellRune(screen, i, 0); r != wantRune {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, wantRune)
		}
	}
}

// TestDrawSpansOffsetStraddlingWideRuneDrawsASpaceForTheVisibleHalf guards
// against a regression where an offset landing mid-cluster (a wide rune
// straddling the boundary) dropped the whole cluster instead of just its
// invisible half — over-consuming the offset by the cluster's full width
// rather than only the requested amount, and so shifting every cell after
// it one column further left than it should be. "日" is 2 cells wide; an
// offset of 1 cuts off its first (invisible) cell and leaves one cell of
// it still visible, which — since half a wide rune cannot be drawn — is
// rendered as a single space, exactly like a terminal clips a double-width
// character at its left edge.
func TestDrawSpansOffsetStraddlingWideRuneDrawsASpaceForTheVisibleHalf(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 1, []Span{{Text: "日ab"}})
	if got != 3 {
		t.Fatalf("DrawSpansOffset returned %d, want 3 (1 space for 日's visible half, then ab)", got)
	}
	if r := cellRune(screen, 0, 0); r != ' ' {
		t.Errorf("cell (0,0) rune = %q, want a space for the straddling wide rune's visible half", r)
	}
	for i, want := range []rune("ab") {
		if r := cellRune(screen, i+1, 0); r != want {
			t.Errorf("cell (%d,0) rune = %q, want %q", i+1, r, want)
		}
	}
}

// TestDrawSpansOffsetSkipsAWideRuneCleanlyWhenOffsetCoversItExactly is
// StraddlingWideRune's sibling: an offset exactly equal to the wide
// rune's own width skips it cleanly, with no leftover space cell and no
// extra shift.
func TestDrawSpansOffsetSkipsAWideRuneCleanlyWhenOffsetCoversItExactly(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 2, []Span{{Text: "日ab"}})
	if got != 2 {
		t.Fatalf("DrawSpansOffset returned %d, want 2 (ab, no leftover space)", got)
	}
	for i, want := range []rune("ab") {
		if r := cellRune(screen, i, 0); r != want {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, want)
		}
	}
}

// TestDrawSpansOffsetTruncatesAfterAStraddlingWideRune guards against
// truncation being computed from the *pre-skip* content width rather than
// what is actually left to draw once a straddling wide rune's visible
// space is accounted for — the two must agree, or an ellipsis can appear
// (or be withheld) incorrectly.
func TestDrawSpansOffsetTruncatesAfterAStraddlingWideRune(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	// "日ab" is 4 cells wide; offset 1 leaves 3 visible cells (1 space + a
	// + b); maxWidth 2 truncates that to a space followed by an ellipsis.
	got := DrawSpansOffset(screen, 0, 0, 2, 1, []Span{{Text: "日ab"}})
	if got != 2 {
		t.Fatalf("DrawSpansOffset returned %d, want 2", got)
	}
	want := []rune{' ', '…'}
	for i, wantRune := range want {
		if r := cellRune(screen, i, 0); r != wantRune {
			t.Errorf("cell (%d,0) rune = %q, want %q", i, r, wantRune)
		}
	}
}

func TestDrawSpansOffsetBeyondContentIsANoOp(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	got := DrawSpansOffset(screen, 0, 0, 10, 100, []Span{{Text: "hi"}})
	if got != 0 {
		t.Fatalf("DrawSpansOffset with an offset past all content returned %d, want 0", got)
	}
}

func TestDrawSpansOffsetZeroWidthIsANoOp(t *testing.T) {
	screen := newTestScreen(t, 20, 3)
	if got := DrawSpansOffset(screen, 0, 0, 0, 0, []Span{{Text: "hi"}}); got != 0 {
		t.Errorf("DrawSpansOffset with maxWidth 0 returned %d, want 0", got)
	}
}
