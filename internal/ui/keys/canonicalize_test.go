package keys

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestCanonicalizeRewritesCSIuReportsToLegacyKeys(t *testing.T) {
	tests := []struct {
		name string
		ev   *tcell.EventKey
		want tcell.Key
	}{
		{"Ctrl-I", tcell.NewEventKey(tcell.KeyCtrlI, 0, tcell.ModCtrl), tcell.KeyTab},
		{"Ctrl-[", tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModCtrl), tcell.KeyEsc},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Canonicalize(tc.ev); got.Key() != tc.want || got.Modifiers() != tcell.ModNone {
				t.Errorf("Canonicalize() = key %v mods %v, want key %v with no modifiers", got.Key(), got.Modifiers(), tc.want)
			}
		})
	}
}

func TestNormalizeSeesCanonicalizedKeys(t *testing.T) {
	got := Normalize(tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModCtrl))
	if len(got) != 1 || got[0] != (Key{Kind: KindSpecial, Special: tcell.KeyEsc}) {
		t.Errorf("Normalize(Ctrl-[ rune) = %+v, want a single <Esc>", got)
	}
}

func TestParseCtrlBracketIsEsc(t *testing.T) {
	got, err := Parse("<C-[>")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 || got[0] != (Key{Kind: KindSpecial, Special: tcell.KeyEsc}) {
		t.Errorf("Parse(<C-[>) = %+v, want a single <Esc>, the event it can actually match", got)
	}
}

func TestCanonicalizeLeavesOtherEventsUntouched(t *testing.T) {
	tests := []struct {
		name string
		ev   *tcell.EventKey
	}{
		{"plain [", tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModNone)},
		{"Alt-[", tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModAlt)},
		{"Ctrl-Alt-[", tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModCtrl|tcell.ModAlt)},
		{"Ctrl-Shift-[", tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModCtrl|tcell.ModShift)},
		{"Ctrl-C", tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)},
		{"Tab", tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Canonicalize(tc.ev); got != tc.ev {
				t.Errorf("Canonicalize() returned a new event for %s, want the original", tc.name)
			}
		})
	}
}
