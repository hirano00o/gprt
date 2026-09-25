package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// exampleGoPatch is a 2-hunk patch for a small Go file: an added comment
// line long enough to require horizontal scrolling within the test
// screen's narrow diff pane, a context "func" line (whose "func" keyword
// the real chroma highlighter tokenises once highlighting completes), and
// a second hunk replacing one call with another.
const exampleGoPatch = `@@ -1,4 +1,5 @@
 package example
+// Example is documented here with a deliberately long line so zl/zh horizontal scrolling has somewhere to go.
 func Example() {
 	return
 }
@@ -10,1 +11,1 @@
-	oldCall()
+	newCall()
`

const renamedGoPatch = `@@ -1,1 +1,2 @@
 package renamed
+// tiny addition
`

// fixtureFilesPages returns two REST pages of changed files: page 1 has a
// Go file (exampleGoPatch, above) and a renamed file; page 2 has a binary
// file with no patch at all and HasNext false.
func fixtureFilesPages() []gh.FilesResult {
	return []gh.FilesResult{
		{
			HasNext: true,
			Files: []model.ChangedFile{
				{Path: "pkg/example.go", Status: model.FileStatusModified, Additions: 2, Deletions: 1, HasPatch: true, Patch: exampleGoPatch},
				{Path: "pkg/renamed_new.go", PreviousPath: "pkg/renamed_old.go", Status: model.FileStatusRenamed, Additions: 1, Deletions: 0, HasPatch: true, Patch: renamedGoPatch},
			},
		},
		{
			HasNext: false,
			Files: []model.ChangedFile{
				{Path: "assets/image.png", Status: model.FileStatusModified, HasPatch: false},
			},
		},
	}
}

// fixtureFilesPR builds a pull request fixture for the Files-tab tests:
// three files' worth of ChangedFiles and four review threads covering
// every bucket widget.DiffView renders (a resolved line thread, an
// unresolved line thread with a reply, a file-level thread, and an
// outdated one).
func fixtureFilesPR(ref model.PRRef) model.PullRequest {
	now := time.Now()
	return model.PullRequest{
		ID:           fmt.Sprintf("PR_files_%d", ref.Number),
		Ref:          ref,
		Title:        "Add example package",
		Author:       model.User{Login: "alice"},
		State:        model.PRStateOpen,
		ChangedFiles: 3,
		ReviewThreads: []model.ReviewThread{
			{
				ID: "thread-resolved", Path: "pkg/example.go", Line: 1, Side: model.DiffSideRight, IsResolved: true,
				Comments: []model.ReviewComment{{Author: model.User{Login: "alice"}, Body: "looks good", CreatedAt: now, URL: "https://github.com/acme/widgets/pull/1#thread-resolved"}},
			},
			{
				ID: "thread-open", Path: "pkg/example.go", Line: 2, Side: model.DiffSideRight, IsResolved: false,
				Comments: []model.ReviewComment{
					{Author: model.User{Login: "bob"}, Body: "please clarify", CreatedAt: now, URL: "https://github.com/acme/widgets/pull/1#thread-open"},
					{Author: model.User{Login: "alice"}, Body: "sure, updating now", CreatedAt: now},
				},
			},
			{
				ID: "thread-file", Path: "pkg/renamed_new.go", SubjectType: model.ThreadSubjectFile, IsResolved: false,
				Comments: []model.ReviewComment{{Author: model.User{Login: "carol"}, Body: "consider splitting this file", CreatedAt: now, URL: "https://github.com/acme/widgets/pull/1#thread-file"}},
			},
			{
				ID: "thread-outdated", Path: "pkg/example.go", Line: 999, Side: model.DiffSideRight, IsOutdated: true,
				Comments: []model.ReviewComment{{Author: model.User{Login: "dave"}, Body: "outdated note", CreatedAt: now, URL: "https://github.com/acme/widgets/pull/1#thread-outdated"}},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
		URL:       fmt.Sprintf("https://github.com/%s/pull/%d", ref.Repo.NameWithOwner(), ref.Number),
	}
}

// openFilesTabForFixture opens the direct-review fixture pull request
// (fixtureRef(1), the cursor's starting row per newTestApp), waits for its
// detail (with threads) to resolve, then switches to the Files tab and
// waits for its first page of files to load.
func openFilesTabForFixture(t *testing.T) (*App, *fakeGitHub, tcell.SimulationScreen, model.PRRef) {
	t.Helper()
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureFilesPR(ref)})
	fake.SetFilesPages(ref, fixtureFilesPages())

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool { return len(app.deps.Store.Files()) > 0 })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})
	return app, fake, screen, ref
}

// screenText flattens the runes drawn within (x, y, w, h) into one string,
// one line per row, for substring assertions against a primitive's
// rendered screen content — the same technique internal/ui/widget's own
// span_test.go uses (cellRune/cellStyle), duplicated here in package ui
// since those helpers are unexported to internal/ui/widget. Every caller
// must already be running on the UI goroutine (inside a waitFor condition,
// or a single act/query callback — see this file's own helpers): the
// primitives' GetRect() has no synchronization of its own.
func screenText(screen tcell.Screen, x, y, w, h int) string {
	var b strings.Builder
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			s, _, _ := screen.Get(col, row)
			if s != "" {
				b.WriteString(s)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// diffText and treeText read diffView's/treeView's rendered region.
// Callers must already be on the UI goroutine (see screenText's own doc
// comment).
func diffText(app *App, screen tcell.Screen) string {
	x, y, w, h := app.diffView.GetRect()
	return screenText(screen, x, y, w, h)
}

func treeText(app *App, screen tcell.Screen) string {
	x, y, w, h := app.treeView.GetRect()
	return screenText(screen, x, y, w, h)
}

// diffTextSync is diffText wrapped in a single, top-level query call, for
// use outside a waitFor condition (never nest this inside one — see
// query's own doc comment on why that would deadlock).
func diffTextSync(app *App, screen tcell.Screen) string {
	return query(app.app, func() string { return diffText(app, screen) })
}

func treeTextSync(app *App, screen tcell.Screen) string {
	return query(app.app, func() string { return treeText(app, screen) })
}

// hasStyleInRect reports whether any cell within (x, y, w, h) was drawn
// with exactly style. tcell.Screen.Get locks internally (shared with
// Draw's own SetContent calls), so — unlike GetRect — this is safe to call
// directly from the test goroutine once (x, y, w, h) have themselves been
// read safely (see query4).
func hasStyleInRect(screen tcell.Screen, x, y, w, h int, style tcell.Style) bool {
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			_, s, _ := screen.Get(col, row)
			if s == style {
				return true
			}
		}
	}
	return false
}

// query4 is query's 4-int-result sibling, for reading a primitive's
// GetRect() safely from the test goroutine.
func query4(app *tview.Application, f func() (int, int, int, int)) (int, int, int, int) {
	var a, b, c, d int
	app.QueueUpdate(func() {
		app.ForceDraw()
		a, b, c, d = f()
	})
	return a, b, c, d
}

// TestFilesTabRebuildPreservesDirectoryCollapseState guards against a
// directory the user collapsed being silently re-expanded by the next
// rebuildFileTree call (another page arriving, a 5-minute refresh, ...),
// since each rebuild constructs entirely new *tview.TreeNode values that
// default to expanded.
// TestFilesTabRebuildKeepsATreeCursorWhenTheCurrentFileIsNotYetLoaded
// guards against two related regressions after a reload (R) whose current
// file happens to live on a page that has not (re-)arrived yet: (1) the
// tree ending up with no current node at all (tview never auto-selects
// one on its own), leaving j/k/Enter dead until — if ever — that page
// arrives; and (2) the wanted path itself being discarded in favour of
// whatever fallback node was picked, so even once the file does become
// available, nothing switches back to it.
func TestFilesTabRebuildKeepsATreeCursorWhenTheCurrentFileIsNotYetLoaded(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok
	})

	// Simulate "R" reloading: the wanted file is not currently found among
	// Store.Files() (its own page has not re-arrived yet). currentFilePath
	// is allowed to change to whatever fallback node the tree picks (its
	// own deferred SetChangedFunc callback will show that file's diff so
	// the pane never looks frozen); wantedFilePath must not.
	act(app.app, func() {
		app.wantedFilePath = "not/yet/loaded.go"
		app.rebuildFileTree()
	})

	node := query(app.app, func() *tview.TreeNode { return app.treeView.GetCurrentNode() })
	if node == nil {
		t.Fatal("tree has no current node once the wanted file is not found among the loaded files; j/k/Enter would be dead")
	}
	waitFor(t, app.app, func() bool { return app.lastFallbackNode == nil }) // let the deferred callback resolve
	if got := query(app.app, func() string { return app.wantedFilePath }); got != "not/yet/loaded.go" {
		t.Errorf("wantedFilePath = %q after a rebuild that could not find it, want it preserved as %q so a later rebuild can restore it once the file arrives", got, "not/yet/loaded.go")
	}

	// Once the wanted file "arrives" (simulated here by making it
	// findable again), the next rebuild must select it, not stay on
	// whatever fallback node the previous rebuild picked.
	act(app.app, func() { app.wantedFilePath = "pkg/example.go" })
	act(app.app, func() { app.rebuildFileTree() })
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return app.currentFilePath == "pkg/example.go"
	})
	got := query(app.app, func() struct {
		ref treeFileRef
		ok  bool
	} {
		r, ok := app.treeView.GetCurrentNode().GetReference().(treeFileRef)
		return struct {
			ref treeFileRef
			ok  bool
		}{r, ok}
	})
	if !got.ok || got.ref.path != "pkg/example.go" {
		t.Errorf("tree current node after the file became available = %+v, want it restored to pkg/example.go", got)
	}
}

// findTreeDirNodeByText walks the tree for the directory node whose own
// GetText() equals text (a leaf's text always includes a status glyph and
// stats, so this only ever matches a plain directory node).
func findTreeDirNodeByText(root *tview.TreeNode, text string) *tview.TreeNode {
	var found *tview.TreeNode
	root.Walk(func(node, _ *tview.TreeNode) bool {
		if _, isFile := node.GetReference().(treeFileRef); !isFile && node.GetText() == text {
			found = node
			return false
		}
		return found == nil
	})
	return found
}

// TestFilesTabRebuildDoesNotMisclassifyALaterGenuineSelectionAsTheFallback
// guards against lastFallbackNode surviving past the "changed" callback
// it was meant for: rebuildFileTree's fallback selection can itself sit
// inside a directory collectDirExpansion preserved as collapsed, in which
// case tview's own process() (its selected node not being among the
// visible, flattened nodes) substitutes a *different* node — often the
// collapsed directory itself, which carries no treeFileRef, so
// onTreeNodeChanged used to return before ever reaching the "clear the
// marker" branch. Left set, the marker then survives to the next
// "changed" firing for *any* reason, including the user later genuinely
// expanding that directory and selecting the original fallback file
// themselves — misclassified as the fallback resolving (showFile, not
// openFile), silently discarding that deliberate choice as
// wantedFilePath.
func TestFilesTabRebuildDoesNotMisclassifyALaterGenuineSelectionAsTheFallback(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.Files()) == 3 })

	// "assets/image.png" sorts alphabetically first among the fixture's
	// three files, so rebuildFileTree's own firstNode/fallback pick is
	// always this file — collapsing its directory puts the fallback
	// exactly where item 21 describes.
	act(app.app, func() {
		findTreeDirNodeByText(app.treeView.GetRoot(), "assets").SetExpanded(false)
	})

	act(app.app, func() {
		app.wantedFilePath = "not/yet/loaded.go"
		app.rebuildFileTree()
	})
	// Force the deferred "changed" callback tview's own process() fires on
	// the next Draw to actually run: with "assets" collapsed, the fallback
	// leaf (assets/image.png) set as currentNode is not among the visible
	// nodes, so process() substitutes the first visible candidate instead
	// (the "assets" directory node itself) and fires "changed" for that.
	query(app.app, func() bool { return true }) // query itself forces a draw first

	if got := query(app.app, func() *tview.TreeNode { return app.lastFallbackNode }); got != nil {
		t.Fatal("lastFallbackNode still set after a \"changed\" firing for a directory node (no treeFileRef); it must be cleared unconditionally so it cannot misclassify a later, genuine selection of the same file")
	}

	// The user now deliberately re-expands "assets" and selects the exact
	// file rebuildFileTree had picked as its own fallback.
	act(app.app, func() {
		findTreeDirNodeByText(app.treeView.GetRoot(), "assets").SetExpanded(true)
	})
	leaf := query(app.app, func() *tview.TreeNode { return app.findTreeNodeByPath("assets/image.png") })
	if leaf == nil {
		t.Fatal("setup: assets/image.png has no tree node")
	}
	act(app.app, func() { app.treeView.SetCurrentNode(leaf) })
	waitFor(t, app.app, func() bool { return app.currentFilePath == "assets/image.png" })

	if got := query(app.app, func() string { return app.wantedFilePath }); got != "assets/image.png" {
		t.Errorf("wantedFilePath = %q after the user deliberately selected assets/image.png, want it recorded as the user's own choice", got)
	}
}

// TestFilesTabTreeGGReturnsToTheTop guards against a lone "g" being
// swallowed forever on the tree: gt/gT (tab switching) are bound in
// ContextFiles, so a bare "g" always matches at least a Prefix there,
// meaning it can never fall through to tview.TreeView's own native "g"
// handling by going unconsumed — see keys.ctxTable.lookup and
// Sequencer.Feed. list.top (bound to "gg", like every other movable
// context) resolves this: the Sequencer completes the *sequence* "gg"
// unambiguously and dispatches list.top, which treeMovablePane.MoveTop
// moves the cursor to the tree's actual first row for (not tview's own
// native "g", which only scrolls the viewport and never touches
// currentNode at all — see MoveTop's own doc comment). The expected
// destination is asserted by identity against the root's own first child,
// not a specific file path: the fixture's alphabetically-first top-level
// node is the "assets" directory, not the first *file* rebuildFileTree
// happened to auto-open — "top of the tree" and "first file to load" are
// two different things.
func TestFilesTabTreeGGReturnsToTheTop(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.Files()) == 3 })

	first := query(app.app, func() *tview.TreeNode {
		children := app.treeView.GetRoot().GetChildren()
		if len(children) == 0 {
			return nil
		}
		return children[0]
	})
	if first == nil {
		t.Fatal("setup: tree has no top-level node")
	}

	sendRune(app.app, 'j')
	sendRune(app.app, 'j')
	sendRune(app.app, 'g')
	sendRune(app.app, 'g')

	waitFor(t, app.app, func() bool { return app.treeView.GetCurrentNode() == first })
}

func TestFilesTabRebuildPreservesDirectoryCollapseState(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(treeText(app, screen), "example.go")
	})

	act(app.app, func() {
		root := app.treeView.GetRoot()
		root.Walk(func(node, _ *tview.TreeNode) bool {
			if node.GetText() == "pkg" {
				node.SetExpanded(false)
			}
			return true
		})
	})
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(treeText(app, screen), "example.go")
	})

	act(app.app, func() { app.rebuildFileTree() })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := treeText(app, screen)
		return containsSubstring(text, "pkg") && !containsSubstring(text, "example.go")
	})
}

// TestFilesTabRebuildKeepsTheCursorOnADirectoryInsteadOfWantedFile guards
// against rebuildFileTree always moving the cursor back to wantedNode (M2
// review round 3, item 28a): wantedFilePath only ever tracks a *file* the
// user explicitly opened (App.openFile) and is left completely untouched
// by navigating onto a directory (onTreeNodeChanged returns early for a
// node with no treeFileRef) — so with "pkg/example.go" still the wanted
// file, a user who then presses "h" to collapse "pkg" and stays there
// would have every subsequent rebuild (a new page of files arriving, most
// commonly) yank the cursor straight back onto "pkg/example.go", as if the
// directory had never been selected at all.
func TestFilesTabRebuildKeepsTheCursorOnADirectoryInsteadOfWantedFile(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(treeText(app, screen), "example.go")
	})
	if got := query(app.app, func() string { return app.wantedFilePath }); got != "pkg/example.go" {
		t.Fatalf("setup: wantedFilePath = %q, want pkg/example.go (auto-opened first)", got)
	}

	act(app.app, func() {
		pkg := findTreeDirNodeByText(app.treeView.GetRoot(), "pkg")
		app.treeView.SetCurrentNode(pkg)
		pkg.SetExpanded(false)
	})
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(treeText(app, screen), "example.go")
	})

	// Simulates a new page of files arriving (the same trigger
	// TestFilesTabRebuildPreservesDirectoryCollapseState uses): nothing
	// about wantedFilePath changes on its own from navigating onto a
	// directory, so without this fix the rebuild below would move the
	// cursor straight back to "pkg/example.go".
	act(app.app, func() { app.rebuildFileTree() })

	got := query(app.app, func() struct {
		text  string
		isPkg bool
	} {
		node := app.treeView.GetCurrentNode()
		_, isFile := node.GetReference().(treeFileRef)
		return struct {
			text  string
			isPkg bool
		}{node.GetText(), node.GetText() == "pkg" && !isFile}
	})
	if !got.isPkg {
		t.Errorf("current node after the rebuild = %q, want the cursor to stay on the collapsed \"pkg\" directory instead of jumping back to wantedFilePath", got.text)
	}
}

// TestFilesTabDirectoryCollapseSurvivesAForcePushReload guards against a
// force-push (or any HeadOID-changed reload) discarding a collapsed
// directory's state (M2 review round 3, item 23): the reload's own
// LoadFiles resets Store.Files() to empty before the new page's network
// fetch resolves (see internal/store's LoadFiles doc comment), so
// rebuildFileTree runs at least once with zero entries in between —
// exactly the moment collectDirExpansion, reading whatever the *current*
// tree looks like, would otherwise collect an empty map and overwrite the
// one thing remembering "pkg" was collapsed, well before the rebuild that
// actually has files to apply it to ever runs.
func TestFilesTabDirectoryCollapseSurvivesAForcePushReload(t *testing.T) {
	app, fake, screen, ref := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(treeText(app, screen), "example.go")
	})

	act(app.app, func() {
		root := app.treeView.GetRoot()
		root.Walk(func(node, _ *tview.TreeNode) bool {
			if node.GetText() == "pkg" {
				node.SetExpanded(false)
			}
			return true
		})
	})
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(treeText(app, screen), "example.go")
	})

	pr2 := fixtureFilesPR(ref)
	pr2.HeadOID = "head2"
	act(app.app, func() { fake.SetPRResult(ref, gh.DetailResult{PR: pr2}) })
	act(app.app, func() { fake.SetFilesPages(ref, fixtureFilesPages()) })
	block := make(chan struct{})
	act(app.app, func() { fake.SetFilesBlock(block) })

	act(app.app, func() { app.deps.Store.RefreshPR() })
	waitFor(t, app.app, func() bool { return app.deps.Store.FilesState().Loading })
	waitFor(t, app.app, func() bool { return len(app.deps.Store.Files()) == 0 }) // the transient empty rebuild this guards against

	close(block)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.Files()) == 3 })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := treeText(app, screen)
		return containsSubstring(text, "pkg") && !containsSubstring(text, "example.go")
	})
}

func TestFilesTabOpeningLoadsFilesAndTreeShowsDirsAndFiles(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := treeText(app, screen)
		return containsSubstring(text, "pkg") &&
			containsSubstring(text, "example.go") &&
			containsSubstring(text, "renamed_new.go") &&
			containsSubstring(text, "assets")
	})

	if text := treeTextSync(app, screen); !containsSubstring(text, "💬") {
		t.Errorf("tree text = %q, want a 💬N marker for example.go's unresolved threads", text)
	}
}

func TestFilesTabSelectingFileRendersHunksGuttersAndMarkers(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)

	// The wait condition deliberately avoids "@@ -1,4 +1,5 @@"-style hunk
	// header text: it already contains literal "+"/"-" characters, so
	// waiting on it would make the "+"/"-" gutter-marker assertion below
	// vacuously true regardless of whether the diff's own per-line markers
	// ever render at all (M2 review round 3, item 24). "func Example()" is
	// a context line's own text, with neither character in it.
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := diffText(app, screen)
		return containsSubstring(text, "example.go") && containsSubstring(text, "func Example()")
	})

	text := diffTextSync(app, screen)
	if !containsSubstring(text, "@@ -1,4 +1,5 @@") || !containsSubstring(text, "@@ -10,1 +11,1 @@") {
		t.Errorf("diff text = %q, want both hunk headers", text)
	}
	if !containsSubstring(text, "+") || !containsSubstring(text, "-") {
		t.Errorf("diff text = %q, want both + and - markers", text)
	}
}

func TestFilesTabNextAndPrevFileSwitchFiles(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	// ]f/[f are only bound in ContextDiff.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	// Files sort alphabetically: assets/image.png, pkg/example.go,
	// pkg/renamed_new.go. example.go is auto-selected first (only page 1
	// has loaded when the tree first auto-selects); "[f" moves back to the
	// alphabetically preceding file (assets/image.png).
	sendRune(app.app, '[')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return app.currentFilePath == "assets/image.png" })

	sendRune(app.app, ']')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return app.currentFilePath == "pkg/example.go" })

	sendRune(app.app, ']')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return app.currentFilePath == "pkg/renamed_new.go" })

	// Already at the last file (alphabetically): ]f must not re-open it
	// (wasted work, and a redundant tree SetCurrentNode) and should tell
	// the user there is nowhere further to go.
	sendRune(app.app, ']')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "last file") })
	if got := query(app.app, func() string { return app.currentFilePath }); got != "pkg/renamed_new.go" {
		t.Errorf("currentFilePath = %q after ]f at the last file, want it unchanged", got)
	}
}

func TestFilesTabStepFileAtTheFirstFileToasts(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	sendRune(app.app, '[')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return app.currentFilePath == "assets/image.png" })

	sendRune(app.app, '[')
	sendRune(app.app, 'f')
	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "first file") })
	if got := query(app.app, func() string { return app.currentFilePath }); got != "assets/image.png" {
		t.Errorf("currentFilePath = %q after [f at the first file, want it unchanged", got)
	}
}

func TestFilesTabVisualSelectAndEscClears(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	// The last two rows are hunk 2's own two lines (oldCall()/newCall()),
	// adjacent with no thread row between them (unlike hunk 1's lines,
	// several of which have inline threads) — a reliable place to start a
	// two-line selection from regardless of how the threads above bucket.
	act(app.app, func() {
		app.diffView.MoveBottom()
		app.diffView.MoveBy(-1)
	})
	waitFor(t, app.app, func() bool {
		_, ok := app.diffView.CursorLine()
		return ok
	})

	sendRune(app.app, 'V')
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool {
		s, ok := app.diffView.Selection()
		return ok && len(s) == 2
	})

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool {
		_, ok := app.diffView.Selection()
		return !ok
	})
}

func TestFilesTabFoldUnfoldThreadAndFoldAll(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "please clarify")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	act(app.app, func() { app.diffView.MoveTop() })
	sendRune(app.app, ']')
	sendRune(app.app, 'c')
	sendRune(app.app, ']')
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool {
		th, _ := app.diffView.CursorThread()
		return th.ID == "thread-open"
	})

	sendRune(app.app, 'z')
	sendRune(app.app, 'a')
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(diffText(app, screen), "please clarify")
	})

	sendRune(app.app, 'z')
	sendRune(app.app, 'R')
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "please clarify")
	})

	sendRune(app.app, 'z')
	sendRune(app.app, 'M')
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return !containsSubstring(diffText(app, screen), "please clarify")
	})
}

// TestFilesTabEscInVisualModeAlsoResetsSequencerPendingPrefix guards
// against a regression where the router's Esc-ends-visual-mode check
// returned before feeding Esc through the Sequencer: since Esc's own job
// there is to reset a pending multi-key prefix, skipping it left a
// half-typed sequence (for example "z", a prefix of za/zM/zR/zh/zl) alive
// across the Esc keypress, so the very next unrelated key could complete
// it (a bare "a" completing the surviving "z" into "za" and folding
// whatever thread happens to be under the cursor by then).
func TestFilesTabEscInVisualModeAlsoResetsSequencerPendingPrefix(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	// A line row (not a thread row) so StartVisual (V) actually activates.
	act(app.app, func() {
		app.diffView.MoveBottom()
		app.diffView.MoveBy(-1)
	})
	waitFor(t, app.app, func() bool {
		_, ok := app.diffView.CursorLine()
		return ok
	})

	sendRune(app.app, 'V')
	waitFor(t, app.app, func() bool { return app.diffView.InVisual() })

	sendRune(app.app, 'z') // a pending prefix of za/zM/zR/zh/zl
	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool { return !app.diffView.InVisual() })

	// "l" alone is bound to nothing in ContextDiff (list.open only binds it
	// in ContextList/ContextFiles); with the bug, the surviving "z"
	// combines with it into "zl" (diff.scroll_right) instead.
	sendRune(app.app, 'l')

	if got := query(app.app, func() int { return app.diffView.HOffset() }); got != 0 {
		t.Errorf("HOffset() = %d after a lone \"l\" following Esc, want 0 (Esc must reset the Sequencer's pending \"z\" prefix, not just end visual mode)", got)
	}
}

func TestFilesTabNextThreadJumps(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	act(app.app, func() { app.diffView.MoveTop() })
	first := query(app.app, func() model.ReviewThread { th, _ := app.diffView.CursorThread(); return th })
	if first.ID == "" {
		t.Fatal("MoveTop() did not land on a thread row (thread-outdated should sort first)")
	}

	sendRune(app.app, ']')
	sendRune(app.app, 'c')
	waitFor(t, app.app, func() bool {
		th, _ := app.diffView.CursorThread()
		return th.ID != "" && th.ID != first.ID
	})
}

// TestFilesTabPageDownMovesFurtherThanHalfDown guards list.page_down/
// list.page_up's wiring through the router into the focused DiffView:
// <PageDown> (a full page) must move the cursor further from the top than
// <C-d> (half a page) does, and <PageUp> must return it to the top.
func TestFilesTabPageDownMovesFurtherThanHalfDown(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	// cursorPos identifies the selectable row under the cursor without
	// comparing model.ReviewThread directly (it holds a []ReviewComment,
	// making it non-comparable with ==): a rowKindLine row's (OldNo, NewNo)
	// together with a rowKindThreadHeader row's thread ID are each unique
	// within this fixture's single file, so either one alone pins down the
	// row down to identity.
	type cursorPos struct {
		lineOK   bool
		oldNo    int
		newNo    int
		threadID string
	}
	cursor := func() cursorPos {
		return query(app.app, func() cursorPos {
			line, lineOK := app.diffView.CursorLine()
			th, _ := app.diffView.CursorThread()
			return cursorPos{lineOK: lineOK, oldNo: line.OldNo, newNo: line.NewNo, threadID: th.ID}
		})
	}

	act(app.app, func() { app.diffView.MoveTop() })
	top := cursor()
	act(app.app, func() { app.diffView.MoveBottom() })
	bottom := cursor()

	act(app.app, func() { app.diffView.MoveTop() })
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl))
	if halfDown := cursor(); halfDown == bottom {
		t.Fatalf("<C-d> from the top already reached the bottom (%+v); fixture too short to distinguish half page from full page", halfDown)
	}

	act(app.app, func() { app.diffView.MoveTop() })
	sendSpecial(app.app, tcell.KeyPgDn)
	if pageDown := cursor(); pageDown != bottom {
		t.Fatalf("<PageDown> from the top = %+v, want the bottom (%+v): a full page must move further than <C-d>", pageDown, bottom)
	}

	sendSpecial(app.app, tcell.KeyPgUp)
	if backAtTop := cursor(); backAtTop != top {
		t.Fatalf("<PageUp> after <PageDown> = %+v, want back at the top (%+v)", backAtTop, top)
	}
}

func TestFilesTabHorizontalScroll(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	sendRune(app.app, 'z')
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.diffView.HOffset() > 0 })

	sendRune(app.app, 'z')
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool { return app.diffView.HOffset() == 0 })
}

func TestFilesTabCtrlWtHidesTree(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	if !query(app.app, func() bool { return app.treeExpanded }) {
		t.Fatal("the tree must start expanded")
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return !app.treeExpanded })
}

// TestFilesTabCtrlWtFromDiffShowsTreeAgain guards against files.toggle_tree
// being bound only in ContextFiles: once the tree is hidden, focus can only
// ever be on the diff (a hidden tree cannot hold focus), so if Ctrl-w t is
// not also reachable from ContextDiff, the tree could never be shown again.
func TestFilesTabCtrlWtFromDiffShowsTreeAgain(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return !app.treeExpanded && app.app.GetFocus() == app.diffView })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.treeExpanded })
}

// TestFilesTabCtrlWtWithNestedDirectoriesDoesNotHang guards against
// files.toggle_tree hiding the tree by shrinking it to zero width: tview
// v0.42.0's TreeView.Draw never returns at width 0 once any node sits three
// levels deep (root/dir/subdir/file — its ancestor-branch loop `continue`s
// without advancing when graphicsX >= width), freezing the UI goroutine
// for good. The shared fixture nests only one level (pkg/example.go), so
// this test adds internal/ui/deep.go. The toggle is driven off-thread under
// a deadline because a regression never returns; the stuck goroutine then
// also blocks the app's own Stop, so go test's own timeout follows the
// failure message.
func TestFilesTabCtrlWtWithNestedDirectoriesDoesNotHang(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureFilesPR(ref)})
	pages := fixtureFilesPages()
	last := &pages[len(pages)-1]
	last.Files = append(last.Files, model.ChangedFile{Path: "internal/ui/deep.go", Status: model.FileStatusModified, Additions: 1})
	fake.SetFilesPages(ref, pages)

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return strings.Contains(treeText(app, screen), "deep.go") })

	done := make(chan struct{})
	go func() {
		defer close(done)
		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
		sendRune(app.app, 't')
		app.app.QueueUpdate(func() { app.app.ForceDraw() })
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI goroutine never returned from drawing after Ctrl-w t hid a tree with nested directories")
	}
	waitFor(t, app.app, func() bool { return !app.treeExpanded && app.app.GetFocus() == app.diffView })
}

// TestFilesTabDiffHintReflectsTreeVisibility guards against the diff
// pane's status-bar hint claiming "Ctrl-w h tree" when the tree is hidden
// (Ctrl-w h from the diff then goes straight to the list, per
// App.focusSequence, not to a hidden tree).
func TestFilesTabDiffHintReflectsTreeVisibility(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	const expandedWant = "j/k move  V select  za fold  ]c/[c thread  ]f/[f file  zh/zl scroll  o browser  Ctrl-w h tree  Ctrl-w t hide tree  gt/Ctrl-l next tab  ? help  q quit"
	expandedHint := query(app.app, func() string { return app.statusBar.hint })
	if expandedHint != expandedWant {
		t.Fatalf("hint with the tree visible = %q, want %q", expandedHint, expandedWant)
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return !app.treeExpanded })

	const hiddenWant = "j/k move  V select  za fold  ]c/[c thread  ]f/[f file  zh/zl scroll  o browser  Ctrl-w t show tree  Ctrl-w h list  gt/Ctrl-l next tab  ? help  q quit"
	hiddenHint := query(app.app, func() string { return app.statusBar.hint })
	if hiddenHint != hiddenWant {
		t.Errorf("hint with the tree hidden = %q, want %q", hiddenHint, hiddenWant)
	}
}

func TestFilesTabEventFileHighlightedAppliesKeywordStyles(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)

	waitFor(t, app.app, func() bool {
		e, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok && e.Highlighted
	})

	x, y, w, h := query4(app.app, func() (int, int, int, int) { return app.diffView.GetRect() })
	keywordStyle := theme.TokenStyle(chroma.Keyword)
	if !hasStyleInRect(screen, x, y, w, h, keywordStyle) {
		t.Errorf("diff pane never drew a cell in chroma.Keyword's style after highlighting completed; text=%q", diffTextSync(app, screen))
	}
}

// TestFilesTabStatusBarUpdatesWhenHighlightingFinishes guards against the
// status bar's "highlighting N" segment freezing at whatever count was
// current the last time some unrelated event (or key press) happened to
// trigger a render: once every hunk across every loaded file has finished
// highlighting, FilesState().Highlighting is 0, and the status bar must
// reflect that on its own, without needing another key pressed first (this
// test sends none after opening the tab).
func TestFilesTabStatusBarUpdatesWhenHighlightingFinishes(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	waitFor(t, app.app, func() bool {
		e, ok := app.deps.Store.FileByPath("pkg/example.go")
		return ok && e.Highlighted
	})
	waitFor(t, app.app, func() bool {
		e, ok := app.deps.Store.FileByPath("pkg/renamed_new.go")
		return ok && e.Highlighted
	})

	got := query(app.app, func() string { return app.statusBar.right })
	if containsSubstring(got, "highlighting") {
		t.Errorf("status bar right = %q after every hunk finished highlighting, want no \"highlighting\" segment", got)
	}
	if !containsSubstring(got, "files 3") {
		t.Errorf("status bar right = %q, want it to still show the files segment (files 3)", got)
	}
}

func TestFilesTabCtrlWlFromTreeFocusesDiffAndCtrlWhReturns(t *testing.T) {
	app, _, _, _ := openFilesTabForFixture(t)

	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.treeView })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.treeView })
}

func TestFilesTabBinaryFileShowsNotAvailable(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)

	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("assets/image.png")
		return ok
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	act(app.app, func() { app.stepFile(-1) }) // example.go -> assets/image.png (alphabetically first)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "Patch not available")
	})
}

// TestFilesTabOWithNoPullRequestOpenShowsAToast mirrors the PR tab's own
// "no pull request open" toast (TestAppOpenBrowserWithPRTabFocusedAndNoPROpenShowsAToast):
// "o" on the diff with nothing open (no thread under the cursor, and
// Store.CurrentPR() nil) must tell the user there is nothing to open,
// rather than silently doing nothing.
// TestFilesTabDiffShowsNoPullRequestOpenWhenNothingIsOpen guards against
// refreshCurrentFile's empty-currentFilePath branch falling into the
// loaded-file rendering path (which would show a misleading "Patch not
// available" and "+0 −0" for a file that does not exist at all) instead of
// a plain, honest empty state.
// TestFilesTabForcePushDoesNotFlashThePreviousCommitsCachedFile is the
// UI-level regression test for a force-push landing while the Files tab is
// visible: App.onPRChangedForFiles calls Store.LoadFiles(false)
// unconditionally on every EventPRChanged, which the store emits *before*
// its own internal HeadOID-based force-reload coupling runs — so by the
// time that internal coupling would have forced a reload, the UI's own
// LoadFiles(false) call may already have shown the previous commit's
// cached page. internal/store.LoadFiles must recognise the HeadOID change
// itself (see the dedicated store-level test in internal/store) so this
// never happens regardless of which caller runs first.
//
// The force-push is driven through Store.RefreshPR (the background
// auto-refresh ticker's own trigger, see internal/store/autorefresh.go),
// not the "R" key: "R" routes through Store.ReloadPR, which invalidates the
// pull request's entire on-disk cache (including its changed-files pages)
// before ever reloading, so the previous commit's page is already gone by
// the time the race this test guards against would otherwise show it.
// RefreshPR never invalidates anything, matching the real background-poll
// scenario the bug report describes.
func TestFilesTabForcePushDoesNotFlashThePreviousCommitsCachedFile(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr1 := fixtureFilesPR(ref)
	pr1.HeadOID = "head1"
	fake.SetPRResult(ref, gh.DetailResult{PR: pr1})
	fake.SetFilesPages(ref, []gh.FilesResult{{Files: []model.ChangedFile{{Path: "old.go", Status: model.FileStatusModified}}}})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("old.go")
		return ok
	})

	// A force-push: the same pull request now reports a different HeadOID
	// and a different set of changed files. The files fetch is blocked so
	// the test can inspect the state strictly between LoadFiles' own
	// synchronous cache-apply step (which runs before the network fetch
	// even starts) and that fetch's confirmation — the exact window the
	// bug this guards against would show stale content in.
	pr2 := fixtureFilesPR(ref)
	pr2.HeadOID = "head2"
	fake.SetPRResult(ref, gh.DetailResult{PR: pr2})
	fake.SetFilesPages(ref, []gh.FilesResult{{Files: []model.ChangedFile{{Path: "new.go", Status: model.FileStatusModified}}}})
	block := make(chan struct{})
	fake.SetFilesBlock(block)

	act(app.app, func() { app.deps.Store.RefreshPR() })
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.HeadOID == "head2"
	})
	waitFor(t, app.app, func() bool { return app.deps.Store.FilesState().Loading })

	if query(app.app, func() struct {
		v  store.FileEntry
		ok bool
	} {
		v, ok := app.deps.Store.FileByPath("old.go")
		return struct {
			v  store.FileEntry
			ok bool
		}{v, ok}
	}).ok {
		t.Error("Store.FileByPath(\"old.go\") still found once the head2 reload started (network fetch still blocked); the previous commit's cache must not be shown for a different HeadOID")
	}

	close(block)
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("new.go")
		return ok
	})
	if text := diffTextSync(app, screen); containsSubstring(text, "old.go") {
		t.Errorf("diff text = %q, want no trace of old.go after the force-push reload settled", text)
	}
}

func TestFilesTabDiffShowsNoPullRequestOpenWhenNothingIsOpen(t *testing.T) {
	app, _, _, screen := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := diffText(app, screen)
		return containsSubstring(text, "no pull request open") &&
			!containsSubstring(text, "Patch not available") &&
			!containsSubstring(text, "+0")
	})
}

// TestFilesTabDiffReportsAFileNotFoundInsteadOfStuckLoading guards against
// a path that will never resolve (every page loaded successfully, no
// error, but the path simply is not among them — a stale selection, most
// plausibly) leaving the diff stuck on "Loading…" forever.
func TestFilesTabDiffReportsAFileNotFoundInsteadOfStuckLoading(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		_, ok := app.deps.Store.FileByPath("pkg/renamed_new.go")
		return ok
	})
	waitFor(t, app.app, func() bool { return !app.deps.Store.FilesState().HasNext })

	act(app.app, func() { app.openFile("does/not/exist.go") })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		text := diffText(app, screen)
		return containsSubstring(text, "file not found") && containsSubstring(text, "does/not/exist.go")
	})
	if text := diffTextSync(app, screen); containsSubstring(text, "Loading") {
		t.Errorf("diff text = %q, want no lingering \"Loading…\" once every page has loaded without error", text)
	}
}

// TestFilesTabDiffShowsAStandingFilesErrorInsteadOfStuckLoading guards
// against a *failed* page fetch leaving the diff on "Loading…" forever for
// a file that would have been on that page — refreshCurrentFile must
// surface FilesState().Err instead once nothing is currently loading.
func TestFilesTabDiffShowsAStandingFilesErrorInsteadOfStuckLoading(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureFilesPR(ref)})
	pages := fixtureFilesPages()
	fake.SetFilesPages(ref, pages[:1]) // only page 1 registered
	fake.SetFilesError(ref, 2, errors.New("network unreachable"))

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool { return app.deps.Store.FilesState().Err != nil })

	// assets/image.png only exists on page 2, which failed to load.
	act(app.app, func() { app.openFile("assets/image.png") })

	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		// The pane is narrow enough in tests to truncate the full message
		// ("network unreachable") with an ellipsis; "network unreach" is
		// short enough to always survive that.
		return containsSubstring(diffText(app, screen), "network unreach")
	})
	if text := diffTextSync(app, screen); containsSubstring(text, "Loading") {
		t.Errorf("diff text = %q, want the standing files error, not a lingering \"Loading…\"", text)
	}
}

func TestFilesTabOWithNoPullRequestOpenShowsAToast(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	// gt is only bound in the detail-side contexts (ContextDetail/Files/
	// Diff), not ContextList, so it only does anything once focus has
	// already moved into the detail column.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })

	// switchTab moved focus to the tree (the files tab's first pane, since
	// the PR tab had focus beforehand); Ctrl-w l advances it to the diff.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "no pull request open")
	})
}

// TestFilesTabTreeOWithNoPullRequestOpenShowsAToast is
// TestFilesTabOWithNoPullRequestOpenShowsAToast's sibling for the tree
// pane itself (M2 review round 3, item 26): openCurrentInBrowser had no
// case for the tree at all, so "o" there fell into the generic default
// branch, which silently does nothing when no pull request is open —
// unlike every other pane's own "no pull request open" toast.
func TestFilesTabTreeOWithNoPullRequestOpenShowsAToast(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.treeView })

	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "no pull request open")
	})
}

// TestFilesTabTreeOWithAPullRequestOpenOpensItsFilesURL is
// TestFilesTabTreeOWithNoPullRequestOpenShowsAToast's positive-path
// counterpart: with a pull request open, "o" on the tree must actually
// open something (the pull request's own "/files" URL, matching the diff
// pane's own fallback when no thread is under its cursor), not merely stop
// short of the toast.
func TestFilesTabTreeOWithAPullRequestOpenOpensItsFilesURL(t *testing.T) {
	app, _, _, ref := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.treeView })

	var opened []string
	act(app.app, func() {
		app.deps.Browser = &browser.Opener{
			Env:      func(string) string { return "" },
			Fallback: func(rawURL string) error { opened = append(opened, rawURL); return nil },
		}
	})

	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return len(opened) == 1 })

	want := fmt.Sprintf("https://github.com/%s/pull/%d/files", ref.Repo.NameWithOwner(), ref.Number)
	if opened[0] != want {
		t.Errorf("o on the tree opened %q, want %q", opened[0], want)
	}
}

func TestFilesTabOOnThreadOpensItsURL(t *testing.T) {
	app, _, screen, _ := openFilesTabForFixture(t)
	waitFor(t, app.app, func() bool {
		app.app.ForceDraw()
		return containsSubstring(diffText(app, screen), "@@ -1,4 +1,5 @@")
	})

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.diffView })

	act(app.app, func() { app.diffView.MoveTop() })
	first := query(app.app, func() model.ReviewThread { th, _ := app.diffView.CursorThread(); return th })
	if first.ID == "" {
		t.Fatal("expected the cursor to start on a thread row")
	}

	var opened []string
	act(app.app, func() {
		app.deps.Browser = &browser.Opener{
			Env:      func(string) string { return "" },
			Fallback: func(rawURL string) error { opened = append(opened, rawURL); return nil },
		}
	})

	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return len(opened) == 1 })
	if want := first.Comments[0].URL; opened[0] != want {
		t.Errorf("o on the thread opened %q, want %q", opened[0], want)
	}
}

// newTreeWithNChildren builds a minimal, real *tview.TreeView with n
// selectable top-level leaf children under an unselectable root (mirroring
// buildFilesTab's own shape), for treeMovablePane unit tests that need a
// tree of a controlled size but nothing else buildFilesTab/rebuildFileTree
// wires up (no App, no store, no fixture data).
func newTreeWithNChildren(n int) (*tview.TreeView, []*tview.TreeNode) {
	tree := tview.NewTreeView()
	root := tview.NewTreeNode("").SetSelectable(false)
	tree.SetRoot(root)
	tree.SetTopLevel(1)
	tree.SetRect(0, 0, 30, 10)
	nodes := make([]*tview.TreeNode, n)
	for i := range nodes {
		nodes[i] = tview.NewTreeNode(fmt.Sprintf("file%d", i))
		root.AddChild(nodes[i])
	}
	if n > 0 {
		tree.SetCurrentNode(nodes[0])
	}
	return tree, nodes
}

// TestTreeMovablePaneMoveByClampsAHugeCountSoItReturnsPromptly guards
// against a numeric prefix like "999999999j": MoveBy replayed one native
// keypress per unit of n with no upper bound, so a huge, deliberately
// absurd count would keep replaying for as long as it takes to exhaust it —
// tens of minutes for a nine-digit count — during which the single-
// threaded event loop that keypress dispatches on (the same one Ctrl-C's
// own key event needs to reach handleKey) can do nothing else at all.
// Clamping n to the tree's own size the moment MoveBy starts makes even an
// absurd count finish in the time an actual "move to the end" would take.
func TestTreeMovablePaneMoveByClampsAHugeCountSoItReturnsPromptly(t *testing.T) {
	tree, nodes := newTreeWithNChildren(20)
	pane := treeMovablePane{tree: tree}

	done := make(chan struct{})
	go func() {
		pane.MoveBy(1_000_000_000)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("MoveBy(a huge count) did not return promptly; it must clamp to the tree's own size instead of replaying the count literally")
	}
	if got := tree.GetCurrentNode(); got != nodes[len(nodes)-1] {
		t.Errorf("current node after MoveBy(a huge count) = %v, want the last node (clamped, not overshooting)", got)
	}
}

// TestTreeMovablePaneMoveTopAndMoveBottomFireChangedAtMostOnceAndAreFast
// guards against MoveTop/MoveBottom replaying nodeCount() native keypresses
// one at a time: each replayed keypress's process() call walks the *whole*
// tree from scratch (O(n) per step, O(n^2) total) and, whenever it actually
// moves, fires the tree's "changed" callback synchronously (unlike
// SetCurrentNode, which defers "changed" to the next Draw — see
// onTreeNodeChanged's own doc comment) — so "G" on a large tree would call
// App.openFile (parsing/highlighting-enqueue included) once per node
// between the old and new cursor position, not just once for the actual
// destination.
func TestTreeMovablePaneMoveTopAndMoveBottomFireChangedAtMostOnceAndAreFast(t *testing.T) {
	const n = 1000
	tree, nodes := newTreeWithNChildren(n)
	tree.SetCurrentNode(nodes[0])

	changedCount := 0
	tree.SetChangedFunc(func(*tview.TreeNode) { changedCount++ })

	pane := treeMovablePane{tree: tree}
	start := time.Now()
	pane.MoveBottom()
	tree.Draw(newTestScreenForFiles(t, 30, 10)) // "changed" only fires on Draw, see onTreeNodeChanged's own doc comment
	elapsed := time.Since(start)

	if changedCount > 1 {
		t.Errorf("changed callback fired %d times after MoveBottom, want at most once (every intermediate node must not be opened)", changedCount)
	}
	if got := tree.GetCurrentNode(); got != nodes[n-1] {
		t.Errorf("current node after MoveBottom = %v, want the last node", got)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("MoveBottom on a %d-node tree took %s, want well under 100ms", n, elapsed)
	}

	changedCount = 0
	pane.MoveTop()
	tree.Draw(newTestScreenForFiles(t, 30, 10))
	if changedCount > 1 {
		t.Errorf("changed callback fired %d times after MoveTop, want at most once", changedCount)
	}
	if got := tree.GetCurrentNode(); got != nodes[0] {
		t.Errorf("current node after MoveTop = %v, want the first node", got)
	}
}

// newTestScreenForFiles mirrors internal/ui/widget's own newTestScreen,
// duplicated here since that helper is unexported to a different package.
func newTestScreenForFiles(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init() failed: %v", err)
	}
	screen.SetSize(w, h)
	return screen
}
