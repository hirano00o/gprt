package widget

import (
	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/gdamore/tcell/v2"
)

// DrawSpansOffset draws spans onto screen starting at (x, y) like DrawSpans,
// but first skips offset display cells of content (used by widget.DiffView
// to scroll a diff line's content horizontally with zh/zl while its gutter
// stays fixed). A grapheme cluster can never be split, so one straddling
// the skip boundary (offset cells to skip land in the middle of it) has its
// already-scrolled-past portion dropped and its remaining, still-visible
// cells drawn as plain spaces instead of the cluster's own glyph — the same
// convention a terminal uses when a double-width character is clipped at
// its left edge; the whole cluster is treated as consumed by the skip
// either way, so later clusters are never shifted by more than offset
// cells. Returns the width actually drawn, which is always <= maxWidth.
func DrawSpansOffset(screen tcell.Screen, x, y, maxWidth, offset int, spans []Span) int {
	if maxWidth <= 0 {
		return 0
	}
	if offset < 0 {
		offset = 0
	}

	// visible is exactly the width DrawSpansOffset will draw when nothing
	// is truncated: skipping offset cells (splitting a straddling cluster
	// into a dropped part and a same-width run of visible-half spaces, as
	// above) always removes precisely offset cells of width from the
	// total, never more and never less.
	visible := SpanWidth(spans) - offset
	if visible <= 0 {
		return 0
	}
	truncated := visible > maxWidth
	limit := x + maxWidth
	if truncated {
		limit = x + maxWidth - 1 // reserve the final cell for the ellipsis
	}

	cursor := x
	skipped := 0
	lastStyle := tcell.StyleDefault
spans:
	for _, s := range spans {
		lastStyle = s.Style
		it := graphemes.FromString(s.Text)
		for it.Next() {
			cluster := it.Value()
			width := displaywidth.String(cluster)

			if skipped+width <= offset {
				// Fully within the skipped region: drop it entirely.
				skipped += width
				continue
			}

			if skipped < offset {
				// Straddles the skip boundary: draw its still-visible
				// cells (width - cut of them) as spaces, then treat the
				// whole cluster as consumed by the skip.
				cut := offset - skipped
				skipped = offset
				for range width - cut {
					if cursor >= limit {
						break spans
					}
					screen.SetContent(cursor, y, ' ', nil, s.Style)
					cursor++
				}
				continue
			}

			if width > 0 && cursor+width > limit {
				break spans
			}
			runes := []rune(cluster)
			screen.SetContent(cursor, y, runes[0], runes[1:], s.Style)
			cursor += width
		}
	}
	if truncated {
		// Either the loop broke early because it reached limit, or every
		// cluster fit exactly up to (but not past) it with room to spare
		// (only possible with zero-width clusters); either way, show the
		// ellipsis so truncation remains visible, matching DrawSpans.
		screen.SetContent(cursor, y, ellipsisRune, nil, lastStyle)
		return cursor + 1 - x
	}
	return cursor - x
}
