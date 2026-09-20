package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestNamedStylesAreDistinct(t *testing.T) {
	named := map[string]tcell.Style{
		"Base":    Base,
		"Header":  Header,
		"Cursor":  Cursor,
		"Stale":   Stale,
		"Muted":   Muted,
		"Accent":  Accent,
		"Error":   Error,
		"Success": Success,
		"Warning": Warning,
		"Pending": Pending,
	}
	seen := map[tcell.Style]string{}
	for name, style := range named {
		if other, ok := seen[style]; ok {
			t.Errorf("style %q is identical to style %q; named styles should be visually distinguishable", name, other)
		}
		seen[style] = name
	}
}

func TestApplySetsTviewStyles(t *testing.T) {
	// Reset to tview's own zero-value default first so this test does not
	// depend on whether an earlier test already called Apply.
	tview.Styles = tview.Theme{}

	Apply()

	zero := tview.Theme{}
	if tview.Styles.PrimitiveBackgroundColor == zero.PrimitiveBackgroundColor {
		t.Errorf("Apply() did not set PrimitiveBackgroundColor")
	}
	if tview.Styles.PrimaryTextColor == zero.PrimaryTextColor {
		t.Errorf("Apply() did not set PrimaryTextColor")
	}
	if tview.Styles.BorderColor == zero.BorderColor {
		t.Errorf("Apply() did not set BorderColor")
	}
}
