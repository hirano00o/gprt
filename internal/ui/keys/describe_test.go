package keys

import "testing"

// maxDescriptionLen keeps a help row (sequence, action ID, description)
// within a typical 80-column terminal.
const maxDescriptionLen = 40

// TestDescriptionCoversEveryAction fails when an action is added to
// AllActions without a help description, or when a description is too
// long for the help overlay's row.
func TestDescriptionCoversEveryAction(t *testing.T) {
	for _, a := range AllActions() {
		d := a.Description()
		if d == "" {
			t.Errorf("%s has no description", a)
		}
		if len(d) > maxDescriptionLen {
			t.Errorf("%s description %q is %d chars, max %d", a, d, len(d), maxDescriptionLen)
		}
	}
}

// TestDescriptionHasNoOrphans fails when the description map keeps an entry
// for an action that no longer exists.
func TestDescriptionHasNoOrphans(t *testing.T) {
	known := make(map[Action]bool)
	for _, a := range AllActions() {
		known[a] = true
	}
	for a := range descriptions {
		if !known[a] {
			t.Errorf("description for unknown action %q", a)
		}
	}
}

func TestDescriptionUnknownActionIsEmpty(t *testing.T) {
	if got := Action("no.such").Description(); got != "" {
		t.Errorf("Description() = %q, want empty", got)
	}
}
