package editor

// replace performs one buffer mutation and records it as a single undo
// entry, clearing the redo stack (a fresh edit invalidates whatever redo
// history existed) — every normal/visual-mode command that is not part of
// an insert session goes through this, so "one vim command, one Replace,
// one undo entry" (see the package doc comment) holds without each
// command having to remember to record it itself.
//
// While an insert session is active (see enterInsertSession), the
// mutation is still applied to text, but is deliberately NOT recorded:
// the whole session collapses into one entry pushed by leaveInsertSession
// instead, so `u` undoes an entire "i...<Esc>" (or "cc...<Esc>", …) at
// once, matching vim.
func (v *Vim) replace(start, end int, s string) {
	if v.insertActive {
		v.text.Replace(start, end, s)
		return
	}
	old := v.text.Text()[start:end]
	v.text.Replace(start, end, s)
	v.pushUndo(undoEntry{start: start, old: old, new: s})
}

func (v *Vim) pushUndo(e undoEntry) {
	if e.old == e.new {
		// A no-op edit (for example an insert session that made no net
		// change, or an operator whose motion resolved to an empty
		// range) has nothing for `u` to usefully undo, and would only
		// waste an undo slot.
		return
	}
	v.undoStack = append(v.undoStack, e)
	v.redoStack = nil
}

// undo reverts the most recent undo entry (":u"/"u"), if any.
func (v *Vim) undo() {
	if len(v.undoStack) == 0 {
		return
	}
	e := v.undoStack[len(v.undoStack)-1]
	v.undoStack = v.undoStack[:len(v.undoStack)-1]
	v.text.Replace(e.start, e.start+len(e.new), e.old)
	v.redoStack = append(v.redoStack, e)
	// clampToLineIfAtEnd: e.start alone can rest exactly at the reverted
	// line's own end (one past its last character) — a position normal
	// mode never otherwise leaves the cursor at.
	v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), e.start))
}

// redo re-applies the most recently undone entry ("Ctrl-r"), if any.
func (v *Vim) redo() {
	if len(v.redoStack) == 0 {
		return
	}
	e := v.redoStack[len(v.redoStack)-1]
	v.redoStack = v.redoStack[:len(v.redoStack)-1]
	v.text.Replace(e.start, e.start+len(e.old), e.new)
	v.undoStack = append(v.undoStack, e)
	// See undo's identical clampToLineIfAtEnd call above.
	v.text.SetCursor(clampToLineIfAtEnd(v.text.Text(), e.start+len(e.new)))
}

// enterInsertSession snapshots the buffer before an insert-entering
// command (i a I A o O cc cw c$ C s) performs any mutation of its own, and
// switches Vim into insert mode. Every mutation performed from here until
// leaveInsertSession bypasses replace's own undo recording (see replace).
func (v *Vim) enterInsertSession() {
	v.insertSnapshot = v.text.Text()
	v.insertActive = true
	v.mode = ModeInsert
}

// leaveInsertSession ends the current insert session (Esc from insert
// mode): it diffs the buffer's current content against the snapshot taken
// when the session began, pushes the whole session as one undo entry (a
// no-op session, e.g. "i<Esc>" with nothing typed, pushes nothing — see
// pushUndo), moves the cursor left one grapheme cluster within the
// current line like vim, and returns to normal mode.
func (v *Vim) leaveInsertSession() {
	before := v.insertSnapshot
	after := v.text.Text()
	v.insertActive = false
	v.insertSnapshot = ""
	v.mode = ModeNormal

	start, oldMid, newMid := diffSpan(before, after)
	v.pushUndo(undoEntry{start: start, old: oldMid, new: newMid})

	v.moveCursorLeftAfterInsert()
}

// moveCursorLeftAfterInsert implements vim's "leaving insert mode moves
// the cursor left one column" rule, never crossing onto the previous
// line.
func (v *Vim) moveCursorLeftAfterInsert() {
	text := v.text.Text()
	cursor := v.text.Cursor()
	lineStart, _ := lineBounds(text, cursor)
	if cursor <= lineStart {
		return
	}
	v.text.SetCursor(moveH(text, cursor, 1, false, false))
}

// SetTextFromOutside replaces the whole buffer with text (a restored
// draft, or the result of ":e"'s external editor), recorded as one undo
// entry via the same common-prefix/suffix diff leaveInsertSession uses, so
// `u` returns to whatever the buffer held immediately before.
func (v *Vim) SetTextFromOutside(text string) {
	if v.insertActive {
		// The buffer is being replaced from outside, so the insert
		// session's own snapshot no longer describes it; closing the
		// session here keeps the next Esc from pushing a second,
		// overlapping undo entry for the same change.
		v.leaveInsertSession()
	}
	before := v.text.Text()
	start, oldMid, newMid := diffSpan(before, text)
	if oldMid != newMid {
		v.text.Replace(start, start+len(oldMid), newMid)
		v.pushUndo(undoEntry{start: start, old: oldMid, new: newMid})
	}
	v.text.SetCursor(0)
}

// diffSpan computes the minimal replacement that turns before into after,
// snapped to grapheme-cluster boundaries in both strings (Text.Replace
// requires boundary-aligned offsets): the byte offset of the first
// differing cluster, and the differing middle portion of before and of
// after. Returns (len(before), "", "") when before == after.
func diffSpan(before, after string) (start int, oldMid, newMid string) {
	prefix := snapBoundary(before, commonPrefixLen(before, after))

	maxSuffix := len(before) - prefix
	if remain := len(after) - prefix; remain < maxSuffix {
		maxSuffix = remain
	}
	rawSuffix := commonSuffixLen(before, after)
	if rawSuffix > maxSuffix {
		rawSuffix = maxSuffix
	}

	// Round each string's own cut point down to its own nearest
	// boundary. Both candidate cut points fall strictly within the
	// common-suffix region (identical bytes in before and after), so
	// widening the reported middle by rounding down never drops an
	// actual difference — it only occasionally keeps one extra,
	// unchanged cluster inside oldMid/newMid.
	beforeSuffix := len(before) - snapBoundary(before, len(before)-rawSuffix)
	afterSuffix := len(after) - snapBoundary(after, len(after)-rawSuffix)
	suffix := beforeSuffix
	if afterSuffix < suffix {
		suffix = afterSuffix
	}

	return prefix, before[prefix : len(before)-suffix], after[prefix : len(after)-suffix]
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}
