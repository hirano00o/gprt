package theme

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/diff"
)

// Diff-row background colours: dark, desaturated tints that stay
// distinguishable from Base without competing with the foreground token
// colours TokenStyle draws on top of them.
var (
	colorDiffAddBg      = tcell.NewRGBColor(0, 40, 0)
	colorDiffDelBg      = tcell.NewRGBColor(50, 0, 0)
	colorDiffSelectedBg = tcell.NewRGBColor(40, 40, 70)
)

// DiffLineStyle returns the background style for one diff row: kind picks
// the base tint (Add/Del tinted, Context the plain Base background);
// selected (a "V" visual-selection row) overlays a distinct highlight; and
// cursor (the row under the cursor) overlays theme.Cursor's background,
// taking priority over selected when both apply — the single row the
// cursor is on should always be the most visually prominent one, even
// inside an active selection. Callers layer their own foreground spans
// (TokenStyle, gutter numbers, markers) on top with this style's
// background, per gutter/content column.
func DiffLineStyle(kind diff.LineKind, cursor, selected bool) tcell.Style {
	s := Base
	switch kind {
	case diff.Add:
		s = s.Background(colorDiffAddBg)
	case diff.Del:
		s = s.Background(colorDiffDelBg)
	case diff.Context:
		// Base's own background already applies.
	}

	if selected {
		s = s.Background(colorDiffSelectedBg)
	}
	if cursor {
		_, bg, _ := Cursor.Decompose()
		s = s.Background(bg)
	}
	return s
}
