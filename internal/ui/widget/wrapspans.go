package widget

import (
	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/gdamore/tcell/v2"
)

// cell is one grapheme cluster with its display width and the style of the
// span it came from: WrapSpans' unit of work, so a wrap point can fall
// anywhere (even inside a span, or between two differently styled
// fragments of one word) without losing any fragment's style.
type cell struct {
	text  string
	width int
	style tcell.Style
}

// WrapSpans word-wraps spans to fit within width display columns, keeping
// every fragment's own style. It breaks at spaces (never at other
// whitespace), repeats a paragraph's leading indent on every wrapped line,
// preserves runs of spaces inside a line (but never starts a wrapped line
// with one), and hard-breaks, by grapheme cluster, any word wider than
// width. A "\n" inside a span text is a hard paragraph break, and a blank
// one an empty line. Every character is literal text: unlike
// tview.WordWrap, "[" is not a colour or region tag, so "[x]" or
// "[docs](url)" is measured at its drawn width. Adjacent fragments of equal
// style are merged back into one Span per line, and every returned line's
// SpanWidth is <= width (except a lone double-width cluster in a width-1
// line). width <= 0 returns nil; empty input returns one empty line.
func WrapSpans(spans []Span, width int) [][]Span {
	if width <= 0 {
		return nil
	}
	var lines [][]Span
	for _, paragraph := range splitParagraphs(spans) {
		for _, line := range wrapCells(paragraph, width) {
			lines = append(lines, cellsToSpans(line))
		}
	}
	return lines
}

// splitParagraphs flattens spans into grapheme-cluster cells, starting a
// new paragraph at every "\n". There is always at least one paragraph, so
// empty input wraps to one empty line.
func splitParagraphs(spans []Span) [][]cell {
	paragraphs := [][]cell{nil}
	for _, s := range spans {
		for _, c := range spanCells(s) {
			if c.text == "\n" {
				paragraphs = append(paragraphs, nil)
				continue
			}
			last := len(paragraphs) - 1
			paragraphs[last] = append(paragraphs[last], c)
		}
	}
	return paragraphs
}

// spanCells splits s into one cell per grapheme cluster.
func spanCells(s Span) []cell {
	cells, _, _ := appendSpanCells(nil, s, 0, 0)
	return cells
}

// appendSpanCells appends s's cells to dst, where used is the width already
// collected. With budget > 0 it stops, reporting true, as soon as used
// exceeds budget while text remains, so a huge span is not segmented in full.
func appendSpanCells(dst []cell, s Span, used, budget int) (cells []cell, newUsed int, stopped bool) {
	it := graphemes.FromString(s.Text)
	for it.Next() {
		if budget > 0 && used > budget {
			return dst, used, true
		}
		cluster := it.Value()
		c := cell{text: cluster, width: displaywidth.String(cluster), style: s.Style}
		dst = append(dst, c)
		used += c.width
	}
	return dst, used, false
}

// chopSpans splits spans into lines of at most width columns without any
// word-boundary search (never splitting a grapheme cluster), for text such
// as source code whose spacing must be shown as written. Empty input yields
// one empty line.
func chopSpans(spans []Span, width int) [][]Span {
	lines, _ := chopSpansLimit(spans, width, 0)
	return lines
}

// chopSpansLimit is chopSpans keeping at most maxLines lines (no limit when
// maxLines <= 0), reporting whether text was left out. It stops segmenting
// text once maxLines lines are certainly full, even in the middle of a span.
func chopSpansLimit(spans []Span, width, maxLines int) (lines [][]Span, truncated bool) {
	// Every chunk is at most width wide (callers wrap at width >= 2, so no
	// cell is wider than a line), so maxLines chunks hold at most
	// maxLines*width columns: reading more than that guarantees text is left
	// over. 0 when unlimited.
	budget := maxLines * width
	var cells []cell
	used := 0
	for _, s := range spans {
		var stopped bool
		if cells, used, stopped = appendSpanCells(cells, s, used, budget); stopped {
			truncated = true
			break
		}
	}
	if len(cells) == 0 {
		return [][]Span{nil}, false
	}
	chunks := hardBreakCells(cells, width)
	if maxLines > 0 && len(chunks) > maxLines {
		chunks, truncated = chunks[:maxLines], true
	}
	for _, chunk := range chunks {
		lines = append(lines, cellsToSpans(chunk))
	}
	return lines, truncated
}

// wordToken is a word together with the space cell that preceded it in the
// source (nil for the paragraph's first word); the space is re-emitted only
// when the word lands mid-line, and dropped at a wrap point.
type wordToken struct {
	sep   *cell
	cells []cell
	width int
}

// wrapCells wraps one paragraph. Splitting on each single space (rather than
// collapsing whitespace runs, as strings.Fields would) leaves an empty word
// between consecutive spaces, which is how a run of spaces survives.
func wrapCells(paragraph []cell, width int) [][]cell {
	indentLen := 0
	indentWidth := 0
	for indentLen < len(paragraph) && (paragraph[indentLen].text == " " || paragraph[indentLen].text == "\t") {
		indentWidth += paragraph[indentLen].width
		indentLen++
	}
	indent, rest := paragraph[:indentLen], paragraph[indentLen:]
	effectiveWidth := width - indentWidth
	if effectiveWidth <= 0 {
		indent = nil
		effectiveWidth = width
	}
	if len(rest) == 0 {
		return [][]cell{indent}
	}

	var tokens []wordToken
	tok := wordToken{}
	for i := range rest {
		if rest[i].text == " " {
			tokens = append(tokens, tok)
			tok = wordToken{sep: &rest[i]}
			continue
		}
		tok.cells = append(tok.cells, rest[i])
		tok.width += rest[i].width
	}
	tokens = append(tokens, tok)

	var lines [][]cell
	var current []wordToken
	currentWidth := 0
	newLine := func(body []cell) []cell {
		return append(append([]cell(nil), indent...), body...)
	}
	flush := func() {
		var body []cell
		for i, t := range current {
			if i > 0 && t.sep != nil {
				body = append(body, *t.sep)
			}
			body = append(body, t.cells...)
		}
		lines = append(lines, newLine(body))
		current, currentWidth = nil, 0
	}
	for _, t := range tokens {
		if t.width > effectiveWidth {
			if len(current) > 0 {
				flush()
			}
			for _, chunk := range hardBreakCells(t.cells, effectiveWidth) {
				lines = append(lines, newLine(chunk))
			}
			continue
		}
		if t.width == 0 && len(current) == 0 {
			// The rest of a space run right after a hard-broken word:
			// keeping it would start the line with a space.
			continue
		}
		sep := 0
		if len(current) > 0 {
			sep = 1
		}
		if currentWidth+sep+t.width > effectiveWidth {
			if t.width == 0 {
				// The rest of a space run that ends a full line: starting
				// the next line with it would only make a blank or
				// space-prefixed line.
				continue
			}
			flush()
			current, currentWidth = []wordToken{t}, t.width
		} else {
			current = append(current, t)
			currentWidth += sep + t.width
		}
	}
	if len(current) > 0 {
		flush()
	}
	return lines
}

// hardBreakCells is hardBreak over cells: width-wide chunks that never
// split a cluster.
func hardBreakCells(cells []cell, width int) [][]cell {
	var chunks [][]cell
	var chunk []cell
	chunkWidth := 0
	for _, c := range cells {
		if chunkWidth > 0 && chunkWidth+c.width > width {
			chunks = append(chunks, chunk)
			chunk, chunkWidth = nil, 0
		}
		chunk = append(chunk, c)
		chunkWidth += c.width
	}
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}
	return chunks
}

// cellsToSpans merges runs of equally styled adjacent cells into Spans.
func cellsToSpans(cells []cell) []Span {
	var spans []Span
	for _, c := range cells {
		if n := len(spans); n > 0 && spans[n-1].Style == c.style {
			spans[n-1].Text += c.text
			continue
		}
		spans = append(spans, Span{Text: c.text, Style: c.style})
	}
	return spans
}
