package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// handleCommand processes one keystroke in ModeCommand (the "layer's own
// ":" line, distinct from gprt's global command line): Esc cancels back
// to normal, Enter submits (see submitCommand), Backspace erases the last
// typed character (or, at an empty line, cancels — matching vim), and any
// plain rune is appended.
func (v *Vim) handleCommand(k keys.Key) Action {
	if isEsc(k) {
		v.mode = ModeNormal
		v.cmdline = ""
		return Action{Kind: ModeChanged}
	}
	if k.Kind == keys.KindSpecial {
		switch k.Special {
		case tcell.KeyEnter:
			return v.submitCommand()
		case tcell.KeyBackspace:
			if v.cmdline == "" {
				v.mode = ModeNormal
				return Action{Kind: ModeChanged}
			}
			v.cmdline = v.cmdline[:len(v.cmdline)-1]
			return Action{Kind: Consumed}
		}
		return Action{Kind: Consumed}
	}
	if k.Mod == modCtrl {
		return Action{Kind: Consumed}
	}
	v.cmdline += string(k.Rune)
	return Action{Kind: Consumed}
}

// submitCommand parses and clears the accumulated command line, always
// returning to normal mode.
func (v *Vim) submitCommand() Action {
	cmd := v.cmdline
	v.cmdline = ""
	v.mode = ModeNormal

	switch cmd {
	case "w", "wq":
		v.lastCmdErr = ""
		return Action{Kind: SendRequested, Command: cmd}
	case "q":
		v.lastCmdErr = ""
		return Action{Kind: CloseRequested, Command: cmd}
	case "q!":
		v.lastCmdErr = ""
		return Action{Kind: DiscardRequested, Command: cmd}
	case "e":
		v.lastCmdErr = ""
		return Action{Kind: ExternalEditRequested, Command: cmd}
	default:
		v.lastCmdErr = "unknown command: " + cmd
		return Action{Kind: Consumed, Command: cmd}
	}
}
