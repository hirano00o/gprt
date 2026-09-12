package editor

import "strings"

// startOrCompleteOperator handles a normal-mode 'd', 'c', or 'y' keypress:
// the first press of an operator arms it (capturing any count typed
// before it); a second press of the *same* operator is the doubled,
// linewise form (dd/cc/yy); any other operator pressed while one is
// already pending abandons it and starts fresh — vim itself does not
// define a "dy"/"yd" combination.
func (v *Vim) startOrCompleteOperator(op rune) Action {
	if v.pendingOp == op {
		v.applyLinewiseDeleteOrYankOrChange(op, v.otherLineForCount())
		return Action{Kind: Consumed}
	}
	v.pendingOp = op
	v.pendingOpCount = v.count
	v.count = 0
	return Action{Kind: Consumed}
}

// applyPendingOperator resolves the operator armed by startOrCompleteOperator
// against a characterwise motion (h, l, w, b, e, 0, ^, or $).
func (v *Vim) applyPendingOperator(motionKey rune) {
	op := v.pendingOp
	count := v.effectiveCount()
	text := v.text.Text()
	cursor := v.text.Cursor()

	// vim's "cw acts like ce" exception: only when the cursor starts on
	// a non-blank character; on whitespace, cw behaves like a plain w
	// (deleting the run of blanks up to the next word, same as dw
	// would).
	// It is not a plain "e" either: when the cursor already sits on the
	// last character of a word, "ce" would run on to the next word's end
	// while vim's "cw" stops at the current one (wordEndPosStop).
	cwStop := false
	if op == 'c' && motionKey == 'w' {
		cs := clusters(text)
		i := indexAtOrAfter(cs, cursor)
		if i < len(cs) && wordClassOf(cs[i]) != wordClassSpace {
			cwStop = true
		}
	}

	var start, end int
	switch motionKey {
	case 'h':
		start, end = moveH(text, cursor, count, false, true), cursor
	case 'l':
		start, end = cursor, moveH(text, cursor, count, true, true)
	case 'w':
		if cwStop {
			start, end = cursor, clusterEnd(text, wordEndPosStop(text, cursor, count))
		} else {
			start, end = cursor, wordForward(text, cursor, count)
		}
	case 'b':
		start, end = wordBackward(text, cursor, count), cursor
	case '0':
		start, end = lineStartPos(text, cursor), cursor
	case '^':
		start, end = lineFirstNonBlankPos(text, cursor), cursor
	case 'e':
		start, end = cursor, clusterEnd(text, wordEndPos(text, cursor, count))
	case '$':
		start, end = cursor, clusterEnd(text, dollarPos(text, cursor))
	default:
		v.resetPending()
		return
	}
	v.applyCharOp(op, start, end)
}

// applyCharOp performs op ('d', 'c', or 'y') over the characterwise range
// [start, end), then resets pending state. The affected text always lands
// in the unnamed register, characterwise, matching vim (d and c both
// "cut", y "copies").
func (v *Vim) applyCharOp(op rune, start, end int) {
	text := v.text.Text()
	if start > end {
		start, end = end, start
	}
	seg := text[start:end]
	switch op {
	case 'y':
		v.register, v.registerLinewise = seg, false
		v.text.SetCursor(start)
	case 'd':
		v.register, v.registerLinewise = seg, false
		v.replace(start, end, "")
		v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), min(start, len(v.text.Text()))))
	case 'c':
		v.register, v.registerLinewise = seg, false
		v.enterInsertSession()
		v.replace(start, end, "")
	}
	v.resetPending()
}

// lineContentSpan returns the byte range [start, end) covering every full
// line between posA and posB (in either order), with no adjacent newline
// included on either side.
func lineContentSpan(text string, posA, posB int) (start, end int) {
	if posA > posB {
		posA, posB = posB, posA
	}
	start, _ = lineBounds(text, posA)
	_, end = lineBounds(text, posB)
	return start, end
}

// captureLinewise builds the unnamed register's text for a linewise
// operator spanning posA..posB (always ending in "\n", regardless of
// whether the buffer's own last line does) and the byte range a delete
// should actually remove: the line content plus its own trailing newline,
// or — for the buffer's last line, which has no newline to take — the
// *preceding* newline instead, so deleting it never leaves a dangling
// blank line behind.
func (v *Vim) captureLinewise(posA, posB int) (regText string, delStart, delEnd int) {
	text := v.text.Text()
	start, end := lineContentSpan(text, posA, posB)
	regText = ensureTrailingNewline(text[start:end])
	delStart, delEnd = start, end
	switch {
	case delEnd < len(text):
		delEnd++
	case delStart > 0:
		delStart--
	}
	return regText, delStart, delEnd
}

// applyLinewiseDeleteOrYankOrChange dispatches a linewise operator (used
// by dd/cc/yy, dj/dk/yj/yk, dgg/dG/ygg/yG, and "S") to the appropriate
// specific implementation.
func (v *Vim) applyLinewiseDeleteOrYankOrChange(op rune, otherPos int) {
	if op == 'c' {
		v.applyLinewiseChange(otherPos)
		return
	}
	v.applyLinewiseDeleteOrYank(op, otherPos)
}

// applyLinewiseDeleteOrYank implements dd/dj/dk/dgg/dG and their y
// counterparts.
func (v *Vim) applyLinewiseDeleteOrYank(op rune, otherPos int) {
	regText, delStart, delEnd := v.captureLinewise(v.text.Cursor(), otherPos)
	v.register, v.registerLinewise = regText, true

	if op == 'y' {
		target := v.text.Cursor()
		if otherPos < target {
			target = otherPos
		}
		lineStart, lineEnd := lineBounds(v.text.Text(), target)
		v.text.SetCursor(firstNonBlankPos(v.text.Text(), lineStart, lineEnd))
		v.resetPending()
		return
	}

	v.replace(delStart, delEnd, "")
	newText := v.text.Text()
	pos := min(delStart, len(newText))
	lineStart, lineEnd := lineBounds(newText, pos)
	v.text.SetCursor(firstNonBlankPos(newText, lineStart, lineEnd))
	v.resetPending()
}

// applyLinewiseChange implements cc/S: the affected lines' own content is
// removed (their surrounding newlines are kept, unlike dd) and an insert
// session begins where it was.
func (v *Vim) applyLinewiseChange(otherPos int) {
	text := v.text.Text()
	start, end := lineContentSpan(text, v.text.Cursor(), otherPos)
	v.register, v.registerLinewise = ensureTrailingNewline(text[start:end]), true
	v.enterInsertSession()
	v.replace(start, end, "")
	v.resetPending()
}

// deleteChars implements x (forward, deletes the character(s) under the
// cursor) and X (backward, deletes the character(s) before it).
func (v *Vim) deleteChars(forward bool) {
	text := v.text.Text()
	cursor := v.text.Cursor()
	count := v.effectiveCount()
	var start, end int
	if forward {
		start, end = cursor, moveH(text, cursor, count, true, true)
	} else {
		start, end = moveH(text, cursor, count, false, true), cursor
	}
	if start == end {
		v.resetPending()
		return
	}
	v.register, v.registerLinewise = text[start:end], false
	v.replace(start, end, "")
	v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), min(start, len(v.text.Text()))))
	v.resetPending()
}

// deleteToEndOfLine implements D (equivalent to "d$").
func (v *Vim) deleteToEndOfLine() {
	text := v.text.Text()
	cursor := v.text.Cursor()
	end := clusterEnd(text, dollarPos(text, cursor))
	if end == cursor {
		v.resetPending()
		return
	}
	v.register, v.registerLinewise = text[cursor:end], false
	v.replace(cursor, end, "")
	v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), cursor))
	v.resetPending()
}

// changeToEndOfLine implements C (equivalent to "c$").
func (v *Vim) changeToEndOfLine() {
	text := v.text.Text()
	cursor := v.text.Cursor()
	end := clusterEnd(text, dollarPos(text, cursor))
	v.register, v.registerLinewise = text[cursor:end], false
	v.enterInsertSession()
	v.replace(cursor, end, "")
	v.resetPending()
}

// substitute implements s: delete effectiveCount() characters forward
// (like "x" repeated, but always entering insert regardless of count) and
// begin an insert session in their place.
func (v *Vim) substitute() {
	text := v.text.Text()
	cursor := v.text.Cursor()
	end := moveH(text, cursor, v.effectiveCount(), true, true)
	v.register, v.registerLinewise = text[cursor:end], false
	v.enterInsertSession()
	v.replace(cursor, end, "")
	v.resetPending()
}

// joinLines implements J: the cursor's line and the next are joined with
// a single space (or nothing, if the next line is entirely blank), the
// next line's own leading whitespace is dropped, and the cursor rests at
// the join point. A no-op on the buffer's last line.
func (v *Vim) joinLines() {
	text := v.text.Text()
	cursor := v.text.Cursor()
	lineStart, lineEnd := lineBounds(text, cursor)
	if lineEnd >= len(text) {
		v.resetPending()
		return
	}
	current := text[lineStart:lineEnd]
	nextStart := lineEnd + 1
	_, nextEnd := lineBounds(text, nextStart)
	next := text[nextStart:nextEnd]
	trimmed := strings.TrimLeft(next, " \t")

	// No separating space when there is nothing to separate (the next
	// line is blank) or nothing left to add one to (vim's own rule): the
	// current line is itself empty, or already ends in whitespace of its
	// own.
	joiner := " "
	if trimmed == "" || current == "" || strings.HasSuffix(current, " ") || strings.HasSuffix(current, "\t") {
		joiner = ""
	}
	leadingLen := len(next) - len(trimmed)
	v.replace(lineEnd, nextStart+leadingLen, joiner)
	v.text.SetCursor(lineEnd)
	v.resetPending()
}

// paste implements p (after) and P (before), using the unnamed register
// recorded by the most recent y/d/c, repeated effectiveCount() times.
func (v *Vim) paste(after bool) {
	if v.register == "" {
		v.resetPending()
		return
	}
	payload := strings.Repeat(v.register, v.effectiveCount())
	text := v.text.Text()
	cursor := v.text.Cursor()

	if v.registerLinewise {
		lineStart, lineEnd := lineBounds(text, cursor)
		pos := lineStart
		prefix := ""
		if after {
			pos = lineEnd
			if lineEnd < len(text) {
				pos++
			} else {
				// The buffer's last line has no newline of its own to
				// paste after, so one is supplied here instead — but
				// payload (built from a register that, per
				// ensureTrailingNewline, always itself ends in "\n")
				// would otherwise add one newline too many, leaving the
				// buffer with a trailing blank line it never had before.
				prefix = "\n"
				payload = strings.TrimSuffix(payload, "\n")
			}
		}
		v.replace(pos, pos, prefix+payload)
		newText := v.text.Text()
		insertedAt := min(pos+len(prefix), len(newText))
		ls, le := lineBounds(newText, insertedAt)
		v.text.SetCursor(firstNonBlankPos(newText, ls, le))
		v.resetPending()
		return
	}

	pos := cursor
	if after && len(text) > 0 {
		pos = moveH(text, cursor, 1, true, true)
	}
	v.replace(pos, pos, payload)
	newText := v.text.Text()
	end := pos + len(payload)
	target := end
	if end > pos {
		target = moveH(newText, end, 1, false, false)
	}
	v.text.SetCursor(clampToLineIfAtEnd(newText, target))
	v.resetPending()
}
