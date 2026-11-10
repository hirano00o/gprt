package keys

import "github.com/gdamore/tcell/v2"

// ctrlPunctuation maps the rune of a CSI-u Ctrl+punctuation report to the key
// tcell reports for the byte a legacy terminal sends instead (xterm's
// convention, which Ghostty follows). The digits and space are the keys that
// send those bytes on a US layout; '@', '^' and '_' are their caret-notation
// names, which other layouts type unshifted and which keys.Key.String
// displays.
var ctrlPunctuation = map[rune]tcell.Key{
	'@': tcell.KeyCtrlSpace, '2': tcell.KeyCtrlSpace, ' ': tcell.KeyCtrlSpace,
	'[': tcell.KeyEsc, '3': tcell.KeyEsc,
	'\\': tcell.KeyCtrlBackslash, '4': tcell.KeyCtrlBackslash,
	']': tcell.KeyCtrlRightSq, '5': tcell.KeyCtrlRightSq,
	'^': tcell.KeyCtrlCarat, '6': tcell.KeyCtrlCarat,
	'_': tcell.KeyCtrlUnderscore, '/': tcell.KeyCtrlUnderscore, '7': tcell.KeyCtrlUnderscore,
	'8': tcell.KeyBackspace,
}

// Canonicalize rewrites a CSI-u key report to the event a legacy terminal
// produces for the same key, so every widget behaves the same on both:
//
//   - Ctrl-H, Ctrl-I and Ctrl-M become Backspace, Tab and Enter.
//   - Ctrl with a digit or punctuation that sends a control byte on a legacy
//     terminal becomes the key tcell reports for that byte (see
//     ctrlPunctuation): Esc and Backspace without modifiers, the KeyCtrl*
//     constants with ModCtrl.
//   - Any other ASCII digit or punctuation with Ctrl, or with Ctrl and Shift,
//     returns nil and the caller must drop the event. A legacy terminal sends
//     nothing for these, except Ctrl-0, Ctrl-1 and Ctrl-9, which it sends as
//     the plain digit; they are dropped too, so these keys never type text.
//
// Any other event is returned unchanged. Normalize alone is not enough: the
// raw event is what reaches tview's widgets, which would type a rune and only
// know the legacy keys. tcell maps only Ctrl+letter to a KeyCtrl* constant,
// so the punctuation keys arrive as runes.
func Canonicalize(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlH:
		return tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone)
	case tcell.KeyCtrlI:
		return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
	case tcell.KeyCtrlM:
		return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	case tcell.KeyRune:
	default:
		return ev
	}
	r, mod := ev.Rune(), ev.Modifiers()
	if mod&^tcell.ModShift != tcell.ModCtrl || r < ' ' || r > '~' || isASCIILetter(r) {
		return ev
	}
	k, ok := ctrlPunctuation[r]
	switch {
	case !ok || mod != tcell.ModCtrl:
		return nil
	case k == tcell.KeyEsc || k == tcell.KeyBackspace:
		return tcell.NewEventKey(k, 0, tcell.ModNone)
	default:
		return tcell.NewEventKey(k, 0, tcell.ModCtrl)
	}
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

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
// no special modifier) passes through unchanged. A key Canonicalize drops
// normalizes to no keys at all.
func Normalize(ev *tcell.EventKey) []Key {
	if ev = Canonicalize(ev); ev == nil {
		return nil
	}
	k := ev.Key()
	mod := ev.Modifiers()

	switch k {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyBackspace}}
	case tcell.KeyTab:
		return []Key{{Kind: KindSpecial, Special: tcell.KeyTab}}
	case tcell.KeyEnter:
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
