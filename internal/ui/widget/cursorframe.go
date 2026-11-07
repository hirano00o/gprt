package widget

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/theme"
)

// frameInset returns the x and width of the content area inside the cursor
// frame: one column is reserved on each side for the frame bars so layout
// stays stable as the cursor moves. Widths under 3 are returned unchanged,
// leaving no room for a frame.
func frameInset(x, width int) (int, int) {
	if width < 3 {
		return x, width
	}
	return x + 1, width - 2
}

// drawCursorFrame draws an accent vertical bar, on background bg, in the
// leftmost and rightmost column of the width-wide line at (x, y). Callers
// draw it after the line's content: when width is under 3 (frameInset
// reserved no columns) the bars overwrite content instead, because with
// no background fill they are the only thing marking the cursor row.
func drawCursorFrame(screen tcell.Screen, x, y, width int, bg tcell.Color) {
	if width <= 0 {
		return
	}
	style := theme.Accent.Background(bg)
	screen.SetContent(x, y, '┃', nil, style)
	screen.SetContent(x+width-1, y, '┃', nil, style)
}

// withBackground returns spans with every span's background replaced by
// bg, keeping each span's own foreground and attributes.
func withBackground(spans []Span, bg tcell.Color) []Span {
	out := make([]Span, len(spans))
	for i, s := range spans {
		out[i] = Span{Text: s.Text, Style: s.Style.Background(bg)}
	}
	return out
}
