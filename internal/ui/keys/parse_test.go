package keys

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want []Key
	}{
		{"single rune", "j", []Key{{Kind: KindRune, Rune: 'j'}}},
		{"uppercase rune preserved", "G", []Key{{Kind: KindRune, Rune: 'G'}}},
		{"two-rune sequence", "gg", []Key{{Kind: KindRune, Rune: 'g'}, {Kind: KindRune, Rune: 'g'}}},
		{"gt tab switch", "gt", []Key{{Kind: KindRune, Rune: 'g'}, {Kind: KindRune, Rune: 't'}}},
		{"bracket motion", "]c", []Key{{Kind: KindRune, Rune: ']'}, {Kind: KindRune, Rune: 'c'}}},
		{
			"ctrl-w then o",
			"<C-w>o",
			[]Key{
				{Kind: KindRune, Rune: 'w', Mod: tcell.ModCtrl},
				{Kind: KindRune, Rune: 'o'},
			},
		},
		{"ctrl letter uppercase notation", "<C-W>", []Key{{Kind: KindRune, Rune: 'w', Mod: tcell.ModCtrl}}},
		{"esc", "<Esc>", []Key{{Kind: KindSpecial, Special: tcell.KeyEsc}}},
		{"enter", "<Enter>", []Key{{Kind: KindSpecial, Special: tcell.KeyEnter}}},
		{"tab", "<Tab>", []Key{{Kind: KindSpecial, Special: tcell.KeyTab}}},
		{"space", "<Space>", []Key{{Kind: KindRune, Rune: ' '}}},
		{"backspace", "<BS>", []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}}},
		{"shift tab", "<S-Tab>", []Key{{Kind: KindSpecial, Special: tcell.KeyBacktab}}},
		{"up", "<Up>", []Key{{Kind: KindSpecial, Special: tcell.KeyUp}}},
		{"down", "<Down>", []Key{{Kind: KindSpecial, Special: tcell.KeyDown}}},
		{"left", "<Left>", []Key{{Kind: KindSpecial, Special: tcell.KeyLeft}}},
		{"right", "<Right>", []Key{{Kind: KindSpecial, Special: tcell.KeyRight}}},
		{"page up", "<PageUp>", []Key{{Kind: KindSpecial, Special: tcell.KeyPgUp}}},
		{"page down", "<PageDown>", []Key{{Kind: KindSpecial, Special: tcell.KeyPgDn}}},
		{"home", "<Home>", []Key{{Kind: KindSpecial, Special: tcell.KeyHome}}},
		{"end", "<End>", []Key{{Kind: KindSpecial, Special: tcell.KeyEnd}}},
		{
			"mixed literal and bracket",
			"<C-w>l",
			[]Key{
				{Kind: KindRune, Rune: 'w', Mod: tcell.ModCtrl},
				{Kind: KindRune, Rune: 'l'},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.seq)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.seq, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tc.seq, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Parse(%q)[%d] = %#v, want %#v", tc.seq, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		{"empty input", ""},
		{"unknown bracket name", "<Foo>"},
		{"unterminated bracket", "<C-w"},
		{"empty bracket", "<>"},
		{"ctrl with more than one rune", "<C-ab>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.seq); err == nil {
				t.Errorf("Parse(%q) returned nil error, want an error", tc.seq)
			}
		})
	}
}
