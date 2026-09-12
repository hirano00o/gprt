package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// handleInsert processes one keystroke in ModeInsert: Esc ends the insert
// session (see leaveInsertSession); every other whitelisted key is
// reported as Forward, since Vim never reimplements TextArea's own rune
// insertion, wrapping-aware cursor movement, or paste handling (see the
// package doc comment); anything else is silently dropped.
func (v *Vim) handleInsert(k keys.Key) Action {
	if isEsc(k) {
		v.leaveInsertSession()
		return Action{Kind: ModeChanged}
	}
	if isInsertWhitelisted(k) {
		return Action{Kind: Forward}
	}
	return Action{Kind: Consumed}
}

// isInsertWhitelisted reports whether k is one of insert mode's forwarded
// keys: any plain (non-Ctrl) rune, Ctrl-w/Ctrl-u, or one of the named
// editing/navigation special keys TextArea itself handles.
func isInsertWhitelisted(k keys.Key) bool {
	if k.Kind == keys.KindRune {
		if k.Mod == modCtrl {
			return k.Rune == 'w' || k.Rune == 'u'
		}
		return true
	}
	switch k.Special {
	case tcell.KeyEnter, tcell.KeyTab, tcell.KeyBackspace, tcell.KeyDelete,
		tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyHome, tcell.KeyEnd:
		return true
	default:
		return false
	}
}
