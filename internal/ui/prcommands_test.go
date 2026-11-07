// prcommands_test.go covers the ":close", ":reopen" and ":ready"
// command-line commands: an open, loaded pull request is required; each
// refuses locally (a toast, no store call) when the viewer lacks the
// matching viewer-can-* flag or the pull request's state does not fit;
// otherwise a confirm dialog ("Close #N?"/"Reopen #N?"/"Mark ready #N?",
// followed by the repository, title and author) gates the actual
// Store.Close/Reopen/SetDraft call, whose outcome (Store.MutationError())
// surfaces as a "closed"/"reopened"/"marked ready for review" success
// toast or a failure one.
package ui

import (
	"errors"
	"strconv"
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

func TestPRConfirmMessage(t *testing.T) {
	tests := []struct {
		name string
		pr   model.PullRequest
		want string
	}{
		{
			name: "names the repository, title and author",
			pr:   model.PullRequest{Ref: fixtureRef(12), Title: "Add widget support", Author: model.User{Login: "alice"}},
			want: "Close #12?\n\nacme/widgets #12\nAdd widget support\nby @alice",
		},
		{
			name: "escapes a title that looks like a style tag",
			pr:   model.PullRequest{Ref: fixtureRef(3), Title: "[red] fix", Author: model.User{Login: "bob"}},
			want: "Close #3?\n\nacme/widgets #3\n[red[] fix\nby @bob",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := "Close #" + strconv.Itoa(tt.pr.Ref.Number) + "?"
			if got := prConfirmMessage(q, &tt.pr); got != tt.want {
				t.Errorf("prConfirmMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCmdCloseConfirmsThenCallsStoreClose(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, closablePR(ref))

	sendCommand(app, "close")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	got := confirmText(app, screen)
	for _, want := range []string{"Close #1?", "acme/widgets #1", "by @alice"} {
		if !containsSubstring(got, want) {
			t.Fatalf("confirm dialog text = %q, want it to contain %q", got, want)
		}
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

// readyablePR returns fixtureDetailPR(ref) as an OPEN draft the viewer can
// update, so ":ready" is available on it.
func readyablePR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.ViewerCanUpdate = true
	pr.State = model.PRStateOpen
	pr.IsDraft = true
	return pr
}

func TestCmdReadyConfirmsThenMarksReadyForReview(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, readyablePR(ref))

	sendCommand(app, "ready")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	got := confirmText(app, screen)
	for _, want := range []string{"Mark ready #1?", "acme/widgets #1", "by @alice"} {
		if !containsSubstring(got, want) {
			t.Fatalf("confirm dialog text = %q, want it to contain %q", got, want)
		}
	}

	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Mark ready"

	waitFor(t, app.app, func() bool { return len(fake.MarkReadyForReviewCalls()) == 1 })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "marked ready for review" })
	if n := len(fake.ConvertToDraftCalls()); n != 0 {
		t.Fatalf("ConvertToDraft called %d times, want 0", n)
	}
}

func TestCmdReadyRefused(t *testing.T) {
	tests := []struct {
		name   string
		modify func(pr *model.PullRequest)
		toast  string
	}{
		{"viewer cannot update", func(pr *model.PullRequest) { pr.ViewerCanUpdate = false }, "cannot mark this pull request ready"},
		{"not a draft", func(pr *model.PullRequest) { pr.IsDraft = false }, "not an open draft"},
		{"closed draft", func(pr *model.PullRequest) { pr.State = model.PRStateClosed }, "not an open draft"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _, fake, _ := newTestApp(t, nil)
			ref := fixtureRef(1)
			pr := readyablePR(ref)
			tt.modify(&pr)
			openDetailForComposer(t, app, fake, pr)

			sendCommand(app, "ready")

			waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, tt.toast) })
			if app.overlay == "confirm" {
				t.Fatal("a refused :ready must not show a confirm dialog")
			}
			if len(fake.MarkReadyForReviewCalls()) != 0 {
				t.Fatal("a refused :ready must not call the store")
			}
		})
	}
}

func TestCmdReadyRefusedWithNoPullRequestOpen(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendCommand(app, "ready")

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
}

func TestCmdReadyFailureToastsError(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, readyablePR(ref))
	fake.SetMarkReadyForReviewError(errors.New("boom"))

	sendCommand(app, "ready")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "boom") })
}

// TestCmdReadyRefusesWhenTargetPRChangedBeforeConfirm mirrors the
// ":close" test of the same shape: confirming after another pull request
// became current must not take that unrelated one out of draft.
func TestCmdReadyRefusesWhenTargetPRChangedBeforeConfirm(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, readyablePR(ref1))

	sendCommand(app, "ready")
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: readyablePR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref2
	})

	confirmYes(app.app)

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "different pull request") })
	if len(fake.MarkReadyForReviewCalls()) != 0 {
		t.Fatal("confirming after the target pull request changed must not call the store")
	}
}
