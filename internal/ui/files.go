// files.go implements the Files tab: a tview.TreeView of the current pull
// request's changed files (grouped by directory) on the left, and a
// widget.DiffView of whichever file is selected on the right.
package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// treeFileWidth is the file tree's fixed column width when expanded.
const treeFileWidth = 30

// diffScrollStep is how many cells zh/zl scroll the diff's content per
// keypress.
const diffScrollStep = 4

// errNoPullRequestOpen and errNoFileSelected are refreshCurrentFile's two
// distinct empty-state messages for an unset currentFilePath — see its own
// doc comment for when each applies.
var (
	errNoPullRequestOpen = errors.New("no pull request open")
	errNoFileSelected    = errors.New("no file selected")
)

// treeFileRef is the reference tview.TreeNode.SetReference attaches to a
// file leaf node (never a directory node, which carries no reference at
// all), so the tree's callbacks and h/l input capture can tell a file apart
// from a directory without inspecting its display text.
type treeFileRef struct{ path string }

// buildFilesTab constructs the tree and diff panes and wires the callbacks
// that do not depend on Run having started (no store/network calls here —
// see switchTab/subscribeStore for what actually loads data).
func (a *App) buildFilesTab() {
	a.treeView = tview.NewTreeView()
	a.treeView.SetRoot(tview.NewTreeNode("").SetSelectable(false))
	a.treeView.SetTopLevel(1) // hide the invisible root row itself
	a.treeView.SetChangedFunc(a.onTreeNodeChanged)
	a.treeView.SetInputCapture(a.treeInputCapture)
	a.treeExpanded = true

	a.diffView = widget.NewDiffView()

	a.filesFlex = tview.NewFlex().SetDirection(tview.FlexColumn)
	a.layoutFilesPanes()
}

// layoutFilesPanes lays the Files tab's columns out from treeExpanded: the
// tree (when shown) followed by the diff. A hidden tree is left out of the
// Flex entirely, never kept in it at zero width: tview v0.42.0's
// TreeView.Draw never returns at width 0 once any node sits three levels
// deep (its ancestor-branch loop `continue`s without advancing whenever
// graphicsX >= width), which froze the whole UI on Ctrl-w t.
func (a *App) layoutFilesPanes() {
	a.filesFlex.Clear()
	if a.treeExpanded {
		a.filesFlex.AddItem(a.treeView, treeFileWidth, 0, true)
	}
	a.filesFlex.AddItem(a.diffView, 0, 1, false)
}

// onTreeNodeChanged is the tree's native SetChangedFunc callback, firing on
// every cursor movement (including tview's own j/k/g/G/K/J native
// handling): it re-renders the diff for whatever file node the cursor is
// now on, with no debounce (rendering an already-parsed, already-loaded
// file is local, unlike the PR list's network-backed preview). Landing on
// a directory node (no treeFileRef) leaves the diff showing whatever file
// was last open.
//
// lastFallbackNode is captured into a local and cleared unconditionally
// before anything else, rather than only inside the "node == fallback"
// branch below (M2 review round 3, item 21): rebuildFileTree's own
// fallback node can itself sit inside a directory collectDirExpansion
// preserved as collapsed, in which case tview's process() — finding that
// node not among the tree's currently visible, flattened nodes — silently
// substitutes a different one (often the collapsed directory itself, which
// has no treeFileRef and returns above before ever reaching the "node ==
// fallback" check). Left set in that case, the marker would survive to the
// *next* "changed" firing for any reason at all, including the user later
// genuinely expanding that directory and selecting the original fallback
// file themselves — which this callback would then wrongly treat as the
// fallback still resolving (showFile, discarding the choice) instead of a
// deliberate open (openFile, recorded as wantedFilePath).
func (a *App) onTreeNodeChanged(node *tview.TreeNode) {
	fallback := a.lastFallbackNode
	a.lastFallbackNode = nil

	ref, ok := node.GetReference().(treeFileRef)
	if !ok {
		return
	}
	if node == fallback {
		// This transition is rebuildFileTree's own placeholder selection
		// resolving (see lastFallbackNode's doc comment), not the user
		// choosing this file: show it without overwriting wantedFilePath.
		a.showFile(ref.path)
		return
	}
	a.openFile(ref.path)
}

// treeInputCapture adds "h" (collapse the current node, or move to its
// parent if already collapsed or a leaf) on top of tview.TreeView's own
// input handling — which binds j/k/g/G/Enter/Space/K/J itself but nothing
// to "h"/"l" — every other key is returned unchanged.
func (a *App) treeInputCapture(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyRune && ev.Rune() == 'h' && ev.Modifiers() == tcell.ModNone {
		a.treeCollapseOrParent()
		return nil
	}
	return ev
}

// treeCollapseOrParent implements "h": collapse the current node if it is
// an expanded directory, otherwise move to its parent — the latter reuses
// tview.TreeView's own 'K' ("move to parent") handling via a synthetic key
// event, since TreeNode exposes no parent pointer of its own and TreeView's
// selection index is only correct relative to its own flattened,
// draw-time node list.
func (a *App) treeCollapseOrParent() {
	node := a.treeView.GetCurrentNode()
	if node == nil {
		return
	}
	if node.IsExpanded() && len(node.GetChildren()) > 0 {
		node.SetExpanded(false)
		return
	}
	a.treeView.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'K', tcell.ModNone), func(p tview.Primitive) { a.app.SetFocus(p) })
}

// treeMovablePane adapts a *tview.TreeView to the router's movablePane
// interface (see router.go) so list.down/up/top/bottom/half_down/half_up
// reach the tree the same way they reach every other pane, instead of
// leaving it as the sole exception no list.* action can move. MoveBy/
// MoveHalfPage synthesise the exact native key events tview.TreeView's own
// InputHandler already moves the cursor with (bounded — see MoveBy's own
// doc comment); MoveTop/MoveBottom cannot reuse "g"/"G" or a replayed
// "k"/"j" the same way — see their own doc comment for why — and instead
// jump straight to the tree's first/last node with a single Walk plus one
// SetCurrentNode call.
type treeMovablePane struct {
	tree *tview.TreeView
}

// feed forwards one synthetic key event through the tree's own
// InputHandler. setFocus is a no-op: none of TreeView's movement handling
// (treeMove/treeHome/treeEnd, see tview's own InputHandler switch) ever
// calls it, unlike Tab/Backtab/Escape's t.done callback, which
// treeMovablePane never sends.
func (p treeMovablePane) feed(key tcell.Key, r rune) {
	p.tree.InputHandler()(tcell.NewEventKey(key, r, tcell.ModNone), func(tview.Primitive) {})
}

// MoveBy sends n native "j"/"k" keypresses (one per step, matching
// PgDn/PgUp's own one-InputHandler-call-per-page shape below): TreeView's
// InputHandler moves by exactly one node per call, there is no "move by n"
// entry point to call directly instead. n is clamped to ±nodeCount() first
// (M2 review round 3, item 19): list.down/up's count comes straight from a
// user-typed numeric prefix (Sequencer's own count, "999999999j"), and
// without a bound here, replaying it literally would occupy gprt's single-
// threaded event loop — the same one a Ctrl-C keypress needs reach
// handleKey through — for as long as it takes to exhaust an arbitrarily
// large count, effectively freezing the whole application.
func (p treeMovablePane) MoveBy(n int) {
	if bound := p.nodeCount(); n > bound {
		n = bound
	} else if n < -bound {
		n = -bound
	}
	r := 'j'
	if n < 0 {
		r = 'k'
		n = -n
	}
	for range n {
		p.feed(tcell.KeyRune, r)
	}
}

// MoveTop and MoveBottom do NOT send tview's own native "g"/"G": despite
// binding those runes, TreeView's InputHandler sets movement to treeHome/
// treeEnd, which process()'s own selection switch (treeMove/treeParent/
// treeChild) has no case for at all — Draw() moves the *scroll offset*
// (t.offsetY) for treeHome/treeEnd, but t.currentNode itself never changes
// (confirmed by reading tview's own treeview.go: this is a scroll, not a
// selection jump). They also do NOT replay nodeCount() native "k"/"j"
// keypresses one at a time the way an earlier version of this adapter did
// (M2 review round 3, item 20): each replayed keypress's process() call
// walks the *whole* tree from scratch (O(n) per step, so O(n²) total for a
// tree of n nodes) and, whenever it actually moves, synchronously fires the
// tree's "changed" callback (process(), unlike SetCurrentNode, never defers
// it to Draw) — so "G" on a large tree would call onTreeNodeChanged, and so
// App.openFile, once per node between the old and new position, not once
// for the actual destination. Instead, firstAndLastVisible walks the tree
// exactly once to find both ends and SetCurrentNode jumps there directly —
// the same one-Walk-then-SetCurrentNode-once shape App.stepFile already
// uses for ]f/[f.
func (p treeMovablePane) MoveTop() {
	if first, _ := p.firstAndLastVisible(); first != nil {
		p.tree.SetCurrentNode(first)
	}
}

func (p treeMovablePane) MoveBottom() {
	if _, last := p.firstAndLastVisible(); last != nil {
		p.tree.SetCurrentNode(last)
	}
}

// firstAndLastVisible returns the tree's first and last node in the same
// order tview.TreeView's own Draw flattens it into for display: a single
// Walk, descending into a node's children only when it reports itself
// expanded (Walk's own callback return value), skipping the invisible root
// itself (which SetTopLevel(1) also hides from Draw, and which
// buildFilesTab's own root.SetSelectable(false) marks as never a real
// destination). Every non-root node gprt's own tree ever builds is
// selectable (rebuildFileTree never calls SetSelectable(false) on a
// directory or file node), so — unlike a general-purpose TreeView — the
// only node here that is not a legitimate destination is the root.
func (p treeMovablePane) firstAndLastVisible() (first, last *tview.TreeNode) {
	root := p.tree.GetRoot()
	if root == nil {
		return nil, nil
	}
	root.Walk(func(node, _ *tview.TreeNode) bool {
		if node != root {
			if first == nil {
				first = node
			}
			last = node
		}
		return node.IsExpanded()
	})
	return first, last
}

// nodeCount returns an upper bound on the tree's node count: Walk visits
// every node regardless of expansion state, so this may overcount past what
// is actually visible, which is harmless for MoveTop/MoveBottom's purposes
// (each extra step is a no-op once the boundary is reached) and far cheaper
// than re-flattening the tree the same way TreeView's own Draw does.
func (p treeMovablePane) nodeCount() int {
	root := p.tree.GetRoot()
	if root == nil {
		return 0
	}
	n := 0
	root.Walk(func(*tview.TreeNode, *tview.TreeNode) bool {
		n++
		return true
	})
	return n
}

// MoveHalfPage and MovePage both send the native PgUp/PgDn keypresses:
// TreeView's own InputHandler moves by its full visible height for these
// (not literally half), which this adapter keeps rather than reimplementing
// ListView's own half-page/page arithmetic against the tree's unrelated
// flattened node list — so both methods are identical here even though they
// differ (half vs. full page) on every other movable pane.
func (p treeMovablePane) MoveHalfPage(dir int) {
	if dir < 0 {
		p.feed(tcell.KeyPgUp, 0)
		return
	}
	p.feed(tcell.KeyPgDn, 0)
}

func (p treeMovablePane) MovePage(dir int) {
	if dir < 0 {
		p.feed(tcell.KeyPgUp, 0)
		return
	}
	p.feed(tcell.KeyPgDn, 0)
}

// treeOpen implements "l"/Enter on the tree (list.open, ContextFiles): a
// collapsed directory expands; an already-expanded directory moves the
// cursor onto its first child; a file opens it and moves focus to the
// diff.
func (a *App) treeOpen() {
	node := a.treeView.GetCurrentNode()
	if node == nil {
		return
	}
	if children := node.GetChildren(); len(children) > 0 {
		if !node.IsExpanded() {
			node.SetExpanded(true)
		} else {
			a.treeView.SetCurrentNode(children[0])
		}
		return
	}
	ref, ok := node.GetReference().(treeFileRef)
	if !ok {
		return
	}
	a.openFile(ref.path)
	a.app.SetFocus(a.diffView)
}

// toggleTree shows or hides the file tree column (files.toggle_tree,
// "Ctrl-w t"), moving focus to the diff first if the tree had it (a pane
// no longer in the layout cannot hold focus).
func (a *App) toggleTree() {
	if a.treeExpanded && a.app.GetFocus() == a.treeView {
		a.app.SetFocus(a.diffView)
	}
	a.treeExpanded = !a.treeExpanded
	a.layoutFilesPanes()
}

// openFile makes path the file currently shown in the diff.
func (a *App) openFile(path string) {
	a.wantedFilePath = path
	a.showFile(path)
}

// showFile re-renders the diff for path without recording it as
// wantedFilePath: used by onTreeNodeChanged for a placeholder-selection
// transition (see lastFallbackNode's doc comment), which must not be
// mistaken for the user asking to open path.
func (a *App) showFile(path string) {
	a.currentFilePath = path
	a.refreshCurrentFile()
}

// refreshCurrentFile re-renders the diff from the store's current data for
// currentFilePath: called after a file is opened, after a files/highlight
// event, and after the open pull request's threads change.
//
// currentFilePath == "" renders a plain empty state — "No pull request
// open" when none is, else "No file selected" — never the loaded-file
// branch below (which would otherwise show a misleading "Patch not
// available" and "+0 −0" for a file that does not exist at all).
//
// Otherwise, once the file is actually found in Store.Files(), its own
// data always wins (including its own ParseErr, if any). Until it is
// found, FilesState() decides what "not found yet" means: a standing
// files-fetch error takes priority (so a failed page load reports that
// error instead of showing "Loading…" forever); still loading, or more
// pages remain to search, keeps showing "Loading…"; otherwise every page
// has arrived without error and the file simply is not among them —
// reported as its own distinct error rather than silently reusing either
// of the above.
func (a *App) refreshCurrentFile() {
	// refreshCurrentFileCalls exists only so tests can assert this is not
	// called more often than it needs to be (see composer.go's
	// onComposerChange, which used to call it once per keystroke) — never
	// read outside a test.
	a.refreshCurrentFileCalls++
	if a.currentFilePath == "" {
		var err error
		if a.deps.Store.CurrentPR() == nil {
			err = errNoPullRequestOpen
		} else {
			err = errNoFileSelected
		}
		a.diffView.SetFile(widget.DiffFile{Err: err})
		return
	}

	if entry, ok := a.deps.Store.FileByPath(a.currentFilePath); ok {
		a.diffView.SetFile(widget.DiffFile{
			Path:         entry.File.Path,
			PreviousPath: entry.File.PreviousPath,
			Status:       entry.File.Status,
			Additions:    entry.File.Additions,
			Deletions:    entry.File.Deletions,
			HasPatch:     entry.File.HasPatch,
			Hunks:        entry.Hunks,
			Tokens:       entry.Tokens,
			Threads:      threadsForPath(a.deps.Store.CurrentPR(), entry.File.Path),
			Err:          entry.ParseErr,
			DraftLines:   a.draftLinesForFile(entry.Hunks, entry.File.Path),
			DraftMarker:  a.deps.Icons.DraftMarker,
		})
		return
	}

	st := a.deps.Store.FilesState()
	switch {
	case st.Err != nil:
		a.diffView.SetFile(widget.DiffFile{Path: a.currentFilePath, Err: st.Err})
	case st.Loading || st.HasNext:
		a.diffView.SetFile(widget.DiffFile{Path: a.currentFilePath, Loading: true})
	default:
		a.diffView.SetFile(widget.DiffFile{Path: a.currentFilePath, Err: fmt.Errorf("file not found: %s", a.currentFilePath)})
	}
}

// draftLinesForFile returns the (hunk, line) markers for every saved
// line/range-comment draft anchored to path, for the diff gutter's own "✎"
// marker (widget.DiffFile.DraftLines) — O(drafts for the current pull
// request) per call. Computed once whenever the file is (re-)shown
// (refreshCurrentFile, this method's only caller) and once more after a
// draft for path is saved or deleted (composer.go's
// refreshDraftGutterIfNeeded), never per Draw. A range draft's marker sits
// on its own end line (Line), matching parseLineAnchor's own convention.
func (a *App) draftLinesForFile(hunks []diff.Hunk, path string) map[widget.DraftAnchor]bool {
	if a.deps.Drafts == nil {
		return nil
	}
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return nil
	}
	list, err := a.deps.Drafts.List(ref.Key())
	if err != nil {
		a.deps.Logger.Warn("draft list for gutter marker failed", "err", err)
	}
	if len(list) == 0 {
		return nil
	}

	marks := make(map[widget.DraftAnchor]bool)
	for _, d := range list {
		if d.Key.Kind != drafts.KindComment {
			continue
		}
		p, side, _, line, ok := parseLineAnchor(d.Key.Anchor)
		if !ok || p != path {
			continue
		}
		hi, li, ok := diff.LocateLine(hunks, side, line)
		if !ok {
			continue
		}
		marks[widget.DraftAnchor{Hunk: hi, Line: li}] = true
	}
	return marks
}

// refreshDraftGutterIfNeeded re-renders the diff (which recomputes
// draftLinesForFile as a side effect) when target's own draft was just
// saved or deleted and its path is the file currently shown — composer.go
// calls this from onComposerChange (every keystroke) and closeComposer
// (final save/delete), so the "✎" marker tracks a line/range/file draft's
// text as it is written, not only once the composer closes. A no-op for
// every other composer kind, or when target's path is not the open file.
func (a *App) refreshDraftGutterIfNeeded(target composerTarget) {
	switch target.kind {
	case composerKindLineComment, composerKindFileComment:
		a.refreshDraftGutterForPath(target.path)
	}
}

// refreshDraftGutterForPath re-renders the diff when path is the file
// currently shown — the same effect as refreshDraftGutterIfNeeded, for a
// caller (the "p" pending-list dialog's own draft deletion) that has a
// bare path rather than a composerTarget.
func (a *App) refreshDraftGutterForPath(path string) {
	if path == a.currentFilePath {
		a.refreshCurrentFile()
	}
}

// threadsForPath returns pr's review threads anchored to path, or nil when
// pr is nil.
func threadsForPath(pr *model.PullRequest, path string) []model.ReviewThread {
	if pr == nil {
		return nil
	}
	var out []model.ReviewThread
	for _, t := range pr.ReviewThreads {
		if t.Path == path {
			out = append(out, t)
		}
	}
	return out
}

// onPRChangedForFiles resets the Files tab's own state (currentFilePath,
// wantedFilePath, lastFallbackNode, the tree) when EventPRChanged reports
// a *different* pull request than the one the Files tab was last built
// for — a stale path from the previous pull request must never be looked
// up against the new one's file list — then rebuilds it and, while the
// Files tab is the visible one, starts loading the new pull request's
// files.
func (a *App) onPRChangedForFiles() {
	ref, hasRef := a.deps.Store.CurrentRef()
	if prRefChanged(a.filesForRef, ref, hasRef) {
		a.currentFilePath = ""
		a.wantedFilePath = ""
		a.lastFallbackNode = nil
		a.dirExpansion = nil
		if hasRef {
			r := ref
			a.filesForRef = &r
		} else {
			a.filesForRef = nil
		}
	}
	if a.currentTab == "files" {
		a.deps.Store.LoadFiles(false)
	}
	a.rebuildFileTree()
	a.refreshCurrentFile()
}

// sortedFiles returns the current pull request's loaded files sorted by
// path — the fixed order both the tree and stepFile use, so "next/previous
// file" always agrees with the tree's own layout.
func (a *App) sortedFiles() []store.FileEntry {
	entries := append([]store.FileEntry(nil), a.deps.Store.Files()...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].File.Path < entries[j].File.Path })
	return entries
}

// rebuildFileTree rebuilds the tree from the store's current files,
// grouping them by directory (each directory node created once, reused by
// every file/subdirectory under it, and expanded by default). The cursor is
// restored in priority order: the directory the user was sitting on, if
// still present (see currentTreeCursorDirPath's own doc comment for why
// this is directories only, and why it outranks the wanted file below);
// otherwise the currently open file's node, if it still exists; otherwise
// the first file is selected and opened automatically once any files have
// arrived.
func (a *App) rebuildFileTree() {
	pr := a.deps.Store.CurrentPR()
	entries := a.sortedFiles()
	// Only overwrite the remembered expansion state when this rebuild's own
	// tree actually had directories to collect it from: a reload/force-push
	// rebuilds the tree at least once while Store.Files() is transiently
	// empty (see internal/store's LoadFiles doc comment), and collecting an
	// empty map right then would otherwise erase every directory's
	// collapsed state before the rebuild that has files to apply it to ever
	// runs (M2 review round 3, item 23) — see dirExpansion's own doc
	// comment on the App struct.
	if collected := a.collectDirExpansion(); len(collected) > 0 {
		a.dirExpansion = collected
	}
	cursorDirPath, hadCursorDir := a.currentTreeCursorDirPath()

	root := tview.NewTreeNode("").SetSelectable(false)
	dirNodes := map[string]*tview.TreeNode{"": root}

	var wantedNode, firstNode, cursorDirNode *tview.TreeNode
	var firstPath string
	for _, e := range entries {
		segments := strings.Split(e.File.Path, "/")
		parent := root
		prefix := ""
		for i, seg := range segments {
			if prefix == "" {
				prefix = seg
			} else {
				prefix = prefix + "/" + seg
			}
			if i == len(segments)-1 {
				leaf := tview.NewTreeNode(fileNodeText(e, threadMarkerFor(pr, e.File.Path)))
				leaf.SetColor(fileNodeColor(e.File.Status))
				leaf.SetReference(treeFileRef{path: e.File.Path})
				parent.AddChild(leaf)
				if firstNode == nil {
					firstNode, firstPath = leaf, e.File.Path
				}
				if e.File.Path == a.wantedFilePath {
					wantedNode = leaf
				}
				continue
			}
			dir, ok := dirNodes[prefix]
			if !ok {
				dir = tview.NewTreeNode(seg)
				if exp, ok := a.dirExpansion[prefix]; ok {
					dir.SetExpanded(exp)
				}
				dirNodes[prefix] = dir
				parent.AddChild(dir)
			}
			if hadCursorDir && prefix == cursorDirPath {
				cursorDirNode = dir
			}
			parent = dir
		}
	}

	a.treeView.SetRoot(root)
	switch {
	case cursorDirNode != nil:
		// The user was sitting on this exact directory (see
		// currentTreeCursorDirPath's own doc comment for why this takes
		// priority over wantedNode, and why it is directories only): a new
		// page of files arriving must not yank the cursor away from it back
		// onto wantedFilePath, which navigating onto a directory never
		// touches at all.
		a.treeView.SetCurrentNode(cursorDirNode)
	case wantedNode != nil:
		a.treeView.SetCurrentNode(wantedNode)
	case firstNode != nil:
		// The wanted file (if any was ever chosen) is not among the
		// currently loaded files — its own page has not arrived, or
		// re-arrived after a reload, yet. A tree with files in it must
		// never end up with no current node at all regardless (tview
		// otherwise leaves it nil forever once it has gone nil once,
		// which no key here would ever recover from on its own), so fall
		// back to the first file; lastFallbackNode marks this specific
		// selection so onTreeNodeChanged knows not to mistake it for the
		// user deliberately choosing this file once the tree's own
		// deferred "changed" callback fires for it.
		a.lastFallbackNode = firstNode
		a.treeView.SetCurrentNode(firstNode)
		if a.wantedFilePath == "" {
			a.wantedFilePath = firstPath
		}
		if a.currentFilePath == "" {
			a.showFile(firstPath)
		}
	}
}

// collectDirExpansion walks the tree's *current* nodes and records each
// directory's own expanded state, keyed by its full path prefix (built the
// same way the main build loop above does, from the accumulated segment
// names) — every rebuild otherwise constructs entirely new
// *tview.TreeNode values, which default to expanded, silently re-expanding
// anything the user had collapsed.
func (a *App) collectDirExpansion() map[string]bool {
	root := a.treeView.GetRoot()
	if root == nil {
		return nil
	}
	expanded := map[string]bool{}
	var walk func(node *tview.TreeNode, prefix string)
	walk = func(node *tview.TreeNode, prefix string) {
		for _, child := range node.GetChildren() {
			if _, isFile := child.GetReference().(treeFileRef); isFile {
				continue // files have no expand/collapse state of their own
			}
			childPrefix := child.GetText()
			if prefix != "" {
				childPrefix = prefix + "/" + childPrefix
			}
			expanded[childPrefix] = child.IsExpanded()
			walk(child, childPrefix)
		}
	}
	walk(root, "")
	return expanded
}

// currentTreeCursorDirPath returns the tree's current node's own path
// prefix, if that node is a directory — built the same way
// collectDirExpansion computes one, and only ever non-empty for a
// directory, never a file or the invisible root (M2 review round 3, item
// 28a).
//
// This is deliberately directory-only, not a general "restore the cursor
// to wherever it was" mechanism covering files too: wantedFilePath already
// tracks "the file the user genuinely wants shown" independently of the
// tree's own cursor (see its own doc comment on the App struct), including
// the case where the tree's cursor is only *temporarily* sitting on a
// fallback file while the true wanted one has not arrived yet (see
// lastFallbackNode's own doc comment, and
// TestFilesTabRebuildKeepsATreeCursorWhenTheCurrentFileIsNotYetLoaded).
// Unifying file positions into this same priority tier would make a
// rebuild's own fallback pick "sticky" across further rebuilds instead of
// re-evaluating wantedFilePath/the true first file each time, which is a
// different (and weaker) guarantee than wantedFilePath already gives.
// Directories carry no such competing state at all — onTreeNodeChanged
// returns immediately for any node without a treeFileRef, so navigating
// onto one never touches wantedFilePath or currentFilePath — which is
// exactly why rebuildFileTree had nothing of its own remembering a
// directory selection before this.
func (a *App) currentTreeCursorDirPath() (path string, ok bool) {
	current := a.treeView.GetCurrentNode()
	root := a.treeView.GetRoot()
	if current == nil || root == nil || current == root {
		return "", false
	}
	if _, isFile := current.GetReference().(treeFileRef); isFile {
		return "", false
	}
	var walk func(node *tview.TreeNode, prefix string) bool
	walk = func(node *tview.TreeNode, prefix string) bool {
		for _, child := range node.GetChildren() {
			if _, isFile := child.GetReference().(treeFileRef); isFile {
				continue
			}
			childPrefix := child.GetText()
			if prefix != "" {
				childPrefix = prefix + "/" + childPrefix
			}
			if child == current {
				path, ok = childPrefix, true
				return true
			}
			if walk(child, childPrefix) {
				return true
			}
		}
		return false
	}
	walk(root, "")
	return path, ok
}

// fileNodeText renders a file leaf's label: "<status glyph> name  +n −m",
// plus marker if non-empty.
func fileNodeText(e store.FileEntry, marker string) string {
	segments := strings.Split(e.File.Path, "/")
	name := segments[len(segments)-1]
	return fmt.Sprintf("%s %s  +%d −%d%s", widget.FileStatusGlyph(e.File.Status), name, e.File.Additions, e.File.Deletions, marker)
}

// fileNodeColor picks a file leaf's colour by status: added green, removed
// red, renamed cyan, everything else the base foreground.
func fileNodeColor(status model.FileStatus) tcell.Color {
	switch status {
	case model.FileStatusAdded:
		fg, _, _ := theme.Success.Decompose()
		return fg
	case model.FileStatusRemoved:
		fg, _, _ := theme.Error.Decompose()
		return fg
	case model.FileStatusRenamed:
		return tcell.ColorDarkCyan
	default:
		fg, _, _ := theme.Base.Decompose()
		return fg
	}
}

// threadMarkerFor returns a file's thread-count marker for the tree: "" for
// no threads, " 💬N" while at least one is unresolved (an active
// discussion), or " ◌N" when every one of its N threads is resolved.
func threadMarkerFor(pr *model.PullRequest, path string) string {
	if pr == nil {
		return ""
	}
	total, unresolved := 0, 0
	for _, t := range pr.ReviewThreads {
		if t.Path != path {
			continue
		}
		total++
		if !t.IsResolved {
			unresolved++
		}
	}
	if total == 0 {
		return ""
	}
	if unresolved > 0 {
		return fmt.Sprintf(" 💬%d", total)
	}
	return fmt.Sprintf(" ◌%d", total)
}

// findTreeNodeByPath walks the tree for the leaf node referencing path.
func (a *App) findTreeNodeByPath(path string) *tview.TreeNode {
	root := a.treeView.GetRoot()
	if root == nil {
		return nil
	}
	var found *tview.TreeNode
	root.Walk(func(node, _ *tview.TreeNode) bool {
		if ref, ok := node.GetReference().(treeFileRef); ok && ref.path == path {
			found = node
			return false
		}
		return found == nil
	})
	return found
}

// stepFile moves the currently open file by dir positions through
// sortedFiles (]f/[f, diff.next_file/prev_file): a no-op with no files
// loaded. The tree's own selection follows, so it never disagrees with
// what the diff is showing. Already at the first/last file, it toasts
// rather than silently re-opening (and re-selecting in the tree) the
// exact file already open.
func (a *App) stepFile(dir int) {
	entries := a.sortedFiles()
	if len(entries) == 0 {
		return
	}

	idx := -1
	for i, e := range entries {
		if e.File.Path == a.currentFilePath {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	} else {
		next := clampInt(idx+dir, 0, len(entries)-1)
		if next == idx {
			if dir > 0 {
				a.showToast("last file", theme.Warning)
			} else {
				a.showToast("first file", theme.Warning)
			}
			return
		}
		idx = next
	}

	path := entries[idx].File.Path
	a.openFile(path)
	if node := a.findTreeNodeByPath(path); node != nil {
		a.treeView.SetCurrentNode(node)
	}
}

// stepThread crosses ]c/[c (diff.next_thread/prev_thread) into a
// neighbouring file once widget.DiffView.NextThread/PrevThread report no
// further thread-header row in the current one: modeled on stepFile, but
// skipping past any file with no review threads at all (threadsForPath) —
// the file list itself is small enough per pull request that filtering it
// here, rather than precomputing an index, costs nothing worth avoiding. A
// no-op with no files loaded; when idx is not found (nothing open yet) the
// scan starts from the very first (dir > 0) or very last (dir < 0) file, so
// every file is covered either way. The tree's own selection follows the
// opened file, exactly like stepFile. No file (current or beyond) has a
// thread left in the given direction: toasts "last comment"/"first
// comment", mirroring stepFile's own "last file"/"first file" wording,
// rather than silently doing nothing.
func (a *App) stepThread(dir int) {
	entries := a.sortedFiles()
	if len(entries) == 0 {
		return
	}

	idx := -1
	for i, e := range entries {
		if e.File.Path == a.currentFilePath {
			idx = i
			break
		}
	}
	if idx < 0 && dir < 0 {
		idx = len(entries)
	}

	pr := a.deps.Store.CurrentPR()
	for i := idx + dir; i >= 0 && i < len(entries); i += dir {
		path := entries[i].File.Path
		if len(threadsForPath(pr, path)) == 0 {
			continue
		}
		a.openFile(path)
		if node := a.findTreeNodeByPath(path); node != nil {
			a.treeView.SetCurrentNode(node)
		}
		a.diffView.JumpToThreadEdge(dir)
		return
	}

	if dir > 0 {
		a.showToast("last comment", theme.Warning)
	} else {
		a.showToast("first comment", theme.Warning)
	}
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// filesStatusSummary renders the Files tab's status-bar segment: "files
// <loaded>/<total>  " once the pull request's own ChangedFiles count makes
// a fraction meaningful, or "files <pages> pages…  " while more pages
// remain and the total is not yet known; "highlighting N  " is appended
// while any hunk highlight job is still outstanding. "" while nothing is
// loading or loaded at all.
func (a *App) filesStatusSummary() string {
	st := a.deps.Store.FilesState()
	loaded := len(a.deps.Store.Files())

	var out string
	switch {
	case loaded == 0 && !st.Loading:
		// Nothing loaded yet and nothing in flight: no segment at all.
	case !st.HasNext:
		out = fmt.Sprintf("files %d  ", loaded)
	case a.deps.Store.CurrentPR() != nil && a.deps.Store.CurrentPR().ChangedFiles > 0:
		out = fmt.Sprintf("files %d/%d  ", loaded, a.deps.Store.CurrentPR().ChangedFiles)
	default:
		out = fmt.Sprintf("files %d pages…  ", st.PagesLoaded)
	}

	if st.Highlighting > 0 {
		out += fmt.Sprintf("highlighting %d  ", st.Highlighting)
	}
	return out
}

// checkFilesWarnings toasts the current pull request's files warnings the
// first time they appear, mirroring checkSectionWarnings' "announce once"
// rule for the list's own per-section warnings.
func (a *App) checkFilesWarnings() {
	warnings := a.deps.Store.FilesState().Warnings
	if len(warnings) == 0 {
		a.filesWarned = false
		return
	}
	if a.filesWarned {
		return
	}
	a.filesWarned = true
	a.showToast("files: "+warnings[0], theme.Warning)
}
