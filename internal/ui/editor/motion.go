package editor

// lineBounds returns the byte range [start, end) of the line containing
// pos: start is right after the preceding "\n" (or 0), end is the
// position of the following "\n" (or len(text) on the last line) — end
// never includes the newline itself. Scanning for the single-byte ASCII
// "\n" one byte at a time is safe without decoding runes: 0x0A can never
// appear as a UTF-8 continuation byte.
func lineBounds(text string, pos int) (start, end int) {
	start = pos
	for start > 0 && text[start-1] != '\n' {
		start--
	}
	end = pos
	for end < len(text) && text[end] != '\n' {
		end++
	}
	return start, end
}

// clusterIndexInLine returns the index into cs (a line's own clusters,
// relative to that line's start) whose start equals relPos, or len(cs)
// (a one-past-the-end index, matching the language's own slicing
// convention) when relPos is the line's own end, one past its last
// cluster — every caller's own forward/backward clamping already handles
// that value correctly; returning len(cs)-1 here instead would treat a
// cursor resting one past the last character as if it were already ON
// that character, throwing off any subsequent motion by exactly one
// cluster (see decisis pitfall for the bug this once caused with Esc/X at
// the end of a line).
func clusterIndexInLine(cs []cluster, relPos int) int {
	for i, c := range cs {
		if c.start == relPos {
			return i
		}
	}
	return len(cs)
}

// moveH computes h/l's target position within the current line, never
// crossing a line boundary. forOperator relaxes the forward clamp so an
// operator (dl, d3l, …) can reach the true end of the line — one past the
// last character — rather than stopping on the last character the way a
// bare cursor move does.
func moveH(text string, cursor, count int, forward, forOperator bool) int {
	if count < 1 {
		count = 1
	}
	lineStart, lineEnd := lineBounds(text, cursor)
	cs := clusters(text[lineStart:lineEnd])
	idx := clusterIndexInLine(cs, cursor-lineStart)

	if forward {
		idx += count
		maxIdx := len(cs) - 1
		if forOperator {
			maxIdx = len(cs)
		}
		if idx > maxIdx {
			idx = maxIdx
		}
	} else {
		idx -= count
		if idx < 0 {
			idx = 0
		}
	}

	if len(cs) == 0 || idx >= len(cs) {
		return lineEnd
	}
	return lineStart + cs[idx].start
}

// lineOffsets returns the byte offset each line of text starts at (line 0
// is always offset 0).
func lineOffsets(text string) []int {
	offsets := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// lineIndexAt returns the index into offsets of the line containing pos.
func lineIndexAt(offsets []int, pos int) int {
	idx := 0
	for i, o := range offsets {
		if o <= pos {
			idx = i
		} else {
			break
		}
	}
	return idx
}

// columnOf returns the display-cell width between lineStart and pos, used
// as j/k's "desired column".
func columnOf(text string, lineStart, pos int) int {
	width := 0
	for _, c := range clusters(text[lineStart:pos]) {
		width += c.width
	}
	return width
}

// posAtColumn returns the byte offset within [lineStart, lineEnd) whose
// display column is closest to (without exceeding) col, resting on the
// line's last character rather than past it — matching moveH's non-
// operator clamp, since this is only ever used for the bare cursor
// position j/k leave the cursor at.
func posAtColumn(text string, lineStart, lineEnd, col int) int {
	cs := clusters(text[lineStart:lineEnd])
	if len(cs) == 0 {
		return lineStart
	}
	width := 0
	for _, c := range cs {
		if width+c.width > col {
			return lineStart + c.start
		}
		width += c.width
	}
	return lineStart + cs[len(cs)-1].start
}

// moveV computes j/k's target position, count lines away, landing at
// display column desiredCol (or the target line's last character, if it
// is shorter).
func moveV(text string, cursor, count, desiredCol int, down bool) int {
	if count < 1 {
		count = 1
	}
	offsets := lineOffsets(text)
	idx := lineIndexAt(offsets, cursor)
	if down {
		idx += count
	} else {
		idx -= count
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(offsets) {
		idx = len(offsets) - 1
	}
	lineStart := offsets[idx]
	lineEnd := len(text)
	if idx+1 < len(offsets) {
		lineEnd = offsets[idx+1] - 1
	}
	return posAtColumn(text, lineStart, lineEnd, desiredCol)
}

// firstNonBlankPos returns the position of the first non-space cluster in
// [start, end), or start itself when the range is entirely blank.
func firstNonBlankPos(text string, start, end int) int {
	for _, c := range clusters(text[start:end]) {
		if wordClassOf(c) != wordClassSpace {
			return start + c.start
		}
	}
	return start
}

// lineStartPos is the "0" motion: the start of pos's line.
func lineStartPos(text string, pos int) int {
	start, _ := lineBounds(text, pos)
	return start
}

// lineFirstNonBlankPos is the "^" motion: the first non-blank character of
// pos's line.
func lineFirstNonBlankPos(text string, pos int) int {
	start, end := lineBounds(text, pos)
	return firstNonBlankPos(text, start, end)
}

// dollarPos is the "$" motion's bare-cursor target: the line's last
// character, or its start when the line is empty.
func dollarPos(text string, pos int) int {
	start, end := lineBounds(text, pos)
	cs := clusters(text[start:end])
	if len(cs) == 0 {
		return start
	}
	return start + cs[len(cs)-1].start
}

// clusterEnd returns the byte end of the cluster starting exactly at pos
// (pos == len(text) returns len(text)), used to turn an inclusive
// motion's bare-cursor target (dollarPos, wordEndPos) into an operator's
// exclusive range end.
func clusterEnd(text string, pos int) int {
	if pos >= len(text) {
		return len(text)
	}
	for _, c := range clusters(text[pos:]) {
		return pos + c.end
	}
	return pos
}

// gotoLine is "gg"/"G"'s target: the first non-blank character of line
// lineNum (1-based, clamped to the buffer's actual line range).
func gotoLine(text string, lineNum int) int {
	offsets := lineOffsets(text)
	if lineNum < 1 {
		lineNum = 1
	}
	if lineNum > len(offsets) {
		lineNum = len(offsets)
	}
	start := offsets[lineNum-1]
	end := len(text)
	if lineNum < len(offsets) {
		end = offsets[lineNum] - 1
	}
	return firstNonBlankPos(text, start, end)
}

// wordForward is the "w" motion: the start of the next word, count times.
// Exclusive: an operator combined with it never includes the character
// landed on.
func wordForward(text string, cursor, count int) int {
	if count < 1 {
		count = 1
	}
	cs := clusters(text)
	i := indexAtOrAfter(cs, cursor)
	for n := 0; n < count; n++ {
		i = wordForwardOnce(cs, i)
	}
	if i >= len(cs) {
		return len(text)
	}
	return cs[i].start
}

func wordForwardOnce(cs []cluster, i int) int {
	if i >= len(cs) {
		return len(cs)
	}
	startClass := wordClassOf(cs[i])
	if startClass != wordClassSpace {
		i++
		if startClass != wordClassWide {
			for i < len(cs) && wordClassOf(cs[i]) == startClass {
				i++
			}
		}
	}
	for i < len(cs) && wordClassOf(cs[i]) == wordClassSpace {
		i++
	}
	return i
}

// wordBackward is the "b" motion: the start of the previous word, count
// times. Exclusive.
func wordBackward(text string, cursor, count int) int {
	if count < 1 {
		count = 1
	}
	cs := clusters(text)
	i := indexAtOrAfter(cs, cursor)
	for n := 0; n < count; n++ {
		i = wordBackwardOnce(cs, i)
	}
	if i >= len(cs) {
		return len(text)
	}
	return cs[i].start
}

func wordBackwardOnce(cs []cluster, i int) int {
	if i <= 0 {
		return 0
	}
	i--
	for i > 0 && wordClassOf(cs[i]) == wordClassSpace {
		i--
	}
	cls := wordClassOf(cs[i])
	if cls != wordClassWide {
		for i > 0 && wordClassOf(cs[i-1]) == cls {
			i--
		}
	}
	return i
}

// wordEndPos is the "e" motion's bare-cursor target: the last character of
// the current or next word, count times. Inclusive.
func wordEndPos(text string, cursor, count int) int {
	if count < 1 {
		count = 1
	}
	cs := clusters(text)
	i := indexAtOrAfter(cs, cursor)
	for n := 0; n < count; n++ {
		i = wordEndOnce(cs, i)
	}
	if i >= len(cs) {
		if len(cs) == 0 {
			return 0
		}
		return cs[len(cs)-1].start
	}
	return cs[i].start
}

func wordEndOnce(cs []cluster, i int) int {
	if len(cs) == 0 {
		return 0
	}
	if i+1 >= len(cs) {
		return len(cs) - 1
	}
	i++
	for i < len(cs) && wordClassOf(cs[i]) == wordClassSpace {
		i++
	}
	if i >= len(cs) {
		return len(cs) - 1
	}
	cls := wordClassOf(cs[i])
	if cls != wordClassWide {
		for i+1 < len(cs) && wordClassOf(cs[i+1]) == cls {
			i++
		}
	}
	return i
}

// atWordEnd reports whether cs[i] is the last cluster of its word: a wide
// (CJK) cluster is a word by itself, whitespace never ends a word, and any
// other cluster ends its word when the next cluster has a different class
// or the text ends.
func atWordEnd(cs []cluster, i int) bool {
	cls := wordClassOf(cs[i])
	switch cls {
	case wordClassSpace:
		return false
	case wordClassWide:
		return true
	default:
		return i+1 >= len(cs) || wordClassOf(cs[i+1]) != cls
	}
}

// wordEndPosStop is wordEndPos with vim's "cw" rule: when the cursor is
// already on the last character of a word, the first count stops there
// instead of advancing to the next word's end (vim's end_word with
// stop=TRUE), so "cw" on that character changes only the current word.
func wordEndPosStop(text string, cursor, count int) int {
	if count < 1 {
		count = 1
	}
	cs := clusters(text)
	if len(cs) == 0 {
		return 0
	}
	i := indexAtOrAfter(cs, cursor)
	if i >= len(cs) {
		return cs[len(cs)-1].start
	}
	if !atWordEnd(cs, i) {
		i = wordEndOnce(cs, i)
	}
	for n := 1; n < count; n++ {
		i = wordEndOnce(cs, i)
	}
	if i >= len(cs) {
		return cs[len(cs)-1].start
	}
	return cs[i].start
}
