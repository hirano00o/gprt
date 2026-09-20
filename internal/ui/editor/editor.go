package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/google/shlex"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/keys"
)

// Candidate is one "@" mention completion candidate offered by
// Options.Candidates.
type Candidate struct {
	// Login is inserted (as "@Login ") when the candidate is accepted.
	Login string
	// Name is shown alongside Login in the popup, when non-empty.
	Name string
}

// Options configure a new Editor.
type Options struct {
	// Candidates returns the mention candidates matching prefix (the
	// text typed after "@"), used to populate the completion popup. A
	// nil func disables the popup entirely — "@" then behaves like any
	// other character.
	Candidates func(prefix string) []Candidate
	// Suspend runs f with the TUI suspended, wired to
	// tview.Application.Suspend, and reports whether it actually did so
	// (mirroring Suspend's own return value — false when the
	// application was already suspended or has no screen yet). Required
	// for ":e" to do anything; a nil Suspend, or one returning false,
	// aborts ":e" with an error (see OnError) rather than running the
	// external editor blind and feeding its (necessarily empty) result
	// back into the buffer.
	Suspend func(func()) bool
	// ExternalEditor is the command ":e" runs, split with shell-like
	// quoting rules (google/shlex) before the buffer's temp file path is
	// appended as its final argument. Empty falls back to $EDITOR, then
	// "vim".
	ExternalEditor string
	// OnChange is called with the buffer's current text after every
	// edit (typing, an operator, undo/redo, a mention accepted, ":e"'s
	// result, …).
	OnChange func(text string)
	// OnAction is called for every Action Handle resolves to except
	// Consumed, Forward, and MentionQuery (all handled internally) —
	// in practice SendRequested, CloseRequested, DiscardRequested, and
	// ModeChanged, so a host can keep its own chrome (a title line, for
	// instance) in sync with the editor's mode.
	OnAction func(Action)
	// OnError is called with a message whenever ":e" fails for any
	// reason — Suspend was nil or returned false, the external command
	// itself failed, or a temp-file operation failed — in addition to
	// the same message being shown in the editor's own status line. The
	// buffer is always left unchanged when this fires. A nil OnError
	// just leaves the failure visible in the status line only.
	OnError func(message string)
}

// textAreaAdapter implements Text over a *tview.TextArea, the production
// backing store for Editor's Vim (see the package doc comment for why
// Vim itself never depends on tview directly).
type textAreaAdapter struct{ area *tview.TextArea }

func (a *textAreaAdapter) Text() string { return a.area.GetText() }

// Cursor returns GetSelection's start, which tview.TextArea documents as
// the cursor position whenever nothing is selected — the only state this
// adapter is ever read in outside visual mode (see Vim's own visualCursor
// field for why visual mode never relies on this).
func (a *textAreaAdapter) Cursor() int {
	_, start, _ := a.area.GetSelection()
	return start
}

func (a *textAreaAdapter) SetCursor(pos int)                { a.area.Select(pos, pos) }
func (a *textAreaAdapter) Replace(start, end int, s string) { a.area.Replace(start, end, s) }
func (a *textAreaAdapter) Select(start, end int)            { a.area.Select(start, end) }

// Editor is gprt's vim-style comment/PR-body editor primitive: a
// *tview.TextArea plus a one-line mode/hint status row, wrapped in a
// *tview.Flex and driven entirely by a Vim.
type Editor struct {
	*tview.Flex

	opts    Options
	area    *tview.TextArea
	status  *tview.TextView
	vim     *Vim
	adapter *textAreaAdapter

	popup      *tview.List
	popupOpen  bool
	candidates []Candidate

	// lastScreen is the tcell.Screen most recently passed to Draw, kept
	// only so the blur callback registered in New (which tview invokes
	// with no arguments of its own) has a Screen to call SetCursorStyle
	// on when resetting it — see that callback's own doc comment.
	lastScreen tcell.Screen

	// suppressChange is true only for the duration of SetText's own call
	// into area.SetText, so the "changed" callback that call triggers
	// does not write through a draft (OnChange) or run mention detection
	// for a prefill/restore SetText never itself represents a user edit.
	suppressChange bool
	// externalEditorErr is the most recent ":e" failure's message, shown
	// by renderStatus until the next ":e" attempt (successful or not)
	// clears or replaces it — see runExternalEditor.
	externalEditorErr string
}

// New builds an Editor with opts, seeded with the empty buffer.
func New(opts Options) *Editor {
	area := tview.NewTextArea()
	area.SetWrap(true)

	e := &Editor{
		opts:   opts,
		area:   area,
		status: tview.NewTextView(),
		Flex:   tview.NewFlex().SetDirection(tview.FlexRow),
		popup:  tview.NewList().ShowSecondaryText(false),
	}
	e.adapter = &textAreaAdapter{area: area}
	e.vim = NewVim(e.adapter)
	area.SetChangedFunc(e.onChanged)
	area.SetBlurFunc(e.resetCursorStyle)

	e.AddItem(area, 0, 1, true)
	e.AddItem(e.status, 1, 0, false)
	e.renderStatus()
	return e
}

// SetText replaces the buffer's content and cursor, without affecting
// undo history and without triggering OnChange or mention detection —
// used to seed the editor with a restored draft or a prefilled comment
// body before the user has typed anything, so merely opening a composer
// for editing never itself write-throughs a draft. Use (*Vim via)
// ":e"'s own restore path (SetTextFromOutside, exercised through Handle)
// once editing is already underway, so `u` still returns to the previous
// content.
func (e *Editor) SetText(text string) {
	e.suppressChange = true
	e.area.SetText(normalizeCRLF(text), false)
	e.suppressChange = false
}

// normalizeCRLF rewrites "\r\n" to "\n" — applied to every text that can
// have originated outside gprt's own buffer (a prefilled/restored
// SetText, and an external editor's file read-back in
// runExternalEditor), since uniseg treats a "\r\n" pair as a single
// grapheme cluster while every line-oriented computation in this package
// (lineBounds and everything built on it) splits purely on "\n": left
// unnormalized, a "\r" would end up stranded as a trailing character of
// the previous line, corrupting undo entries and confusing line-based
// motions the moment such a line is edited.
func normalizeCRLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Text returns the buffer's current content.
func (e *Editor) Text() string { return e.area.GetText() }

// Vim returns the underlying Vim state machine, mainly for tests that
// need to assert on Mode()/LastCommandError() directly.
func (e *Editor) Vim() *Vim { return e.vim }

// PopupOpen reports whether the mention completion popup is currently
// showing.
func (e *Editor) PopupOpen() bool { return e.popupOpen }

// HasFocus reports whether the underlying TextArea currently has focus.
// Overrides the embedded *tview.Flex's own HasFocus: like tview.Form,
// Flex.Focus delegates keyboard focus straight down to whichever child
// item was added with focus=true (the TextArea here — see New), so
// tview.Application actually tracks focus at that leaf, not at this
// wrapper; a caller comparing tview.Application.GetFocus() against an
// *Editor value directly would never match. This is the one property
// callers need instead, and it is exactly what the router (and this
// primitive's own Draw) needs to ask.
func (e *Editor) HasFocus() bool { return e.area.HasFocus() }

// Handle processes one raw key event by normalizing it and calling
// HandleKey once per resulting key. A thin wrapper: most callers (an
// Editor used on its own) want this; a caller that has *already*
// normalized ev itself before deciding how to route each resulting key —
// gprt's router, whose composer-focus routing must recognize a Ctrl-w
// chord one normalized key at a time — must call HandleKey directly
// instead, exactly once per key, or a multi-key expansion (Alt+rune
// normalizes to [Esc, rune]) would be normalized and processed all over
// again on every one of its own keys, corrupting the sequence (and,
// worse, risking a nil-pointer panic on this very Editor value if an
// earlier key in the sequence caused the caller to close and discard it
// before the loop reaches the next one).
func (e *Editor) Handle(ev *tcell.EventKey) bool {
	for _, k := range keys.Normalize(ev) {
		e.HandleKey(k, ev)
	}
	return true
}

// HandleKey processes exactly one already-normalized key: a mention
// popup (if open) gets first refusal at Ctrl-n/Ctrl-p/Up/Down/Tab/Enter/
// Esc, and everything else goes to Vim.Handle, whose resulting Action is
// dispatched (Forward reaches the TextArea's own InputHandler via ev,
// which must be k's own original event — see Handle's doc comment for
// why a caller iterating several keys from one multi-key-normalizing
// event must pass that same original event to every one of them, not
// re-derive it). Always returns true: gprt's router hands every key
// while the composer is focused to Editor.HandleKey except its own
// Ctrl-w chords and Ctrl-c (see docs/DESIGN.md), so there is never a key
// this method declines.
func (e *Editor) HandleKey(k keys.Key, ev *tcell.EventKey) bool {
	if e.popupOpen && e.handlePopupKey(k) {
		e.renderStatus()
		return true
	}
	e.dispatch(e.vim.Handle(k), ev)
	e.renderStatus()
	return true
}

func (e *Editor) dispatch(act Action, ev *tcell.EventKey) {
	switch act.Kind {
	case Forward:
		if h := e.area.InputHandler(); h != nil {
			h(ev, func(tview.Primitive) {})
		}
		// A bare cursor move (Left/Right/Home/End) does not itself edit
		// the buffer, so TextArea's own "changed" callback — the only
		// place OnTextChanged normally re-evaluates the mention span —
		// never fires for it; without closing here, the popup would keep
		// showing a completion for whatever "@..." span the cursor has
		// since moved away from (AcceptMention's own Cursor()/span
		// validation, in mention.go, guards the same gap from the other
		// side, in case some other path leaves the popup open too).
		if e.popupOpen && isBareCursorMovement(ev) {
			e.closePopup()
		}
	case ExternalEditRequested:
		e.runExternalEditor()
	case ModeChanged:
		e.closePopup()
		e.notify(act)
	case SendRequested, CloseRequested, DiscardRequested:
		e.notify(act)
	case MentionQuery:
		// Not normally reached from Handle (see OnTextChanged's doc
		// comment on why mention detection runs from onChanged
		// instead), but handled defensively all the same.
		e.openPopup(act.Query)
	}
}

// isBareCursorMovement reports whether ev is one of the plain cursor keys
// TextArea moves the cursor with but never edits the buffer for (see
// dispatch's Forward case for why that distinction matters here).
func isBareCursorMovement(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyEnd:
		return true
	default:
		return false
	}
}

func (e *Editor) notify(act Action) {
	if e.opts.OnAction != nil {
		e.opts.OnAction(act)
	}
}

// onChanged is TextArea's own "changed" callback: it fires after every
// edit, however it happened (a Forwarded keystroke, Replace from an
// operator, undo/redo, AcceptMention, SetTextFromOutside, …), which is
// exactly when Vim.OnTextChanged can usefully re-evaluate the mention
// pattern (see its own doc comment for why Handle itself cannot).
func (e *Editor) onChanged() {
	if e.suppressChange {
		return
	}
	text := e.area.GetText()
	if e.opts.OnChange != nil {
		e.opts.OnChange(text)
	}
	if e.opts.Candidates == nil {
		return
	}
	if act := e.vim.OnTextChanged(); act.Kind == MentionQuery {
		e.openPopup(act.Query)
	} else {
		e.closePopup()
	}
}

// handlePopupKey handles a keystroke that the mention popup, not Vim,
// owns while it is open: Ctrl-n/Ctrl-p/Up/Down move the selection,
// Tab/Enter accept it, and Esc closes only the popup (never leaving
// insert mode the way it normally would). Reports whether it handled k.
func (e *Editor) handlePopupKey(k keys.Key) bool {
	switch {
	case isEsc(k):
		e.closePopup()
		return true
	case k.Kind == keys.KindRune && k.Mod == modCtrl && k.Rune == 'n':
		e.movePopup(1)
		return true
	case k.Kind == keys.KindRune && k.Mod == modCtrl && k.Rune == 'p':
		e.movePopup(-1)
		return true
	case k.Kind == keys.KindSpecial && k.Special == tcell.KeyDown:
		e.movePopup(1)
		return true
	case k.Kind == keys.KindSpecial && k.Special == tcell.KeyUp:
		e.movePopup(-1)
		return true
	case k.Kind == keys.KindSpecial && (k.Special == tcell.KeyTab || k.Special == tcell.KeyEnter):
		e.acceptPopup()
		return true
	default:
		return false
	}
}

func (e *Editor) movePopup(delta int) {
	n := e.popup.GetItemCount()
	if n == 0 {
		return
	}
	cur := (e.popup.GetCurrentItem() + delta + n) % n
	e.popup.SetCurrentItem(cur)
}

func (e *Editor) acceptPopup() {
	idx := e.popup.GetCurrentItem()
	if idx < 0 || idx >= len(e.candidates) {
		e.closePopup()
		return
	}
	login := e.candidates[idx].Login
	e.closePopup()
	e.vim.AcceptMention(login)
}

func (e *Editor) openPopup(query string) {
	candidates := e.opts.Candidates(query)
	if len(candidates) == 0 {
		e.closePopup()
		return
	}
	e.candidates = candidates
	e.popup.Clear()
	for _, c := range candidates {
		label := "@" + c.Login
		if c.Name != "" {
			label += "  " + c.Name
		}
		e.popup.AddItem(label, "", 0, nil)
	}
	e.popup.SetCurrentItem(0)
	e.popupOpen = true
}

func (e *Editor) closePopup() {
	e.popupOpen = false
	e.candidates = nil
}

// renderStatus refreshes the one-line status/hint row from Vim's current
// mode.
func (e *Editor) renderStatus() {
	switch e.vim.Mode() {
	case ModeInsert:
		e.status.SetText("-- INSERT --")
	case ModeVisual:
		e.status.SetText("-- VISUAL --")
	case ModeVisualLine:
		e.status.SetText("-- VISUAL LINE --")
	case ModeCommand:
		e.status.SetText(":" + e.vim.CommandLine())
	default:
		switch {
		case e.externalEditorErr != "":
			e.status.SetText(e.externalEditorErr)
		case e.vim.LastCommandError() != "":
			e.status.SetText(e.vim.LastCommandError())
		default:
			e.status.SetText("Ctrl-s send · :q close · :e $EDITOR")
		}
	}
}

// Draw renders the Flex (TextArea + status row), then the mention popup
// (if open) on top, then sets the terminal cursor's shape for Vim's
// current mode — a steady block in normal/visual, a steady bar in
// insert — only while the TextArea itself has focus, so an unfocused
// Editor never fights whatever primitive focus actually rests on for the
// shared, single terminal cursor.
func (e *Editor) Draw(screen tcell.Screen) {
	e.lastScreen = screen
	e.Flex.Draw(screen)
	if e.area.HasFocus() {
		style := tcell.CursorStyleSteadyBlock
		if e.vim.Mode() == ModeInsert {
			style = tcell.CursorStyleSteadyBar
		}
		screen.SetCursorStyle(style)
	}
	if e.popupOpen {
		x, y, w, h := e.popupRect(screen)
		e.popup.SetRect(x, y, w, h)
		e.popup.Draw(screen)
	}
}

// resetCursorStyle restores the terminal's default cursor style,
// registered (in New) as the TextArea's own blur callback — tview.Box
// calls it via Blur() the moment Application.SetFocus moves focus
// elsewhere (see tview's own SetFocus: it calls the previously focused
// primitive's Blur() before assigning the new one). Without this, the
// terminal would keep showing whatever shape Draw last set (typically a
// steady bar, from insert mode) even after the composer closes or loses
// focus some other way, since nothing else ever draws over it. lastScreen
// is nil only if this fires before the first Draw, which cannot happen
// for a primitive that must already be visible (and therefore drawn at
// least once) to have been focused, and thus blurred, at all.
func (e *Editor) resetCursorStyle() {
	if e.lastScreen != nil {
		e.lastScreen.SetCursorStyle(tcell.CursorStyleDefault)
	}
}

// popupRect computes the popup's screen rectangle, anchored just below
// the cursor's current screen position and opening upward instead when
// it would not otherwise fit.
func (e *Editor) popupRect(screen tcell.Screen) (x, y, w, h int) {
	areaX, areaY, areaW, _ := e.area.GetInnerRect()
	_, _, toRow, toColumn := e.area.GetCursor()
	offRow, offColumn := e.area.GetOffset()
	cursorX := areaX + toColumn - offColumn
	cursorY := areaY + toRow - offRow

	w = min(30, max(areaW, 10))
	h = min(len(e.candidates), 8)
	if h < 1 {
		h = 1
	}

	x = cursorX
	if x+w > areaX+areaW {
		x = areaX + areaW - w
	}
	if x < 0 {
		x = 0
	}

	_, screenH := screen.Size()
	y = cursorY + 1
	if y+h > screenH {
		y = cursorY - h
		if y < 0 {
			y = 0
		}
	}
	return x, y, w, h
}

// runExternalEditor implements ":e": it suspends the TUI (via
// Options.Suspend), writes the buffer to a temp file, runs
// Options.ExternalEditor (falling back to $EDITOR, then "vim") on it,
// and feeds the (CRLF-normalized) result back through
// Vim.SetTextFromOutside (pushing one undo entry, so `u` returns to the
// pre-":e" content). Any failure — no Suspend configured, Suspend itself
// reporting it could not suspend, or the external command/temp-file
// round trip failing — reports the error (see reportExternalEditorError)
// and leaves the buffer exactly as it was; the external editor's own
// (in that case unreliable, possibly empty) result is never applied.
func (e *Editor) runExternalEditor() {
	e.externalEditorErr = ""
	if e.opts.Suspend == nil {
		e.reportExternalEditorError("no external editor configured")
		return
	}
	cmdLine := e.opts.ExternalEditor
	if cmdLine == "" {
		cmdLine = os.Getenv("EDITOR")
	}
	if cmdLine == "" {
		cmdLine = "vim"
	}

	var result string
	var runErr error
	suspended := e.opts.Suspend(func() {
		result, runErr = runExternalEditorProcess(cmdLine, e.area.GetText())
	})
	if !suspended {
		e.reportExternalEditorError("could not suspend the terminal")
		return
	}
	if runErr != nil {
		e.reportExternalEditorError(runErr.Error())
		return
	}
	e.vim.SetTextFromOutside(normalizeCRLF(result))
}

// reportExternalEditorError records msg (prefixed for the status line)
// as the current ":e" failure, re-renders the status line to show it
// immediately, and calls Options.OnError so a host can additionally toast
// and/or log it.
func (e *Editor) reportExternalEditorError(msg string) {
	e.externalEditorErr = "editor failed: " + msg
	e.renderStatus()
	if e.opts.OnError != nil {
		e.opts.OnError(e.externalEditorErr)
	}
}

// runExternalEditorProcess writes text to a temp file, runs cmdLine
// (split with shell-like quoting via google/shlex) on it with the file's
// path appended as the final argument, and returns the file's content
// after the command exits.
func runExternalEditorProcess(cmdLine, text string) (string, error) {
	f, err := os.CreateTemp("", "gprt-comment-*.md")
	if err != nil {
		return "", fmt.Errorf("editor: create temp file: %w", err)
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()

	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("editor: write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("editor: close temp file: %w", err)
	}

	parts, err := shlex.Split(cmdLine)
	if err != nil || len(parts) == 0 {
		return "", fmt.Errorf("editor: invalid command %q", cmdLine)
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor: run %q: %w", cmdLine, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("editor: read temp file: %w", err)
	}
	return string(data), nil
}
