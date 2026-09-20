package editor

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// fakeText is a string-backed Text implementation for testing Vim in
// isolation. It fails the test immediately if Vim ever hands it an offset
// that is not 0, len(text), or a grapheme-cluster boundary — the
// contract every Text implementation (including the production TextArea
// adapter) is entitled to assume Vim never violates.
type fakeText struct {
	t          *testing.T
	text       string
	selA, selB int
}

func newFakeText(t *testing.T, text string, cursor int) *fakeText {
	t.Helper()
	f := &fakeText{t: t, text: text}
	f.checkBoundary(cursor)
	f.selA, f.selB = cursor, cursor
	return f
}

func (f *fakeText) checkBoundary(pos int) {
	f.t.Helper()
	if pos < 0 || pos > len(f.text) {
		f.t.Fatalf("offset %d out of range for %q (len %d)", pos, f.text, len(f.text))
	}
	if pos == 0 || pos == len(f.text) {
		return
	}
	for _, c := range clusters(f.text) {
		if c.start == pos {
			return
		}
	}
	f.t.Fatalf("offset %d is not a grapheme-cluster boundary in %q", pos, f.text)
}

func (f *fakeText) Text() string { return f.text }
func (f *fakeText) Cursor() int  { return f.selA }

func (f *fakeText) SetCursor(pos int) {
	f.checkBoundary(pos)
	f.selA, f.selB = pos, pos
}

func (f *fakeText) Select(start, end int) {
	if start > end {
		f.t.Fatalf("Select(%d, %d): start > end", start, end)
	}
	f.checkBoundary(start)
	f.checkBoundary(end)
	f.selA, f.selB = start, end
}

func (f *fakeText) Replace(start, end int, s string) {
	if start > end {
		f.t.Fatalf("Replace(%d, %d, ...): start > end", start, end)
	}
	f.checkBoundary(start)
	f.checkBoundary(end)
	f.text = f.text[:start] + s + f.text[end:]
	pos := start + len(s)
	f.selA, f.selB = pos, pos
}

// press feeds each key in seq (vim-notation runes only — no <Esc>-style
// tokens) to v, in order, as plain KindRune keys.
func press(v *Vim, seq string) {
	for _, r := range seq {
		v.Handle(keys.Key{Kind: keys.KindRune, Rune: r})
	}
}

func esc() keys.Key { return keys.Key{Kind: keys.KindSpecial, Special: tcell.KeyEsc} }

func ctrl(r rune) keys.Key { return keys.Key{Kind: keys.KindRune, Rune: r, Mod: tcell.ModCtrl} }

func runeKey(r rune) keys.Key { return keys.Key{Kind: keys.KindRune, Rune: r} }

// --- Motions -----------------------------------------------------------

func TestMotionsHL(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		start int
		seq   string
		want  int
	}{
		{"l moves one cluster", "abc", 0, "l", 1},
		{"l stops at last char", "abc", 2, "l", 2},
		{"2l moves two clusters", "abc", 0, "2l", 2},
		{"h moves one cluster back", "abc", 2, "h", 1},
		{"h stops at line start", "abc", 0, "h", 0},
		{"l does not cross newline", "ab\ncd", 1, "l", 1},
		{"h does not cross newline", "ab\ncd", 3, "h", 3},
		{"l over CJK moves one cluster (not one byte)", "日本語", 0, "l", len("日")},
		{"2l over CJK moves two clusters", "日本語", 0, "2l", len("日本")},
		{"h over CJK moves one cluster back", "日本語", len("日本"), "h", len("日")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.start)
			v := NewVim(ft)
			press(v, tc.seq)
			if got := ft.Cursor(); got != tc.want {
				t.Errorf("cursor = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMotionsLineWise(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		start int
		key   rune
		want  int
	}{
		{"0 goes to line start", "  abc", 4, '0', 0},
		{"^ goes to first non-blank", "  abc", 4, '^', 2},
		{"$ goes to last char", "abc", 0, '$', 2},
		{"$ on empty line stays", "\nabc", 0, '$', 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.start)
			v := NewVim(ft)
			v.Handle(runeKey(tc.key))
			if got := ft.Cursor(); got != tc.want {
				t.Errorf("cursor = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMotionsJK(t *testing.T) {
	text := "short\nlonger line\nmid"
	// Lines: "short"(0-5), "\n"(5), "longer line"(6-17), "\n"(17), "mid"(18-21)
	ft := newFakeText(t, text, 3) // column 3 on "short"
	v := NewVim(ft)
	v.Handle(runeKey('j'))
	if got := ft.Cursor(); got != 6+3 {
		t.Fatalf("after j, cursor = %d, want %d", got, 9)
	}
	// Move to a longer line at column 3, then down to "mid" (len 3):
	// desired column 3 should clamp to the last char (index 2 relative).
	v.Handle(runeKey('j'))
	if got := ft.Cursor(); got != 18+2 {
		t.Fatalf("after second j (short target line), cursor = %d, want %d", got, 20)
	}
	v.Handle(runeKey('k'))
	if got := ft.Cursor(); got != 6+3 {
		t.Fatalf("after k, cursor = %d, want %d (desired column restored)", got, 9)
	}
}

func TestMotionsGgG(t *testing.T) {
	text := "one\ntwo\nthree"
	ft := newFakeText(t, text, len(text))
	v := NewVim(ft)
	press(v, "gg")
	if got := ft.Cursor(); got != 0 {
		t.Fatalf("gg -> cursor = %d, want 0", got)
	}
	v.Handle(runeKey('G'))
	if got := ft.Cursor(); got != len("one\ntwo\n") {
		t.Fatalf("G -> cursor = %d, want %d", got, len("one\ntwo\n"))
	}
	press(v, "2gg")
	if got := ft.Cursor(); got != len("one\n") {
		t.Fatalf("2gg -> cursor = %d, want %d", got, len("one\n"))
	}
}

func TestMotionsWordForwardBackwardEnd(t *testing.T) {
	text := "foo bar.baz"
	tests := []struct {
		name  string
		start int
		key   rune
		want  int
	}{
		{"w from start skips word+space", 0, 'w', 4},
		{"w from within word lands on punctuation", 4, 'w', 7},
		{"b from end of buffer", len(text), 'b', 8},
		{"e from start lands on last char of word", 0, 'e', 2},
		{"e from letter lands on end of next word", 3, 'e', 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, text, tc.start)
			v := NewVim(ft)
			v.Handle(runeKey(tc.key))
			if got := ft.Cursor(); got != tc.want {
				t.Errorf("cursor = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMotionsEOnEmptyBufferDoesNotPanic reproduces a panic in wordEndOnce:
// on an empty buffer, indexAtOrAfter returns 0 for a 0-length cluster
// slice, and the unguarded "i+1 >= len(cs)" branch returned len(cs)-1
// (-1), which wordEndPos then indexed into cs directly (cs[-1]) since its
// own "i >= len(cs)" guard does not catch a negative index.
func TestMotionsEOnEmptyBufferDoesNotPanic(t *testing.T) {
	seqs := []string{"e", "de", "ye", "ce"}
	for _, seq := range seqs {
		t.Run(seq, func(t *testing.T) {
			ft := newFakeText(t, "", 0)
			v := NewVim(ft)
			press(v, seq)
			if got := ft.Cursor(); got != 0 {
				t.Errorf("cursor = %d, want 0", got)
			}
		})
	}
}

func TestMotionsWordCJK(t *testing.T) {
	// Each CJK character is its own word: "日本語 abc" — w from 0 should
	// land on the next CJK character, not skip the whole run.
	text := "日本語 abc"
	ft := newFakeText(t, text, 0)
	v := NewVim(ft)
	v.Handle(runeKey('w'))
	if got, want := ft.Cursor(), len("日"); got != want {
		t.Fatalf("w over CJK: cursor = %d, want %d (next CJK cluster, not the whole run)", got, want)
	}
}

// --- Insert mode ---------------------------------------------------------

func TestInsertModeEntryAndForwarding(t *testing.T) {
	ft := newFakeText(t, "abc", 0)
	v := NewVim(ft)

	act := v.Handle(runeKey('i'))
	if v.Mode() != ModeInsert {
		t.Fatalf("i did not enter insert mode")
	}
	if act.Kind != ModeChanged {
		t.Fatalf("i action = %v, want ModeChanged", act.Kind)
	}

	act = v.Handle(runeKey('x'))
	if act.Kind != Forward {
		t.Fatalf("insert-mode rune action = %v, want Forward", act.Kind)
	}

	act = v.Handle(ctrl('n'))
	if act.Kind != Consumed {
		t.Fatalf("Ctrl-n in insert mode = %v, want Consumed (not forwarded)", act.Kind)
	}
}

func TestEscLeavesInsertAndMovesCursorLeft(t *testing.T) {
	ft := newFakeText(t, "abc", 0)
	v := NewVim(ft)
	v.Handle(runeKey('a')) // append after 'a' -> cursor at 1
	if ft.Cursor() != 1 {
		t.Fatalf("after 'a', cursor = %d, want 1", ft.Cursor())
	}
	// Simulate the host forwarding a typed 'X' to TextArea directly
	// (fakeText has no InputHandler, so the test performs the same
	// mutation a real forward would).
	ft.Replace(ft.Cursor(), ft.Cursor(), "X")
	if v.Mode() != ModeInsert {
		t.Fatalf("mode = %v, want ModeInsert", v.Mode())
	}
	act := v.Handle(esc())
	if v.Mode() != ModeNormal {
		t.Fatalf("Esc did not return to normal mode")
	}
	if act.Kind != ModeChanged {
		t.Fatalf("Esc action = %v, want ModeChanged", act.Kind)
	}
	if got, want := ft.Text(), "aXbc"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if got := ft.Cursor(); got != 1 {
		t.Fatalf("cursor after Esc = %d, want 1 (moved left one column)", got)
	}
}

// TestEscAtEndOfLineMovesExactlyOneColumn reproduces a
// clusterIndexInLine off-by-one: at the true end of a line (one past the
// last cluster, matching no cluster's own start), it fell back to
// len(cs)-1 (as if the cursor were already resting ON the last cluster)
// instead of len(cs) (one past it), so a subsequent backward moveH (from
// leaving insert mode) landed one cluster too far left.
func TestEscAtEndOfLineMovesExactlyOneColumn(t *testing.T) {
	ft := newFakeText(t, "ab", 1) // cursor on 'b'
	v := NewVim(ft)
	v.Handle(runeKey('a')) // append after 'b' -> cursor at 2 (end of line)
	if got := ft.Cursor(); got != 2 {
		t.Fatalf("after 'a', cursor = %d, want 2", got)
	}
	v.Handle(esc())
	if got, want := ft.Cursor(), 1; got != want {
		t.Fatalf("cursor after Esc = %d, want %d (one column left, landing on 'b')", got, want)
	}
}

func TestEscAfterAppendAtEndOfLineLandsOnLastTypedChar(t *testing.T) {
	ft := newFakeText(t, "ab", 2) // cursor at end of line already
	v := NewVim(ft)
	v.Handle(runeKey('A')) // append at end of line -> cursor stays at 2
	for _, r := range "cd" {
		ft.Replace(ft.Cursor(), ft.Cursor(), string(r))
	}
	if got, want := ft.Text(), "abcd"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	v.Handle(esc())
	if got, want := ft.Cursor(), 3; got != want {
		t.Fatalf("cursor after Esc = %d, want %d (on 'd', the last typed char)", got, want)
	}
}

// TestXAtEndOfLineDeletesExactlyOneChar reproduces the same
// clusterIndexInLine off-by-one from X's (backward-delete) side: at the
// true end of a line, it deleted two clusters instead of one.
func TestXAtEndOfLineDeletesExactlyOneChar(t *testing.T) {
	ft := newFakeText(t, "abc", 3) // cursor at end of line
	v := NewVim(ft)
	v.Handle(runeKey('X'))
	if got, want := ft.Text(), "ab"; got != want {
		t.Fatalf("text = %q, want %q (X at end of line must delete exactly one char)", got, want)
	}
}

func TestCtrlSSendsInAnyMode(t *testing.T) {
	modes := []func(v *Vim){
		func(v *Vim) {},
		func(v *Vim) { v.Handle(runeKey('i')) },
		func(v *Vim) { v.Handle(runeKey('v')) },
		func(v *Vim) { v.Handle(runeKey(':')) },
	}
	for i, setup := range modes {
		ft := newFakeText(t, "abc", 0)
		v := NewVim(ft)
		setup(v)
		act := v.Handle(ctrl('s'))
		if act.Kind != SendRequested {
			t.Errorf("case %d: Ctrl-s action = %v, want SendRequested", i, act.Kind)
		}
	}
}

// --- Operators -----------------------------------------------------------

func TestOperatorsCharacterwise(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		start    int
		seq      string
		wantText string
		wantPos  int
	}{
		{"dw deletes word and trailing space", "foo bar", 0, "dw", "bar", 0},
		{"de deletes word only", "foo bar", 0, "de", " bar", 0},
		{"d$ deletes to end of line", "abc def", 4, "d$", "abc ", 3},
		{"db deletes back to word start", "foo bar", 4, "db", "bar", 0},
		{"dl deletes one char", "abc", 0, "dl", "bc", 0},
		{"dh deletes previous char", "abc", 2, "dh", "ac", 1},
		{"2dw deletes two words", "one two three", 0, "2dw", "three", 0},
		{"d2w deletes two words", "one two three", 0, "d2w", "three", 0},
		{"yw yanks without deleting", "foo bar", 0, "yw", "foo bar", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.start)
			v := NewVim(ft)
			press(v, tc.seq)
			if ft.Text() != tc.wantText {
				t.Errorf("text = %q, want %q", ft.Text(), tc.wantText)
			}
			if got := ft.Cursor(); got != tc.wantPos {
				t.Errorf("cursor = %d, want %d", got, tc.wantPos)
			}
		})
	}
}

func TestOperatorCwActsLikeCe(t *testing.T) {
	ft := newFakeText(t, "foo bar", 0)
	v := NewVim(ft)
	press(v, "cw")
	if v.Mode() != ModeInsert {
		t.Fatalf("cw did not enter insert mode")
	}
	if got, want := ft.Text(), " bar"; got != want {
		t.Fatalf("text = %q, want %q (cw must not eat the trailing space, unlike dw)", got, want)
	}
}

func TestOperatorCwStopsAtEndOfCurrentWord(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cursor int
		seq    string
		want   string
	}{
		{"cw on last char of a word changes only that word", "ab cd", 1, "cw", "a cd"},
		{"cw on a single-char word", "a bb cc", 0, "cw", " bb cc"},
		{"c2w on last char counts the current word as the first", "ab cd ef", 1, "c2w", "a ef"},
		{"cw mid-word still acts like ce", "foo bar", 0, "cw", " bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.cursor)
			v := NewVim(ft)
			press(v, tc.seq)
			if v.Mode() != ModeInsert {
				t.Fatalf("mode = %v, want insert", v.Mode())
			}
			if got := ft.Text(); got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetTextFromOutsideDuringInsertClosesTheSession(t *testing.T) {
	ft := newFakeText(t, "typed", 0)
	v := NewVim(ft)
	v.Handle(runeKey('i')) // insert session open, as when a composer is reopened
	v.SetTextFromOutside("failed\n\ntyped")
	v.Handle(esc())

	press(v, "u")
	if got := ft.Text(); got != "typed" {
		t.Fatalf("after one undo text = %q, want %q", got, "typed")
	}
	press(v, "u")
	if got := ft.Text(); got != "typed" {
		t.Fatalf("a second undo changed the text to %q, want no further entry", got)
	}
	v.Handle(ctrl('r'))
	if got := ft.Text(); got != "failed\n\ntyped" {
		t.Fatalf("after redo text = %q, want %q", got, "failed\n\ntyped")
	}
	v.Handle(ctrl('r'))
	if got := ft.Text(); got != "failed\n\ntyped" {
		t.Fatalf("a second redo duplicated text: %q", got)
	}
}

func TestOperatorsLinewise(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		start    int
		seq      string
		wantText string
	}{
		{"dd deletes current line", "one\ntwo\nthree", 4, "dd", "one\nthree"},
		{"dd on last line steals the preceding newline", "one\ntwo", 4, "dd", "one"},
		{"2dd deletes two lines", "one\ntwo\nthree", 0, "2dd", "three"},
		{"yy then p duplicates the line below", "one\ntwo", 0, "yyp", "one\none\ntwo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.start)
			v := NewVim(ft)
			press(v, tc.seq)
			if ft.Text() != tc.wantText {
				t.Errorf("text = %q, want %q", ft.Text(), tc.wantText)
			}
		})
	}
}

func TestOperatorCCReplacesLineContentAndEntersInsert(t *testing.T) {
	ft := newFakeText(t, "one\ntwo\nthree", 5)
	v := NewVim(ft)
	press(v, "cc")
	if v.Mode() != ModeInsert {
		t.Fatalf("cc did not enter insert mode")
	}
	if got, want := ft.Text(), "one\n\nthree"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestXXDCS(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		start    int
		key      rune
		wantText string
		wantMode Mode
	}{
		{"x deletes char under cursor", "abc", 0, 'x', "bc", ModeNormal},
		{"X deletes char before cursor", "abc", 2, 'X', "ac", ModeNormal},
		{"D deletes to end of line", "abc def", 4, 'D', "abc ", ModeNormal},
		{"C changes to end of line", "abc def", 4, 'C', "abc ", ModeInsert},
		{"s substitutes one char", "abc", 0, 's', "bc", ModeInsert},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.start)
			v := NewVim(ft)
			v.Handle(runeKey(tc.key))
			if ft.Text() != tc.wantText {
				t.Errorf("text = %q, want %q", ft.Text(), tc.wantText)
			}
			if v.Mode() != tc.wantMode {
				t.Errorf("mode = %v, want %v", v.Mode(), tc.wantMode)
			}
		})
	}
}

func TestJoinLines(t *testing.T) {
	ft := newFakeText(t, "foo\n  bar", 0)
	v := NewVim(ft)
	v.Handle(runeKey('J'))
	if got, want := ft.Text(), "foo bar"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestJoinLinesNoExtraSpaceWhenCurrentLineIsEmptyOrEndsInWhitespace covers
// vim's own join rule beyond "the next line is blank": no space is added
// either when the *current* line is itself empty, or when it already
// ends in trailing whitespace (there is nothing to separate it from).
func TestJoinLinesNoExtraSpaceWhenCurrentLineIsEmptyOrEndsInWhitespace(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"current line ends with a space", "foo \nbar", "foo bar"},
		{"current line ends with a tab", "foo\t\nbar", "foo\tbar"},
		{"current line is empty", "\nbar", "bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, 0)
			v := NewVim(ft)
			v.Handle(runeKey('J'))
			if got := ft.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPasteCharacterwiseAndLinewise(t *testing.T) {
	// Characterwise: yank "foo", paste after with p.
	ft := newFakeText(t, "foo bar", 0)
	v := NewVim(ft)
	press(v, "yw")
	ft.SetCursor(4) // move to "bar"
	v.Handle(runeKey('p'))
	if got, want := ft.Text(), "foo bfoo ar"; got != want {
		t.Fatalf("characterwise p: text = %q, want %q", got, want)
	}

	// Linewise: yy then p pastes the whole line below.
	ft2 := newFakeText(t, "one\ntwo", 0)
	v2 := NewVim(ft2)
	press(v2, "yy")
	v2.Handle(runeKey('p'))
	if got, want := ft2.Text(), "one\none\ntwo"; got != want {
		t.Fatalf("linewise p: text = %q, want %q", got, want)
	}

	// Linewise P pastes above.
	ft3 := newFakeText(t, "one\ntwo", 4)
	v3 := NewVim(ft3)
	press(v3, "yy")
	v3.Handle(runeKey('P'))
	if got, want := ft3.Text(), "one\ntwo\ntwo"; got != want {
		t.Fatalf("linewise P: text = %q, want %q", got, want)
	}
}

// TestPasteLinewiseAtTrueEndOfBufferDoesNotAddATrailingBlankLine
// reproduces a bug in the "paste after, past the last line, which itself
// has no trailing newline" branch: it always inserted "\n" + payload,
// where payload (the register) always ends in "\n" too, leaving the
// buffer with a newline it never had before "dd" removed the line "p"
// then restores — "a\nb" -> dd -> p should round-trip back to "a\nb",
// not "a\nb\n".
func TestPasteLinewiseAtTrueEndOfBufferDoesNotAddATrailingBlankLine(t *testing.T) {
	ft := newFakeText(t, "a\nb", 2) // cursor on "b", the last line
	v := NewVim(ft)
	press(v, "dd")
	if got, want := ft.Text(), "a"; got != want {
		t.Fatalf("after dd: text = %q, want %q", got, want)
	}
	v.Handle(runeKey('p'))
	if got, want := ft.Text(), "a\nb"; got != want {
		t.Fatalf("after p: text = %q, want %q (round trip must not add a trailing newline)", got, want)
	}
}

// --- Undo/redo -----------------------------------------------------------

func TestUndoRedoSimpleOperator(t *testing.T) {
	ft := newFakeText(t, "foo bar", 0)
	v := NewVim(ft)
	press(v, "dw")
	if ft.Text() != "bar" {
		t.Fatalf("text after dw = %q, want %q", ft.Text(), "bar")
	}
	v.Handle(runeKey('u'))
	if ft.Text() != "foo bar" {
		t.Fatalf("text after u = %q, want %q", ft.Text(), "foo bar")
	}
	v.Handle(ctrl('r'))
	if ft.Text() != "bar" {
		t.Fatalf("text after Ctrl-r = %q, want %q", ft.Text(), "bar")
	}
}

func TestUndoGroupsAWholeInsertSession(t *testing.T) {
	ft := newFakeText(t, "", 0)
	v := NewVim(ft)
	v.Handle(runeKey('i'))
	for _, r := range "hello" {
		ft.Replace(ft.Cursor(), ft.Cursor(), string(r)) // simulate forwarding
	}
	v.Handle(esc())
	if ft.Text() != "hello" {
		t.Fatalf("text = %q, want %q", ft.Text(), "hello")
	}
	v.Handle(runeKey('u'))
	if got, want := ft.Text(), ""; got != want {
		t.Fatalf("one u after a whole insert session: text = %q, want %q (single undo unit)", got, want)
	}
}

func TestUndoOfCcRestoresWholeLineInOneStep(t *testing.T) {
	ft := newFakeText(t, "one\ntwo\nthree", 5)
	v := NewVim(ft)
	press(v, "cc")
	for _, r := range "TWO" {
		ft.Replace(ft.Cursor(), ft.Cursor(), string(r))
	}
	v.Handle(esc())
	if got, want := ft.Text(), "one\nTWO\nthree"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	v.Handle(runeKey('u'))
	if got, want := ft.Text(), "one\ntwo\nthree"; got != want {
		t.Fatalf("text after u = %q, want %q (cc+typing is one undo unit)", got, want)
	}
}

func TestSetTextFromOutsidePushesOneUndoEntry(t *testing.T) {
	ft := newFakeText(t, "old text", 0)
	v := NewVim(ft)
	v.SetTextFromOutside("brand new text")
	if got, want := ft.Text(), "brand new text"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	v.Handle(runeKey('u'))
	if got, want := ft.Text(), "old text"; got != want {
		t.Fatalf("text after u = %q, want %q", got, want)
	}
}

func TestRedoStackClearedByANewEdit(t *testing.T) {
	ft := newFakeText(t, "foo bar baz", 0)
	v := NewVim(ft)
	press(v, "dw")
	v.Handle(runeKey('u')) // undo -> back to "foo bar baz"
	press(v, "dw")         // a fresh edit: the old redo entry must not resurrect
	v.Handle(ctrl('r'))    // nothing to redo now
	if got, want := ft.Text(), "bar baz"; got != want {
		t.Fatalf("text = %q, want %q (Ctrl-r after a fresh edit must be a no-op)", got, want)
	}
}

// TestUndoRedoAtEndOfLineClampCursorToLastChar reproduces undo/redo
// leaving the cursor one past the last character on a non-empty line
// (normal mode never rests there otherwise): both set the cursor to a
// raw undo-entry byte offset without clamping it the way every other
// command here does via clampToLineIfAtEnd.
func TestUndoRedoAtEndOfLineClampCursorToLastChar(t *testing.T) {
	ft := newFakeText(t, "ab", 1) // cursor on 'b'
	v := NewVim(ft)
	v.Handle(runeKey('A')) // append at end of line -> cursor at 2
	for _, r := range "cd" {
		ft.Replace(ft.Cursor(), ft.Cursor(), string(r))
	}
	v.Handle(esc())
	if got, want := ft.Text(), "abcd"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}

	v.Handle(runeKey('u'))
	if got, want := ft.Text(), "ab"; got != want {
		t.Fatalf("text after u = %q, want %q", got, want)
	}
	if got, want := ft.Cursor(), 1; got != want {
		t.Fatalf("cursor after u = %d, want %d (clamped to the last char, not past it)", got, want)
	}

	v.Handle(ctrl('r'))
	if got, want := ft.Text(), "abcd"; got != want {
		t.Fatalf("text after Ctrl-r = %q, want %q", got, want)
	}
	if got, want := ft.Cursor(), 3; got != want {
		t.Fatalf("cursor after Ctrl-r = %d, want %d (clamped to the last char, not past it)", got, want)
	}
}

// --- Visual mode -----------------------------------------------------------

func TestVisualDeleteCharacterwise(t *testing.T) {
	// Unlike operator-pending "dw" (exclusive, stops before the next
	// word), visual mode's motions are always inclusive of the character
	// the cursor lands on — a well-known vim asymmetry — so "vwd" here
	// deletes "one t", not just "one ".
	ft := newFakeText(t, "one two three", 0)
	v := NewVim(ft)
	press(v, "vwd")
	if v.Mode() != ModeNormal {
		t.Fatalf("mode after visual d = %v, want ModeNormal", v.Mode())
	}
	if got, want := ft.Text(), "wo three"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestVisualYankThenPaste(t *testing.T) {
	ft := newFakeText(t, "one two three", 0)
	v := NewVim(ft)
	press(v, "vwy")
	if got, want := ft.Text(), "one two three"; got != want {
		t.Fatalf("yank must not change the text: got %q, want %q", got, want)
	}
	v.Handle(runeKey('p'))
	if got, want := ft.Text(), "oone tne two three"; got != want {
		t.Fatalf("text after p = %q, want %q", got, want)
	}
}

func TestVisualLineDelete(t *testing.T) {
	ft := newFakeText(t, "one\ntwo\nthree", 5)
	v := NewVim(ft)
	press(v, "Vjd")
	if got, want := ft.Text(), "one"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestVisualChangeEntersInsert(t *testing.T) {
	ft := newFakeText(t, "one two three", 0)
	v := NewVim(ft)
	press(v, "vwc")
	if v.Mode() != ModeInsert {
		t.Fatalf("mode after visual c = %v, want ModeInsert", v.Mode())
	}
	if got, want := ft.Text(), "wo three"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestVisualEscCollapsesSelection(t *testing.T) {
	ft := newFakeText(t, "one two three", 0)
	v := NewVim(ft)
	press(v, "ww") // move forward, extending nothing yet (normal mode)
	v.Handle(runeKey('v'))
	v.Handle(runeKey('w'))
	v.Handle(esc())
	if v.Mode() != ModeNormal {
		t.Fatalf("mode after Esc = %v, want ModeNormal", v.Mode())
	}
	if ft.selA != ft.selB {
		t.Fatalf("selection not collapsed after Esc: [%d,%d)", ft.selA, ft.selB)
	}
}

// TestVisualCtrlRWithShrunkenBufferDoesNotPanic reproduces a panic: Ctrl-r
// in visual mode redid an earlier edit that shrank the buffer, but
// visualCursor (deliberately never resynced from Text.Cursor() while a
// selection is active — see Vim's own doc comment on that field) was left
// pointing past the now-shorter buffer's end; the next visual-mode motion
// then called lineBounds with that stale, out-of-range offset, indexing
// before the start of the (now shorter) text.
func TestVisualCtrlRWithShrunkenBufferDoesNotPanic(t *testing.T) {
	ft := newFakeText(t, "abc\ndef", 0)
	v := NewVim(ft)
	press(v, "j")
	press(v, "dd")
	press(v, "u")
	press(v, "j")
	press(v, "$")
	press(v, "v")
	v.Handle(ctrl('r'))
	press(v, "h")

	if v.Mode() != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal (Ctrl-r in visual mode must exit visual first, like vim)", v.Mode())
	}
}

// --- Command line ----------------------------------------------------------

func TestCommandLine(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want ActionKind
	}{
		{":w sends", "w", SendRequested},
		{":wq sends", "wq", SendRequested},
		{":q closes keeping the draft", "q", CloseRequested},
		{":q! discards", "q!", DiscardRequested},
		{":e opens the external editor", "e", ExternalEditRequested},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, "abc", 0)
			v := NewVim(ft)
			v.Handle(runeKey(':'))
			press(v, tc.cmd)
			act := v.Handle(keys.Key{Kind: keys.KindSpecial, Special: tcell.KeyEnter})
			if act.Kind != tc.want {
				t.Errorf("action = %v, want %v", act.Kind, tc.want)
			}
			if v.Mode() != ModeNormal {
				t.Errorf("mode after command = %v, want ModeNormal", v.Mode())
			}
		})
	}
}

func TestCommandLineUnknownCommandReportsError(t *testing.T) {
	ft := newFakeText(t, "abc", 0)
	v := NewVim(ft)
	v.Handle(runeKey(':'))
	press(v, "bogus")
	act := v.Handle(keys.Key{Kind: keys.KindSpecial, Special: tcell.KeyEnter})
	if act.Kind != Consumed {
		t.Fatalf("action = %v, want Consumed", act.Kind)
	}
	if v.LastCommandError() == "" {
		t.Fatalf("LastCommandError() is empty, want a message about the unknown command")
	}
}

func TestCommandLineEscCancels(t *testing.T) {
	ft := newFakeText(t, "abc", 0)
	v := NewVim(ft)
	v.Handle(runeKey(':'))
	press(v, "q")
	v.Handle(esc())
	if v.Mode() != ModeNormal {
		t.Fatalf("mode after Esc = %v, want ModeNormal", v.Mode())
	}
}

func TestCommandLineBackspaceOnEmptyCancels(t *testing.T) {
	ft := newFakeText(t, "abc", 0)
	v := NewVim(ft)
	v.Handle(runeKey(':'))
	v.Handle(keys.Key{Kind: keys.KindSpecial, Special: tcell.KeyBackspace})
	if v.Mode() != ModeNormal {
		t.Fatalf("mode after Backspace on an empty command line = %v, want ModeNormal", v.Mode())
	}
}

// --- Mentions ----------------------------------------------------------

func TestMentionQueryDetection(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		cursor    int
		wantMatch bool
		wantQuery string
	}{
		{"bare @ at start", "@", 1, true, ""},
		{"@ with prefix", "hi @oct", 7, true, "oct"},
		{"@ mid-word does not trigger", "foo@bar", 7, false, ""},
		{"@ after space triggers", "hello @al", 9, true, "al"},
		{"no @ at all", "hello", 5, false, ""},
		{"@ with hyphenated login", "@foo-bar", 8, true, "foo-bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeText(t, tc.text, tc.cursor)
			v := NewVim(ft)
			v.Handle(runeKey('i')) // enter insert mode so OnTextChanged is active
			ft.SetCursor(tc.cursor)
			act := v.OnTextChanged()
			if tc.wantMatch {
				if act.Kind != MentionQuery {
					t.Fatalf("action = %v, want MentionQuery", act.Kind)
				}
				if act.Query != tc.wantQuery {
					t.Fatalf("query = %q, want %q", act.Query, tc.wantQuery)
				}
			} else if act.Kind != Consumed {
				t.Fatalf("action = %v, want Consumed (no match)", act.Kind)
			}
		})
	}
}

func TestAcceptMentionReplacesPrefix(t *testing.T) {
	ft := newFakeText(t, "hi @oc", 6)
	v := NewVim(ft)
	v.Handle(runeKey('i'))
	act := v.OnTextChanged()
	if act.Kind != MentionQuery {
		t.Fatalf("action = %v, want MentionQuery", act.Kind)
	}
	v.AcceptMention("octocat")
	if got, want := ft.Text(), "hi @octocat "; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestAcceptMentionAfterCursorMovedAwayIsANoOp reproduces text corruption
// from a stale mention span: a Forwarded arrow key (Left/Right/Home/End)
// moves the cursor without going through Vim.Handle and without
// triggering TextArea's own "changed" callback (pure cursor movement is
// not an edit), so OnTextChanged never re-runs and mentionStart/
// mentionActive are left pointing at a span the cursor has since moved
// out of. AcceptMention must notice this itself — via Cursor() and the
// span's own content, not just mentionActive — rather than trust
// whatever OnTextChanged last computed.
func TestAcceptMentionAfterCursorMovedAwayIsANoOp(t *testing.T) {
	ft := newFakeText(t, "hi @da", 6)
	v := NewVim(ft)
	v.Handle(runeKey('i'))
	if act := v.OnTextChanged(); act.Kind != MentionQuery {
		t.Fatalf("precondition: OnTextChanged = %v, want MentionQuery", act.Kind)
	}

	ft.SetCursor(1) // simulates Left x5, bypassing Vim entirely
	v.AcceptMention("dave")

	if got, want := ft.Text(), "hi @da"; got != want {
		t.Fatalf("text = %q, want %q (AcceptMention must no-op once the cursor left the mention span)", got, want)
	}
}

func TestOnTextChangedOutsideInsertModeIsANoOp(t *testing.T) {
	ft := newFakeText(t, "@oct", 4)
	v := NewVim(ft)
	act := v.OnTextChanged()
	if act.Kind != Consumed {
		t.Fatalf("action = %v, want Consumed outside insert mode", act.Kind)
	}
}
