package theme

import (
	"testing"

	"github.com/hirano00o/gprt/internal/diff"
)

func TestDiffLineStyleBackgroundByKind(t *testing.T) {
	_, baseBG, _ := Base.Decompose()
	_, addBG, _ := DiffLineStyle(diff.Add, false).Decompose()
	_, delBG, _ := DiffLineStyle(diff.Del, false).Decompose()
	_, ctxBG, _ := DiffLineStyle(diff.Context, false).Decompose()

	if ctxBG != baseBG {
		t.Errorf("Context background = %v, want theme.Base's background %v", ctxBG, baseBG)
	}
	if addBG == baseBG {
		t.Error("Add background must differ from the base (context) background")
	}
	if delBG == baseBG {
		t.Error("Del background must differ from the base (context) background")
	}
	if addBG == delBG {
		t.Error("Add and Del backgrounds must differ from each other")
	}
}

func TestDiffLineStyleSelectedDiffersFromUnselected(t *testing.T) {
	_, unselected, _ := DiffLineStyle(diff.Context, false).Decompose()
	_, selected, _ := DiffLineStyle(diff.Context, true).Decompose()
	if unselected == selected {
		t.Error("a selected context row must render with a different background than an unselected one")
	}
}
