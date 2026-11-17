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

	// Word-emphasis backgrounds for the changed words of a modified line:
	// clearly brighter than the row tints above, yet dark enough for the
	// token foreground colours to stay readable.
	colorDiffAddWordBg = tcell.NewRGBColor(20, 95, 35)
	colorDiffDelWordBg = tcell.NewRGBColor(120, 30, 35)
)

// DiffLineStyle returns the background style for one diff row: kind picks
// the base tint (Add/Del tinted, Context the plain Base background) and
// selected (a "V" visual-selection row) overlays a distinct highlight. The
// cursor row is not tinted here; widgets mark it with a frame so the kind
// tint stays visible. Callers layer their own foreground spans (TokenStyle,
// gutter numbers, markers) on top with this style's background, per
// gutter/content column.
func DiffLineStyle(kind diff.LineKind, selected bool) tcell.Style {
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
	return s
}

// DiffWordBg returns the emphasis background of a changed word inside a line
// of kind kind. ok is false for Context, which has no emphasis; callers must
// not use the returned colour then.
func DiffWordBg(kind diff.LineKind) (bg tcell.Color, ok bool) {
	switch kind {
	case diff.Add:
		return colorDiffAddWordBg, true
	case diff.Del:
		return colorDiffDelWordBg, true
	case diff.Context:
		// No emphasis on unchanged lines.
	}
	return tcell.ColorDefault, false
}

// IsDiffWordBg reports whether c is one of the word-emphasis backgrounds,
// so a widget redrawing a row background can tell emphasised spans from
// ones that should take the row's own background.
func IsDiffWordBg(c tcell.Color) bool {
	return c == colorDiffAddWordBg || c == colorDiffDelWordBg
}

// DiffFillerBg is the background of a split-layout half that has no line
// (the shorter side of a modified block): subtler than any line tint.
var DiffFillerBg = tcell.NewRGBColor(25, 25, 25)
