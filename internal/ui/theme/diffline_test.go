package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"

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

func TestDiffWordBg(t *testing.T) {
	addBG, addOK := DiffWordBg(diff.Add)
	delBG, delOK := DiffWordBg(diff.Del)
	_, ctxOK := DiffWordBg(diff.Context)
	_, addLine, _ := DiffLineStyle(diff.Add, false).Decompose()
	_, delLine, _ := DiffLineStyle(diff.Del, false).Decompose()

	if !addOK || !delOK {
		t.Fatalf("Add/Del must have a word background, got ok=%v/%v", addOK, delOK)
	}
	if ctxOK {
		t.Error("Context has no word emphasis, want ok=false")
	}
	if addBG == addLine || delBG == delLine || addBG == delBG {
		t.Errorf("word backgrounds must differ from the line tints and each other: add %v/%v del %v/%v", addBG, addLine, delBG, delLine)
	}
}

func TestIsDiffWordBg(t *testing.T) {
	addBG, _ := DiffWordBg(diff.Add)
	delBG, _ := DiffWordBg(diff.Del)
	_, lineAdd, _ := DiffLineStyle(diff.Add, false).Decompose()
	_, sel, _ := DiffLineStyle(diff.Context, true).Decompose()
	_, base, _ := Base.Decompose()

	for _, c := range []tcell.Color{addBG, delBG} {
		if !IsDiffWordBg(c) {
			t.Errorf("IsDiffWordBg(%v) = false, want true", c)
		}
	}
	for _, c := range []tcell.Color{lineAdd, sel, base} {
		if IsDiffWordBg(c) {
			t.Errorf("IsDiffWordBg(%v) = true, want false", c)
		}
	}
}
