package keys

import "github.com/gdamore/tcell/v2"

// Normalize converts a raw tcell key event into the canonical Key(s) it
// represents, collapsing terminal-protocol differences that would otherwise
// make the same physical keypress look different to the router:
//
//   - KeyCtrlH, KeyBackspace, and KeyBackspace2 (legacy vs. CSI-u reporting
//     of Backspace/Ctrl-H, which are indistinguishable on a legacy terminal)
//     all normalize to a single <BS> special key.
//   - KeyCtrlI and KeyTab both normalize to <Tab>.
//   - KeyCtrlM and KeyEnter both normalize to <Enter>.
//   - KeyCtrlLeftSq and KeyEsc both normalize to <Esc>.
//   - Any other KeyCtrl* constant (Ctrl-A .. Ctrl-Z) becomes a plain rune
//     key with ModCtrl set, so "<C-w>" has one representation regardless of
//     which constant the terminal happened to report.
//   - A rune reported with ModAlt (the legacy Esc-prefix convention many
//     terminals use for Alt) expands into two keys: <Esc> followed by the
//     rune, since that is what a legacy terminal actually sent and is
//     indistinguishable from the user pressing Esc and then the rune key
//     separately.
//
// Every other key (arrows, function keys, page/home/end, a plain rune with
// no special modifier) passes through unchanged.
func Normalize(ev *tcell.EventKey) []Key {
	k := ev.Key()
	mod := ev.Modifiers()

	switch k {
	case tcell.KeyCtrlH, tcell.KeyBackspace, tcell.KeyBackspace2:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}}
	case tcell.KeyCtrlI, tcell.KeyTab:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyTab}}
	case tcell.KeyCtrlM, tcell.KeyEnter:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyEnter}}
	case tcell.KeyCtrlLeftSq, tcell.KeyEsc:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyEsc}}
	case tcell.KeyRune:
		r := ev.Rune()
		if mod&tcell.ModAlt != 0 {
			return []Key{
				{Kind: KindSpecial, Special: tcell.KeyEsc},
				{Kind: KindRune, Rune: r},
			}
		}
		return []Key{{Kind: KindRune, Rune: r, Mod: mod & tcell.ModCtrl}}
	default:
		if k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
			letter := rune('a' + (k - tcell.KeyCtrlA))
			return []Key{{Kind: KindRune, Rune: letter, Mod: tcell.ModCtrl}}
		}
		return []Key{{Kind: KindSpecial, Special: k, Mod: mod}}
	}
}
