package editor

import "github.com/hirano00o/gprt/internal/ui/keys"

// handleNormal processes one keystroke in ModeNormal.
func (v *Vim) handleNormal(k keys.Key) Action {
	if isEsc(k) {
		v.resetPending()
		return Action{Kind: Consumed}
	}
	if k.Kind == keys.KindRune && k.Mod == modCtrl {
		if k.Rune == 'r' {
			v.redo()
			v.resetPending()
			return Action{Kind: Consumed}
		}
		v.resetPending()
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
			v.gotoLineOrOperate(v.effectiveCount())
			return Action{Kind: Consumed}
		}
		return v.handleNormal(k)
	}

	// wasVertical is this keystroke's own view of "was the previous
	// keystroke a j/k", captured before it is overwritten below — see
	// verticalMove's doc comment for why this, rather than always
	// resyncing desiredCol on every motion, is what makes a *run* of j/k
	// presses keep trying to reach the same column.
	wasVertical := v.lastWasVertical
	v.lastWasVertical = false

	switch k.Rune {
	case 'h', 'l', 'w', 'b', 'e', '0', '^', '$':
		if v.pendingOp != 0 {
			v.applyPendingOperator(k.Rune)
		} else {
			v.moveCursor(k.Rune)
		}
		return Action{Kind: Consumed}
	case 'j':
		v.verticalMove(true, wasVertical)
		return Action{Kind: Consumed}
	case 'k':
		v.verticalMove(false, wasVertical)
		return Action{Kind: Consumed}
	case 'g':
		v.pendingG = true
		return Action{Kind: Consumed}
	case 'G':
		n := v.count
		if n == 0 {
			n = lastLineNumber(v.text.Text())
		}
		v.gotoLineOrOperate(n)
		return Action{Kind: Consumed}
	case 'd', 'c', 'y':
		return v.startOrCompleteOperator(k.Rune)
	case 'x':
		v.deleteChars(true)
		return Action{Kind: Consumed}
	case 'X':
		v.deleteChars(false)
		return Action{Kind: Consumed}
	case 'D':
		v.deleteToEndOfLine()
		return Action{Kind: Consumed}
	case 'C':
		v.changeToEndOfLine()
		return Action{Kind: ModeChanged}
	case 's':
		v.substitute()
		return Action{Kind: ModeChanged}
	case 'S':
		v.applyLinewiseChange(v.otherLineForCount())
		return Action{Kind: ModeChanged}
	case 'J':
		v.joinLines()
		return Action{Kind: Consumed}
	case 'p':
		v.paste(true)
		return Action{Kind: Consumed}
	case 'P':
		v.paste(false)
		return Action{Kind: Consumed}
	case 'u':
		v.undo()
		v.resetPending()
		return Action{Kind: Consumed}
	case 'i':
		v.startInsert(insertAt)
		return Action{Kind: ModeChanged}
	case 'a':
		v.startInsert(insertAfter)
		return Action{Kind: ModeChanged}
	case 'I':
		v.startInsert(insertLineStart)
		return Action{Kind: ModeChanged}
	case 'A':
		v.startInsert(insertLineEnd)
		return Action{Kind: ModeChanged}
	case 'o':
		v.startInsert(insertOpenBelow)
		return Action{Kind: ModeChanged}
	case 'O':
		v.startInsert(insertOpenAbove)
		return Action{Kind: ModeChanged}
	case 'v':
		v.startVisual(ModeVisual)
		return Action{Kind: ModeChanged}
	case 'V':
		v.startVisual(ModeVisualLine)
		return Action{Kind: ModeChanged}
	case ':':
		v.mode = ModeCommand
		v.cmdline = ""
		v.lastCmdErr = ""
		v.resetPending()
		return Action{Kind: ModeChanged}
	default:
		v.resetPending()
		return Action{Kind: Consumed}
	}
}

// computeMotionTarget resolves a single characterwise motion key (h, l,
// w, b, e, 0, ^, or $) to a byte offset in text, shared by a bare cursor
// move (moveCursor/moveCursorVisual) and an operator's own range
// computation (applyPendingOperator).
func computeMotionTarget(text string, cursor, count int, motionKey rune) int {
	switch motionKey {
	case 'h':
		return moveH(text, cursor, count, false, false)
	case 'l':
		return moveH(text, cursor, count, true, false)
	case 'w':
		return wordForward(text, cursor, count)
	case 'b':
		return wordBackward(text, cursor, count)
	case 'e':
		return wordEndPos(text, cursor, count)
	case '0':
		return lineStartPos(text, cursor)
	case '^':
		return lineFirstNonBlankPos(text, cursor)
	case '$':
		return dollarPos(text, cursor)
	default:
		return cursor
	}
}

// moveCursor executes a bare (non-operator) characterwise motion. It does
// not touch desiredCol itself: verticalMove/verticalMoveVisual instead
// recompute it lazily, from the cursor's actual column, whenever the
// *previous* keystroke was not itself a vertical move (see their own doc
// comments) — equivalent in effect, since every motion other than j/k
// changes the actual column anyway, but without every motion needing to
// remember to update a field only j/k ever reads.
func (v *Vim) moveCursor(motionKey rune) {
	text := v.text.Text()
	target := computeMotionTarget(text, v.text.Cursor(), v.effectiveCount(), motionKey)
	v.text.SetCursor(target)
	v.resetPending()
}

// verticalMove executes j/k, either as a bare cursor move or (with a
// pending operator) as a linewise operator spanning the lines in between.
// wasVertical is whether the *previous* keystroke was itself a j/k: when
// it was not, desiredCol is resynced to the cursor's actual current
// column first, so a fresh run of j/k presses starts from wherever the
// cursor really is rather than some stale column left over from an
// earlier run; once a run is underway, desiredCol is deliberately left
// untouched, so it keeps trying to reach the same column even across
// shorter lines in between.
func (v *Vim) verticalMove(down, wasVertical bool) {
	text := v.text.Text()
	cursor := v.text.Cursor()
	if !wasVertical {
		lineStart, _ := lineBounds(text, cursor)
		v.desiredCol = columnOf(text, lineStart, cursor)
	}
	target := moveV(text, cursor, v.effectiveCount(), v.desiredCol, down)
	v.lastWasVertical = true
	if v.pendingOp != 0 {
		v.applyLinewiseDeleteOrYankOrChange(v.pendingOp, target)
		return
	}
	v.text.SetCursor(target)
	v.resetPending()
}

// lastLineNumber returns the buffer's 1-based number of lines.
func lastLineNumber(text string) int {
	return len(lineOffsets(text))
}

// gotoLineOrOperate implements "gg"/"G": jump to (the first non-blank of)
// line lineNum, or — with a pending operator — apply it linewise from the
// current line to lineNum.
func (v *Vim) gotoLineOrOperate(lineNum int) {
	target := gotoLine(v.text.Text(), lineNum)
	if v.pendingOp != 0 {
		v.applyLinewiseDeleteOrYankOrChange(v.pendingOp, target)
		return
	}
	v.text.SetCursor(target)
	v.resetPending()
}

// otherLineForCount returns the line effectiveCount() total lines away
// (down) from the cursor's own line, for the doubled-operator forms
// (dd/cc/yy) and "S", or the cursor's own line unchanged when the count is
// 1 (or unset).
func (v *Vim) otherLineForCount() int {
	count := v.effectiveCount()
	if count <= 1 {
		return v.text.Cursor()
	}
	return moveV(v.text.Text(), v.text.Cursor(), count-1, 0, true)
}

// insertKind identifies which of vim's six insert-entering motions (i a I
// A o O) startInsert is performing.
type insertKind int

// Known insert-entry kinds.
const (
	insertAt insertKind = iota
	insertAfter
	insertLineStart
	insertLineEnd
	insertOpenBelow
	insertOpenAbove
)

// startInsert begins an insert session at the position kind describes.
func (v *Vim) startInsert(kind insertKind) {
	text := v.text.Text()
	cursor := v.text.Cursor()
	switch kind {
	case insertAfter:
		v.enterInsertSession()
		v.text.SetCursor(moveH(text, cursor, 1, true, true))
	case insertLineStart:
		v.enterInsertSession()
		v.text.SetCursor(lineFirstNonBlankPos(text, cursor))
	case insertLineEnd:
		v.enterInsertSession()
		v.text.SetCursor(clusterEnd(text, dollarPos(text, cursor)))
	case insertOpenBelow:
		_, lineEnd := lineBounds(text, cursor)
		v.enterInsertSession()
		v.replace(lineEnd, lineEnd, "\n")
		v.text.SetCursor(lineEnd + 1)
	case insertOpenAbove:
		lineStart, _ := lineBounds(text, cursor)
		v.enterInsertSession()
		v.replace(lineStart, lineStart, "\n")
		v.text.SetCursor(lineStart)
	default: // insertAt
		v.enterInsertSession()
		v.text.SetCursor(cursor)
	}
	v.resetPending()
}

// clampToLineIfAtEnd moves pos to the start of its line's own last
// cluster when it sits exactly at the line's end (one past the last
// character) — vim never leaves the cursor there after a delete unless
// the line is empty.
func clampToLineIfAtEnd(text string, pos int) int {
	lineStart, lineEnd := lineBounds(text, pos)
	if pos != lineEnd || lineEnd <= lineStart {
		return pos
	}
	cs := clusters(text[lineStart:lineEnd])
	return lineStart + cs[len(cs)-1].start
}
