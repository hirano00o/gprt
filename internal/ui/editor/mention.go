package editor

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

// mentionPattern matches an "@" mention trigger and everything typed
// after it, anchored to the end of the string (the text immediately
// before the cursor) so it only ever reports the *closest* preceding "@".
var mentionPattern = regexp.MustCompile(`@[\w-]*$`)

// OnTextChanged inspects the buffer for a "@[\w-]*" mention trigger
// immediately before the cursor, at a word boundary — the start of the
// buffer, or a character that is not itself part of a word (so
// "foo@bar" never triggers mid-word, only a leading "@bar"). It has no
// side effects on the buffer or undo history, and is meaningful only in
// ModeInsert (any other mode clears mentionActive and reports Consumed).
//
// This is a separate method from Handle, not a Kind Handle itself
// returns for a keystroke, because most insert-mode keys are reported as
// Forward (see the package doc comment): Vim returns before the
// underlying TextArea has actually applied a Forwarded key, so it cannot
// evaluate "the text after this keystroke" within that same call. The
// production Editor primitive instead calls OnTextChanged from
// TextArea's own "changed" callback, after every edit — forwarded or
// not — actually lands; pure tests call it directly against a fake Text
// positioned as if a keystroke had just landed.
func (v *Vim) OnTextChanged() Action {
	if v.mode != ModeInsert {
		v.mentionActive = false
		return Action{Kind: Consumed}
	}
	text := v.text.Text()
	before := text[:v.text.Cursor()]
	loc := mentionPattern.FindStringIndex(before)
	if loc == nil || !atWordBoundary(before, loc[0]) {
		v.mentionActive = false
		return Action{Kind: Consumed}
	}
	v.mentionActive = true
	v.mentionStart = loc[0]
	return Action{Kind: MentionQuery, Query: before[loc[0]+1:]}
}

// atWordBoundary reports that the "@" found at byte index i in s is not
// itself preceded by a word character.
func atWordBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// AcceptMention replaces the most recently detected "@prefix" (see
// OnTextChanged) with "@login " via one Replace — a single undo entry. A
// no-op if OnTextChanged has not most recently reported a match, or if
// the cursor has since moved such that [mentionStart, cursor) is no
// longer itself a bare "@[\w-]*" span: a cursor-only key (Left/Right/
// Home/End, forwarded straight to TextArea) neither goes through
// Vim.Handle nor triggers TextArea's own "changed" callback, so
// OnTextChanged never gets a chance to notice the cursor has left the
// span before AcceptMention would otherwise act on stale coordinates
// (the production Editor also closes the popup on such a move, as a
// first line of defense — this is the second).
func (v *Vim) AcceptMention(login string) {
	if !v.mentionActive {
		return
	}
	v.mentionActive = false

	cursor := v.text.Cursor()
	text := v.text.Text()
	if cursor < v.mentionStart || cursor > len(text) {
		return
	}
	if !isMentionSpan(text[v.mentionStart:cursor]) {
		return
	}
	v.replace(v.mentionStart, cursor, "@"+login+" ")
}

// isMentionSpan reports whether s is exactly "@" followed by zero or more
// word characters or hyphens — the same shape mentionPattern matches, but
// checked against the whole of s rather than a suffix of some larger
// string.
func isMentionSpan(s string) bool {
	if len(s) == 0 || s[0] != '@' {
		return false
	}
	for _, r := range s[1:] {
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
