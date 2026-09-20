package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/editor"
)

// openDetailForComposer opens PR #1 with pr as its detail and waits for
// the PR tab to have focus, ready for a composer test to press a key on
// it.
func openDetailForComposer(t *testing.T, app *App, fake *fakeGitHub, pr model.PullRequest) {
	t.Helper()
	fake.SetPRResult(pr.Ref, gh.DetailResult{PR: pr})
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.isDetailFocused() && app.deps.Store.CurrentPR() != nil })
}

// ownCommentPR returns fixtureDetailPR(ref) with its one issue comment
// ("IC_1", by "dave") given to the viewer instead, so comment.edit/delete
// are available on it.
func ownCommentPR(ref model.PRRef) model.PullRequest {
	pr := fixtureDetailPR(ref)
	pr.Timeline[0].IssueComment.Author = model.User{Login: "octocat"}
	pr.Timeline[0].IssueComment.ViewerCanUpdate = true
	pr.Timeline[0].IssueComment.ViewerCanDelete = true
	return pr
}

// focusCommentBlock moves the PR tab's cursor onto the timeline:0 block
// (the fixture's one issue comment).
func focusCommentBlock(app *App) {
	act(app.app, func() {
		idx := detailSelectableIndex(app.prView.Rows(), "timeline:0")
		app.prView.SetCursor(idx)
	})
}

// composerText and composerOpen read App state directly, with no
// synchronization of their own: every call site below is already running
// on the UI goroutine (inside a waitFor condition, an act callback, or a
// query callback) except where explicitly wrapped in query() — see
// decisis pitfall #93 and query's own doc comment in app_test.go for why
// an unwrapped read anywhere else would be a real race, and why wrapping
// one of these *again* from inside an already-synchronized callback would
// deadlock instead (nested QueueUpdate).
func composerText(app *App) string { return app.composerEditor.Text() }

func composerOpen(app *App) bool { return app.composerEditor != nil }

func TestComposerCOpensWithFocus(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	if got := query(app.app, func() bool { return app.composerEditor.HasFocus() }); !got {
		t.Fatalf("composer did not take focus")
	}
	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Comment on #1"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}
}

func TestComposerTypingInsertModeAndEsc(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	waitFor(t, app.app, func() bool { return app.composerEditor.Vim().Mode() == editor.ModeInsert })
	for _, r := range "hello" {
		sendRune(app.app, r)
	}
	waitFor(t, app.app, func() bool { return composerText(app) == "hello" })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.composerEditor.Vim().Mode() == editor.ModeNormal })
}

func TestComposerDDAndUndo(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "line1" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEnter)
	for _, r := range "line2" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return composerText(app) == "line1\nline2" })

	sendRune(app.app, 'g')
	sendRune(app.app, 'g')
	sendRune(app.app, 'd')
	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return composerText(app) == "line2" })

	sendRune(app.app, 'u')
	waitFor(t, app.app, func() bool { return composerText(app) == "line1\nline2" })
}

func TestComposerColonQKeepsDraftAndReopeningRestoresIt(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "keep me" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "keep me" })
}

func TestComposerColonQBangDiscardsDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "discard me" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendRune(app.app, '!')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "" })
}

func TestComposerCtrlSAddsCommentAndClosesImmediately(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "great work" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool { return len(fake.AddCommentBodies()) == 1 })
	if got := fake.AddCommentBodies()[0]; got != "great work" {
		t.Fatalf("AddIssueComment body = %q, want %q", got, "great work")
	}
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

func TestComposerSendRefusedWhileMutating(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "second" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	if !query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatalf("composer closed despite Send being refused while a mutation is in flight")
	}
	if n := len(fake.AddCommentBodies()); n != 0 {
		t.Fatalf("AddIssueComment called %d time(s) before the in-flight mutation finished, want 0", n)
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

func TestComposerSendWithUnloadedDetailKeepsComposerAndText(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	block := make(chan struct{})
	fake.SetPRBlock(block)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	// "c" only needs CurrentRef() (set synchronously by OpenPR), so the
	// composer opens before the (blocked) detail fetch resolves.
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })
	if query(app.app, func() bool { return app.deps.Store.CurrentPR() != nil }) {
		t.Fatal("precondition: detail must still be loading (blocked)")
	}

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "hello" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	if !query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatal("composer closed despite the mutation never having been enqueued (detail not loaded yet)")
	}
	if got := query(app.app, func() string { return composerText(app) }); got != "hello" {
		t.Fatalf("composer text = %q, want %q (typed text must not be lost)", got, "hello")
	}
	if n := len(fake.AddCommentBodies()); n != 0 {
		t.Fatalf("AddIssueComment called %d time(s), want 0 (detail was never loaded)", n)
	}

	close(block)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
}

func TestComposerMutationFailureResavesDraftAndToasts(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetAddCommentError(errors.New("network unreachable"))
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "will fail" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "not sent") && containsSubstring(app.statusBar.toast, "draft kept")
	})

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "will fail" })
}

// TestComposerFailedSendMergesTextIntoReopenedComposerForSameTarget
// covers a failed send whose draft re-save would otherwise clobber a
// composer the user has since reopened (before the failure is even
// known) for the very same target and started typing something new
// into: onMutationChanged must merge the failed text into the live
// editor instead of only writing the draft file underneath it.
func TestComposerFailedSendMergesTextIntoReopenedComposerForSameTarget(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	fake.SetAddCommentError(errors.New("network unreachable"))
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "first attempt" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "second attempt" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return composerText(app) == "second attempt" })

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })

	want := "first attempt\n\nsecond attempt"
	waitFor(t, app.app, func() bool { return composerText(app) == want })
}

// TestComposerMutationSuccessNotMaskedByStandingSectionError reproduces
// the bug a naive Store.LastError() read would have: LastError() also
// surfaces a lower-priority, unrelated standing error (a list section's)
// whenever nothing higher-priority masks it, so onMutationChanged must
// key off Store.MutationError() (mutation-specific) instead, or a
// perfectly successful send would be reported as a failure.
func TestComposerMutationSuccessNotMaskedByStandingSectionError(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	teamQuery := gh.BuildSearchQuery(model.SectionKindTeamReview, "", "open")
	fake.SetError(teamQuery, errors.New("team section boom"))
	act(app.app, func() { app.deps.Store.Reload() })
	waitFor(t, app.app, func() bool { return app.deps.Store.LastError() != nil })
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "hi there" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool { return len(fake.AddCommentBodies()) == 1 })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "comment posted" })
}

func TestComposerEditOwnCommentPrefillsAndColonWCallsEditComment(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "Thanks for the PR!" })
	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Edit comment by @octocat"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool { return len(fake.UpdateCommentBodies()) == 1 })
	if got := fake.UpdateCommentBodies()[0]; got != "Thanks for the PR!" {
		t.Fatalf("UpdateIssueComment body = %q, want %q", got, "Thanks for the PR!")
	}
}

// TestComposerEditWithoutTypingLeavesNoDraft covers opening an edit
// composer (which seeds the buffer with the comment's live body via
// Editor.SetText) and closing it again with ":q" without ever typing:
// this must not itself create a draft file, since nothing was actually
// edited.
func TestComposerEditWithoutTypingLeavesNoDraft(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "Thanks for the PR!" })

	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	if n := query(app.app, app.draftCount); n != 0 {
		t.Fatalf("draftCount = %d, want 0 (opening for edit without typing must not create a draft)", n)
	}
}

func TestComposerEOnOthersCommentToastsInsteadOfOpening(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1))) // author "dave", not the viewer
	focusCommentBlock(app)

	sendRune(app.app, 'e')
	if query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatalf("composer opened for a comment the viewer does not own")
	}
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "your own comments") })
}

func TestComposerDeleteWithConfirmCallsDeleteComment(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	sendSpecial(app.app, tcell.KeyEnter) // the Modal's default button is "Delete"
	waitFor(t, app.app, func() bool { return len(fake.DeleteCommentIDs()) == 1 })
	if got, want := fake.DeleteCommentIDs()[0], "IC_1"; got != want {
		t.Fatalf("DeleteIssueComment id = %q, want %q", got, want)
	}
	if got := query(app.app, func() string { return app.overlay }); got != "" {
		t.Fatalf("overlay = %q after confirming delete, want none", got)
	}
}

func TestComposerMentionPopupListsParticipantsAndTabInserts(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1))) // has an issue comment by "dave"
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "hi @d" {
		sendRune(app.app, r)
	}
	waitFor(t, app.app, func() bool { return app.composerEditor.PopupOpen() })

	sendSpecial(app.app, tcell.KeyTab)
	waitFor(t, app.app, func() bool { return composerText(app) == "hi @dave " })
}

func TestComposerExternalEditorRunsScriptAndUpdatesText(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	scriptPath := filepath.Join(t.TempDir(), "fake-editor.sh")
	script := "#!/bin/sh\necho ' EDITED' >> \"$1\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write fake editor script: %v", err)
	}
	act(app.app, func() { app.deps.Config.Editor = scriptPath })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "original" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)

	sendRune(app.app, ':')
	sendRune(app.app, 'e')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return composerText(app) == "original EDITED\n" })
}

// TestComposerAltRuneDoesNotDoubleProcess reproduces a bug in
// routeComposerKey: keys.Normalize expands Alt+rune into [Esc, rune], but
// the composer's key loop fed the *raw* event back into Editor.Handle for
// each of those normalized keys in turn — and Editor.Handle itself
// re-normalizes that same raw event and loops over it again, so a single
// Alt-x delivered [Esc, x] twice (processed as Esc,x,Esc,x instead of
// Esc,x). In insert mode, that means Esc leaves insert (cursor moves left
// one column, onto the last typed char) and the first 'x' deletes it,
// followed by a second, spurious 'x' deleting the character before that
// too.
func TestComposerAltRuneDoesNotDoubleProcess(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "abc" {
		sendRune(app.app, r)
	}
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt))

	waitFor(t, app.app, func() bool { return composerText(app) == "ab" })
}

// TestComposerCtrlWInInsertModeForwardsToTextArea reproduces a bug where
// the composer's own Ctrl-w focus-chord intercepted every Ctrl-w
// regardless of mode, making TextArea's native Ctrl-w (delete the word
// before the cursor) unreachable in insert mode — the one mode vim users
// actually expect it to work in.
func TestComposerCtrlWInInsertModeForwardsToTextArea(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, 'i')
	for _, r := range "hello world" {
		sendRune(app.app, r)
	}
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return composerText(app) != "hello world" })
	if !query(app.app, func() bool { return app.composerEditor.HasFocus() }) {
		t.Fatalf("composer lost focus: Ctrl-w in insert mode must not start the focus chord")
	}
}

// TestComposerClosesWhenPRChangesAndPreservesDraftUnderOriginalTarget
// covers a composer belonging to one pull request when a *different* one
// becomes current (switching in the list, a background refresh landing,
// …): it must close itself immediately (saving its draft under its
// original target's key) rather than risk a later Send posting to
// whichever pull request happens to be open by then.
func TestComposerClosesWhenPRChangesAndPreservesDraftUnderOriginalTarget(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref1 := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref1))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "keep me" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return composerText(app) == "keep me" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: fixtureDetailPR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	act(app.app, func() { app.deps.Store.OpenPR(ref1) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref1
	})
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "keep me" })
}

// TestComposerSendRefusesWhenTargetPRIsNoLongerCurrent exercises
// sendComposer's own defensive check in isolation (bypassing the
// proactive EventPRChanged close this same fix also adds, by mutating
// composerTarget directly), so a Send can never reach the store for a
// pull request other than the one the composer was actually opened for.
func TestComposerSendRefusesWhenTargetPRIsNoLongerCurrent(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "hello" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)

	act(app.app, func() { app.composerTarget.ref = fixtureRef(999) })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
	if n := len(fake.AddCommentBodies()); n != 0 {
		t.Fatalf("AddIssueComment called %d time(s), want 0 (target PR no longer current)", n)
	}
	if !query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatalf("composer closed despite the send being refused")
	}
}

// TestComposerStrayCtrlWChordDoesNotEatNextKeyAfterCtrlC reproduces a
// stuck composerCtrlWPending: starting a "Ctrl-w" chord and then having
// Ctrl-C intercept the next keystroke (here, by showing a confirm dialog
// while a mutation is in flight) bypasses routeComposerKey entirely,
// leaving the chord pending; the very next ordinary key typed into the
// composer afterward would otherwise be misread as that chord's own
// continuation.
func TestComposerStrayCtrlWChordDoesNotEatNextKeyAfterCtrlC(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	if !query(app.app, func() bool { return app.composerEditor.HasFocus() }) {
		t.Fatalf("composer lost focus after cancelling the confirm dialog")
	}
	sendRune(app.app, 'j')
	if !query(app.app, func() bool { return app.composerEditor.HasFocus() }) {
		t.Fatalf("stray Ctrl-w chord ate the next key: composer lost focus after a plain 'j'")
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestCtrlCWhileMutatingClosesHelpOverlayFirst reproduces showConfirm
// being a silent no-op while the help/messages overlay is open (both
// gate on the same App.overlay field): Ctrl-C while a mutation is in
// flight must still get a confirm dialog, closing whatever overlay was
// open first rather than doing nothing at all.
// TestComposerDraftLoadFailureToasts covers a draft that exists on disk
// but cannot be read back (as opposed to simply not existing, which Load
// reports as ok == false with no error at all): opening a composer for
// that target must toast the failure, not just log it at Warn where the
// user would never see it.
func TestComposerDraftLoadFailureToasts(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	dir := t.TempDir()
	draftStore, err := drafts.New(dir)
	if err != nil {
		t.Fatalf("drafts.New: %v", err)
	}
	key := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "issue"}
	if err := draftStore.Save(key, "existing draft"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Deny all access to the whole drafts root after seeding it, so the
	// later Load fails with a genuine permission error rather than
	// "no draft exists".
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	act(app.app, func() { app.deps.Drafts = draftStore })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "draft load failed") })
}

// TestComposerDraftDeleteFailureAfterSendToasts covers a send whose
// otherwise-successful mutation closes the composer and tries to delete
// its now-obsolete draft, but the deletion itself fails: without a
// toast, a stale draft could resurrect text that was already posted
// successfully, with no visible warning that it happened.
func TestComposerDraftDeleteFailureAfterSendToasts(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))

	dir := t.TempDir()
	draftStore, err := drafts.New(dir)
	if err != nil {
		t.Fatalf("drafts.New: %v", err)
	}
	key := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "issue"}
	if err := draftStore.Save(key, "existing draft"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one draft subdirectory, got %v (err=%v)", entries, err)
	}
	subDir := filepath.Join(dir, entries[0].Name())
	// Read+execute only, no write: os.Remove on a file inside subDir
	// needs write access to subDir itself, which this denies, while
	// Load (a plain read) still succeeds.
	if err := os.Chmod(subDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o700) })
	act(app.app, func() { app.deps.Drafts = draftStore })

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	for _, r := range "posting now" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))

	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "draft delete failed") })

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

func TestCtrlCWhileMutatingClosesHelpOverlayFirst(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

func TestComposerCtrlWJKMoveFocus(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'k')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.prView })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModCtrl))
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.composerEditor.HasFocus() })
}

func TestCtrlCWhileMutatingAsksForConfirmation(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	// Cancel: the app must not have quit (its Run goroutine is still
	// alive, which newTestApp's own cleanup already checks at the end of
	// the test) and the confirm overlay closes.
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestQKeyWhileMutatingAsksForConfirmation covers the global "q" key
// (global.quit), which dispatched straight to App.quit() and bypassed
// quitWithConfirmIfMutating entirely — Ctrl-C was the only quit path that
// asked for confirmation while a mutation was in flight.
func TestQKeyWhileMutatingAsksForConfirmation(t *testing.T) {
	app, done, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	select {
	case <-done:
		t.Fatal("app quit immediately instead of asking for confirmation")
	case <-time.After(50 * time.Millisecond):
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

// TestColonQWhileMutatingAsksForConfirmation covers the ":q"/":quit"
// command line, which also dispatched straight to App.quit().
func TestColonQWhileMutatingAsksForConfirmation(t *testing.T) {
	app, done, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	block := make(chan struct{})
	fake.SetAddCommentBlock(block)
	act(app.app, func() { app.deps.Store.AddComment("first") })
	waitFor(t, app.app, func() bool { return app.deps.Store.Mutating() })

	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	select {
	case <-done:
		t.Fatal("app quit immediately instead of asking for confirmation")
	case <-time.After(50 * time.Millisecond):
	}

	close(block)
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
}

func TestComposerDraftCountShowsInStatusBar(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	if n := query(app.app, app.draftCount); n != 0 {
		t.Fatalf("draftCount = %d before typing, want 0", n)
	}

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	sendRune(app.app, 'x')
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return app.draftCount() == 1 })
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.right, "✎ 1") })
}

// TestMentionCandidatesMergesRepositoryUsersAfterParticipantsDeduped covers
// mentionCandidates appending Store.MentionableUsers() after the PR-derived
// participants: fixtureDetailPR's own participants (alice, bob, carol,
// dave, in that order — see mentionCandidates' own doc comment) come
// first, then the repository's mentionable users, with a repeat of an
// already-listed participant (carol) skipped rather than duplicated.
func TestMentionCandidatesMergesRepositoryUsersAfterParticipantsDeduped(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	fake.SetMentionableUsers(ref.Repo, []model.User{
		{Login: "carol", Name: "Carol Repeated"}, // already a participant: must not duplicate
		{Login: "erin"},
		{Login: "frank"},
	})
	openDetailForComposer(t, app, fake, pr)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 3 })

	got := query(app.app, func() []string {
		var logins []string
		for _, c := range app.mentionCandidates("") {
			logins = append(logins, c.Login)
		}
		return logins
	})
	want := []string{"alice", "bob", "carol", "dave", "erin", "frank"}
	if len(got) != len(want) {
		t.Fatalf("mentionCandidates logins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mentionCandidates logins = %v, want %v", got, want)
		}
	}
}

// TestMentionCandidatesBackfillsNameFromMentionableUsers covers a
// participant added with no Name (fixtureDetailPR's "bob", a
// ReviewRequests entry — model.Reviewer has no Name field at all) getting
// its Name filled in from Store.MentionableUsers() once that repository's
// fetch resolves, as a single candidate — not a second, duplicate entry —
// still in its original participant-priority position.
func TestMentionCandidatesBackfillsNameFromMentionableUsers(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	fake.SetMentionableUsers(ref.Repo, []model.User{{Login: "bob", Name: "Bob Smith"}})
	openDetailForComposer(t, app, fake, pr)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	got := query(app.app, func() []editor.Candidate { return app.mentionCandidates("") })
	var bobCount int
	var bobName string
	for _, c := range got {
		if c.Login == "bob" {
			bobCount++
			bobName = c.Name
		}
	}
	if bobCount != 1 {
		t.Fatalf("candidate list has %d entries for bob, want exactly 1", bobCount)
	}
	if bobName != "Bob Smith" {
		t.Fatalf("bob's candidate Name = %q, want it backfilled to %q", bobName, "Bob Smith")
	}
}

// TestMentionCandidatesFuzzyMatchesSubsequences covers "hro" fuzzy-matching
// "hirano00o" (F5 / docs/DESIGN.md: sahilm/fuzzy-based completion) — a
// plain prefix match alone would reject this.
func TestMentionCandidatesFuzzyMatchesSubsequences(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetMentionableUsers(ref.Repo, []model.User{{Login: "hirano00o"}})
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	got := query(app.app, func() []editor.Candidate { return app.mentionCandidates("hro") })
	if len(got) != 1 || got[0].Login != "hirano00o" {
		t.Fatalf("mentionCandidates(%q) = %+v, want a single hirano00o match", "hro", got)
	}
}

// TestMentionCandidatesFuzzyMatchIsCaseInsensitive covers an uppercase
// query still matching a lowercase login.
func TestMentionCandidatesFuzzyMatchIsCaseInsensitive(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetMentionableUsers(ref.Repo, []model.User{{Login: "hirano00o"}})
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref))
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	got := query(app.app, func() []editor.Candidate { return app.mentionCandidates("HIR") })
	if len(got) != 1 || got[0].Login != "hirano00o" {
		t.Fatalf("mentionCandidates(%q) = %+v, want a single hirano00o match", "HIR", got)
	}
}

// TestMentionCandidatesFuzzyRanksExactPrefixFirst covers a candidate whose
// login starts with the query ranking ahead of one where the query only
// appears as a scattered subsequence.
func TestMentionCandidatesFuzzyRanksExactPrefixFirst(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetMentionableUsers(ref.Repo, []model.User{{Login: "malice"}})
	openDetailForComposer(t, app, fake, fixtureDetailPR(ref)) // author's login is "alice"
	waitFor(t, app.app, func() bool { return len(app.deps.Store.MentionableUsers()) == 1 })

	got := query(app.app, func() []editor.Candidate { return app.mentionCandidates("ali") })
	aliceIdx, maliceIdx := -1, -1
	for i, c := range got {
		switch c.Login {
		case "alice":
			aliceIdx = i
		case "malice":
			maliceIdx = i
		}
	}
	if aliceIdx == -1 || maliceIdx == -1 {
		t.Fatalf("mentionCandidates(%q) = %+v, want both alice and malice", "ali", got)
	}
	if aliceIdx > maliceIdx {
		t.Fatalf("alice (exact prefix) ranked after malice (scattered match): %+v", got)
	}
}

// TestMentionCandidatesHandlesNilMentionableUsers covers no repository
// mentionable-users fetch having resolved yet (Store.MentionableUsers()
// returns nil): mentionCandidates must not panic and returns just the
// PR-derived participants.
func TestMentionCandidatesHandlesNilMentionableUsers(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	openDetailForComposer(t, app, fake, fixtureDetailPR(fixtureRef(1)))

	got := query(app.app, func() []editor.Candidate { return app.mentionCandidates("") })
	if len(got) == 0 {
		t.Fatal("mentionCandidates returned no participants at all")
	}
}
