package keys

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestKeyString(t *testing.T) {
	tests := []struct {
		name string
		key  Key
		want string
	}{
		{"lowercase rune", Key{Kind: KindRune, Rune: 'j'}, "j"},
		{"uppercase rune", Key{Kind: KindRune, Rune: 'G'}, "G"},
		{"digit rune", Key{Kind: KindRune, Rune: '3'}, "3"},
		{"space rune", Key{Kind: KindRune, Rune: ' '}, "<Space>"},
		{"ctrl rune lowercase", Key{Kind: KindRune, Rune: 'w', Mod: tcell.ModCtrl}, "<C-w>"},
		{"ctrl rune from uppercase", Key{Kind: KindRune, Rune: 'W', Mod: tcell.ModCtrl}, "<C-w>"},
		{"escape", Key{Kind: KindSpecial, Special: tcell.KeyEsc}, "<Esc>"},
		{"enter", Key{Kind: KindSpecial, Special: tcell.KeyEnter}, "<Enter>"},
		{"tab", Key{Kind: KindSpecial, Special: tcell.KeyTab}, "<Tab>"},
		{"shift tab", Key{Kind: KindSpecial, Special: tcell.KeyBacktab}, "<S-Tab>"},
		{"backspace", Key{Kind: KindSpecial, Special: tcell.KeyBackspace}, "<BS>"},
		{"up", Key{Kind: KindSpecial, Special: tcell.KeyUp}, "<Up>"},
		{"down", Key{Kind: KindSpecial, Special: tcell.KeyDown}, "<Down>"},
		{"left", Key{Kind: KindSpecial, Special: tcell.KeyLeft}, "<Left>"},
		{"right", Key{Kind: KindSpecial, Special: tcell.KeyRight}, "<Right>"},
		{"page up", Key{Kind: KindSpecial, Special: tcell.KeyPgUp}, "<PageUp>"},
		{"page down", Key{Kind: KindSpecial, Special: tcell.KeyPgDn}, "<PageDown>"},
		{"home", Key{Kind: KindSpecial, Special: tcell.KeyHome}, "<Home>"},
		{"end", Key{Kind: KindSpecial, Special: tcell.KeyEnd}, "<End>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.key.String(); got != tc.want {
				t.Errorf("Key.String() = %q, want %q", got, tc.want)
			}
		})
	}
}
