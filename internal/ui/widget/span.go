// Package widget holds gprt's custom tview primitives: composable Spans of
// styled text drawn with grapheme-aware widths (never len, which counts
// bytes and misrenders East Asian wide characters and multi-rune emoji),
// and ListView, a scrollable list with a single-row cursor. Widgets never
// handle key input themselves — internal/ui's router calls their movement
// methods directly, since multi-key sequences and remapping live in
// internal/ui/keys.
package widget

import (
	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/gdamore/tcell/v2"
)

// Span is a run of text sharing one style: the basic unit gprt's widgets
// compose a rendered row out of.
type Span struct {
	Text  string
	Style tcell.Style
}

// SpanWidth returns the total display width, in terminal cells, of spans.
func SpanWidth(spans []Span) int {
	total := 0
	for _, s := range spans {
		total += displaywidth.String(s.Text)
	}
	return total
}

// ellipsisRune replaces truncated content; it is exactly one cell wide.
const ellipsisRune = '…'

// DrawSpans draws spans onto screen starting at (x, y), never writing past
// column x+maxWidth-1. When spans are wider than maxWidth, the output is
// truncated and its last visible cell becomes an ellipsis; a wide
// (double-width) grapheme cluster that would not fit alongside the
// ellipsis is never split — drawing stops before it instead, one cell
// short of the truncation point if necessary. Returns the width actually
// drawn, which is always <= maxWidth.
func DrawSpans(screen tcell.Screen, x, y, maxWidth int, spans []Span) int {
	if maxWidth <= 0 {
		return 0
	}
	if SpanWidth(spans) <= maxWidth {
		cursor := x
		for _, s := range spans {
			cursor += drawText(screen, cursor, y, s.Text, s.Style)
		}
		return cursor - x
	}

	cursor := x
	limit := x + maxWidth - 1 // reserve the final cell for the ellipsis
	lastStyle := tcell.StyleDefault
	for _, s := range spans {
		lastStyle = s.Style
		it := graphemes.FromString(s.Text)
		for it.Next() {
			cluster := it.Value()
			width := displaywidth.String(cluster)
			if width > 0 && cursor+width > limit {
				screen.SetContent(cursor, y, ellipsisRune, nil, lastStyle)
				return cursor + 1 - x
			}
			runes := []rune(cluster)
			screen.SetContent(cursor, y, runes[0], runes[1:], s.Style)
			cursor += width
		}
	}
	// Every cluster fit exactly up to (but not past) limit with room to
	// spare (only possible with zero-width clusters); still show the
	// ellipsis so truncation remains visible.
	screen.SetContent(cursor, y, ellipsisRune, nil, lastStyle)
	return cursor + 1 - x
}

// highlightRanges splits spans at the byte boundaries named by ranges (each
// a [start, end) pair, as returned by regexp.FindAllStringIndex against the
// concatenation of every span's own Text, i.e. text) and applies style to
// the pieces that fall inside a range; pieces outside keep their own span's
// original style. ranges must be sorted and non-overlapping (true of
// FindAllStringIndex's own output). Byte offsets are rune-safe here because
// both a span boundary and a regexp match boundary always land on a UTF-8
// rune boundary.
//
// Returns spans unchanged if the spans' concatenated text does not
// reconstruct text exactly: defensive only (a diff line's token spans
// always do reconstruct it), so a caller's assumption drifting never
// mis-highlights a line instead of failing loudly elsewhere.
func highlightRanges(spans []Span, text string, ranges [][]int, style tcell.Style) []Span {
	total := 0
	for _, s := range spans {
		total += len(s.Text)
	}
	if total != len(text) || len(ranges) == 0 {
		return spans
	}

	out := make([]Span, 0, len(spans)+2*len(ranges))
	offset := 0 // byte offset of the current span's start within text
	ri := 0     // index of the range currently being consumed
	for _, s := range spans {
		spanStart := offset
		spanEnd := offset + len(s.Text)
		cut := 0 // bytes of s.Text already emitted into out
		for ri < len(ranges) && ranges[ri][0] < spanEnd {
			hiStart := ranges[ri][0]
			if hiStart < spanStart {
				hiStart = spanStart
			}
			hiEnd := ranges[ri][1]
			if hiEnd > spanEnd {
				hiEnd = spanEnd
			}
			if localStart := hiStart - spanStart; localStart > cut {
				out = append(out, Span{Text: s.Text[cut:localStart], Style: s.Style})
				cut = localStart
			}
			if localEnd := hiEnd - spanStart; localEnd > cut {
				out = append(out, Span{Text: s.Text[cut:localEnd], Style: style})
				cut = localEnd
			}
			if ranges[ri][1] > spanEnd {
				break // the range continues into the next span
			}
			ri++
		}
		if cut < len(s.Text) {
			out = append(out, Span{Text: s.Text[cut:], Style: s.Style})
		}
		offset = spanEnd
	}
	return out
}

// drawText draws s at (x, y), one grapheme cluster at a time, and returns
// the display width consumed.
func drawText(screen tcell.Screen, x, y int, s string, style tcell.Style) int {
	cursor := x
	it := graphemes.FromString(s)
	for it.Next() {
		cluster := it.Value()
		runes := []rune(cluster)
		screen.SetContent(cursor, y, runes[0], runes[1:], style)
		cursor += displaywidth.String(cluster)
	}
	return cursor - x
}
