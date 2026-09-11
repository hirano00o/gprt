// Package keys turns raw tcell key events into gprt's action model: a
// terminal-protocol-independent Key representation (Normalize), vim-notation
// parsing for config-file key sequences (Parse), a keymap of Action IDs per
// Context (Keymap, Defaults, Merge), and a multi-key/count state machine
// (Sequencer) that resolves a stream of Keys into dispatched Actions. The
// package has no dependency on tview or gprt's store; it only imports tcell
// for key/modifier constants.
package keys

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// KeyKind distinguishes a printable rune key from a named special key with
// no printable representation.
type KeyKind int

const (
	// KindRune is a printable character key: letters, digits, punctuation,
	// and space.
	KindRune KeyKind = iota
	// KindSpecial is a named key such as Esc, Enter, Tab, or an arrow key.
	KindSpecial
)

// Key is one normalized keypress. Two physical keypresses that a terminal
// cannot reliably distinguish (for example Ctrl-H and Backspace on a legacy
// terminal) normalize to the same Key, so gprt's keymap only ever has to
// reason about one canonical form per logical key. See Normalize.
type Key struct {
	Kind    KeyKind
	Rune    rune
	Special tcell.Key
	Mod     tcell.ModMask
}

// specialNames maps the special keys gprt binds to their vim-notation name
// (without the surrounding angle brackets).
var specialNames = map[tcell.Key]string{
	tcell.KeyEsc:       "Esc",
	tcell.KeyEnter:     "Enter",
	tcell.KeyTab:       "Tab",
	tcell.KeyBacktab:   "S-Tab",
	tcell.KeyBackspace: "BS",
	tcell.KeyUp:        "Up",
	tcell.KeyDown:      "Down",
	tcell.KeyLeft:      "Left",
	tcell.KeyRight:     "Right",
	tcell.KeyPgUp:      "PageUp",
	tcell.KeyPgDn:      "PageDown",
	tcell.KeyHome:      "Home",
	tcell.KeyEnd:       "End",
}

// String renders k in vim notation, e.g. "j", "<C-w>", "<Esc>". This is the
// canonical textual form used both to display bindings (the "?" help
// overlay) and, internally, as the map key a Keymap and Sequencer compare
// sequences by.
func (k Key) String() string {
	if k.Kind == KindSpecial {
		if name, ok := specialNames[k.Special]; ok {
			return "<" + name + ">"
		}
		// Not one of gprt's bound special keys; render defensively rather
		// than panicking, since String() must never fail (help screens and
		// error messages call it unconditionally).
		return fmt.Sprintf("<Key(%d)>", int(k.Special))
	}
	if k.Mod&tcell.ModCtrl != 0 {
		return fmt.Sprintf("<C-%c>", toLowerLetter(k.Rune))
	}
	if k.Rune == ' ' {
		return "<Space>"
	}
	return string(k.Rune)
}

// toLowerLetter lower-cases an ASCII letter, leaving any other rune
// unchanged; Ctrl notation is always written lowercase regardless of the
// case the key itself carried (<C-w>, never <C-W>).
func toLowerLetter(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
