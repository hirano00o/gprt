// threadlist_test.go covers the "t" (pr.threads) dialog: threadlist.go's
// openThreadList/closeThreadList/rebuildThreadList/routeThreadListKey/
// openThreadListEntry.
package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// TestThreadListShowsThreadsSortedByPathThenLine covers rebuildThreadList's
// own sort: fixtureFilesPR (files_test.go) gives pkg/example.go three
// threads at lines 1/2/999 and pkg/renamed_new.go a single file-level one —
// "pkg/example.go" sorts before "pkg/renamed_new.go", so the file-level
// entry (no line of its own) trails every line entry on the earlier path.
func TestThreadListShowsThreadsSortedByPathThenLine(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	if got, want := query(app.app, func() int { return len(app.threadListEntries) }), 4; got != want {
		t.Fatalf("thread list has %d entries, want %d", got, want)
	}

	texts := query(app.app, func() []string {
		out := make([]string, app.threadListView.GetItemCount())
		for i := range out {
			out[i], _ = app.threadListView.GetItemText(i)
		}
		return out
	})

	wantOrder := []string{"pkg/example.go:1", "pkg/example.go:2", "pkg/example.go:999", "pkg/renamed_new.go"}
	for i, want := range wantOrder {
		if i >= len(texts) {
			t.Errorf("entry %d missing, want it to mention %q", i, want)
			continue
		}
		if !containsSubstring(texts[i], want) {
			t.Errorf("entry %d = %q, want it to mention %q", i, texts[i], want)
		}
	}
}

// TestThreadListEnterJumpsToThreadInFilesTab covers Enter from the PR tab:
// the dialog closes, the Files tab opens the entry's own file, the tree
// selection follows, the diff's cursor lands on the thread, and focus ends
// on the diff (so r/x/c work immediately).
func TestThreadListEnterJumpsToThreadInFilesTab(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureFilesPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.prView })

	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	// Entry 0, sorted by path then line, is pkg/example.go:1 (thread-resolved).
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return app.overlay == "" })
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool { return app.currentFilePath == "pkg/example.go" })
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	th := query(app.app, func() model.ReviewThread { th, _ := app.diffView.CursorThread(); return th })
	if th.ID != "thread-resolved" {
		t.Fatalf("cursor thread after Enter = %+v, want thread-resolved (entry 0, pkg/example.go:1)", th)
	}

	node := query(app.app, func() *tview.TreeNode { return app.treeView.GetCurrentNode() })
	if ref, ok := node.GetReference().(treeFileRef); !ok || ref.path != "pkg/example.go" {
		t.Fatalf("tree node reference after Enter = %+v (ok=%v), want it selected on pkg/example.go", ref, ok)
	}
}

// TestThreadListToastsWhenNoPullRequestOpen mirrors
// TestReactionPickerWithNoPullRequestOpenToasts (reactionpicker_test.go):
// Ctrl-w l moves focus into the detail column regardless of whether a pull
// request has been opened, so "t" still resolves through ContextPR there.
func TestThreadListToastsWhenNoPullRequestOpen(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "no pull request open") })
	if app.overlay == "threads" {
		t.Fatal("t with no pull request open must not open the thread list")
	}
}

// TestThreadListQClosesOverlay covers q/Esc closing the dialog.
func TestThreadListQClosesOverlay(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestThreadListAltKeyDoesNotPanic mirrors TestPendingListAltKeyDoesNotPanic
// (reviewui_test.go) for routeThreadListKey.
func TestThreadListAltKeyDoesNotPanic(t *testing.T) {
	app, _, _ := openFilesTabWithReviewThreads(t)
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModAlt))
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

// TestThreadListRebuildsOnPRChanged covers rebuildThreadList's own
// subscription (storeevents.go's EventPRChanged wiring): a reload that adds
// one more review thread to the open pull request grows the list while it
// is still open, rather than only on the next manual re-open.
func TestThreadListRebuildsOnPRChanged(t *testing.T) {
	app, fake, ref := openFilesTabWithReviewThreads(t)

	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.overlay == "threads" })

	before := query(app.app, func() int { return len(app.threadListEntries) })

	pr := reviewThreadsPR(ref)
	pr.ReviewThreads = append(pr.ReviewThreads, model.ReviewThread{
		ID: "t-extra", Path: "pkg/example.go", Line: 11, Side: model.DiffSideRight,
		Comments: []model.ReviewComment{{Author: model.User{Login: "eve"}, Body: "one more"}},
	})
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	act(app.app, func() { app.deps.Store.ReloadPR() })

	waitFor(t, app.app, func() bool { return len(app.threadListEntries) == before+1 })
}
