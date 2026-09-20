package editor

import "github.com/hirano00o/gprt/internal/ui/keys"

// startVisual enters mode (ModeVisual or ModeVisualLine) with the anchor
// and active end both at the current cursor, and selects accordingly.
func (v *Vim) startVisual(mode Mode) {
	v.mode = mode
	v.visualAnchor = v.text.Cursor()
	v.visualCursor = v.visualAnchor
	v.resetPending()
	v.updateVisualSelection()
}

// exitVisual returns to normal mode, collapsing the selection to a plain
// cursor at the active end.
func (v *Vim) exitVisual() {
	v.mode = ModeNormal
	v.text.SetCursor(v.visualCursor)
	v.resetPending()
}

// updateVisualSelection recomputes Text's selection from the anchor and
// active end (visualCursor): characterwise in ModeVisual (inclusive of
// the cluster visualCursor rests on), whole lines (including their
// trailing newline, where one exists) in ModeVisualLine.
func (v *Vim) updateVisualSelection() {
	text := v.text.Text()
	a, b := v.visualAnchor, v.visualCursor
	if a > b {
		a, b = b, a
	}
	if v.mode == ModeVisualLine {
		a = lineStartPos(text, a)
		_, lineEnd := lineBounds(text, b)
		b = lineEnd
		if b < len(text) {
			b++
		}
	} else {
		b = clusterEnd(text, b)
	}
	v.text.Select(a, b)
}

// handleVisual processes one keystroke in ModeVisual/ModeVisualLine.
func (v *Vim) handleVisual(k keys.Key) Action {
	if isEsc(k) {
		v.exitVisual()
		return Action{Kind: ModeChanged}
	}
	if k.Kind == keys.KindRune && k.Mod == modCtrl {
		if k.Rune == 'r' {
			// Exit visual mode *before* redoing: redo can shrink or
			// otherwise restructure the buffer arbitrarily, and
			// visualCursor (deliberately never resynced from
			// Text.Cursor() while a selection is active — see its own
			// doc comment) would then be a stale, possibly out-of-range
			// offset into the new text, which the next visual motion's
			// own lineBounds call would index with directly. vim itself
			// has no visual-mode redo binding at all; exiting first (like
			// Esc) and letting redo run in its usual normal-mode context
			// is the closest equivalent, and keeps redo's own cursor
			// placement (already clamped to the new text) authoritative.
			v.exitVisual()
			v.redo()
			return Action{Kind: ModeChanged}
		}
		return Action{Kind: Consumed}
	}
	if k.Kind != keys.KindRune {
		return Action{Kind: Consumed}
	}
	if d, ok := digitValue(k); ok && (d != 0 || v.count > 0) {
		v.count = v.count*10 + d
		return Action{Kind: Consumed}
	}
	if v.pendingG {
		v.pendingG = false
		if k.Rune == 'g' {
			v.visualCursor = gotoLine(v.text.Text(), v.effectiveCount())
			v.updateVisualSelection()
			v.count = 0
			return Action{Kind: Consumed}
		}
		return v.handleVisual(k)
	}

	wasVertical := v.lastWasVertical
	v.lastWasVertical = false

	switch k.Rune {
	case 'h', 'l', 'w', 'b', 'e', '0', '^', '$':
		v.moveCursorVisual(k.Rune)
		return Action{Kind: Consumed}
	case 'j':
		v.verticalMoveVisual(true, wasVertical)
		return Action{Kind: Consumed}
	case 'k':
		v.verticalMoveVisual(false, wasVertical)
		return Action{Kind: Consumed}
	case 'g':
		v.pendingG = true
		return Action{Kind: Consumed}
	case 'G':
		n := v.count
		if n == 0 {
			n = lastLineNumber(v.text.Text())
		}
		v.visualCursor = gotoLine(v.text.Text(), n)
		v.updateVisualSelection()
		v.count = 0
		return Action{Kind: Consumed}
	case 'd', 'x':
		v.applyVisualOp('d')
		return Action{Kind: ModeChanged}
	case 'y':
		v.applyVisualOp('y')
		return Action{Kind: ModeChanged}
	case 'c':
		v.applyVisualOp('c')
		return Action{Kind: ModeChanged}
	case 'v':
		if v.mode == ModeVisual {
			v.exitVisual()
		} else {
			v.mode = ModeVisual
			v.updateVisualSelection()
		}
		return Action{Kind: ModeChanged}
	case 'V':
		if v.mode == ModeVisualLine {
			v.exitVisual()
		} else {
			v.mode = ModeVisualLine
			v.updateVisualSelection()
		}
		return Action{Kind: ModeChanged}
	default:
		v.count = 0
		return Action{Kind: Consumed}
	}
}

// moveCursorVisual executes a characterwise motion in visual mode,
// relative to the active end (visualCursor, never Text.Cursor() — see its
// doc comment), extending the selection.
func (v *Vim) moveCursorVisual(motionKey rune) {
	text := v.text.Text()
	target := computeMotionTarget(text, v.visualCursor, v.effectiveCount(), motionKey)
	v.visualCursor = target
	v.updateVisualSelection()
	v.count = 0
}

// verticalMoveVisual executes j/k in visual mode, extending the
// selection; see normal mode's verticalMove for wasVertical/desiredCol's
// meaning.
func (v *Vim) verticalMoveVisual(down, wasVertical bool) {
	if !wasVertical {
		text := v.text.Text()
		lineStart, _ := lineBounds(text, v.visualCursor)
		v.desiredCol = columnOf(text, lineStart, v.visualCursor)
	}
	target := moveV(v.text.Text(), v.visualCursor, v.effectiveCount(), v.desiredCol, down)
	v.lastWasVertical = true
	v.visualCursor = target
	v.updateVisualSelection()
	v.count = 0
}

// applyVisualOp implements v/V's own d (and its x alias), y, and c: the
// current selection (characterwise or linewise, matching Mode) is cut,
// copied, or replaced, mode returns to normal (or, for c, to insert), and
// pending state resets.
func (v *Vim) applyVisualOp(op rune) {
	text := v.text.Text()
	a, b := v.visualAnchor, v.visualCursor
	if a > b {
		a, b = b, a
	}

	if v.mode == ModeVisualLine {
		v.applyVisualLineOp(op, text, a, b)
	} else {
		v.applyVisualCharOp(op, text, a, clusterEnd(text, b))
	}
	v.resetPending()
}

func (v *Vim) applyVisualLineOp(op rune, text string, a, b int) {
	start, end := lineContentSpan(text, a, b)
	v.register, v.registerLinewise = ensureTrailingNewline(text[start:end]), true

	switch op {
	case 'y':
		v.mode = ModeNormal
		v.text.SetCursor(start)
	case 'd':
		delStart, delEnd := start, end
		switch {
		case delEnd < len(text):
			delEnd++
		case delStart > 0:
			delStart--
		}
		v.mode = ModeNormal
		v.replace(delStart, delEnd, "")
		newText := v.text.Text()
		pos := min(delStart, len(newText))
		ls, le := lineBounds(newText, pos)
		v.text.SetCursor(firstNonBlankPos(newText, ls, le))
	case 'c':
		v.mode = ModeNormal
		v.text.SetCursor(start)
		v.enterInsertSession()
		v.replace(start, end, "")
	}
}

func (v *Vim) applyVisualCharOp(op rune, text string, start, end int) {
	v.register, v.registerLinewise = text[start:end], false

	switch op {
	case 'y':
		v.mode = ModeNormal
		v.text.SetCursor(start)
	case 'd':
		v.mode = ModeNormal
		v.replace(start, end, "")
		v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), min(start, len(v.text.Text()))))
	case 'c':
		v.mode = ModeNormal
		v.text.SetCursor(start)
		v.enterInsertSession()
		v.replace(start, end, "")
	}
}

// ensureTrailingNewline appends "\n" to s unless it already ends with
// one, matching every other linewise register's own normalisation (see
// operator.go's captureLinewise).
func ensureTrailingNewline(s string) string {
	if len(s) == 0 || s[len(s)-1] != '\n' {
		return s + "\n"
	}
	return s
}
