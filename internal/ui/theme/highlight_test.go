package theme

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/gdamore/tcell/v2"
)

func TestTokenStyleBackgroundNeverSet(t *testing.T) {
	SetHighlightStyle("github-dark")
	_, wantBG, _ := Base.Decompose()

	for _, tt := range []chroma.TokenType{chroma.Keyword, chroma.NameFunction, chroma.Comment, chroma.LiteralString, chroma.Text} {
		_, bg, _ := TokenStyle(tt).Decompose()
		if bg != wantBG {
			t.Errorf("TokenStyle(%v) background = %v, want theme.Base's background %v (chroma's own background must never leak through)", tt, bg, wantBG)
		}
	}
}

func TestTokenStyleForegroundDiffersByTokenType(t *testing.T) {
	SetHighlightStyle("github-dark")

	kwFG, _, _ := TokenStyle(chroma.Keyword).Decompose()
	nameFG, _, _ := TokenStyle(chroma.NameOther).Decompose()
	if kwFG == nameFG {
		t.Errorf("Keyword and NameOther resolved to the same foreground %v; expected github-dark to distinguish them", kwFG)
	}
}

func TestTokenStyleBoldAndItalicFromStyleDefinition(t *testing.T) {
	SetHighlightStyle("github-dark")

	// github-dark's own XML marks Operator bold and Comment italic.
	_, _, opAttrs := TokenStyle(chroma.Operator).Decompose()
	if opAttrs&tcell.AttrBold == 0 {
		t.Error("TokenStyle(Operator) is not bold, want bold per github-dark's style definition")
	}

	_, _, commentAttrs := TokenStyle(chroma.Comment).Decompose()
	if commentAttrs&tcell.AttrItalic == 0 {
		t.Error("TokenStyle(Comment) is not italic, want italic per github-dark's style definition")
	}
}

func TestTokenStyleFallsBackForUnknownStyleName(t *testing.T) {
	// styles.Get already falls back to a default for an unknown name;
	// SetHighlightStyle must not panic and TokenStyle must still return a
	// usable style with gprt's own background.
	SetHighlightStyle("this-style-does-not-exist")
	_, wantBG, _ := Base.Decompose()
	_, bg, _ := TokenStyle(chroma.Keyword).Decompose()
	if bg != wantBG {
		t.Errorf("TokenStyle background with an unknown style name = %v, want %v", bg, wantBG)
	}

	SetHighlightStyle("github-dark") // restore for other tests
}

// TestSetHighlightStyleEmptyNameMatchesTheDefault guards the agreement
// config.Validate and SetHighlightStyle must keep between them (M2 review
// round 3, item 27/28c): Validate accepts an empty highlight_style without
// checking it against chroma's styles.Names() at all, specifically because
// SetHighlightStyle already treats "" the same as gprt's own default style
// name — if that stopped holding, Validate's own exemption would silently
// let an empty value produce a *different* result than the one Validate's
// own doc comment says it is equivalent to.
func TestSetHighlightStyleEmptyNameMatchesTheDefault(t *testing.T) {
	SetHighlightStyle(defaultHighlightStyleName)
	wantFG, wantBG, wantAttrs := TokenStyle(chroma.Keyword).Decompose()

	SetHighlightStyle("")
	gotFG, gotBG, gotAttrs := TokenStyle(chroma.Keyword).Decompose()

	if gotFG != wantFG || gotBG != wantBG || gotAttrs != wantAttrs {
		t.Errorf("TokenStyle(Keyword) after SetHighlightStyle(\"\") = (%v, %v, %v), want the same as SetHighlightStyle(%q): (%v, %v, %v)",
			gotFG, gotBG, gotAttrs, defaultHighlightStyleName, wantFG, wantBG, wantAttrs)
	}

	SetHighlightStyle("github-dark") // restore for other tests
}

func TestTokenStyleIsCachedPerType(t *testing.T) {
	SetHighlightStyle("github-dark")
	first := TokenStyle(chroma.Keyword)
	second := TokenStyle(chroma.Keyword)
	if first != second {
		t.Errorf("TokenStyle(Keyword) returned different styles on repeated calls: %v vs %v", first, second)
	}
}
