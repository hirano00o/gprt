package widget

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/theme"
)

// mdText renders body and returns each line's concatenated text.
func mdText(body string, width int) []string {
	return linesText(RenderMarkdown(body, width))
}

// styleOf returns the style of the first span of lines whose text is
// exactly sub, failing the test when there is none.
func styleOf(t *testing.T, lines [][]Span, sub string) tcell.Style {
	t.Helper()
	for _, line := range lines {
		for _, s := range line {
			if s.Text == sub {
				return s.Style
			}
		}
	}
	t.Fatalf("no span with text %q in %q", sub, linesText(lines))
	return tcell.StyleDefault
}

func hasAttr(style tcell.Style, attr tcell.AttrMask) bool {
	_, _, a := style.Decompose()
	return a&attr != 0
}

func TestRenderMarkdown_BlockText(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		width int
		want  []string
	}{
		{"heading drops the marker", "# Title", 40, []string{"Title"}},
		{"soft break is a hard break", "line one\nline two", 40, []string{"line one", "line two"}},
		{"explicit hard break", "one  \ntwo", 40, []string{"one", "two"}},
		{"paragraph wraps", "aaa bbb ccc", 7, []string{"aaa bbb", "ccc"}},
		{"blank line between blocks", "# H\npara\n\n- a\n- b", 40, []string{"H", "", "para", "", "• a", "• b"}},
		{"tight list has no blank lines", "- a\n- b\n- c", 40, []string{"• a", "• b", "• c"}},
		{"loose list keeps blank lines", "- a\n\n- b", 40, []string{"• a", "", "• b"}},
		{"ordered list honours the start number", "3. a\n4. b", 40, []string{"3. a", "4. b"}},
		{"ordered list pads markers to align", "9. a\n10. b", 40, []string{"9.  a", "10. b"}},
		{"nested lists indent two columns", "- a\n  - b\n    - c", 40, []string{"• a", "  • b", "    • c"}},
		{"wrapped item aligns with its text", "- aaa bbb ccc", 8, []string{"• aaa", "  bbb", "  ccc"}},
		{"task list replaces the bullet", "- [ ] todo\n- [x] done", 40, []string{"[ ] todo", "[x] done"}},
		{"blockquote", "> quoted", 40, []string{"│ quoted"}},
		{"nested blockquote", "> > deep", 40, []string{"│ │ deep"}},
		{"blockquote wraps inside its prefix", "> aaa bbb", 7, []string{"│ aaa", "│ bbb"}},
		{"thematic break", "---", 5, []string{"─────"}},
		{"fence lines are hidden", "```go\nx := 1\n```", 40, []string{"x := 1"}},
		{"indented code", "para\n\n    code line", 40, []string{"para", "", "code line"}},
		{"code is chopped, never word-wrapped", "```\naaa bbb ccc ddd\n```", 8, []string{"aaa bbb ", "ccc ddd"}},
		{"wide code never overflows", "```\n日本語日本語\n```", 5, []string{"日本", "語日", "本語"}},
		{"code tabs are expanded", "```\n\tx\n```", 40, []string{"    x"}},
		{"escaped punctuation", `\*not emphasis\*`, 40, []string{"*not emphasis*"}},
		{"entities are decoded", "a &amp; b", 40, []string{"a & b"}},
		{"inline br", "a<br>b", 40, []string{"a", "b"}},
		{"inline html comment is dropped", "a<!-- c -->b", 40, []string{"ab"}},
		{"image shows alt", "![logo](u.png)", 40, []string{"[image: logo]"}},
		{"link shows its url", "[docs](https://e.com)", 40, []string{"docs (https://e.com)"}},
		{"link with text equal to url has no suffix", "[https://e.com](https://e.com)", 40, []string{"https://e.com"}},
		{"autolink shows only the url", "<https://e.com>", 40, []string{"https://e.com"}},
		{"linkified url", "see https://e.com now", 40, []string{"see https://e.com now"}},
		{"html comment block is hidden", "before\n\n<!-- hidden\nmulti -->\n\nafter", 40, []string{"before", "", "after"}},
		{
			"html tags are stripped, text kept",
			"<details>\n<summary>Title</summary>\n\nbody text\n\n</details>",
			40,
			[]string{"Title", "", "body text"},
		},
		{"single-line details", "<details><summary>Title</summary>", 40, []string{"Title"}},
		{"code tab padding is not doubled", "-\t\tfoo", 40, []string{"•   foo"}},
		{"trailing br adds no blank line", "foo<br>", 40, []string{"foo"}},
		{"trailing double br adds no blank line", "foo<br><br>", 40, []string{"foo"}},
		{"task item aligns with numbered items", "1. [ ] x\n2. y", 40, []string{"[ ] x", "2.  y"}},
		{"script content is hidden", "<script>\nvar a = 1;\n</script>\n\ntext", 40, []string{"text"}},
		{"style content is hidden", "<style>\n.a { color: red }\n</style>\n\ntext", 40, []string{"text"}},
		{"pre keeps its lines verbatim", "<pre>\nline  one\n  second\n</pre>", 40, []string{"line  one", "  second"}},
		{"pre is chopped, not word-wrapped", "<pre>\naaa bbb ccc ddd\n</pre>", 8, []string{"aaa bbb ", "ccc ddd"}},
		{"textarea keeps tags stripped", "<textarea>\nx  y\n</textarea>", 40, []string{"x  y"}},
		{"inline pre block", "<pre>a  b</pre>", 40, []string{"a  b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mdText(tc.body, tc.width); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RenderMarkdown(%q, %d) = %q, want %q", tc.body, tc.width, got, tc.want)
			}
		})
	}
}

func TestRenderMarkdown_EmptyResults(t *testing.T) {
	for _, body := range []string{"", "  \n\t\n", "<!-- only a comment -->", "<script>x</script>", "<style>\n</style>", "```\n```", "```go\n\n```", "<br>"} {
		if got := RenderMarkdown(body, 40); len(got) != 0 {
			t.Errorf("RenderMarkdown(%q) = %q, want no lines", body, linesText(got))
		}
	}
}

func TestRenderMarkdown_InlineStyles(t *testing.T) {
	lines := RenderMarkdown("*i* **b** ***bi*** ~~s~~ `c` [t](http://x) ![a](u)", 80)

	if got := styleOf(t, lines, "i"); !hasAttr(got, tcell.AttrItalic) || hasAttr(got, tcell.AttrBold) {
		t.Errorf("emphasis style = %v, want italic only", got)
	}
	if got := styleOf(t, lines, "b"); !hasAttr(got, tcell.AttrBold) || hasAttr(got, tcell.AttrItalic) {
		t.Errorf("strong style = %v, want bold only", got)
	}
	if got := styleOf(t, lines, "bi"); !hasAttr(got, tcell.AttrBold) || !hasAttr(got, tcell.AttrItalic) {
		t.Errorf("strong emphasis style = %v, want bold and italic", got)
	}
	if got := styleOf(t, lines, "s"); !hasAttr(got, tcell.AttrStrikeThrough) {
		t.Errorf("strikethrough style = %v, want strikethrough", got)
	}
	if got := styleOf(t, lines, "c"); got != theme.Code {
		t.Errorf("code span style = %v, want theme.Code", got)
	}
	if got := styleOf(t, lines, "t"); got.GetUnderlineStyle() == tcell.UnderlineStyleNone {
		t.Errorf("link text style = %v, want an underline style (tcell draws underline from ulStyle only)", got)
	}
	if got := styleOf(t, lines, " (http://x)"); got != theme.Muted {
		t.Errorf("link url suffix style = %v, want theme.Muted", got)
	}
	if got := styleOf(t, lines, "[image: a]"); got != theme.Muted {
		t.Errorf("image style = %v, want theme.Muted", got)
	}
}

func TestRenderMarkdown_LinkifiedAndAutolinksUseLinkStyle(t *testing.T) {
	for _, body := range []string{"see https://e.com now", "<https://e.com>"} {
		lines := RenderMarkdown(body, 80)
		got := styleOf(t, lines, "https://e.com")
		if got.GetUnderlineStyle() == tcell.UnderlineStyleNone {
			t.Errorf("RenderMarkdown(%q): url style = %v, want link style with an underline style", body, got)
		}
	}
}

func TestRenderMarkdown_HeadingAndTaskStyles(t *testing.T) {
	if got := styleOf(t, RenderMarkdown("# Title", 40), "Title"); got != theme.Header {
		t.Errorf("heading style = %v, want theme.Header", got)
	}
	lines := RenderMarkdown("- [x] done\n- [ ] todo", 40)
	if got := styleOf(t, lines, "[x]"); got != theme.Success {
		t.Errorf("checked box style = %v, want theme.Success", got)
	}
}

func TestRenderMarkdown_CodeStyles(t *testing.T) {
	plain := RenderMarkdown("```\nx := 1\n```", 40)
	if got := styleOf(t, plain, "x := 1"); got != theme.Muted {
		t.Errorf("unlabelled code style = %v, want theme.Muted", got)
	}
	unknown := RenderMarkdown("```nosuchlang\nx := 1\n```", 40)
	if got := styleOf(t, unknown, "x := 1"); got != theme.Muted {
		t.Errorf("unknown-language code style = %v, want theme.Muted", got)
	}

	highlighted := RenderMarkdown("```go\nfunc main() {}\n```", 40)
	if len(highlighted) != 1 || len(highlighted[0]) < 2 {
		t.Fatalf("highlighted go code = %+v, want one line of several token spans", highlighted)
	}
	if got := spansText(highlighted[0]); got != "func main() {}" {
		t.Errorf("highlighted text = %q, want the source unchanged", got)
	}
}

func TestRenderMarkdown_BlockquoteAndRulePrefixStyle(t *testing.T) {
	quote := RenderMarkdown("> q", 40)
	if got := styleOf(t, quote, "│ "); got != theme.Muted {
		t.Errorf("quote prefix style = %v, want theme.Muted", got)
	}
	rule := RenderMarkdown("---", 4)
	if got := styleOf(t, rule, "────"); got != theme.Muted {
		t.Errorf("rule style = %v, want theme.Muted", got)
	}
}

func TestRenderMarkdown_Table(t *testing.T) {
	body := "| a | bb |\n|---|---|\n| 1 | 22 |"
	want := []string{"a │ bb", "──┼───", "1 │ 22"}
	lines := RenderMarkdown(body, 40)
	if got := linesText(lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("table = %q, want %q", got, want)
	}
	if got := styleOf(t, lines, "a"); !hasAttr(got, tcell.AttrBold) {
		t.Errorf("header cell style = %v, want bold", got)
	}
}

func TestRenderMarkdown_TableAlignment(t *testing.T) {
	body := "| l | r | c |\n|:--|--:|:-:|\n| 1 | 2 | 3 |\n| aaa | bbb | ccc |"
	want := []string{
		"l   │   r │  c",
		"────┼─────┼────",
		"1   │   2 │  3",
		"aaa │ bbb │ ccc",
	}
	// The final column is not right-padded, so only compare lines after
	// trimming trailing spaces.
	var got []string
	for _, l := range mdText(body, 40) {
		got = append(got, strings.TrimRight(l, " "))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aligned table = %q, want %q", got, want)
	}
}

func TestRenderMarkdown_TableShrinksToWidth(t *testing.T) {
	body := "| name | description | notes |\n|---|---|---|\n" +
		"| short | a rather long description of the thing | more words here |\n" +
		"| x | y | z |"
	for _, width := range []int{12, 20, 30, 80} {
		for i, line := range RenderMarkdown(body, width) {
			if w := SpanWidth(line); w > width {
				t.Errorf("width %d: table line %d %q has width %d", width, i, spansText(line), w)
			}
		}
	}
	narrow := strings.Join(mdText(body, 30), "\n")
	if !strings.Contains(narrow, "…") {
		t.Errorf("narrow table has no truncation marker:\n%s", narrow)
	}
	wide := strings.Join(mdText(body, 80), "\n")
	if strings.Contains(wide, "…") {
		t.Errorf("table that fits was truncated:\n%s", wide)
	}
}

const mixedFixture = "# Heading with `code` and a [link](https://example.com/a/very/long/path)\n\n" +
	"Paragraph with **bold**, *italic*, ~~gone~~ and a very long unbroken token " +
	"supercalifragilisticexpialidocious plus 日本語のテキストが続きます。\n" +
	"soft break line\n\n" +
	"- item one with enough words to wrap around a narrow terminal\n" +
	"  - nested [ ] item\n" +
	"    1. deep ordered item that also wraps nicely\n" +
	"- [x] done task with text\n" +
	"- [ ] open task\n\n" +
	"> quote with 日本語 text that is long enough to wrap\n" +
	"> > nested quote\n\n" +
	"```go\nfunc veryLongFunctionName(argumentOne, argumentTwo string) 日本語 { return nil }\n```\n\n" +
	"    indented code with 日本語日本語日本語\n\n" +
	"| 名前 | description | n |\n|:--|--:|:-:|\n| 日本語日本語 | long long long long long | 1 |\n| b | c | 2 |\n\n" +
	"---\n\n" +
	"<details><summary>Summary text</summary>\n\n" +
	"<!-- hidden -->\n\n" +
	"https://example.com/a/really/long/url/that/never/ends/and/never/breaks\n\n" +
	"![img](https://example.com/i.png) end<br>after break\n\n</details>\n"

func TestRenderMarkdown_EveryLineFitsWidth(t *testing.T) {
	for _, width := range []int{10, 20, 40, 80} {
		lines := RenderMarkdown(mixedFixture, width)
		if len(lines) == 0 {
			t.Fatalf("width %d: no lines", width)
		}
		for i, line := range lines {
			if w := SpanWidth(line); w > width {
				t.Errorf("width %d: line %d %q has width %d", width, i, spansText(line), w)
			}
		}
	}
}

// TestRenderMarkdown_MalformedInputStaysWithinWidth also covers prefixes
// that eat the whole width: below, deep nesting is only checked at widths
// that leave room for the markers themselves.
func TestRenderMarkdown_MalformedInputStaysWithinWidth(t *testing.T) {
	inputs := []string{
		"|a|\n|-|", "|a|b|\n|-|-|\n|1|", "> ", "- ", "1.", "```", "~~~go\nx", "<", "<!-- open", "[a](", "![", "***",
		"\t\t", "<div>\n<p>text &amp; &#x41; more</p>\n</div>", "a\\\nb", "[ref]\n\n[ref]: https://e.com",
		"- [x]", "| 日本語 |\n|---|\n| 日本語日本語日本語 |",
	}
	for _, in := range inputs {
		for _, width := range []int{6, 7, 12, 40} {
			for i, line := range RenderMarkdown(in, width) {
				if w := SpanWidth(line); w > width {
					t.Errorf("RenderMarkdown(%q, %d) line %d %q has width %d", in, width, i, spansText(line), w)
				}
			}
		}
	}

	for _, in := range []string{"- - - a", "> - > 1. x"} {
		for _, width := range []int{12, 40} {
			for i, line := range RenderMarkdown(in, width) {
				if w := SpanWidth(line); w > width {
					t.Errorf("RenderMarkdown(%q, %d) line %d %q has width %d", in, width, i, spansText(line), w)
				}
			}
		}
	}
}

func TestRenderMarkdown_PreStyleIsMuted(t *testing.T) {
	lines := RenderMarkdown("<pre>\nraw text\n</pre>", 40)
	if got := styleOf(t, lines, "raw text"); got != theme.Muted {
		t.Errorf("pre content style = %v, want theme.Muted", got)
	}
}

// TestRenderMarkdown_TinyWidthsDoNotPanic covers the degenerate widths a
// pane can briefly have: nothing may panic, no text may be lost, and a
// double-width cluster gets a line of its own rather than being split.
func TestRenderMarkdown_TinyWidthsDoNotPanic(t *testing.T) {
	bodies := []string{"日本語 👍🏽 emoji", "```\n日本語👍\n```", "- 日本語\n> 👍", "| 日本 |\n|---|\n| 👍 |", "# 日本語"}
	for _, body := range bodies {
		for _, width := range []int{-1, 0, 1} {
			if lines := RenderMarkdown(body, width); len(lines) == 0 {
				t.Errorf("RenderMarkdown(%q, %d) returned no lines", body, width)
			}
		}
	}

	var text strings.Builder
	for _, line := range RenderMarkdown("日本語 👍🏽 emoji", 1) {
		text.WriteString(spansText(line))
	}
	if got := text.String(); got != "日本語👍🏽emoji" {
		t.Errorf("width-1 text (spaces dropped at wrap points) = %q, want every character kept", got)
	}
}

func TestRenderMarkdown_ConcurrentCallsAgree(t *testing.T) {
	const body = "# Title\n\n```go\nfunc main() { println(\"hi\") }\n```\n\n- a\n- b\n"
	want := mdText(body, 30)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if got := mdText(body, 30); !reflect.DeepEqual(got, want) {
					t.Errorf("concurrent RenderMarkdown = %q, want %q", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
