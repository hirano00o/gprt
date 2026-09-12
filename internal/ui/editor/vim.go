// Package editor implements gprt's vim-style modal text editing: a pure
// state machine (Vim) over a small Text interface, and (in editor.go) a
// tview primitive that wraps a *tview.TextArea with it. Vim has no
// dependency on tview or tcell's screen APIs — only internal/ui/keys, for
// the normalized Key type every other part of gprt's router already
// speaks — so its whole behaviour is unit-tested against a tiny
// string-backed fake, with no Application or SimulationScreen required.
//
// Undo/redo lives entirely in this layer (see Vim.replace/undo/redo):
// tview.TextArea groups insert-mode keystrokes per word and exposes no
// Undo/Redo of its own, so synthesising it would give word-granularity
// undo that could not be driven through the pure Text interface either.
// Every command that changes the buffer maps to exactly one
// Text.Replace(start, end, new) call, recorded as one {start, old, new}
// undo entry; a whole insert session (i/a/I/A/o/O/cc/cw/c$/C/s through the
// next Esc) collapses to a single entry too, built from the common
// prefix/suffix diff between the text at insert-entry and at Esc, so `u`
// undoes a whole session at once like real vim.
package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// Text is the buffer Vim edits, addressed entirely in byte offsets that
// must always fall on a grapheme-cluster boundary — Vim itself never
// computes an offset that does not (see the cluster helpers in
// cluster.go) — so an implementation is free to reject (or, in tests,
// panic on) any other offset as a caller bug. Select(a, a) represents a
// plain, non-extending cursor at a.
type Text interface {
	// Text returns the whole buffer's current content.
	Text() string
	// Cursor returns the current cursor position: the start of the
	// current selection, or the plain cursor position when nothing is
	// selected.
	Cursor() int
	// SetCursor moves the cursor to pos with nothing selected.
	SetCursor(pos int)
	// Replace replaces the byte range [start, end) with s and leaves the
	// cursor at start+len(s).
	Replace(start, end int, s string)
	// Select sets the selection to [start, end); Select(a, a) is a plain
	// cursor at a.
	Select(start, end int)
}

// Mode is Vim's current editing mode.
type Mode int

// Known modes.
const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeVisualLine
	ModeCommand
)

// ActionKind identifies what a Handle call asks its caller (the Editor
// primitive, or a test) to do next.
type ActionKind int

// Known action kinds.
const (
	// Consumed means the key was fully handled here; the caller does
	// nothing further with it.
	Consumed ActionKind = iota
	// Forward means the caller must forward the original key event to
	// the underlying tview.TextArea's own InputHandler — Vim never
	// reimplements TextArea's rune insertion, wrapping-aware cursor
	// movement, or paste handling itself.
	Forward
	// Command means a ":" command line was submitted; Action.Command
	// holds its (already-parsed) name — "w", "q", "q!", "wq", or "e".
	// SendRequested/CloseRequested/DiscardRequested/
	// ExternalEditRequested below are returned directly for these
	// instead; Command is reserved for forwarding an unrecognised
	// command's name to a caller that wants to report it (Consumed is
	// returned for that case today — see Vim.submitCommand — but the
	// Kind is kept distinct from Consumed for a future caller that might
	// want to distinguish "an unknown command was typed" from "a
	// keystroke did nothing").
	Command
	// MentionQuery means the text immediately before the cursor now
	// matches "@[\w-]*" at a word boundary; Action.Query is the text
	// after "@". Returned by OnTextChanged, not by Handle directly (see
	// OnTextChanged's doc comment for why).
	MentionQuery
	// ModeChanged means Handle changed Vim's Mode (entered/left insert
	// or visual mode, for instance); the caller may want to re-render a
	// mode indicator.
	ModeChanged
	// SendRequested means the buffer should be sent (":w", ":wq", or
	// Ctrl-s in any mode).
	SendRequested
	// CloseRequested means the composer should close, keeping any draft
	// (":q").
	CloseRequested
	// DiscardRequested means the composer should close and discard its
	// draft (":q!").
	DiscardRequested
	// ExternalEditRequested means the caller should suspend the TUI, run
	// the external editor on the buffer, and feed the result back via
	// SetTextFromOutside (":e").
	ExternalEditRequested
)

// Action is what Handle (or OnTextChanged) returns.
type Action struct {
	Kind ActionKind
	// Command is the parsed command name for Kind == Command.
	Command string
	// Query is the text after "@" for Kind == MentionQuery.
	Query string
}

// undoEntry is one recorded buffer mutation: applying
// Text.Replace(start, start+len(New), Old) inverts it: applying
// Text.Replace(start, start+len(Old), New) redoes it.
type undoEntry struct {
	start    int
	old, new string
}

// Vim is a pure vim-style modal editor over a Text buffer. Build one with
// New and feed it every keystroke via Handle.
type Vim struct {
	text Text
	mode Mode

	// count is the numeric prefix accumulated so far in normal/visual
	// mode (0 means "none typed yet", matching vim's own convention that
	// a leading "0" is the "start of line" motion, not the start of a
	// count).
	count int
	// pendingOp is the operator ('d', 'c', or 'y') waiting for a motion,
	// or 0 when none is pending.
	pendingOp rune
	// pendingOpCount is the count typed before pendingOp's own key (the
	// "2" in "2dw"), captured when the operator key is pressed so a
	// count typed before AND after the operator multiply together like
	// vim's own compound counts (effectiveCount).
	pendingOpCount int
	// pendingG is true right after a lone "g" in normal/visual mode,
	// waiting for the second "g" of "gg".
	pendingG bool

	// register holds the unnamed register's text and whether it was
	// recorded linewise (dd/yy/cc) or characterwise.
	register         string
	registerLinewise bool

	// visualAnchor is the buffer offset visual/visual-line mode was
	// entered at; visualCursor is the selection's other, "active" end,
	// which every visual-mode motion updates. The selection Text.Select
	// is given is always [min(anchor, visualCursor),
	// max(anchor, visualCursor)+…), but Text.Cursor() itself only ever
	// reports the *start* of whatever range Select was last called with
	// (see the Text interface's doc comment) — never necessarily the
	// active end — so visual-mode code always tracks the active end
	// itself in visualCursor rather than reading it back from Text.
	visualAnchor, visualCursor int

	// cmdline is the text typed so far after the leading ":" in
	// ModeCommand, not including the ":" itself.
	cmdline string
	// lastCmdErr is set by submitCommand when the typed command is not
	// recognised; see LastCommandError.
	lastCmdErr string

	// insertActive is true from the moment an insert-entering command
	// (i a I A o O cc cw c$ C s) starts until the next Esc leaves insert
	// mode; insertSnapshot is Text() at that moment. Every buffer
	// mutation performed while insertActive is true bypasses the normal
	// per-command undo recording (see replaceRaw) so the whole session
	// collapses into the single entry pushed on Esc (see leaveInsert).
	insertActive   bool
	insertSnapshot string

	undoStack []undoEntry
	redoStack []undoEntry

	// desiredCol is the display-cell column j/k try to preserve across
	// lines of different lengths, like vim's own "desired column".
	// lastWasVertical records whether the previous keystroke was itself a
	// j/k, so verticalMove/verticalMoveVisual know whether to trust
	// desiredCol as-is (mid-run) or resync it to the cursor's actual
	// current column first (starting a fresh run) — see their own doc
	// comments.
	desiredCol      int
	lastWasVertical bool

	// mentionActive/mentionStart record the most recent match
	// OnTextChanged found, so AcceptMention knows what span to replace;
	// mentionActive is false once the pattern stops matching (cleared by
	// OnTextChanged itself) or a candidate is accepted.
	mentionActive bool
	mentionStart  int
}

// NewVim builds a Vim in normal mode over t. Named NewVim, not New, so it
// does not collide with the Editor primitive's own New (editor.go) in
// this same package — Editor is the more commonly constructed of the two
// from outside this package (internal/ui's composer), so it keeps the
// unqualified name.
func NewVim(t Text) *Vim {
	return &Vim{text: t}
}

// Mode returns Vim's current mode.
func (v *Vim) Mode() Mode { return v.mode }

// LastCommandError returns the error message from the most recent
// unrecognised ":" command, or "" if the last submitted command (if any)
// was recognised.
func (v *Vim) LastCommandError() string { return v.lastCmdErr }

// CommandLine returns the text typed so far in ModeCommand, not
// including the leading ":".
func (v *Vim) CommandLine() string { return v.cmdline }

// Handle processes one normalized keystroke and reports what the caller
// should do next. Ctrl-s requests a send in any mode, taking priority over
// every mode-specific binding.
func (v *Vim) Handle(k keys.Key) Action {
	if isCtrlS(k) {
		return Action{Kind: SendRequested}
	}

	switch v.mode {
	case ModeInsert:
		return v.handleInsert(k)
	case ModeCommand:
		return v.handleCommand(k)
	case ModeVisual, ModeVisualLine:
		return v.handleVisual(k)
	default:
		return v.handleNormal(k)
	}
}

func isCtrlS(k keys.Key) bool {
	return k.Kind == keys.KindRune && k.Rune == 's' && k.Mod == modCtrl
}

// isEsc reports whether k is the Esc key, shared by every mode's handler.
func isEsc(k keys.Key) bool {
	return k.Kind == keys.KindSpecial && k.Special == tcell.KeyEsc
}

// digitValue reports the numeric value of k when it is a plain '0'-'9'
// rune with no modifier, mirroring internal/ui/keys' own (unexported)
// helper of the same purpose for the Sequencer's count prefix.
func digitValue(k keys.Key) (int, bool) {
	if k.Kind != keys.KindRune || k.Mod != 0 {
		return 0, false
	}
	if k.Rune < '0' || k.Rune > '9' {
		return 0, false
	}
	return int(k.Rune - '0'), true
}

// resetPending clears count/operator/g-prefix state (but never the
// current Mode), run after every command that resolves — successfully or
// not — so a later, unrelated keystroke never inherits a stale count or
// operator.
func (v *Vim) resetPending() {
	v.pendingOp = 0
	v.pendingOpCount = 0
	v.count = 0
	v.pendingG = false
}

// effectiveCount combines a count typed before an operator with one typed
// between the operator and its motion (vim's compound counts, e.g. "2d3w"
// deletes 6 words): each defaults to 1 when not typed, and the two
// multiply together only when an operator is actually involved — a bare
// motion's own count (no operator at all) is just v.count itself.
func (v *Vim) effectiveCount() int {
	if v.pendingOp == 0 && v.pendingOpCount == 0 {
		m := v.count
		if m == 0 {
			m = 1
		}
		return m
	}
	op := v.pendingOpCount
	if op == 0 {
		op = 1
	}
	m := v.count
	if m == 0 {
		m = 1
	}
	return op * m
}
