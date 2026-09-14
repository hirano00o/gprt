// prcommands_test.go covers the ":close" and ":reopen" command-line
// commands: an open, loaded pull request is required; each refuses locally
// (a toast, no store call) when the viewer lacks the matching viewer-can-*
// flag or the pull request's state does not fit; otherwise a confirm
// dialog ("Close #N?"/"Reopen #N?") gates the actual Store.Close/Reopen
// call, whose outcome (Store.MutationError()) surfaces as a "closed"/
// "reopened" success toast or a failure one.
package ui

import (
	"errors"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// sendCommand types ":"+text and submits it (Enter), exactly as a user
// typing a command would.
func sendCommand(app *App, text string) {
	sendRune(app.app, ':')
	for _, r := range text {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEnter)
}

// confirmText reads the whole screen's rendered content, for a substring
// check against a *tview.Modal's own text — Modal exposes no text getter
// of its own (only SetText), matching files_test.go's screenText's own
// "render then read the screen" technique.
func confirmText(app *App, screen tcell.Screen) string {
	return query(app.app, func() string { return screenText(screen, 0, 0, 100, 30) })
}

// closablePR returns fixtureDetailPR(ref) with ViewerCanClose true and an
// OPEN state, so ":close" is available on it.
func closablePR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.ViewerCanClose = true
	pr.State = model.PRStateOpen
	return pr
}

// reopenablePR returns fixtureDetailPR(ref) with ViewerCanReopen true and a
// CLOSED state, so ":reopen" is available on it.
func reopenablePR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.ViewerCanReopen = true
	pr.State = model.PRStateClosed
	return pr
}

func TestCmdCloseConfirmsThenCallsStoreClose(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))

	sendCommand(app, "close")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if got := confirmText(app, screen); !containsSubstring(got, "Close #1?") {
		t.Fatalf("confirm dialog text = %q, want it to contain %q", got, "Close #1?")
	}

	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Close"

	waitFor(t, app.app, func() bool { return len(fake.ClosePullRequestIDs()) == 1 })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "closed" })
}

func TestCmdCloseRefusedWhenViewerCanCloseIsFalse(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	pr.ViewerCanClose = false
	openDetailForComposer(t, app, fake, pr)

	sendCommand(app, "close")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot close") })
	if app.overlay == "confirm" {
		t.Fatal("a viewer who cannot close must not see a confirm dialog")
	}
	if len(fake.ClosePullRequestIDs()) != 0 {
		t.Fatal("a refused close must not call the store")
	}
}

func TestCmdCloseRefusedWhenAlreadyMerged(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := closablePR(ref)
	pr.State = model.PRStateMerged
	openDetailForComposer(t, app, fake, pr)

	sendCommand(app, "close")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not open") })
	if len(fake.ClosePullRequestIDs()) != 0 {
		t.Fatal("closing an already-merged pull request must not call the store")
	}
}

func TestCmdCloseRefusedWithNoPullRequestOpen(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendCommand(app, "close")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
}

func TestCmdReopenConfirmsThenCallsStoreReopen(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, reopenablePR(ref))

	sendCommand(app, "reopen")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	if got := confirmText(app, screen); !containsSubstring(got, "Reopen #1?") {
		t.Fatalf("confirm dialog text = %q, want it to contain %q", got, "Reopen #1?")
	}

	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Reopen"

	waitFor(t, app.app, func() bool { return len(fake.ReopenPullRequestIDs()) == 1 })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "reopened" })
}

func TestCmdReopenRefusedWhenAlreadyOpen(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := reopenablePR(ref)
	pr.State = model.PRStateOpen
	openDetailForComposer(t, app, fake, pr)

	sendCommand(app, "reopen")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not closed") })
	if len(fake.ReopenPullRequestIDs()) != 0 {
		t.Fatal("reopening an already-open pull request must not call the store")
	}
}

// TestCmdCloseFailureToastsError covers a failed Store.Close (the fake
// GitHub call returning an error) reporting through
// Store.MutationError()'s own message, not a generic failure.
func TestCmdCloseFailureToastsError(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))
	fake.SetClosePullRequestError(errors.New("boom"))

	sendCommand(app, "close")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "boom") })
}

// TestCmdCloseRefusesWhenTargetPRChangedBeforeConfirm covers the deferred
// confirm callback re-checking Store.CurrentRef() against the ref captured
// when the dialog opened (docs/DESIGN.md's "deferred callback" convention):
// a pull request switch while the confirm dialog is still open must not
// let confirming it close the now-current, unrelated pull request.
func TestCmdCloseRefusesWhenTargetPRChangedBeforeConfirm(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref1))

	sendCommand(app, "close")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: closablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref2
	})

	confirmYes(app.app)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "different pull request") })
	if len(fake.ClosePullRequestIDs()) != 0 {
		t.Fatal("confirming after the target pull request changed must not call the store")
	}
}
