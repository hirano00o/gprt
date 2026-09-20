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
