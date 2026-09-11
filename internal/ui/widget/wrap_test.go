package widget

import (
	"strings"
	"testing"
)

func TestWrapTextTreatsBracketsAsLiteralText(t *testing.T) {
	// tview.WordWrap parses "[x]"/"[docs]" as (zero-width) colour/region
	// tags, since gprt does not use tview's tag syntax for its own spans;
	// WrapText must not make the same mistake.
	text := "- [x] tests added and [docs](https://example.com) updated"
	lines := WrapText(text, 20)

	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "[x]") {
		t.Errorf("wrapped text lost \"[x]\"; joined = %q", joined)
	}
	if !strings.Contains(joined, "[docs]") {
		t.Errorf("wrapped text lost \"[docs]\"; joined = %q", joined)
	}
	for _, line := range lines {
		if w := SpanWidth([]Span{{Text: line}}); w > 20 {
			t.Errorf("line %q has display width %d, want <= 20", line, w)
		}
	}
}

func TestWrapTextBreaksAtSpaces(t *testing.T) {
	lines := WrapText("the quick brown fox jumps", 10)
	for _, line := range lines {
		if w := SpanWidth([]Span{{Text: line}}); w > 10 {
			t.Errorf("line %q has display width %d, want <= 10", line, w)
		}
	}
	if got := strings.Join(lines, " "); got != "the quick brown fox jumps" {
		t.Errorf("re-joined wrapped text = %q, want the original words preserved in order", got)
	}
}

func TestWrapTextHardBreaksAWordLongerThanWidth(t *testing.T) {
	lines := WrapText("supercalifragilisticexpialidocious", 10)
	if len(lines) < 2 {
		t.Fatalf("len(lines) = %d, want the long word split across multiple lines", len(lines))
	}
	for _, line := range lines {
		if w := SpanWidth([]Span{{Text: line}}); w > 10 {
			t.Errorf("line %q has display width %d, want <= 10", line, w)
		}
	}
	if got := strings.Join(lines, ""); got != "supercalifragilisticexpialidocious" {
		t.Errorf("re-joined hard-broken word = %q, want the original word preserved", got)
	}
}

func TestWrapTextPreservesBlankLinesAndHardNewlines(t *testing.T) {
	lines := WrapText("first\n\nsecond", 40)
	want := []string{"first", "", "second"}
	if len(lines) != len(want) {
		t.Fatalf("WrapText produced %d lines, want %d: %#v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestWrapTextZeroWidthReturnsNil(t *testing.T) {
	if got := WrapText("anything", 0); got != nil {
		t.Errorf("WrapText with width 0 = %#v, want nil", got)
	}
}

func TestWrapTextEmptyReturnsOneEmptyLine(t *testing.T) {
	got := WrapText("", 10)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("WrapText(\"\", 10) = %#v, want one empty line", got)
	}
}

// TestWrapTextPreservesNestedListIndentation guards against the
// strings.Fields-based implementation's regression versus tview.WordWrap:
// Fields drops every line's leading whitespace entirely, which flattens a
// nested Markdown list (each level normally indented two spaces further
// than its parent) into one level.
func TestWrapTextPreservesNestedListIndentation(t *testing.T) {
	text := "- top\n  - nested\n    - deeper"
	want := []string{"- top", "  - nested", "    - deeper"}
	got := WrapText(text, 40) // wide enough that no line needs to wrap
	if len(got) != len(want) {
		t.Fatalf("WrapText produced %d lines, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestWrapTextPreservesFourSpaceIndentedCode guards the same regression for
// a 4-space indented code block line (Markdown's other code-block syntax,
// alongside fenced ``` blocks).
func TestWrapTextPreservesFourSpaceIndentedCode(t *testing.T) {
	line := "    func foo() {}"
	got := WrapText(line, 40)
	if len(got) != 1 || got[0] != line {
		t.Errorf("WrapText(%q, 40) = %#v, want the line unchanged (wide enough to need no wrapping)", line, got)
	}
}

// TestWrapTextPreservesInternalSpaceRuns guards against strings.Fields also
// collapsing runs of multiple spaces *within* a line (for example
// deliberately aligned table-less text) down to one.
func TestWrapTextPreservesInternalSpaceRuns(t *testing.T) {
	line := "some   spaced   text"
	got := WrapText(line, 40) // wide enough that the whole line fits on one output line
	if len(got) != 1 || got[0] != line {
		t.Errorf("WrapText(%q, 40) = %#v, want the spacing preserved exactly", line, got)
	}
}

// TestWrapTextIndentWiderThanWidthIsDropped guards against an indent that
// is itself >= the target width leaving no room to wrap the rest of the
// line at all.
func TestWrapTextIndentWiderThanWidthIsDropped(t *testing.T) {
	got := WrapText("          x", 5) // 10 spaces of indent, width 5
	if len(got) == 0 {
		t.Fatal("WrapText produced no lines for an over-indented line")
	}
	for _, line := range got {
		if w := SpanWidth([]Span{{Text: line}}); w > 5 {
			t.Errorf("line %q has display width %d, want <= 5", line, w)
		}
	}
}
