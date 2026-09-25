package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/gh"
)

func TestSmartcase(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{"all lowercase gets a case-insensitive prefix", "foo", "(?i)foo"},
		{"a leading uppercase rune disables it", "Foo", "Foo"},
		{"an uppercase rune anywhere in the pattern disables it", "fooBar", "fooBar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := smartcase(tc.pattern); got != tc.want {
				t.Errorf("smartcase(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
}

// focusDiffViewForTest moves focus from the tree (where openFilesTabForFixture
// leaves it) to the diff pane, via Ctrl-w l (global.focus_right) — the same
// sequence internal/ui's other Files-tab tests use.
func focusDiffViewForTest(t *testing.T, app *App) {
	t.Helper()
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })
}

func typeString(app *App, s string) {
	for _, r := range s {
		sendRune(app.app, r)
	}
}

func TestDiffSearchInvalidPatternToasts(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })

	sendRune(app.app, '(')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "invalid pattern")
	})
}

// TestDiffSearchJumpsAcrossFiles covers the whole-diff, cross-file search:
// "addition" only occurs in pkg/renamed_new.go's patch (see renamedGoPatch),
// not the currently open pkg/example.go, so submitting it must open that
// file and land the cursor on its matching line.
func TestDiffSearchJumpsAcrossFiles(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "addition")
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return app.currentFilePath == "pkg/renamed_new.go" })
	waitFor(t, app.app, func() bool {
		line, ok := app.diffView.CursorLine()
		return ok && containsSubstring(line.Text, "addition")
	})
}

// TestDiffSearchNextWrapsWithToast covers a pattern with exactly one match
// across the whole diff: submitting it already lands the cursor on that
// match, so a further "n" has nowhere to go but wrap back to itself.
func TestDiffSearchNextWrapsWithToast(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "oldCall")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		line, ok := app.diffView.CursorLine()
		return ok && containsSubstring(line.Text, "oldCall")
	})

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "search hit BOTTOM")
	})
}

// TestDiffSearchEscClearsHighlightOnly covers router.go's Esc-clears-search
// block: Esc on the diff (outside visual mode) turns off the highlight but
// keeps the match list, so a following "n" still jumps and turns the
// highlight back on.
func TestDiffSearchEscClearsHighlightOnly(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "line")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return len(app.searchMatches) > 0 })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return !app.diffView.HasSearch() })

	sendRune(app.app, 'n')
	waitFor(t, app.app, func() bool { return app.diffView.HasSearch() })
}

// TestDiffSearchFromTreeFocus covers ActionDiffSearch's ContextFiles
// binding: "/" with the tree (not the diff) focused still opens the search
// and, once submitted, returns focus to the tree rather than stealing it.
func TestDiffSearchFromTreeFocus(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.treeView })

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "addition")
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return app.currentFilePath == "pkg/renamed_new.go" })
	if got := query(app.app, func() tview.Primitive { return app.app.GetFocus() }); got != app.treeView {
		t.Errorf("focus after submitting a diff search from the tree = %v, want the tree unchanged", got)
	}
}

// TestDiffSearchIsClearedOnPullRequestSwitch covers clearDiffSearchIfWrongPR
// (storeevents.go, EventPRChanged): opening a different pull request while
// a search is active must drop it entirely, so a later n/N never re-applies
// a stale highlight or jumps into a file that may not even exist on the
// newly opened pull request.
func TestDiffSearchIsClearedOnPullRequestSwitch(t *testing.T) {
	app, fake, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "oldCall")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.searchRE != nil })

	ref2 := fixtureRef(2)
	fake.SetPRResult(ref2, gh.DetailResult{PR: fixtureFilesPR(ref2)})
	act(app.app, func() { app.deps.Store.OpenPR(ref2) })
	waitFor(t, app.app, func() bool {
		cur, ok := app.deps.Store.CurrentRef()
		return ok && cur == ref2
	})

	waitFor(t, app.app, func() bool { return app.searchRE == nil })
	if got := query(app.app, func() bool { return app.diffView.HasSearch() }); got {
		t.Error("diffView.HasSearch() still true after switching to a different pull request")
	}
}

// TestDiffSearchSurvivesSamePRChange covers clearDiffSearchIfWrongPR's own
// exception: EventPRChanged also fires for the pull request already open
// (Store.ReloadPR — "R" — refetches it in place, emitting the event
// synchronously before its own network fetch resolves), which must not
// drop an active search.
func TestDiffSearchSurvivesSamePRChange(t *testing.T) {
	app, fake, _, ref := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "oldCall")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return len(app.searchMatches) > 0 })

	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureFilesPR(ref)})
	act(app.app, func() { app.deps.Store.ReloadPR() })

	// searchRE itself is only ever nilled by clearDiffSearch, reached here
	// only through clearDiffSearchIfWrongPR's ref-mismatch branch — since
	// ReloadPR never changes the current ref, that check is safe to make
	// right away (act's and query's QueueUpdate calls run strictly in the
	// order enqueued).
	if got := query(app.app, func() bool { return app.searchRE != nil }); !got {
		t.Fatal("searchRE cleared by an EventPRChanged for the same pull request")
	}
	// ReloadPR also force-reloads files (the Files tab was already open for
	// this pull request), which transiently empties Store.Files() while
	// its own fetch is in flight — searchMatches recomputes against that
	// through EventFilesChanged (see storeevents.go), so it settles back to
	// non-empty only once the reload's own page(s) land again.
	waitFor(t, app.app, func() bool { return len(app.searchMatches) > 0 })
}

// TestDiffSearchRecomputesOnFilesReload covers EventFilesChanged's own
// recompute-in-place: a files reload that changes a matched file's content
// must update searchMatches to reflect it, not leave the search frozen at
// whatever it matched before.
func TestDiffSearchRecomputesOnFilesReload(t *testing.T) {
	app, fake, _, ref := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "brandnewword")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.searchRE != nil })
	if got := query(app.app, func() int { return len(app.searchMatches) }); got != 0 {
		t.Fatalf("searchMatches = %d before the reload introduces the word, want 0", got)
	}

	const patchWithBrandNewWord = `@@ -1,4 +1,5 @@
 package example
+// brandnewword
 func Example() {
 	return
 }
@@ -10,1 +11,1 @@
-	oldCall()
+	newCall()
`
	pages := fixtureFilesPages()
	pages[0].Files[0].Patch = patchWithBrandNewWord
	fake.SetFilesPages(ref, pages)
	act(app.app, func() { app.deps.Store.LoadFiles(true) })

	waitFor(t, app.app, func() bool { return len(app.searchMatches) > 0 })
}

// TestDiffSearchEscInVisualOnlyEndsSelection covers router.go's wasVisual
// guard: a single Esc while a visual selection is active, with a search
// also running, must only end the selection — the search highlight needs
// its own, separate Esc afterward.
func TestDiffSearchEscInVisualOnlyEndsSelection(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	focusDiffViewForTest(t, app)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.searchInput })
	typeString(app, "line")
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.diffView.HasSearch() })

	sendRune(app.app, 'V')
	waitFor(t, app.app, func() bool { return app.diffView.InVisual() })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return !app.diffView.InVisual() })
	if got := query(app.app, func() bool { return app.diffView.HasSearch() }); !got {
		t.Fatal("the Esc that ended the visual selection also cleared the search highlight")
	}

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return !app.diffView.HasSearch() })
}
