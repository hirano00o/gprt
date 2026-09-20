package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// newTestEditor builds an Editor over a fixed-size SimulationScreen, ready
// for Handle/Draw calls with no tview.Application or event loop involved:
// TextArea's internal row/column layout (which Editor's own popup
// positioning and the textAreaAdapter's Cursor() depend on indirectly via
// GetSelection) is computed inside Draw, so every helper below re-draws
// after each keystroke.
func newTestEditor(t *testing.T, opts Options) (*Editor, tcell.SimulationScreen) {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(60, 10)

	e := New(opts)
	e.SetRect(0, 0, 60, 10)
	e.Draw(screen)
	return e, screen
}

func sendEditorKey(e *Editor, screen tcell.Screen, ev *tcell.EventKey) {
	e.Handle(ev)
	e.Draw(screen)
}

func sendEditorRune(e *Editor, screen tcell.Screen, r rune) {
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

func typeEditorText(e *Editor, screen tcell.Screen, s string) {
	for _, r := range s {
		sendEditorRune(e, screen, r)
	}
}

func TestEditorTypingAndModeIndicator(t *testing.T) {
	e, screen := newTestEditor(t, Options{})

	if got := e.status.GetText(false); got == "" {
		t.Fatalf("initial status is empty, want a hint")
	}

	sendEditorRune(e, screen, 'i')
	if got, want := e.status.GetText(false), "-- INSERT --"; got != want {
		t.Fatalf("status after 'i' = %q, want %q", got, want)
	}

	typeEditorText(e, screen, "hello")
	if got, want := e.Text(), "hello"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}

	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if e.vim.Mode() != ModeNormal {
		t.Fatalf("mode after Esc = %v, want ModeNormal", e.vim.Mode())
	}
	if got := e.status.GetText(false); got == "-- INSERT --" {
		t.Fatalf("status still shows INSERT after Esc")
	}
}

func TestEditorOnChangeFires(t *testing.T) {
	var seen []string
	e, screen := newTestEditor(t, Options{OnChange: func(text string) { seen = append(seen, text) }})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "hi")
	if len(seen) == 0 {
		t.Fatalf("OnChange was never called")
	}
	if last := seen[len(seen)-1]; last != "hi" {
		t.Fatalf("last OnChange text = %q, want %q", last, "hi")
	}
}

func TestEditorMentionPopupAppearsAndTabAccepts(t *testing.T) {
	candidatesFor := func(prefix string) []Candidate {
		all := []Candidate{{Login: "octocat", Name: "The Octocat"}, {Login: "other"}}
		var out []Candidate
		for _, c := range all {
			if len(prefix) == 0 || (len(c.Login) >= len(prefix) && c.Login[:len(prefix)] == prefix) {
				out = append(out, c)
			}
		}
		return out
	}
	e, screen := newTestEditor(t, Options{Candidates: candidatesFor})

	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "hi @oc")
	if !e.popupOpen {
		t.Fatalf("popup did not open after typing '@oc'")
	}
	if len(e.candidates) != 1 || e.candidates[0].Login != "octocat" {
		t.Fatalf("candidates = %+v, want just octocat", e.candidates)
	}

	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if e.popupOpen {
		t.Fatalf("popup still open after Tab")
	}
	if got, want := e.Text(), "hi @octocat "; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestEditorMentionPopupEscClosesOnlyThePopup(t *testing.T) {
	e, screen := newTestEditor(t, Options{
		Candidates: func(string) []Candidate { return []Candidate{{Login: "octocat"}} },
	})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "@o")
	if !e.popupOpen {
		t.Fatalf("popup did not open")
	}
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if e.popupOpen {
		t.Fatalf("popup still open after Esc")
	}
	if e.vim.Mode() != ModeInsert {
		t.Fatalf("mode after popup Esc = %v, want ModeInsert (Esc must close only the popup)", e.vim.Mode())
	}
}

func TestEditorNoCandidatesFuncDisablesPopup(t *testing.T) {
	e, screen := newTestEditor(t, Options{})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "@oct")
	if e.popupOpen {
		t.Fatalf("popup opened despite a nil Candidates func")
	}
}

func TestEditorCtrlSCallsOnActionSendRequested(t *testing.T) {
	var got []Action
	e, screen := newTestEditor(t, Options{OnAction: func(a Action) { got = append(got, a) }})
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
	if len(got) == 0 || got[len(got)-1].Kind != SendRequested {
		t.Fatalf("OnAction calls = %+v, want a trailing SendRequested", got)
	}
}

func TestEditorCommandLineCloseAndDiscard(t *testing.T) {
	tests := []struct {
		cmd  string
		want ActionKind
	}{
		{"q", CloseRequested},
		{"q!", DiscardRequested},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			var got []Action
			e, screen := newTestEditor(t, Options{OnAction: func(a Action) { got = append(got, a) }})
			sendEditorRune(e, screen, ':')
			typeEditorText(e, screen, tc.cmd)
			sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
			if len(got) == 0 || got[len(got)-1].Kind != tc.want {
				t.Fatalf("OnAction calls = %+v, want a trailing %v", got, tc.want)
			}
		})
	}
}

func TestEditorExternalEditorRunsConfiguredCommand(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "fake-editor.sh")
	script := "#!/bin/sh\necho ' EDITED' >> \"$1\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, not attacker-controlled
		t.Fatalf("write fake editor script: %v", err)
	}

	suspendCalled := false
	e, screen := newTestEditor(t, Options{
		ExternalEditor: scriptPath,
		Suspend: func(f func()) bool {
			suspendCalled = true
			f()
			return true
		},
	})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "original")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	sendEditorRune(e, screen, ':')
	typeEditorText(e, screen, "e")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if !suspendCalled {
		t.Fatalf("Suspend was never called")
	}
	if got, want := e.Text(), "original EDITED\n"; got != want {
		t.Fatalf("text after :e = %q, want %q", got, want)
	}

	// The external edit is a single undo entry: `u` returns to the
	// pre-":e" content.
	sendEditorRune(e, screen, 'u')
	if got, want := e.Text(), "original"; got != want {
		t.Fatalf("text after u following :e = %q, want %q", got, want)
	}
}

// TestEditorSetTextDoesNotFireOnChange asserts SetText (used only to seed
// a composer with a restored draft or a prefilled comment body, before
// the user has touched anything) never triggers a write-through draft
// save: OnChange must fire for the user's own edits, not for the editor
// merely being opened with existing content. This also guards against the
// original bug OnChange being called twice for the same SetText call — it
// fires once through TextArea's own "changed" callback (SetText itself
// triggers it) and was, before this fix, called a second time explicitly.
func TestEditorSetTextDoesNotFireOnChange(t *testing.T) {
	var seen []string
	e, _ := newTestEditor(t, Options{OnChange: func(text string) { seen = append(seen, text) }})
	e.SetText("prefilled")
	if got, want := e.Text(), "prefilled"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if len(seen) != 0 {
		t.Fatalf("OnChange calls = %v, want none (SetText must not write through)", seen)
	}
}

func TestEditorSetTextNormalizesCRLF(t *testing.T) {
	e, _ := newTestEditor(t, Options{})
	e.SetText("line1\r\nline2")
	if got, want := e.Text(), "line1\nline2"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestEditorExternalEditorAbortsWhenSuspendFails covers ":e" when
// Options.Suspend reports it could not actually suspend the terminal
// (mirroring tview.Application.Suspend's own false return, e.g. when
// already suspended or the screen is not yet initialized): the external
// editor must never run blind, and the buffer must be left untouched.
func TestEditorExternalEditorAbortsWhenSuspendFails(t *testing.T) {
	var errs []string
	e, screen := newTestEditor(t, Options{
		ExternalEditor: "irrelevant",
		Suspend:        func(func()) bool { return false },
		OnError:        func(msg string) { errs = append(errs, msg) },
	})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "original")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	sendEditorRune(e, screen, ':')
	typeEditorText(e, screen, "e")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if got, want := e.Text(), "original"; got != want {
		t.Fatalf("text = %q, want %q (buffer must be untouched when Suspend fails)", got, want)
	}
	if len(errs) == 0 {
		t.Fatal("OnError was never called")
	}
	if got := e.status.GetText(false); !strings.Contains(got, "editor failed") {
		t.Fatalf("status = %q, want it to mention the editor failure", got)
	}
}

// TestEditorExternalEditorFailureReportsErrorAndKeepsBuffer covers ":e"
// when the external command itself fails (here, one that does not
// exist): the failure must reach OnError/the status line, not be
// swallowed, and the buffer must stay unchanged rather than being wiped
// by an empty/garbage result.
func TestEditorExternalEditorFailureReportsErrorAndKeepsBuffer(t *testing.T) {
	var errs []string
	e, screen := newTestEditor(t, Options{
		ExternalEditor: filepath.Join(t.TempDir(), "does-not-exist-binary"),
		Suspend:        func(f func()) bool { f(); return true },
		OnError:        func(msg string) { errs = append(errs, msg) },
	})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "original")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	sendEditorRune(e, screen, ':')
	typeEditorText(e, screen, "e")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if got, want := e.Text(), "original"; got != want {
		t.Fatalf("text = %q, want %q (buffer must be untouched on external editor failure)", got, want)
	}
	if len(errs) == 0 {
		t.Fatal("OnError was never called")
	}
	if got := e.status.GetText(false); !strings.Contains(got, "editor failed") {
		t.Fatalf("status = %q, want it to mention the editor failure", got)
	}
}

func TestEditorExternalEditorNormalizesCRLF(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "fake-editor-crlf.sh")
	script := "#!/bin/sh\nprintf 'line1\\r\\nline2\\r\\n' > \"$1\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, not attacker-controlled
		t.Fatalf("write fake editor script: %v", err)
	}
	e, screen := newTestEditor(t, Options{
		ExternalEditor: scriptPath,
		Suspend:        func(f func()) bool { f(); return true },
	})

	sendEditorRune(e, screen, ':')
	typeEditorText(e, screen, "e")
	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if got, want := e.Text(), "line1\nline2\n"; got != want {
		t.Fatalf("text = %q, want %q (CRLF from the external editor must be normalized)", got, want)
	}
}

func TestEditorMentionPopupClosesOnCursorMovement(t *testing.T) {
	e, screen := newTestEditor(t, Options{
		Candidates: func(string) []Candidate { return []Candidate{{Login: "octocat"}} },
	})
	sendEditorRune(e, screen, 'i')
	typeEditorText(e, screen, "hi @oc")
	if !e.popupOpen {
		t.Fatalf("popup did not open")
	}

	sendEditorKey(e, screen, tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
	if e.popupOpen {
		t.Fatalf("popup still open after the cursor moved away via Left")
	}
}
