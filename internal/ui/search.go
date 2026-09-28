// search.go implements the "/" cross-file diff search (diff.search,
// diff.search_next/prev): a Go regexp (RE2) with vim smartcase, run on
// Enter (not incremental), matching across every changed file's diff in
// sortedFiles order. n/N step to the next/previous match, jumping across
// files (openFile + tree sync) exactly like stepFile/stepThread when the
// match is not in the currently open file, and wrap with a status-bar
// toast at either end.
package ui

import (
	"fmt"
	"regexp"
	"unicode"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// searchMatch is one regexp hit in a changed file's diff: path, plus the
// (hunk, line) index into that file's store.FileEntry.Hunks — the same
// pair diffRow.hunk/diffRow.line and diff.LocateLine use — and the byte
// offsets of the match within that line's diff.Line.Text.
type searchMatch struct {
	path       string
	hunk, line int
	start, end int
}

// smartcase implements vim's smartcase rule for "/" search: a pattern with
// no uppercase rune is matched case-insensitively (a "(?i)" RE2 flag
// prefix); a pattern containing any uppercase rune is matched exactly as
// typed.
func smartcase(pattern string) string {
	for _, r := range pattern {
		if unicode.IsUpper(r) {
			return pattern
		}
	}
	return "(?i)" + pattern
}

// submitDiffSearch runs the pattern currently typed into a.searchInput
// (Enter): closes the input, compiles it (smartcase, RE2), and recomputes
// every match across the diff. An empty pattern clears any active search
// instead of running one. Compile errors and "no match" are reported as
// toasts, since the diff pane itself has no inline place to show them.
func (a *App) submitDiffSearch() {
	pattern := a.searchInput.GetText()
	a.closeDiffSearch()

	if pattern == "" {
		a.clearDiffSearch()
		return
	}

	re, err := regexp.Compile(smartcase(pattern))
	if err != nil {
		a.showErrorToast("invalid pattern: " + err.Error())
		return
	}

	// searchRef records which pull request this search belongs to, so
	// clearDiffSearchIfWrongPR (storeevents.go) can tell a genuine PR
	// switch apart from an EventPRChanged for the *same* one (a posted
	// comment refetching it, say) — only the former must drop the search.
	if ref, ok := a.deps.Store.CurrentRef(); ok {
		a.searchRef = ref
	}
	a.searchRE = re
	a.searchPattern = pattern
	a.searchMatches = a.computeSearchMatches(re)
	a.searchIdx = -1
	a.diffView.SetSearch(re)

	if len(a.searchMatches) == 0 {
		a.showToast("no match: "+pattern, theme.Warning)
		return
	}
	a.showToast(fmt.Sprintf("%d matches", len(a.searchMatches)), theme.Info)
	// Jump from the cursor's current position right away, mirroring vim's
	// own "/" behaviour — the resulting wrap toast (if the first match
	// happens to be back at the top) is allowed to replace the count toast
	// just shown above.
	a.diffSearchStep(1)
}

// clearDiffSearch discards the active search (an empty "/" submission):
// the diff highlight and every piece of remembered match state.
func (a *App) clearDiffSearch() {
	a.diffView.ClearSearch()
	a.searchRE = nil
	a.searchPattern = ""
	a.searchMatches = nil
	a.searchIdx = -1
}

// clearDiffSearchIfWrongPR drops an active search the instant the currently
// open pull request no longer matches searchRef (a switch while it was
// active), mirroring composer.go's closeComposerIfWrongPR/mergedialog.go's
// closeMergeDialogIfWrongPR — called from EventPRChanged. EventPRChanged
// also fires for the *same* pull request (a posted comment refetching it,
// for example), which must not drop an active search: only a genuine ref
// mismatch does.
func (a *App) clearDiffSearchIfWrongPR() {
	if a.searchRE == nil {
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.searchRef {
		a.clearDiffSearch()
	}
}

// computeSearchMatches finds every occurrence of re across every changed
// file's diff, in sortedFiles (path) order, then hunk, then line, then
// left-to-right match order — the exact order diffSearchStep's
// cursor-relative scan relies on.
func (a *App) computeSearchMatches(re *regexp.Regexp) []searchMatch {
	var matches []searchMatch
	for _, e := range a.sortedFiles() {
		for hi, h := range e.Hunks {
			for li, l := range h.Lines {
				for _, r := range re.FindAllStringIndex(l.Text, -1) {
					matches = append(matches, searchMatch{path: e.File.Path, hunk: hi, line: li, start: r[0], end: r[1]})
				}
			}
		}
	}
	return matches
}

// searchPos orders a diff cursor position, or a searchMatch, for
// diffSearchStep's "next match after/before the cursor" scan: file index
// into sortedFiles, then hunk/line index within that file's Hunks, then
// the match's byte column within the line. -1 in file/hunk/line sorts
// before every real match (see currentSearchPos: "no file open" and "the
// cursor is not on a diff line").
type searchPos struct {
	file, hunk, line, col int
}

// lessSearchPos reports whether a sorts strictly before b in searchPos's
// (file, hunk, line, col) order.
func lessSearchPos(a, b searchPos) bool {
	if a.file != b.file {
		return a.file < b.file
	}
	if a.hunk != b.hunk {
		return a.hunk < b.hunk
	}
	if a.line != b.line {
		return a.line < b.line
	}
	return a.col < b.col
}

// matchSearchPos resolves m's position using fileIndex (path ->
// sortedFiles index, built once per diffSearchStep call).
func matchSearchPos(m searchMatch, fileIndex map[string]int) searchPos {
	return searchPos{file: fileIndex[m.path], hunk: m.hunk, line: m.line, col: m.start}
}

// currentSearchPos resolves the diff cursor's current position against
// entries (a.sortedFiles(), passed in so a caller that already computed it
// need not do so again). col is -1 (any match on the same line counts as
// "after") except when the cursor already sits on the current search
// match (searchIdx), in which case col is that match's own start byte — so
// repeated n presses walk every match on one line individually, instead of
// jumping straight past the rest of them once the cursor first reaches it.
func (a *App) currentSearchPos(entries []store.FileEntry) searchPos {
	file := -1
	for i, e := range entries {
		if e.File.Path == a.currentFilePath {
			file = i
			break
		}
	}

	hunk, line := -1, -1
	if file >= 0 {
		if l, ok := a.diffView.CursorLine(); ok {
			side, no := diff.Anchor(l)
			if hi, li, ok := diff.LocateLine(entries[file].Hunks, side, no); ok {
				hunk, line = hi, li
			}
		}
	}

	col := -1
	if a.searchIdx >= 0 && a.searchIdx < len(a.searchMatches) {
		m := a.searchMatches[a.searchIdx]
		if m.path == a.currentFilePath && m.hunk == hunk && m.line == line {
			col = m.start
		}
	}
	return searchPos{file: file, hunk: hunk, line: line, col: col}
}

// nextSearchMatchIndex finds the first match strictly after pos (dir > 0)
// or the last match strictly before it (dir < 0) in searchMatches' own
// (file, hunk, line, col) order. wrapped reports whether none was found in
// that direction, in which case idx wraps to the opposite end (0 for
// dir > 0, the last index for dir < 0).
func nextSearchMatchIndex(matches []searchMatch, pos searchPos, dir int, fileIndex map[string]int) (idx int, wrapped bool) {
	if dir > 0 {
		for i, m := range matches {
			if lessSearchPos(pos, matchSearchPos(m, fileIndex)) {
				return i, false
			}
		}
		return 0, true
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if lessSearchPos(matchSearchPos(matches[i], fileIndex), pos) {
			return i, false
		}
	}
	return len(matches) - 1, true
}

// diffSearchStep moves the diff cursor to the next (dir > 0) or previous
// (dir < 0) search match relative to its current position
// (diff.search_next/prev, "n"/"N"), wrapping around with a status-bar
// toast when it runs off either end — mirroring vim's own "/" behaviour.
// Toasts "no search pattern"/"no match" instead when there is nothing to
// step through.
func (a *App) diffSearchStep(dir int) {
	if a.searchRE == nil {
		a.showToast("no search pattern", theme.Warning)
		return
	}
	if len(a.searchMatches) == 0 {
		a.showToast("no match: "+a.searchPattern, theme.Warning)
		return
	}
	// Re-enable the highlight unconditionally: cheap (it only forces a
	// rebuild), and simpler than tracking whether an Esc (router.go) had
	// actually cleared it since the last jump.
	a.diffView.SetSearch(a.searchRE)

	entries := a.sortedFiles()
	fileIndex := make(map[string]int, len(entries))
	for i, e := range entries {
		fileIndex[e.File.Path] = i
	}

	idx, wrapped := nextSearchMatchIndex(a.searchMatches, a.currentSearchPos(entries), dir, fileIndex)
	a.jumpToMatch(idx)

	if wrapped {
		if dir > 0 {
			a.showToast("search hit BOTTOM, continuing at TOP", theme.Warning)
		} else {
			a.showToast("search hit TOP, continuing at BOTTOM", theme.Warning)
		}
	}
}

// jumpToMatch moves the diff cursor to searchMatches[idx], opening a
// different file first (and syncing the tree's selection to it, exactly
// like stepFile/stepThread) if the match is not in the file currently
// shown. Focus is left wherever it already was.
//
// The match is validated against the store's current data *before* any of
// that navigation runs: searchMatches can go stale (a pull request switch
// or a files reload — see clearDiffSearchIfWrongPR/EventFilesChanged in
// storeevents.go) between being computed and a later n/N actually jumping
// to one, and a match whose file or line no longer exists must never open
// a missing file or leave the cursor nowhere.
func (a *App) jumpToMatch(idx int) {
	a.searchIdx = idx
	m := a.searchMatches[idx]

	entry, ok := a.deps.Store.FileByPath(m.path)
	if !ok || m.hunk >= len(entry.Hunks) || m.line >= len(entry.Hunks[m.hunk].Lines) {
		return
	}

	if m.path != a.currentFilePath {
		a.openFile(m.path)
		if node := a.findTreeNodeByPath(m.path); node != nil {
			a.treeView.SetCurrentNode(node)
		}
	}

	side, no := diff.Anchor(entry.Hunks[m.hunk].Lines[m.line])
	a.diffView.JumpToLine(side, no)
	a.diffView.SetSearchCurrent(m.path, m.hunk, m.line, m.start, m.end)
}
