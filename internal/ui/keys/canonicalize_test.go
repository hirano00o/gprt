package keys

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestCanonicalizeRewritesCSIuReportsToLegacyKeys(t *testing.T) {
	tests := []struct {
		name string
		ev   *tcell.EventKey
		want tcell.Key
	}{
		{"Ctrl-H", tcell.NewEventKey(tcell.KeyCtrlH, 0, tcell.ModCtrl), tcell.KeyBackspace},
		{"Ctrl-I", tcell.NewEventKey(tcell.KeyCtrlI, 0, tcell.ModCtrl), tcell.KeyTab},
		{"Ctrl-M", tcell.NewEventKey(tcell.KeyCtrlM, 0, tcell.ModCtrl), tcell.KeyEnter},
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

// TestCanonicalizeGivesCtrlPunctuationTheLegacyControlKey: a legacy terminal
// sends a control byte for these keys, which tcell reports as Esc or
// Backspace without modifiers, or as a KeyCtrl* constant with ModCtrl.
func TestCanonicalizeGivesCtrlPunctuationTheLegacyControlKey(t *testing.T) {
	tests := []struct {
		r    rune
		want tcell.Key
		mod  tcell.ModMask
	}{
		{'@', tcell.KeyCtrlSpace, tcell.ModCtrl},
		{'2', tcell.KeyCtrlSpace, tcell.ModCtrl},
		{' ', tcell.KeyCtrlSpace, tcell.ModCtrl},
		{'3', tcell.KeyEsc, tcell.ModNone},
		{'\\', tcell.KeyCtrlBackslash, tcell.ModCtrl},
		{'4', tcell.KeyCtrlBackslash, tcell.ModCtrl},
		{']', tcell.KeyCtrlRightSq, tcell.ModCtrl},
		{'5', tcell.KeyCtrlRightSq, tcell.ModCtrl},
		{'^', tcell.KeyCtrlCarat, tcell.ModCtrl},
		{'6', tcell.KeyCtrlCarat, tcell.ModCtrl},
		{'_', tcell.KeyCtrlUnderscore, tcell.ModCtrl},
		{'/', tcell.KeyCtrlUnderscore, tcell.ModCtrl},
		{'7', tcell.KeyCtrlUnderscore, tcell.ModCtrl},
		{'8', tcell.KeyBackspace, tcell.ModNone},
	}
	for _, tc := range tests {
		t.Run(string(tc.r), func(t *testing.T) {
			got := Canonicalize(tcell.NewEventKey(tcell.KeyRune, tc.r, tcell.ModCtrl))
			if got == nil || got.Key() != tc.want || got.Modifiers() != tc.mod || got.Rune() != 0 {
				t.Errorf("Canonicalize(Ctrl-%c) = %+v, want key %v with mods %v and no rune", tc.r, got, tc.want, tc.mod)
			}
		})
	}
}

// TestCanonicalizeDropsCtrlPunctuationALegacyTerminalCannotSend: these keys
// send nothing on a legacy terminal, so nothing must happen on CSI-u either.
func TestCanonicalizeDropsCtrlPunctuationALegacyTerminalCannotSend(t *testing.T) {
	ctrlShift := tcell.ModCtrl | tcell.ModShift
	tests := []struct {
		r   rune
		mod tcell.ModMask
	}{
		{';', tcell.ModCtrl},
		{',', tcell.ModCtrl},
		{'.', tcell.ModCtrl},
		{'\'', tcell.ModCtrl},
		{'=', tcell.ModCtrl},
		{'`', tcell.ModCtrl},
		{'-', tcell.ModCtrl},
		{'0', tcell.ModCtrl}, // a legacy terminal sends the plain digit
		{'1', tcell.ModCtrl},
		{'9', tcell.ModCtrl},
		{'?', tcell.ModCtrl},
		{'6', ctrlShift},
		{'-', ctrlShift},
		{'2', ctrlShift},
		{']', ctrlShift},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%c mod %d", tc.r, tc.mod), func(t *testing.T) {
			if got := Canonicalize(tcell.NewEventKey(tcell.KeyRune, tc.r, tc.mod)); got != nil {
				t.Errorf("Canonicalize() = %+v, want nil", got)
			}
		})
	}
}

// TestCanonicalizeAppliesTheCtrlRuleUnderAlt: a CSI-u Ctrl-Alt report is
// treated as Alt (an Esc prefix) over the Ctrl key the Ctrl rule gives.
func TestCanonicalizeAppliesTheCtrlRuleUnderAlt(t *testing.T) {
	ctrlAlt := tcell.ModCtrl | tcell.ModAlt
	tests := []struct {
		r    rune
		want tcell.Key
		mod  tcell.ModMask
	}{
		{']', tcell.KeyCtrlRightSq, ctrlAlt},
		{'6', tcell.KeyCtrlCarat, ctrlAlt},
		{' ', tcell.KeyCtrlSpace, ctrlAlt},
		{'[', tcell.KeyEsc, tcell.ModAlt},
		{'3', tcell.KeyEsc, tcell.ModAlt},
		{'8', tcell.KeyBackspace, tcell.ModAlt},
	}
	for _, tc := range tests {
		t.Run(string(tc.r), func(t *testing.T) {
			got := Canonicalize(tcell.NewEventKey(tcell.KeyRune, tc.r, ctrlAlt))
			if got == nil || got.Key() != tc.want || got.Modifiers() != tc.mod || got.Rune() != 0 {
				t.Errorf("Canonicalize(Ctrl-Alt-%c) = %+v, want key %v with mods %v and no rune", tc.r, got, tc.want, tc.mod)
			}
		})
	}
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, ';', ctrlAlt),
		tcell.NewEventKey(tcell.KeyRune, '0', ctrlAlt),
		tcell.NewEventKey(tcell.KeyRune, '6', ctrlAlt|tcell.ModShift),
	} {
		if got := Canonicalize(ev); got != nil {
			t.Errorf("Canonicalize(rune %q mods %v) = %+v, want nil", ev.Rune(), ev.Modifiers(), got)
		}
	}
}

func TestNormalizeExpandsCtrlAltPunctuationToEscThenTheCtrlKey(t *testing.T) {
	want, err := Parse("<Esc><C-]>")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := Normalize(tcell.NewEventKey(tcell.KeyRune, ']', tcell.ModCtrl|tcell.ModAlt))
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Normalize(Ctrl-Alt-]) = %+v, want %+v", got, want)
	}
}

func TestNormalizeDoesNotPrefixEscToOtherCtrlAltSpecialKeys(t *testing.T) {
	got := Normalize(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModCtrl|tcell.ModAlt))
	if len(got) != 1 || got[0].Special != tcell.KeyUp {
		t.Errorf("Normalize(Ctrl-Alt-Up) = %+v, want the single Up key", got)
	}
}

func TestNormalizeCtrlAltEscAndBackspaceKeysAreSingleKeys(t *testing.T) {
	for r, want := range map[rune]tcell.Key{'[': tcell.KeyEsc, '3': tcell.KeyEsc, '8': tcell.KeyBackspace} {
		got := Normalize(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModCtrl|tcell.ModAlt))
		if len(got) != 1 || got[0] != (Key{Kind: KindSpecial, Special: want}) {
			t.Errorf("Normalize(Ctrl-Alt-%c) = %+v, want a single %v", r, got, want)
		}
	}
}

// TestCtrlAltFromLegacyTerminals pins what tcell's legacy parser reports for
// ESC plus a control byte: ESC 0x1D arrives as Ctrl-Alt-'}', which is left
// as it was (Esc then '}'), and ESC ESC 0x1D as KeyCtrlRightSq with
// Ctrl|Alt, which reads as Esc then Ctrl-].
func TestCtrlAltFromLegacyTerminals(t *testing.T) {
	misread := tcell.NewEventKey(tcell.KeyRune, '}', tcell.ModCtrl|tcell.ModAlt)
	if got := Canonicalize(misread); got != misread {
		t.Errorf("Canonicalize(Ctrl-Alt-}) = %+v, want the original event", got)
	}
	if got := Normalize(misread); len(got) != 2 || got[0].Special != tcell.KeyEsc || got[1] != (Key{Kind: KindRune, Rune: '}'}) {
		t.Errorf("Normalize(Ctrl-Alt-}) = %+v, want Esc then '}'", got)
	}

	want, err := Parse("<Esc><C-]>")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := Normalize(tcell.NewEventKey(tcell.KeyCtrlRightSq, 0, tcell.ModCtrl|tcell.ModAlt))
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Normalize(Alt KeyCtrlRightSq) = %+v, want %+v", got, want)
	}
}

func TestNormalizeIgnoresDroppedKeys(t *testing.T) {
	if got := Normalize(tcell.NewEventKey(tcell.KeyRune, ';', tcell.ModCtrl)); len(got) != 0 {
		t.Errorf("Normalize(Ctrl-;) = %+v, want no keys", got)
	}
}

func TestParseCtrlPunctuationMatchesBothTerminals(t *testing.T) {
	for _, seq := range []string{"<C-]>", "<C-5>", "<C-\\>", "<C-4>", "<C-^>", "<C-6>", "<C-_>", "<C-/>", "<C-7>", "<C-@>", "<C-2>", "<C-3>", "<C-8>"} {
		t.Run(seq, func(t *testing.T) {
			got, err := Parse(seq)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			csiu := Normalize(tcell.NewEventKey(tcell.KeyRune, []rune(seq)[3], tcell.ModCtrl))
			if len(got) != 1 || len(csiu) != 1 || got[0] != csiu[0] {
				t.Fatalf("Parse(%s) = %+v, want the CSI-u keypress's %+v", seq, got, csiu)
			}
			legacy := Normalize(tcell.NewEventKey(got[0].Special, 0, got[0].Mod))
			if len(legacy) != 1 || legacy[0] != got[0] {
				t.Errorf("legacy keypress normalizes to %+v, want %+v", legacy, got[0])
			}
			if again, err := Parse(got[0].String()); err != nil || len(again) != 1 || again[0] != got[0] {
				t.Errorf("Parse(%q) = %+v, %v; want the displayed name to parse back", got[0].String(), again, err)
			}
		})
	}
}

func TestParseRejectsCtrlKeysNoTerminalReportsDistinctly(t *testing.T) {
	for _, seq := range []string{"<C-;>", "<C-,>", "<C-0>", "<C-1>", "<C-9>", "<C-=>"} {
		if _, err := Parse(seq); err == nil {
			t.Errorf("Parse(%q) returned nil error, want one", seq)
		}
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
		{"Ctrl-Alt-a", tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModCtrl|tcell.ModAlt)},
		{"Ctrl-Meta-;", tcell.NewEventKey(tcell.KeyRune, ';', tcell.ModCtrl|tcell.ModMeta)},
		{"Ctrl-C", tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)},
		{"Ctrl-Shift-H", tcell.NewEventKey(tcell.KeyRune, 'H', tcell.ModCtrl|tcell.ModShift)},
		{"Ctrl-あ", tcell.NewEventKey(tcell.KeyRune, 'あ', tcell.ModCtrl)},
		{"plain ;", tcell.NewEventKey(tcell.KeyRune, ';', tcell.ModNone)},
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
