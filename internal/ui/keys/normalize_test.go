package keys

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		ev   *tcell.EventKey
		want []Key
	}{
		{
			name: "legacy backspace reports KeyBackspace",
			ev:   tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}},
		},
		{
			name: "legacy backspace2 (DEL) also normalizes to <BS>",
			ev:   tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}},
		},
		{
			name: "CSI-u Ctrl-H reports KeyCtrlH, also normalizes to <BS>",
			ev:   tcell.NewEventKey(tcell.KeyCtrlH, 0, tcell.ModCtrl),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}},
		},
		{
			name: "legacy Tab reports KeyTab",
			ev:   tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyTab}},
		},
		{
			name: "CSI-u Ctrl-I also normalizes to <Tab>",
			ev:   tcell.NewEventKey(tcell.KeyCtrlI, 0, tcell.ModCtrl),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyTab}},
		},
		{
			name: "legacy Enter reports KeyEnter",
			ev:   tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyEnter}},
		},
		{
			name: "CSI-u Ctrl-M also normalizes to <Enter>",
			ev:   tcell.NewEventKey(tcell.KeyCtrlM, 0, tcell.ModCtrl),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyEnter}},
		},
		{
			name: "legacy Esc reports KeyEsc",
			ev:   tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyEsc}},
		},
		{
			name: "CSI-u Ctrl-[ also normalizes to <Esc>",
			ev:   tcell.NewEventKey(tcell.KeyCtrlLeftSq, 0, tcell.ModCtrl),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyEsc}},
		},
		{
			name: "shift-tab reports as Backtab, passed through unchanged",
			ev:   tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModShift),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyBacktab}},
		},
		{
			name: "explicit ctrl letter key normalizes to a Ctrl rune",
			ev:   tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl),
			want: []Key{{Kind: KindRune, Rune: 'w', Mod: tcell.ModCtrl}},
		},
		{
			name: "explicit ctrl letter key from a capital rune stays lowercase",
			ev:   tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl|tcell.ModShift),
			want: []Key{{Kind: KindRune, Rune: 'd', Mod: tcell.ModCtrl}},
		},
		{
			name: "plain lowercase rune",
			ev:   tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
			want: []Key{{Kind: KindRune, Rune: 'j'}},
		},
		{
			name: "uppercase rune stays uppercase with no modifier",
			ev:   tcell.NewEventKey(tcell.KeyRune, 'G', tcell.ModNone),
			want: []Key{{Kind: KindRune, Rune: 'G'}},
		},
		{
			name: "Alt-prefixed rune expands to Esc then the rune",
			ev:   tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt),
			want: []Key{
				{Kind: KindSpecial, Special: tcell.KeyEsc},
				{Kind: KindRune, Rune: 'j'},
			},
		},
		{
			name: "arrow key passes through unchanged",
			ev:   tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone),
			want: []Key{{Kind: KindSpecial, Special: tcell.KeyUp}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.ev)
			if len(got) != len(tc.want) {
				t.Fatalf("Normalize() = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Normalize()[%d] = %#v, want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
