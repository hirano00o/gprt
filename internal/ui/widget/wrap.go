package widget

import (
	"strings"

	"github.com/rivo/uniseg"
)

// WrapText word-wraps text to fit within width display columns, breaking
// at whitespace and hard-breaking (splitting mid-word, by grapheme
// cluster) any single word wider than width on its own. An existing "\n"
// in text is always a hard paragraph break — including a blank line,
// which becomes an empty output line, matching tview.WordWrap's own
// behaviour there.
//
// Unlike tview.WordWrap, every character is treated as literal text:
// tview.WordWrap parses "[" as the start of a colour/region tag (so, for
// example, "[x]" or "[docs]" in a Markdown checkbox or link is measured as
// zero-width, letting the line run over its intended width once actually
// drawn), which is wrong for gprt's own spans — they are built directly as
// widget.Span values and never go through tview's tag syntax at all. width
// <= 0 returns nil, since there is no meaningful way to wrap into zero or
// negative columns.
func WrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		lines = append(lines, wrapParagraph(paragraph, width)...)
	}
	return lines
}

// wrapParagraph wraps a single line (no "\n") of text, greedily packing
// space-separated tokens onto each output line, prefixed with the line's
// own leading whitespace (its "indent") on every output line — so a nested
// Markdown list item or a 4-space indented code line keeps its indentation
// on every line it wraps to, not just its first.
//
// Splitting on the literal " " (rather than strings.Fields, which also
// matches tabs and — critically — collapses any run of consecutive
// whitespace into a single split, discarding it entirely) is what lets a
// run of more than one space survive: strings.Split and strings.Join are
// exact inverses for a single-character separator, so as long as every
// token Split produces (including the empty strings a multi-space run
// produces between two single-space splits) is preserved and rejoined with
// " ", the original spacing comes back unchanged whenever no wrap point
// happens to fall inside it.
func wrapParagraph(text string, width int) []string {
	indent, rest := splitIndent(text)
	indentWidth := uniseg.StringWidth(indent)
	effectiveWidth := width - indentWidth
	if effectiveWidth <= 0 {
		// The indent alone already consumes the whole target width (or
		// more): dropping it is the only way to leave any room at all to
		// wrap the rest of the line.
		indent = ""
		effectiveWidth = width
	}

	tokens := strings.Split(rest, " ")
	if len(tokens) == 1 && tokens[0] == "" {
		return []string{indent}
	}

	var lines []string
	var current []string
	currentWidth := 0
	flush := func() {
		lines = append(lines, indent+strings.Join(current, " "))
		current, currentWidth = nil, 0
	}
	for _, tok := range tokens {
		tokWidth := uniseg.StringWidth(tok)
		if tokWidth > effectiveWidth {
			if len(current) > 0 {
				flush()
			}
			for _, chunk := range hardBreak(tok, effectiveWidth) {
				lines = append(lines, indent+chunk)
			}
			continue
		}

		sep := 0
		if len(current) > 0 {
			sep = 1 // the space that will re-join it to the previous token
		}
		if currentWidth+sep+tokWidth > effectiveWidth {
			flush()
			current, currentWidth = []string{tok}, tokWidth
		} else {
			current = append(current, tok)
			currentWidth += sep + tokWidth
		}
	}
	if len(current) > 0 {
		flush()
	}
	return lines
}

// splitIndent splits line into its leading run of spaces/tabs ("indent")
// and everything after it ("rest").
func splitIndent(line string) (indent, rest string) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i], line[i:]
}

// hardBreak splits a single word wider than width into width-wide chunks,
// one grapheme cluster at a time, so a wide (double-width) cluster is
// never itself split across two lines.
func hardBreak(word string, width int) []string {
	var lines []string
	var b strings.Builder
	lineWidth := 0
	g := uniseg.NewGraphemes(word)
	for g.Next() {
		cw := g.Width()
		if lineWidth > 0 && lineWidth+cw > width {
			lines = append(lines, b.String())
			b.Reset()
			lineWidth = 0
		}
		b.WriteString(string(g.Runes()))
		lineWidth += cw
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}
