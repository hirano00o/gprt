package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
)

// reviewThreadsPR extends fixtureFilesPR (files_test.go) with review
// threads placed on pkg/example.go's own lines (see exampleGoPatch): a
// single own comment (t-single, line 2, the added comment line), a thread
// with two own comments and one not-own (t-multi, line 3), a resolved
// thread (t-resolved, line 4), and a thread with one still-PENDING own
// comment (t-pending, line 5) — every line here is a RIGHT-side context or
// add line the diff actually has (see exampleGoPatch's own line numbering).
func reviewThreadsPR(ref model.PRRef) model.PullRequest {
	pr := fixtureFilesPR(ref)
	now := time.Now()
	pr.ReviewThreads = []model.ReviewThread{
		{
			ID: "t-single", Path: "pkg/example.go", Line: 2, Side: model.DiffSideRight,
			ViewerCanResolve: true, ViewerCanReply: true,
			Comments: []model.ReviewComment{
				{ID: "RC_own", Author: model.User{Login: "octocat"}, Body: "own comment", CreatedAt: now, State: model.ReviewCommentStateSubmitted, ViewerCanUpdate: true, ViewerCanDelete: true},
			},
		},
		{
			ID: "t-multi", Path: "pkg/example.go", Line: 3, Side: model.DiffSideRight,
			ViewerCanResolve: true,
			Comments: []model.ReviewComment{
				{ID: "RC_a", Author: model.User{Login: "octocat"}, Body: "first own\nmore text", CreatedAt: now, State: model.ReviewCommentStateSubmitted, ViewerCanUpdate: true, ViewerCanDelete: true},
				{ID: "RC_b", Author: model.User{Login: "octocat"}, Body: "second own", CreatedAt: now.Add(time.Minute), State: model.ReviewCommentStateSubmitted, ViewerCanUpdate: true, ViewerCanDelete: true},
				{ID: "RC_other", Author: model.User{Login: "dave"}, Body: "not mine", CreatedAt: now, State: model.ReviewCommentStateSubmitted},
			},
		},
		{
			ID: "t-resolved", Path: "pkg/example.go", Line: 4, Side: model.DiffSideRight,
			IsResolved: true, ViewerCanUnresolve: true,
			Comments: []model.ReviewComment{{ID: "RC_r", Author: model.User{Login: "alice"}, Body: "resolved comment", CreatedAt: now, State: model.ReviewCommentStateSubmitted}},
		},
		{
			ID: "t-pending", Path: "pkg/example.go", Line: 5, Side: model.DiffSideRight,
			ViewerCanResolve: true,
			Comments:         []model.ReviewComment{{ID: "RC_pending", Author: model.User{Login: "octocat"}, Body: "still pending", CreatedAt: now, State: model.ReviewCommentStatePending, ViewerCanUpdate: true, ViewerCanDelete: true}},
		},
	}
	return pr
}

// openFilesTabWithReviewThreads opens reviewThreadsPR, switches to the
// Files tab, and focuses the diff — the common setup for every test in
// this file.
func openFilesTabWithReviewThreads(t *testing.T) (*App, *fakeGitHub, model.PRRef) {
	t.Helper()
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: reviewThreadsPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	return app, fake, ref
}

// moveDiffCursorToThread walks NextThread() from the top until the cursor
// lands on id, failing the test if it never does.
func moveDiffCursorToThread(t *testing.T, app *App, id string) {
	t.Helper()
	act(app.app, func() { app.diffView.MoveTop() })
	for range 20 {
		th := query(app.app, func() model.ReviewThread { th, _ := app.diffView.CursorThread(); return th })
		if th.ID == id {
			return
		}
		act(app.app, func() { app.diffView.NextThread() })
	}
	t.Fatalf("could not move the diff cursor to thread %q", id)
}

// TestDiffCOnALineOpensLineCommentComposer covers "c" on a plain diff line
// (not a thread): the composer's title and draft key both name the line's
// path/side/number.
func TestDiffCOnALineOpensLineCommentComposer(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)

	// Hunk 2's own "+newCall()" line (RIGHT, 11): no thread anchored there.
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	wantTitle := "Comment on pkg/example.go:11 (RIGHT)"
	if got := query(app.app, func() string { return app.composerTitle.GetText(false) }); got != wantTitle {
		t.Fatalf("composer title = %q, want %q", got, wantTitle)
	}
	wantAnchor := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "pkg/example.go:RIGHT:11"}
	if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantAnchor {
		t.Fatalf("composer draft key = %+v, want %+v", got, wantAnchor)
	}
}

// TestDiffVRangeOpensRangeCommentComposerWithRangeTitle covers a V range
// selection within one side of one hunk: the composer's title and draft
// key both name the start-end range.
func TestDiffVRangeOpensRangeCommentComposerWithRangeTitle(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)

	// Hunk 1's "// Example..." add line (2) through "func Example() {"
	// (3): a two-line, single-sided (RIGHT) range. The "j" in between
	// lands on t-single's own thread header row (a selectable row that is
	// not a diff line) — Selection() tracks the last *line* row actually
	// visited (curLineHunk/curLineLine), so passing over it does not
	// disturb the range being built.
	act(app.app, func() {
		app.diffView.MoveTop()
		app.diffView.MoveBy(1) // "// Example..." (NewNo 2)
	})
	sendRune(app.app, 'V')
	sendRune(app.app, 'j') // t-single's thread header row
	sendRune(app.app, 'j') // "func Example() {" (NewNo 3)
	waitFor(t, app.app, func() bool {
		s, ok := app.diffView.Selection()
		return ok && len(s) == 2
	})

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	wantTitle := "Comment on pkg/example.go:2-3 (RIGHT)"
	if got := query(app.app, func() string { return app.composerTitle.GetText(false) }); got != wantTitle {
		t.Fatalf("composer title = %q, want %q", got, wantTitle)
	}
	wantAnchor := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "pkg/example.go:RIGHT:2-3"}
	if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantAnchor {
		t.Fatalf("composer draft key = %+v, want %+v", got, wantAnchor)
	}
	if query(app.app, func() bool { return app.diffView.InVisual() }) {
		t.Error("opening a composer from a visual selection must exit visual mode")
	}
}

// TestDiffVMixedSideRangeRefusedWithToastAndKeepsSelection covers hunk 2's
// own del+add pair (oldCall()/newCall()): a range spanning both is refused
// with a toast, and the selection is left exactly as it was so the user
// can adjust it.
func TestDiffVMixedSideRangeRefusedWithToastAndKeepsSelection(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)

	act(app.app, func() {
		app.diffView.MoveBottom()
		app.diffView.MoveBy(-1) // hunk 2's "-oldCall()" line
	})
	sendRune(app.app, 'V')
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool {
		s, ok := app.diffView.Selection()
		return ok && len(s) == 2
	})

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "one side only") })

	if composerOpen(app) {
		t.Fatal("a mixed-side range must not open a composer")
	}
	if !query(app.app, func() bool { return app.diffView.InVisual() }) {
		t.Error("a mixed-side range's own selection must stay active after being refused")
	}
}

// TestDiffCOnAThreadRowRepliesInstead covers docs/KEYBINDINGS.md footnote
// 2: "c" on an existing thread behaves like "r", not "new comment" —
// opening a reply composer instead of a second, unrelated thread on the
// same line.
func TestDiffCOnAThreadRowRepliesInstead(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	wantTitle := "Reply to @octocat"
	if got := query(app.app, func() string { return app.composerTitle.GetText(false) }); got != wantTitle {
		t.Fatalf("composer title = %q, want %q", got, wantTitle)
	}
	wantKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindReply, Anchor: "t-single"}
	if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantKey {
		t.Fatalf("composer draft key = %+v, want %+v", got, wantKey)
	}
}

// TestDiffROnAThreadRowOpensReplyComposer covers the dedicated "r" key.
func TestDiffROnAThreadRowOpensReplyComposer(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-multi")

	sendRune(app.app, 'r')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Reply to @octocat"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}
}

// TestDiffROnANonThreadRowToasts covers "on a non-thread row r/x are no-ops
// with a short toast".
func TestDiffROnANonThreadRowToasts(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'r')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not on a comment thread") })
	if composerOpen(app) {
		t.Fatal("r on a non-thread row must not open a composer")
	}
}

// TestDiffXTogglesResolvedBothWays covers "x" on an unresolved thread
// (ResolveThread) and on an already-resolved one (UnresolveThread).
func TestDiffXTogglesResolvedBothWays(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)

	moveDiffCursorToThread(t, app, "t-single")
	sendRune(app.app, 'x')
	waitFor(t, app.app, func() bool { return len(fake.ResolveThreadIDs()) == 1 })
	if got := fake.ResolveThreadIDs()[0]; got != "t-single" {
		t.Fatalf("ResolveThread id = %q, want %q", got, "t-single")
	}

	moveDiffCursorToThread(t, app, "t-resolved")
	sendRune(app.app, 'x')
	waitFor(t, app.app, func() bool { return len(fake.UnresolveThreadIDs()) == 1 })
	if got := fake.UnresolveThreadIDs()[0]; got != "t-resolved" {
		t.Fatalf("UnresolveThread id = %q, want %q", got, "t-resolved")
	}
}

// TestDiffXOnANonThreadRowToasts mirrors TestDiffROnANonThreadRowToasts for
// "x".
func TestDiffXOnANonThreadRowToasts(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'x')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not on a comment thread") })
	if len(fake.ResolveThreadIDs())+len(fake.UnresolveThreadIDs()) != 0 {
		t.Fatal("x on a non-thread row must not call Resolve/UnresolveThread")
	}
}

// TestDiffEOnANonThreadRowToasts mirrors TestDiffROnANonThreadRowToasts
// for "e".
func TestDiffEOnANonThreadRowToasts(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not on a comment thread") })
	if composerOpen(app) {
		t.Fatal("e on a non-thread row must not open a composer")
	}
}

// TestDiffDOnANonThreadRowToasts mirrors TestDiffROnANonThreadRowToasts
// for "d".
func TestDiffDOnANonThreadRowToasts(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not on a comment thread") })
	if len(fake.DeleteReviewCommentIDs()) != 0 {
		t.Fatal("d on a non-thread row must not call DeleteReviewComment")
	}
}

// TestDiffCWithNoFileOpenToasts covers "c" toasting "no file open" (like
// "C" already did) instead of doing nothing when nothing is open at all.
func TestDiffCWithNoFileOpenToasts(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no file open") })
	if composerOpen(app) {
		t.Fatal("c with no file open must not open a composer")
	}
}

// TestDiffCCommentFileOpensComposer covers "C" on a file with a patch.
func TestDiffCCommentFileOpensComposer(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)

	sendRune(app.app, 'C')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Comment on file pkg/example.go"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}
	wantKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:pkg/example.go"}
	if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantKey {
		t.Fatalf("composer draft key = %+v, want %+v", got, wantKey)
	}
}

// TestDiffCCommentFileOnABinaryFileToasts covers "refused with a toast
// when the file has no patch".
func TestDiffCCommentFileOnABinaryFileToasts(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.openFile("assets/image.png") })
	waitFor(t, app.app, func() bool { return app.currentFilePath == "assets/image.png" })

	sendRune(app.app, 'C')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no patch") })
	if composerOpen(app) {
		t.Fatal("C on a file with no patch must not open a composer")
	}
}

// typeAndSend types text into the open composer's editor (already in
// normal mode) and sends it (Ctrl-s).
func typeAndSend(app *App, text string) {
	sendRune(app.app, 'i')
	for _, r := range text {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEsc)
	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModCtrl))
}

// TestSendModeMenuAppearsWhenNoPendingReviewAndAddSingleSendsImmediately
// covers Store.CanSendSingle() == true (no pending review yet): sending a
// new line comment offers the choice menu, and picking "Add single
// comment" (index 0) calls gh.Client.AddReviewNow (Store.CommentOnLines
// with SendSingle).
func TestSendModeMenuAppearsWhenNoPendingReviewAndAddSingleSendsImmediately(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() }) // "+newCall()", no thread

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "single please")

	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	if got := query(app.app, func() int { return app.choiceMenu.GetItemCount() }); got != 2 {
		t.Fatalf("choice menu item count = %d, want 2", got)
	}
	// The composer itself stays open (with its text intact) behind the
	// menu — only a chosen item's own successful send closes it; Esc/q
	// (see TestSendModeMenuEscCancelsAndKeepsComposerText) must return to
	// it with nothing lost.
	if !query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatal("the composer must stay open behind the send-mode menu")
	}

	sendSpecial(app.app, tcell.KeyEnter) // item 0: "Add single comment"
	waitFor(t, app.app, func() bool { return len(fake.AddReviewNowCalls()) == 1 })
	if got := fake.AddReviewNowCalls()[0].threads[0].Body; got != "single please" {
		t.Fatalf("AddReviewNow thread body = %q, want %q", got, "single please")
	}
	if fake.CreatePendingReviewCalls() != 0 || len(fake.AddReviewThreadCalls()) != 0 {
		t.Error("SendSingle must not create a pending review or call AddReviewThread")
	}
}

// TestSendModeMenuAddToReviewCreatesPendingReviewThenAddsThread covers
// picking "Add to review" (index 1) when none exists yet:
// CreatePendingReview then AddReviewThread.
func TestSendModeMenuAddToReviewCreatesPendingReviewThenAddsThread(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "add to review please")

	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	act(app.app, func() { app.choiceMenu.SetCurrentItem(1) })
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.AddReviewThreadCalls()) == 1 })
	if fake.CreatePendingReviewCalls() != 1 {
		t.Errorf("CreatePendingReview calls = %d, want 1", fake.CreatePendingReviewCalls())
	}
	if got := fake.AddReviewThreadCalls()[0].Body; got != "add to review please" {
		t.Fatalf("AddReviewThread body = %q, want %q", got, "add to review please")
	}
	if len(fake.AddReviewNowCalls()) != 0 {
		t.Error("SendToReview must not call AddReviewNow")
	}
}

// TestSendModeMenuEscCancelsAndKeepsComposerText covers "Esc/q cancels and
// returns to the composer with text intact".
func TestSendModeMenuEscCancelsAndKeepsComposerText(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "do not send yet")
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	if !query(app.app, func() bool { return composerOpen(app) }) {
		t.Fatal("cancelling the send-mode menu must return to the composer")
	}
	if got := query(app.app, func() string { return composerText(app) }); got != "do not send yet" {
		t.Fatalf("composer text = %q after cancelling the menu, want it intact", got)
	}
	if len(fake.AddReviewNowCalls())+len(fake.AddReviewThreadCalls()) != 0 {
		t.Fatal("cancelling the send-mode menu must not call any Store method")
	}
}

// TestPerformReviewSendRefusesWhenTargetPRIsNoLongerCurrent covers
// performReviewSend's own defensive CurrentRef() check in isolation,
// mirroring TestComposerSendRefusesWhenTargetPRIsNoLongerCurrent's own
// technique: the send-mode menu's onChoose closure captures its target by
// value at the moment the menu was shown, so mutating a.composerTarget
// afterward (as that other test does) would not reach it — this calls
// performReviewSend directly with a target whose ref the store's own
// CurrentRef() no longer matches, the same shape a real
// (menu-survives-an-EventPRChanged-somehow) race would produce.
func TestPerformReviewSendRefusesWhenTargetPRIsNoLongerCurrent(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)

	target := composerTarget{
		kind: composerKindLineComment, ref: fixtureRef(999), path: "pkg/example.go",
		anchor: diff.Range{StartSide: model.DiffSideRight, Side: model.DiffSideRight, Line: 11},
	}
	got := query(app.app, func() bool { return app.performReviewSend(target, "should not be sent", store.SendSingle) })
	if got {
		t.Fatal("performReviewSend = true, want false (target PR no longer current)")
	}
	if n := len(fake.AddReviewNowCalls()); n != 0 {
		t.Fatalf("AddReviewNow called %d time(s), want 0", n)
	}
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "different pull request") })

	if cur, ok := app.deps.Store.CurrentRef(); !ok || cur != ref {
		t.Fatalf("precondition: CurrentRef() = (%v, %v), want (%v, true)", cur, ok, ref)
	}
}

// TestEventPRChangedClosesOpenSendModeMenu covers closeComposerIfWrongPR
// also cancelling an open send-mode menu, not just the composer pane
// underneath it, when the current pull request changes while the menu is
// showing.
func TestEventPRChangedClosesOpenSendModeMenu(t *testing.T) {
	app, fake, ref1 := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "keep me")
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: fixtureDetailPR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool { return app.overlay == "" && !composerOpen(app) })

	act(app.app, func() { app.deps.Store.OpenPR(ref1) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref1
	})
	if n := len(fake.AddReviewNowCalls()); n != 0 {
		t.Fatalf("AddReviewNow called %d time(s), want 0", n)
	}
}

// TestSendModeMenuSkippedWhenPendingReviewAlreadyExists covers "If a
// pending review exists, send with SendToReview directly, no menu."
func TestSendModeMenuSkippedWhenPendingReviewAlreadyExists(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_existing", State: model.ReviewStatePending}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	act(app.app, func() { app.diffView.MoveBottom() })
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "goes straight to the review")

	waitFor(t, app.app, func() bool { return len(fake.AddReviewThreadCalls()) == 1 })
	if app.overlay == "choice" {
		t.Fatal("no menu must be shown once a pending review already exists")
	}
	if got := fake.AddReviewThreadCalls()[0].PullRequestReviewID; got != "PVR_existing" {
		t.Fatalf("AddReviewThread pullRequestReviewId = %q, want the existing pending review's own ID", got)
	}
}

// TestReplySendGoesThroughAddThreadReply covers a reply's own Send path.
func TestReplySendGoesThroughAddThreadReply(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'r')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "thanks, fixed")

	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	sendSpecial(app.app, tcell.KeyEnter) // "Add single comment"

	waitFor(t, app.app, func() bool { return len(fake.AddThreadReplyCalls()) == 1 })
	if got := fake.AddThreadReplyCalls()[0]; got.threadID != "t-single" || got.body != "thanks, fixed" {
		t.Fatalf("AddThreadReply call = %+v, want threadID=t-single body=%q", got, "thanks, fixed")
	}
}

// TestSendModeMenuCoercedSendShowsNoticeToastNotSuccessToast covers a
// SendSingle choice from the menu that the store itself coerces to
// SendToReview by the time the mutation actually runs (a pending review
// appeared — here, simulated by a network refresh landing — after the
// menu was shown but before the user picked an item): the toast the user
// actually sees once the mutation finishes must be the coercion notice
// (theme.Info), not the generic "comment posted" (theme.Success) success
// toast, which onMutationChanged would otherwise show a moment later in
// the same synchronous EventNotice-then-EventMutationChanged sequence.
func TestSendModeMenuCoercedSendShowsNoticeToastNotSuccessToast(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "single please")
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })

	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_existing"}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	sendSpecial(app.app, tcell.KeyEnter) // "Add single comment"

	waitFor(t, app.app, func() bool { return len(fake.AddReviewThreadCalls()) == 1 })
	if len(fake.AddReviewNowCalls()) != 0 {
		t.Fatal("a coerced send must go through AddReviewThread, never AddReviewNow")
	}
	waitFor(t, app.app, func() bool { return !app.deps.Store.Mutating() })
	if got := app.statusBar.toast; !containsSubstring(got, "pending review") {
		t.Fatalf("final toast = %q, want the coercion notice, not a generic success toast", got)
	}
}

// TestEditReviewCommentSuccessToastSaysUpdated covers the review-comment
// edit success toast: onMutationChanged's verb switch originally only
// recognised composerKindEdit (issue comments), so editing a review
// comment showed "comment posted" instead of "comment updated".
func TestEditReviewCommentSuccessToastSaysUpdated(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return app.statusBar.toast == "comment updated" })
}

// TestEditReviewCommentSingleCandidateOpensDirectly covers "e" on a thread
// with exactly one editable comment.
func TestEditReviewCommentSingleCandidateOpensDirectly(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Edit review comment"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}
	if got, want := query(app.app, func() string { return composerText(app) }), "own comment"; got != want {
		t.Fatalf("composer text = %q, want it prefilled with %q", got, want)
	}

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return len(fake.UpdateReviewCommentCalls()) == 1 })
	if got := fake.UpdateReviewCommentCalls()[0]; got.id != "RC_own" {
		t.Fatalf("UpdateReviewComment id = %q, want %q", got.id, "RC_own")
	}
}

// TestEditReviewCommentNoCandidatesToasts covers a thread with no comment
// the viewer may edit.
func TestEditReviewCommentNoCandidatesToasts(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-resolved") // alice's own comment, not the viewer's

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "your own comments") })
	if composerOpen(app) {
		t.Fatal("e must not open a composer when the viewer owns none of the thread's comments")
	}
}

// TestEditReviewCommentSeveralCandidatesShowsChoiceMenu covers a thread
// with several editable comments: the choice menu lists them, and picking
// one opens its edit composer.
func TestEditReviewCommentSeveralCandidatesShowsChoiceMenu(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-multi")

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	if got := query(app.app, func() int { return app.choiceMenu.GetItemCount() }); got != 2 {
		t.Fatalf("choice menu item count = %d, want 2 (RC_a and RC_b are the viewer's own; RC_other is not)", got)
	}

	act(app.app, func() { app.choiceMenu.SetCurrentItem(1) }) // "second own"
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if got, want := query(app.app, func() string { return composerText(app) }), "second own"; got != want {
		t.Fatalf("composer text = %q, want %q", got, want)
	}
}

// TestDeleteReviewCommentSingleCandidateConfirmsThenDeletes covers "d" on a
// thread with exactly one deletable comment.
func TestDeleteReviewCommentSingleCandidateConfirmsThenDeletes(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-single")

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Delete"
	waitFor(t, app.app, func() bool { return len(fake.DeleteReviewCommentIDs()) == 1 })
	if got := fake.DeleteReviewCommentIDs()[0]; got != "RC_own" {
		t.Fatalf("DeleteReviewComment id = %q, want %q", got, "RC_own")
	}
}

// TestDeleteReviewCommentNoCandidatesToasts mirrors
// TestEditReviewCommentNoCandidatesToasts for "d".
func TestDeleteReviewCommentNoCandidatesToasts(t *testing.T) {
	app, fake, _ := openFilesTabWithReviewThreads(t)
	moveDiffCursorToThread(t, app, "t-resolved")

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "your own comments") })
	if len(fake.DeleteReviewCommentIDs()) != 0 {
		t.Fatal("d must not call DeleteReviewComment when the viewer owns none of the thread's comments")
	}
}

// TestPendingListShowsPendingCommentsAndDrafts covers "p"'s contents: the
// PENDING comment (t-pending's RC_pending) first, then a saved draft — and
// its header, from PendingReview().Body.
func TestPendingListShowsPendingCommentsAndDrafts(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_1", Body: "overall looks fine"}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	draftKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:pkg/renamed_new.go"}
	if err := app.deps.Drafts.Save(draftKey, "a whole-file thought"); err != nil {
		t.Fatalf("Drafts.Save: %v", err)
	}

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	if got, want := query(app.app, func() int { return len(app.pendingListEntries) }), 2; got != want {
		t.Fatalf("pending list has %d entries, want %d (1 pending comment + 1 draft)", got, want)
	}
	first := query(app.app, func() string { main, _ := app.pendingListView.GetItemText(0); return main })
	if !containsSubstring(first, "still pending") || !containsSubstring(first, "PENDING") {
		t.Errorf("first entry = %q, want the pending comment with a PENDING tag", first)
	}
	second := query(app.app, func() string { main, _ := app.pendingListView.GetItemText(1); return main })
	if !containsSubstring(second, "a whole-file thought") || !containsSubstring(second, "DRAFT") {
		t.Errorf("second entry = %q, want the file draft with a DRAFT tag", second)
	}
	if title := query(app.app, func() string { return app.pendingListView.GetTitle() }); !containsSubstring(title, "overall looks fine") {
		t.Errorf("pending list title = %q, want the pending review's own body summary", title)
	}
}

// TestPendingListEnterOnAPendingCommentOpensEditComposer covers Enter on a
// pending review comment.
func TestPendingListEnterOnAPendingCommentOpensEditComposer(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_1"}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	if app.overlay == "pending" {
		t.Error("the pending list must close once an item is opened")
	}
	if got, want := query(app.app, func() string { return composerText(app) }), "still pending"; got != want {
		t.Fatalf("composer text = %q, want the pending comment's own body %q", got, want)
	}
}

// TestPendingListDDiscardsWholeReviewAfterConfirm covers "D".
func TestPendingListDDiscardsWholeReviewAfterConfirm(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_1"}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	sendRune(app.app, 'D')
	waitFor(t, app.app, func() bool { return app.overlay == "pendingConfirm" })
	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Delete"

	waitFor(t, app.app, func() bool { return len(fake.DeletePendingReviewCalls()) == 1 })
	if got := fake.DeletePendingReviewCalls()[0]; got != "PVR_1" {
		t.Fatalf("DeletePendingReview id = %q, want %q", got, "PVR_1")
	}
	if got := query(app.app, func() string { return app.overlay }); got != "pending" {
		t.Fatalf("overlay = %q after confirming discard, want back on the pending list", got)
	}
}

// TestPendingListDDeletesADraftAfterConfirm covers "d" on a draft entry —
// reviewThreadsPR's own "still pending" comment (t-pending) is entry 0, so
// the saved draft (entry 1, appended after every pending comment — see
// rebuildPendingList) is the one moved to and deleted here.
func TestPendingListDDeletesADraftAfterConfirm(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)
	draftKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:pkg/renamed_new.go"}
	if err := app.deps.Drafts.Save(draftKey, "delete me"); err != nil {
		t.Fatalf("Drafts.Save: %v", err)
	}

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" && len(app.pendingListEntries) == 2 })

	sendRune(app.app, 'j')
	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "pendingConfirm" })
	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Delete"

	waitFor(t, app.app, func() bool { return app.overlay == "pending" && len(app.pendingListEntries) == 1 })
	if _, ok, err := app.deps.Drafts.Load(draftKey); ok || err != nil {
		t.Fatalf("Drafts.Load after delete = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

// TestPendingListDeletingALineCommentDraftRefreshesTheGutterMarker covers
// "d" on a line/range comment draft entry also clearing its own diff
// gutter marker (widget.DiffFile.DraftLines) — not only the composer's
// own onComposerChange/close path did that.
func TestPendingListDeletingALineCommentDraftRefreshesTheGutterMarker(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: reviewThreadsPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})

	draftKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "pkg/example.go:RIGHT:11"}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })
	act(app.app, func() { app.diffView.MoveBottom() }) // hunk 2's "+newCall()" (RIGHT, 11)
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	sendRune(app.app, 'x')
	sendSpecial(app.app, tcell.KeyEsc)
	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter) // keep the draft, do not send it
	waitFor(t, app.app, func() bool { return !composerOpen(app) })
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "✎")
	})

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	act(app.app, func() {
		for i, e := range app.pendingListEntries {
			if e.isDraft && e.draft.Key == draftKey {
				app.pendingListView.SetCurrentItem(i)
			}
		}
	})
	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "pendingConfirm" })
	confirmYes(app.app) // "Cancel" is the Modal's default button; Tab then Enter reaches "Delete"
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(diffText(app, screen), "✎")
	})
}

// TestFailedLineCommentSendRestoresDraftGutterMarker covers a send failure
// re-saving the draft (onMutationChanged's own failure branch) also
// refreshing the diff gutter's own "✎" marker: the composer already
// closed (and deleted the draft) the instant the mutation was enqueued —
// see sendComposer's own doc comment — so the marker briefly disappears,
// and must reappear once the re-save actually happens, not only on some
// later, unrelated refresh.
func TestFailedLineCommentSendRestoresDraftGutterMarker(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: reviewThreadsPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())
	fake.addReviewNowErr = errors.New("network unreachable")

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})
	waitFor(t, app.app, func() bool { return app.deps.Store.FilesState().Highlighting == 0 })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })
	act(app.app, func() { app.diffView.MoveBottom() }) // "+newCall()" (RIGHT, 11), no thread

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "will fail")
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })
	sendSpecial(app.app, tcell.KeyEnter) // "Add single comment"

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "not sent") })
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "✎")
	})
}

// TestPendingListPartialDraftListErrorStillShowsGoodDrafts covers
// rebuildPendingList using Drafts.List's own returned list regardless of
// its error: a single corrupt draft file must not hide every other,
// perfectly valid draft (drafts.Store.List already tolerates and skips a
// file that fails to decode, aggregating the failure into its own
// returned error rather than failing the whole call).
func TestPendingListPartialDraftListErrorStillShowsGoodDrafts(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)

	dir := t.TempDir()
	draftStore, err := drafts.New(dir)
	if err != nil {
		t.Fatalf("drafts.New: %v", err)
	}
	goodKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:pkg/renamed_new.go"}
	badKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:other.go"}
	if err := draftStore.Save(goodKey, "good draft"); err != nil {
		t.Fatalf("Save(good): %v", err)
	}
	if err := draftStore.Save(badKey, "bad draft"); err != nil {
		t.Fatalf("Save(bad): %v", err)
	}

	// Corrupt the "bad" draft's own file directly, bypassing the Store
	// API entirely, so List() has to skip exactly one of the two files
	// it finds.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one draft subdirectory, got %v (err=%v)", entries, err)
	}
	subDir := filepath.Join(dir, entries[0].Name())
	files, err := os.ReadDir(subDir)
	if err != nil || len(files) != 2 {
		t.Fatalf("expected exactly two draft files, got %v (err=%v)", files, err)
	}
	for _, f := range files {
		path := filepath.Join(subDir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if strings.Contains(string(data), "bad draft") {
			if err := os.WriteFile(path, []byte("not valid json"), 0o600); err != nil {
				t.Fatalf("WriteFile(%s): %v", path, err)
			}
		}
	}

	act(app.app, func() { app.deps.Drafts = draftStore })

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	// reviewThreadsPR's own "still pending" comment (t-pending) is always
	// entry 0 (see rebuildPendingList's own comments-then-drafts order);
	// the good draft here is entry 1 — the corrupt one is skipped, not
	// the whole drafts list discarded.
	if got := query(app.app, func() int { return len(app.pendingListEntries) }); got != 2 {
		t.Fatalf("pending list has %d entries, want 2 (1 pending comment + 1 good draft)", got)
	}
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "could not be read") })
}

// TestPendingListRebuildKeepsCursorOnTheSameEntryByIdentity covers
// rebuildPendingList restoring the cursor by entry identity, not raw
// index: an earlier entry disappearing must not silently land the cursor
// on whatever unrelated entry now happens to sit at the old index.
func TestPendingListRebuildKeepsCursorOnTheSameEntryByIdentity(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)

	draftA := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:a.go"}
	draftB := drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:b.go"}
	if err := app.deps.Drafts.Save(draftA, "draft a"); err != nil {
		t.Fatalf("Save(a): %v", err)
	}
	if err := app.deps.Drafts.Save(draftB, "draft b"); err != nil {
		t.Fatalf("Save(b): %v", err)
	}

	sendRune(app.app, 'p')
	// reviewThreadsPR's own pending comment (t-pending) is always entry 0;
	// List sorts by UpdatedAt descending, so the more-recently-saved
	// draftB is entry 1 and draftA (saved first) is entry 2.
	waitFor(t, app.app, func() bool { return app.overlay == "pending" && len(app.pendingListEntries) == 3 })

	act(app.app, func() {
		app.pendingListView.SetCurrentItem(2) // draftA
	})
	if got := query(app.app, func() string { return app.pendingListEntries[2].draft.Key.Anchor }); got != draftA.Anchor {
		t.Fatalf("precondition: entry 2 = %q, want draftA", got)
	}

	// draftB (entry 1) disappears; draftA is now entry 1.
	if err := app.deps.Drafts.Delete(draftB); err != nil {
		t.Fatalf("Delete(b): %v", err)
	}
	act(app.app, func() { app.rebuildPendingList() })

	if got := query(app.app, func() int { return len(app.pendingListEntries) }); got != 2 {
		t.Fatalf("pending list has %d entries after deleting draftB, want 2", got)
	}
	if got := query(app.app, func() bool { return app.pendingListEntries[1].draft.Key.Anchor == draftA.Anchor }); !got {
		t.Fatal("precondition: entry 1 is not draftA after the rebuild")
	}
	if got := query(app.app, func() int { return app.pendingListView.GetCurrentItem() }); got != 1 {
		t.Fatalf("cursor index = %d after rebuild, want 1 (draftA, the entry that was selected, not index 2 clamped to something else)", got)
	}
}

// TestComposerLineCommentTypingCoalescesDraftGutterRefresh covers
// onComposerChange only calling refreshDraftGutterIfNeeded (and therefore
// refreshCurrentFile, which lists every draft for the pull request from
// disk and rebuilds the whole diff) when a line/range comment's own "has
// a draft" state actually flips — once when the first character is typed
// (empty -> non-empty), not once per further keystroke.
func TestComposerLineCommentTypingCoalescesDraftGutterRefresh(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() }) // "+newCall()", no thread

	// Let every hunk-highlight job finish first: EventFileHighlighted
	// itself calls refreshCurrentFile for the currently open file (see
	// storeevents.go), independent of the draft-gutter coalescing this
	// test is about — without waiting, a job landing mid-typing would
	// inflate the count for an unrelated reason.
	waitFor(t, app.app, func() bool { return app.deps.Store.FilesState().Highlighting == 0 })

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')

	before := query(app.app, func() int { return app.refreshCurrentFileCalls })
	for _, r := range "hello world" {
		sendRune(app.app, r)
	}
	waitFor(t, app.app, func() bool { return composerText(app) == "hello world" })

	if got := query(app.app, func() int { return app.refreshCurrentFileCalls }) - before; got != 1 {
		t.Errorf("refreshCurrentFile called %d time(s) while typing 11 characters, want exactly 1 (the empty->non-empty flip on the first one)", got)
	}
}

// TestPendingListEnterOnAGeneralCommentDraftSwitchesToPRTab covers Enter
// on a KindComment draft whose Anchor is "issue" (a general PR comment
// draft, not a line/range one): parseLineAnchor fails for it, so
// openDraftFromList must recognise that and open the PR tab's own general
// composer instead of silently doing nothing.
func TestPendingListEnterOnAGeneralCommentDraftSwitchesToPRTab(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)
	key := drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "issue"}
	if err := app.deps.Drafts.Save(key, "general comment draft"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	act(app.app, func() {
		for i, e := range app.pendingListEntries {
			if e.isDraft && e.draft.Key == key {
				app.pendingListView.SetCurrentItem(i)
			}
		}
	})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return composerOpen(app) })

	if app.currentTab != "pr" {
		t.Fatalf("currentTab = %q, want %q", app.currentTab, "pr")
	}
	if got, want := query(app.app, func() string { return composerText(app) }), "general comment draft"; got != want {
		t.Fatalf("composer text = %q, want %q", got, want)
	}
}

// TestPendingListEnterOnAnUnresolvableEditDraftToastsAndKeepsListOpen
// covers Enter on a KindEdit draft whose comment ID matches neither an
// issue comment nor a review comment on the current pull request (it was
// deleted, or belonged to a different one entirely).
func TestPendingListEnterOnAnUnresolvableEditDraftToastsAndKeepsListOpen(t *testing.T) {
	app, _, ref := openFilesTabWithReviewThreads(t)
	key := drafts.Key{PR: ref.Key(), Kind: drafts.KindEdit, Anchor: "IC_does_not_exist"}
	if err := app.deps.Drafts.Save(key, "orphaned edit"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })
	act(app.app, func() {
		for i, e := range app.pendingListEntries {
			if e.isDraft && e.draft.Key == key {
				app.pendingListView.SetCurrentItem(i)
			}
		}
	})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot open this draft") })

	if composerOpen(app) {
		t.Fatal("an unresolvable edit draft must not open a composer")
	}
	if app.overlay != "pending" {
		t.Fatalf("overlay = %q, want the pending list to stay open", app.overlay)
	}
}

// TestPendingListQClosesOverlay covers q/Esc closing the dialog.
func TestPendingListQClosesOverlay(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestPendingListAltKeyDoesNotPanic reproduces a nil-pointer panic:
// keys.Normalize expands Alt+rune into [Esc, rune] (and tcell itself
// reports a plain keypress within ~50ms of Esc as Alt+that-key on some
// terminals, so a fast "p" then Esc then "j" can trigger the exact same
// path), and routePendingKey's own loop continued past the Esc's own
// close(nilling a.pendingListView) straight into moveListSelection on the
// second key.
func TestPendingListAltKeyDoesNotPanic(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	sendRune(app.app, 'p')
	waitFor(t, app.app, func() bool { return app.overlay == "pending" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestChoiceMenuAltKeyDoesNotPanic mirrors
// TestPendingListAltKeyDoesNotPanic for the choice menu (routeChoiceKey).
func TestChoiceMenuAltKeyDoesNotPanic(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	act(app.app, func() { app.diffView.MoveBottom() })
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	typeAndSend(app, "trigger the menu")
	waitFor(t, app.app, func() bool { return app.overlay == "choice" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestEventNoticeShowsInfoToast covers store.EventNotice's own toast style
// (theme.Info, distinct from Warning/Error): a SendSingle call coerced to
// "add to review" because a pending review already exists by the time the
// mutation actually runs.
func TestEventNoticeShowsInfoToast(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)
	pr := reviewThreadsPR(ref)
	pr.PendingReview = &model.Review{ID: "PVR_existing"}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.PendingReview() != nil })

	// A direct SendSingle call: the store itself coerces it to
	// SendToReview (a pending review already exists) and emits
	// EventNotice — exercising subscribeStore's own wiring of it to a
	// theme.Info toast, independent of the composer's own send-mode menu
	// (which would never offer SendSingle at all once CanSendSingle() is
	// false — see TestSendModeMenuSkippedWhenPendingReviewAlreadyExists).
	act(app.app, func() {
		app.deps.Store.CommentOnLines("pkg/example.go", diff.Range{StartSide: model.DiffSideRight, Side: model.DiffSideRight, Line: 11}, "coerced", store.SendSingle)
	})
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "pending review") })
}

// TestDraftGutterMarkerRendersOnceSavedAndAgainAfterDelete covers
// internal/ui's own wiring of the draft gutter marker (files.go's
// draftLinesForFile, called from refreshCurrentFile and, on every
// keystroke and on close, from composer.go's refreshDraftGutterIfNeeded):
// widget.DiffView's own rendering of DiffFile.DraftLines/DraftMarker is
// covered directly in internal/ui/widget; this covers the path that
// actually produces those fields from a real drafts.Store and a real diff
// composer, end to end.
func TestDraftGutterMarkerRendersOnceSavedAndAgainAfterDelete(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: reviewThreadsPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(diffText(app, screen), "✎")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })
	act(app.app, func() { app.diffView.MoveBottom() }) // hunk 2's "+newCall()" (RIGHT, 11)

	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	sendRune(app.app, 'i')
	sendRune(app.app, 'x')
	sendSpecial(app.app, tcell.KeyEsc)

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "✎")
	})

	sendRune(app.app, ':')
	sendRune(app.app, 'q')
	sendRune(app.app, '!')
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !composerOpen(app) })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(diffText(app, screen), "✎")
	})
}
