package keys

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// namedSpecials maps a bracket token (lower-cased, without the surrounding
// angle brackets) to the tcell.Key it names.
var namedSpecials = map[string]tcell.Key{
	"esc":      tcell.KeyEsc,
	"enter":    tcell.KeyEnter,
	"tab":      tcell.KeyTab,
	"s-tab":    tcell.KeyBacktab,
	"bs":       tcell.KeyBackspace,
	"up":       tcell.KeyUp,
	"down":     tcell.KeyDown,
	"left":     tcell.KeyLeft,
	"right":    tcell.KeyRight,
	"pageup":   tcell.KeyPgUp,
	"pagedown": tcell.KeyPgDn,
	"home":     tcell.KeyHome,
	"end":      tcell.KeyEnd,
}

// Parse decodes a vim-notation key sequence, such as "<C-w>o", "gt", or
// "<Esc>", into the Keys it represents. Every bracketed "<...>" token names
// either a Ctrl combination over a single letter ("<C-w>"), the space key
// ("<Space>"), or one of gprt's named special keys (see namedSpecials); any
// character outside brackets is a literal rune key. Parse returns an error
// for an empty sequence or an unrecognized "<...>" token.
func Parse(seq string) ([]Key, error) {
	if seq == "" {
		return nil, fmt.Errorf("keys: empty key sequence")
	}
	var out []Key
	for i := 0; i < len(seq); {
		if seq[i] == '<' {
			end := strings.IndexByte(seq[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("keys: unterminated %q in %q", "<...>", seq)
			}
			token := seq[i+1 : i+end]
			k, err := parseToken(token)
			if err != nil {
				return nil, fmt.Errorf("keys: %q in %q: %w", token, seq, err)
			}
			out = append(out, k)
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(seq[i:])
		out = append(out, Key{Kind: KindRune, Rune: r})
		i += size
	}
	return out, nil
}

// parseToken decodes the content of one "<...>" bracket.
func parseToken(token string) (Key, error) {
	lower := strings.ToLower(token)
	if lower == "space" {
		return Key{Kind: KindRune, Rune: ' '}, nil
	}
	if special, ok := namedSpecials[lower]; ok {
		return Key{Kind: KindSpecial, Special: special}, nil
	}
	if strings.HasPrefix(lower, "c-") {
		rest := token[2:]
		r, size := utf8.DecodeRuneInString(rest)
		if rest == "" || size != len(rest) || r == utf8.RuneError {
			return Key{}, fmt.Errorf("a Ctrl binding names exactly one rune")
		}
		return Key{Kind: KindRune, Rune: toLowerLetter(r), Mod: tcell.ModCtrl}, nil
	}
	return Key{}, fmt.Errorf("unknown key name")
}
