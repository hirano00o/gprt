package theme

import (
	"testing"

	"github.com/hirano00o/gprt/internal/diff"
)

func TestDiffLineStyleBackgroundByKind(t *testing.T) {
	_, baseBG, _ := Base.Decompose()
	_, addBG, _ := DiffLineStyle(diff.Add, false, false).Decompose()
	_, delBG, _ := DiffLineStyle(diff.Del, false, false).Decompose()
	_, ctxBG, _ := DiffLineStyle(diff.Context, false, false).Decompose()

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

func TestDiffLineStyleCursorOverridesKindBackground(t *testing.T) {
	_, cursorBG, _ := Cursor.Decompose()

	for _, kind := range []diff.LineKind{diff.Context, diff.Add, diff.Del} {
		_, bg, _ := DiffLineStyle(kind, true, false).Decompose()
		if bg != cursorBG {
			t.Errorf("DiffLineStyle(%v, cursor=true, false) background = %v, want theme.Cursor's background %v", kind, bg, cursorBG)
		}
	}
}

func TestDiffLineStyleSelectedDiffersFromUnselected(t *testing.T) {
	_, unselected, _ := DiffLineStyle(diff.Context, false, false).Decompose()
	_, selected, _ := DiffLineStyle(diff.Context, false, true).Decompose()
	if unselected == selected {
		t.Error("a selected context row must render with a different background than an unselected one")
	}
}

func TestDiffLineStyleCursorTakesPriorityOverSelected(t *testing.T) {
	_, cursorBG, _ := Cursor.Decompose()
	_, bg, _ := DiffLineStyle(diff.Context, true, true).Decompose()
	if bg != cursorBG {
		t.Errorf("cursor+selected background = %v, want the cursor's background %v to win", bg, cursorBG)
	}
}
