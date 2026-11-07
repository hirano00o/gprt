package widget

import (
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func linesText(lines [][]Span) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = spansText(l)
	}
	return out
}

func TestWrapSpans_SingleStyleText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{"empty is one empty line", "", 10, []string{""}},
		{"fits", "hello world", 20, []string{"hello world"}},
		{
			"brackets are literal text", "- [x] tests added and [docs](https://example.com) updated", 20,
			[]string{"- [x] tests added", "and", "[docs](https://examp", "le.com)", "updated"},
		},
		{"breaks at spaces", "the quick brown fox jumps", 10, []string{"the quick", "brown fox", "jumps"}},
		{
			"hard-breaks an overlong word", "supercalifragilisticexpialidocious", 10,
			[]string{"supercalif", "ragilistic", "expialidoc", "ious"},
		},
		{"newline and blank line are hard breaks", "first\n\nsecond", 40, []string{"first", "", "second"}},
		{"nested list indentation", "- top\n  - nested\n    - deeper", 40, []string{"- top", "  - nested", "    - deeper"}},
		{"indent repeats on wrapped lines", "  aaa bbb ccc", 9, []string{"  aaa bbb", "  ccc"}},
		{"four-space code line", "    func foo() {}", 40, []string{"    func foo() {}"}},
		{"internal space runs survive", "some   spaced   text", 40, []string{"some   spaced   text"}},
		{"indent wider than width is dropped", "          x", 5, []string{"x"}},
		{"space run at a wrap point starts no blank line", "aaa  bbb", 3, []string{"aaa", "bbb"}},
		{"space run after a full line", "aaaaa  bb", 5, []string{"aaaaa", "bb"}},
		{"space run after a hard-broken word", "aaaaaaaaaaaa  bb", 5, []string{"aaaaa", "aaaaa", "aa", "bb"}},
		{"long space run after a hard-broken word", "aaaaaaaaaaaa   bb", 5, []string{"aaaaa", "aaaaa", "aa", "bb"}},
		{"wide characters", "日本語のテキストを折り返す", 7, []string{"日本語", "のテキ", "ストを", "折り返", "す"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := linesText(WrapSpans([]Span{{Text: tc.text}}, tc.width))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WrapSpans(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
			}
		})
	}
}

func TestWrapSpans_KeepsStylesAcrossWraps(t *testing.T) {
	bold := tcell.StyleDefault.Bold(true)
	plain := tcell.StyleDefault
	spans := []Span{
		{Text: "aaa ", Style: plain},
		{Text: "bbb ccc", Style: bold},
		{Text: " ddd", Style: plain},
	}
	got := WrapSpans(spans, 7)
	want := [][]Span{
		{{Text: "aaa ", Style: plain}, {Text: "bbb", Style: bold}},
		{{Text: "ccc", Style: bold}, {Text: " ddd", Style: plain}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapSpans() = %+v, want %+v", got, want)
	}
}

func TestWrapSpans_StyleChangeInsideAWordKeepsBothFragments(t *testing.T) {
	bold := tcell.StyleDefault.Bold(true)
	plain := tcell.StyleDefault
	got := WrapSpans([]Span{{Text: "foo", Style: bold}, {Text: "bar", Style: plain}}, 4)
	want := [][]Span{
		{{Text: "foo", Style: bold}, {Text: "b", Style: plain}},
		{{Text: "ar", Style: plain}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapSpans() = %+v, want %+v", got, want)
	}
}

func TestWrapSpans_NeverExceedsWidth(t *testing.T) {
	spans := []Span{
		{Text: "日本語 mixed with ", Style: tcell.StyleDefault},
		{Text: "supercalifragilisticexpialidocious", Style: tcell.StyleDefault.Bold(true)},
		{Text: " 👍🏽 emoji and 日本語日本語", Style: tcell.StyleDefault},
	}
	// Starts at 2: a double-width cluster cannot fit in fewer columns.
	for _, width := range []int{2, 3, 5, 8, 13, 40} {
		for i, line := range WrapSpans(spans, width) {
			if w := SpanWidth(line); w > width {
				t.Errorf("width %d: line %d %q has width %d", width, i, spansText(line), w)
			}
		}
	}
}

func TestWrapSpans_NonPositiveWidthReturnsNil(t *testing.T) {
	if got := WrapSpans([]Span{{Text: "x"}}, 0); got != nil {
		t.Errorf("WrapSpans(width 0) = %+v, want nil", got)
	}
}
