//go:build ghostty

package ui

import (
	"strings"
	"testing"

	"go.mitchellh.com/libghostty"
)

// body is the screen without its last row, which has a "refreshed Ns ago"
// clock that changes between frames.
func body(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	return strings.Join(lines[:len(lines)-1], "\n")
}

// suspendObservation is what the suspended callback saw of the terminal. It
// is collected on the UI goroutine and judged on the test goroutine, because
// FailNow must not run on the former.
type suspendObservation struct {
	resumed       bool
	screen        libghostty.TerminalScreen
	screenErr     error
	flags         libghostty.KittyKeyFlags
	flagsErr      error
	editorVisible bool
}

// TestGhosttySuspendRestoresTheScreen runs the real tview Suspend on the real
// tcell screen. While suspended, a program that behaves like an editor enters
// the alternate screen, draws, and leaves it again; afterwards gprt must be
// back on the alternate screen with the kitty keyboard protocol on and its
// screen as it was.
//
// It verifies the restoration of the screen and the terminal modes, but not
// that suspendForExternalEditor needs its app.Sync(): tcell v2.13's disengage
// empties its cell buffer (cells.Resize(0, 0)) and engage resizes it back, so
// the whole screen is redrawn after Resume with or without Sync, and removing
// Sync is not detected here.
func TestGhosttySuspendRestoresTheScreen(t *testing.T) {
	g := newGhosttyApp(t, testAppOpts{}, nil)
	g.Eventually(t, func(s string) bool { return strings.Contains(s, "No checks") })
	before := body(g.Text())

	if screen, err := g.ActiveScreen(); err != nil || screen != libghostty.ScreenAlternate {
		t.Fatalf("screen before suspending = %v (err %v), want the alternate screen", screen, err)
	}

	obs := query(g.app.app, func() suspendObservation {
		var o suspendObservation
		o.resumed = g.app.suspendForExternalEditor(func() {
			o.screen, o.screenErr = g.ActiveScreen()
			o.flags, o.flagsErr = g.KittyFlagsErr()
			g.Scribble("\x1b[?1049h\x1b[2J\x1b[H\x1b[?25lEDITOR OUTPUT")
			o.editorVisible = strings.Contains(g.Text(), "EDITOR OUTPUT")
			g.Scribble("\x1b[?25h\x1b[?1049l")
		})
		return o
	})
	if !obs.resumed {
		t.Fatal("suspendForExternalEditor reported failure")
	}
	if obs.screenErr != nil || obs.screen != libghostty.ScreenPrimary {
		t.Errorf("screen while suspended = %v (err %v), want the primary screen", obs.screen, obs.screenErr)
	}
	if obs.flagsErr != nil || obs.flags != libghostty.KittyKeyDisabled {
		t.Errorf("kitty keyboard flags while suspended = %#b (err %v), want none", obs.flags, obs.flagsErr)
	}
	if !obs.editorVisible {
		t.Error("the editor's output never reached the terminal's screen")
	}

	g.Eventually(t, func(s string) bool {
		return !strings.Contains(s, "EDITOR OUTPUT") && body(s) == before
	})
	if screen, err := g.ActiveScreen(); err != nil || screen != libghostty.ScreenAlternate {
		t.Errorf("screen after resuming = %v (err %v), want the alternate screen", screen, err)
	}
	if got, err := g.KittyFlagsErr(); err != nil || got&libghostty.KittyKeyDisambiguate == 0 {
		t.Errorf("kitty keyboard flags after resuming = %#b (err %v), want the disambiguate bit", got, err)
	}
}
