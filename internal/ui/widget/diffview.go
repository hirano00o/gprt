// diffview.go implements DiffView, the Files tab's right-hand pane: a
// unified diff (file header, hunk headers, add/context/delete lines with
// gutters and syntax highlighting) with inline, foldable review-thread
// blocks and vim-style navigation (cursor, visual line selection,
// horizontal scroll). Like ListView/DetailView, it never handles key input
// itself — internal/ui's router calls its movement/fold/selection methods
// directly.
package widget

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// DiffFile is the data DiffView renders: one changed file's status, parsed
// hunks, per-hunk syntax tokens (nil until highlighting completes), and the
// review threads anchored to it.
type DiffFile struct {
	Path         string
	PreviousPath string
	Status       model.FileStatus
	Additions    int
	Deletions    int
	// HasPatch is false for a binary or too-large file, which GitHub never
	// returns a patch for; Hunks/Tokens are meaningless in that case.
	HasPatch bool
	Hunks    []diff.Hunk
	// Tokens holds one slice of per-line tokens per hunk (hunk index -> line
	// index -> tokens), mirroring internal/store's FileEntry.Tokens. A nil
	// (or too-short) entry falls back to plain, unhighlighted text.
	Tokens [][][]highlight.Token
	// Threads are the review threads on this file: a subset of the current
	// pull request's ReviewThreads matching Path (internal/ui's Files tab
	// filters by path before calling SetFile).
	Threads []model.ReviewThread
	// Loading reports that the file's content (or its containing page) is
	// still being fetched: Hunks/Tokens are not rendered while true, even
	// if present from a previous SetFile call, since they may not reflect
	// this file at all yet.
	Loading bool
	Err     error
	// DraftLines marks which lines have a saved comment draft anchored to
	// them, keyed by their (hunk, line) index into Hunks — internal/ui
	// computes this once per file open/draft save/delete (never per Draw;
	// see docs/DESIGN.md), by parsing its own draft anchors back into a
	// location (see internal/ui's draft anchor helpers). Empty/nil means no
	// drafts for this file.
	DraftLines map[DraftAnchor]bool
	// DraftMarker is the gutter glyph drawn on a DraftLines line, from the
	// configured icon set (theme.Icons.DraftMarker). Empty disables the
	// marker column's glyph, but the column itself is always reserved so
	// every line's gutter stays the same width.
	DraftMarker string
	// HeadLineCount is the file's head (new/right side) full line count,
	// known once its content has been fetched once (see internal/store's
	// ExpandGap) - meaningless while HeadKnown is false. Only used to size
	// the trailing gap row (see diffBuilder.build): while unknown, that row
	// shows an "expand" prompt with no line count instead of a number.
	HeadLineCount int
	HeadKnown     bool
}

// DraftAnchor identifies one diff line (by its Hunk/Line index into
// DiffFile.Hunks, the same index pair diffRow.hunk/diffRow.line use) that
// has a saved comment draft, for DiffFile.DraftLines.
type DraftAnchor struct {
	Hunk, Line int
	// Side is the side of the diff the draft's anchor names. The split
	// layout marks only the column on that side, so a draft on a context
	// line shows once; the unified layout marks a line only for the side
	// diff.Anchor derives from its kind (Del LEFT, else RIGHT).
	Side model.DiffSide
}

// FileStatusGlyph returns the single-character glyph DiffView's file header
// and the Files tab's tree both use for a file's status, matching
// well-known git/GitHub short status codes.
func FileStatusGlyph(s model.FileStatus) string {
	switch s {
	case model.FileStatusAdded:
		return "A"
	case model.FileStatusRemoved:
		return "D"
	case model.FileStatusModified:
		return "M"
	case model.FileStatusRenamed:
		return "R"
	case model.FileStatusCopied:
		return "C"
	case model.FileStatusChanged:
		return "~"
	default:
		return " "
	}
}

// diffRowKind distinguishes the kinds of row DiffView's rebuild produces.
type diffRowKind int

const (
	rowKindFileHeader diffRowKind = iota
	rowKindOutdatedHeader
	rowKindHunkHeader
	rowKindLine
	rowKindThreadHeader
	rowKindThreadComment
	// rowKindGap is a collapsed-region row ("⋯ N lines hidden ⋯") between
	// hunks, before the first, or after the last (see diffBuilder.build).
	// Selectable: Enter on it expands the region (internal/ui's
	// App.expandGapAtCursor).
	rowKindGap
)

// diffHalf is one column of a rowKindLine row: the whole row in the unified
// layout, the old or the new side in the split layout (line -1 marks a blank
// half facing a line that has no counterpart). It keeps the gutter (line numbers + marker,
// never horizontally scrolled) and the content (syntax spans, scrolled by
// hOffset) separate so Draw can apply the horizontal-scroll offset to the
// content only. content holds one []Span per display line, so a half can
// span several screen lines without the row's other halves having to; a row
// is as tall as its tallest half.
type diffHalf struct {
	// line indexes DiffFile.Hunks[row.hunk].Lines (never OldNo/NewNo, which
	// are not unique per line).
	line    int
	kind    diff.LineKind
	gutter  []Span
	content [][]Span
	// truncated reports that text past the last content line was dropped
	// at rebuild (see maxWrapLines).
	truncated bool
}

// diffRow is one row of DiffView's rebuilt content. Only rowKindLine,
// rowKindThreadHeader and rowKindGap are selectable. rowKindLine rows carry their
// columns in halves; every other kind uses the generic, possibly-multi-line
// lines field instead.
type diffRow struct {
	kind diffRowKind

	// hunk identifies a rowKindLine row's hunk in DiffFile.Hunks (its line
	// within the hunk is halves[i].line), and is otherwise unused (zero).
	hunk int

	// halves is a rowKindLine row's columns: one in the unified layout, two
	// (old, new) in the split layout.
	halves []diffHalf

	// threadID identifies a rowKindThreadHeader row's thread, for
	// SetThreadFolded/ToggleFoldAtCursor/CursorThread and for restoring the
	// cursor by identity across a rebuild.
	threadID string

	// gapStart/gapCount identify a rowKindGap row: gapStart is the new-side
	// line number of the gap's first hidden line (its identity - see
	// keyForRow's doc comment for why never a hunk index), gapCount is how
	// many lines it hides (0 means not yet known - the trailing gap before
	// the file's head content has ever been fetched).
	gapStart, gapCount int

	lines [][]Span
}

// primary returns r's first half (its only one in the unified layout), or the
// zero diffHalf for kinds that have none.
func (r diffRow) primary() diffHalf {
	if len(r.halves) == 0 {
		return diffHalf{}
	}
	return r.halves[0]
}

// isSplit reports whether r is a two-column (split layout) line row.
func (r diffRow) isSplit() bool { return len(r.halves) == 2 }

// halfOfLine returns the index of r's half showing line, or -1.
func (r diffRow) halfOfLine(line int) int {
	if line < 0 {
		return -1
	}
	for i, h := range r.halves {
		if h.line == line {
			return i
		}
	}
	return -1
}

// sideIdx is the index of side's half in a split row.
func sideIdx(side model.DiffSide) int {
	if side == model.DiffSideLeft {
		return 0
	}
	return 1
}

// sideOf is the side of the half at index i of a split row (sideIdx's
// inverse).
func sideOf(i int) model.DiffSide {
	if i == 0 {
		return model.DiffSideLeft
	}
	return model.DiffSideRight
}

// pinnedBlank reports whether r is a split row with nothing on the side the
// open selection is pinned to.
func (dv *DiffView) pinnedBlank(r diffRow) bool {
	return dv.inVisual && r.isSplit() && r.halves[sideIdx(dv.visualSide)].line < 0
}

// activeIdx returns the index of the half of r the cursor is in: the
// preferred side's, or the other one when the preferred is blank. While a
// selection is open the side it started on is the preference.
func (dv *DiffView) activeIdx(r diffRow) int {
	if !r.isSplit() {
		return 0
	}
	pref := dv.cursorSide
	if dv.inVisual {
		pref = dv.visualSide
	}
	i := sideIdx(pref)
	if r.halves[i].line < 0 {
		return 1 - i
	}
	return i
}

// frameIdx is the half of r the cursor bars go around: while selecting, the
// pinned side even when it is blank on this row (the selection continues
// through such rows), else the active half.
func (dv *DiffView) frameIdx(r diffRow) int {
	if dv.inVisual && r.isSplit() {
		return sideIdx(dv.visualSide)
	}
	return dv.activeIdx(r)
}

// activeHalf is r's half at activeIdx, or a blank half for a row without any.
func (dv *DiffView) activeHalf(r diffRow) diffHalf {
	if len(r.halves) == 0 {
		return diffHalf{line: -1}
	}
	return r.halves[dv.activeIdx(r)]
}

// adoptSide makes the side of r's half showing line the preferred cursor
// side when that line is an Add or a Del. A Context line shows on both sides,
// so the user's own choice stays.
func (dv *DiffView) adoptSide(r diffRow, line int) {
	if r.kind != rowKindLine {
		return
	}
	if dv.inVisual {
		return // the selection locks the side
	}
	if i := r.halfOfLine(line); i >= 0 && r.halves[i].kind != diff.Context {
		dv.cursorSide, _ = diff.Anchor(diff.Line{Kind: r.halves[i].kind})
	}
}

func (r diffRow) selectable() bool {
	return r.kind == rowKindLine || r.kind == rowKindThreadHeader || r.kind == rowKindGap
}

// lineCount is how many physical screen lines r occupies.
func (r diffRow) lineCount() int {
	if r.kind == rowKindLine {
		n := 0
		for _, h := range r.halves {
			n = max(n, len(h.content))
		}
		return n
	}
	return len(r.lines)
}

// displayLines is how many screen lines r takes in a pane height lines tall:
// lineCount, except a line row never exceeds the pane (see drawHalf's
// ellipsis), since the view cannot scroll inside a row.
func (r diffRow) displayLines(height int) int {
	if r.kind == rowKindLine {
		return min(r.lineCount(), max(height, 1))
	}
	return r.lineCount()
}

// diffRowKey identifies a selectable row's logical identity, stable across
// a rebuild (unlike its index into rows, which shifts whenever folds or
// thread placement change row counts) — the same role widget.ListRow.ID
// plays for ListView.
type diffRowKey struct {
	isThread bool
	hunk     int
	line     int
	threadID string
	// isGap/gapStart identify a rowKindGap row by its new-side start line
	// (never by hunk index): expanding one gap can merge hunks and shift
	// every later hunk's index, while line numbers never change - see
	// diff.ExpandGap's own doc comment for the same rule. This also means a
	// gap row's key can never again match any row once it is expanded
	// (there is no longer a gap at that start line at all), so rebuild's
	// by-identity restore naturally falls through to its clamp(oldCursor)
	// fallback, landing on whatever now occupies the gap row's old
	// position - the first revealed line.
	isGap    bool
	gapStart int
}

// keyForRow is r's identity; a line row is identified by the half the cursor
// is in.
func (dv *DiffView) keyForRow(r diffRow) diffRowKey {
	switch r.kind {
	case rowKindThreadHeader:
		return diffRowKey{isThread: true, threadID: r.threadID}
	case rowKindGap:
		return diffRowKey{isGap: true, gapStart: r.gapStart}
	default:
		return diffRowKey{hunk: r.hunk, line: dv.activeHalf(r).line}
	}
}

// rowMatchesKey reports whether r is the row k identifies: for a line row,
// one with a half showing k's line, in whichever layout.
func rowMatchesKey(r diffRow, k diffRowKey) bool {
	switch r.kind {
	case rowKindThreadHeader:
		return k.isThread && k.threadID == r.threadID
	case rowKindGap:
		return k.isGap && k.gapStart == r.gapStart
	case rowKindLine:
		return !k.isThread && !k.isGap && r.hunk == k.hunk && r.halfOfLine(k.line) >= 0
	default:
		return false
	}
}

// DiffView is the Files tab's diff pane: a custom tview primitive (never a
// plain TextView, since it needs a logical, re-resolvable cursor, per-line
// backgrounds, and horizontal scrolling that a TextView cannot provide).
type DiffView struct {
	*tview.Box

	file   DiffFile
	folded map[string]bool
	// images supplies each review comment's ImageSource; see SetImageSource.
	images func(subjectID string) ImageSource

	rows       []diffRow
	selectable []int
	cursor     int
	offset     int
	lastWidth  int

	// curLineHunk/curLineLine track the most recent rowKindLine the cursor
	// actually rested on (updated by refreshLineTracking, called after every
	// cursor movement and rebuild). Selection uses this as visual mode's
	// "current" endpoint even while the cursor has since moved onto a
	// non-line row (a thread header, most commonly reached via
	// NextThread/PrevThread while a selection is open) — see Selection's
	// own doc comment. -1 means "no line seen yet" (an empty file).
	curLineHunk, curLineLine int

	inVisual                           bool
	visualAnchorHunk, visualAnchorLine int

	hOffset    int
	maxHOffset int

	// split is the requested layout (SetSplit); splitActive is the one the
	// rows were last built in, which falls back to unified on a pane too
	// narrow for two columns (see splitFits).
	split, splitActive bool
	// drawWidth is the inner width of the last Draw, for SplitShown.
	drawWidth int
	// gutterDigits is gutterDigitWidth of the file's hunks, set by SetFile.
	gutterDigits int
	// cursorSide is the preferred half of a split row (SetCursorSide);
	// visualSide is the side the open selection is pinned to.
	cursorSide, visualSide model.DiffSide
	// reanchor/reanchorLines ask the next Draw to restore the cursor row's
	// screen position after a layout switch changed the row count.
	reanchor      bool
	reanchorLines int

	// wrap soft-wraps long diff lines onto continuation display lines
	// instead of truncating them; see SetWrap.
	wrap bool

	// resetCursor tells the next rebuild to skip its usual by-identity
	// cursor restore (diffRowKey) entirely, set by SetFile when the file's
	// own path changes — see its doc comment.
	resetCursor bool

	// pendingCursorSet/Side/Line name a diff-side line the next (and, while
	// the file is still loading, every following) rebuild should move the
	// cursor to once it exists — see JumpToLine.
	pendingCursorSet  bool
	pendingCursorSide model.DiffSide
	pendingCursorLine int
	// pendingCursorAdopt makes the jump also set the cursor side from
	// pendingCursorSide (JumpToAnchor).
	pendingCursorAdopt bool

	// pendingThreadSet/ID/Edge are JumpToLine's own counterpart for a
	// thread-header target — see JumpToThread/JumpToThreadEdge. Only one of
	// a line target and a thread target is ever pending at a time: setting
	// either clears the other, since both compete for the same cursor.
	// pendingThreadEdge is 0 for a by-ID target (pendingThreadID), positive
	// for "first in display order", negative for "last".
	pendingThreadSet  bool
	pendingThreadID   string
	pendingThreadEdge int

	// wordRanges holds, per hunk, the changed-word byte ranges by line index
	// (diff.IntraLineRanges). They depend only on file.Hunks, so SetFile
	// computes them once instead of every rebuild (n/N, folds, jumps and
	// resizes all rebuild).
	wordRanges []map[int][][]int
	// wordHunks identifies the hunks wordRanges was computed for: the first
	// element's address and the length. A refresh passing the same slice
	// (highlight completion, thread or draft changes) reuses wordRanges;
	// diff.ExpandGap always builds new hunks.
	wordHunks    *diff.Hunk
	wordHunksLen int

	// searchRE is the currently active "/" search's compiled pattern (see
	// SetSearch/ClearSearch), or nil when no search is active. Applied at
	// rebuild time only (lineHalf, once per rebuild), not per Draw — the
	// same span-precomputation rule every other line decoration here
	// follows.
	searchRE *regexp.Regexp

	// searchCurrentSet/Path/Hunk/Line/Start/End name the one match (among
	// every one searchRE highlights) styled theme.SearchCurrent instead of
	// theme.SearchMatch — see SetSearchCurrent. Path guards against a stale
	// hunk/line/byte-offset triple left over from a different file falsely
	// matching an unrelated row after SetFile switches the displayed file:
	// hunk/line indices (and byte offsets within a line) are only
	// meaningful relative to the file they were computed against, and
	// SetFile does not clear this state on a file switch (nor need it,
	// given the path guard) so revisiting the file the current match is in
	// shows it again without another n/N.
	searchCurrentSet                     bool
	searchCurrentPath                    string
	searchCurrentHunk, searchCurrentLine int
	searchCurrentStart, searchCurrentEnd int
}

// SetSearch activates re: every line matching it is highlighted (see
// lineHalf) starting at the next rebuild. Any current-match marker from a
// previous search (SetSearchCurrent) is cleared, since it belongs to that
// search's own match list, not this one; callers that jump to a match right
// after (diffSearchStep, submitDiffSearch) call SetSearchCurrent again to
// re-mark it.
func (dv *DiffView) SetSearch(re *regexp.Regexp) {
	dv.searchRE = re
	dv.searchCurrentSet = false
	dv.lastWidth = -1
}

// ClearSearch deactivates the current search highlight (for example Esc on
// the diff outside visual mode), including the current-match marker. It
// does not touch any other search state (the caller's own match
// list/cursor, kept so a later n/N can reactivate the same search via
// SetSearch without recomputing it).
func (dv *DiffView) ClearSearch() {
	dv.searchRE = nil
	dv.searchCurrentSet = false
	dv.lastWidth = -1
}

// SetSearchCurrent marks the match at (path, hunk, line, start:end) — the
// same coordinates search.go's searchMatch carries — as the current one
// among every match searchRE highlights (see SetSearch): styled
// theme.SearchCurrent instead of theme.SearchMatch, starting at the next
// rebuild.
func (dv *DiffView) SetSearchCurrent(path string, hunk, line, start, end int) {
	dv.searchCurrentSet = true
	dv.searchCurrentPath = path
	dv.searchCurrentHunk = hunk
	dv.searchCurrentLine = line
	dv.searchCurrentStart = start
	dv.searchCurrentEnd = end
	dv.lastWidth = -1
}

// ClearSearchCurrent removes the current-match marker without touching the
// rest of the active search (SetSearch's own highlight, and searchRE
// itself, are left alone) — used when a caller's own notion of "current"
// goes stale (for example internal/ui's EventFilesChanged recompute, which
// resets its searchIdx to -1) without the whole search being cleared.
func (dv *DiffView) ClearSearchCurrent() {
	dv.searchCurrentSet = false
	dv.lastWidth = -1
}

// HasSearch reports whether a search highlight is currently active.
func (dv *DiffView) HasSearch() bool {
	return dv.searchRE != nil
}

// JumpToLine arranges for the cursor to move to the line anchored at
// (side, line) — the same anchor a diff.Anchor/diff.RangeAnchor call, or a
// review thread's own Side/Line, would report — as soon as the current
// file's hunks contain one. Unlike the identity-based cursor restore
// rebuild otherwise performs, the target here survives across rebuilds
// while DiffFile.Loading stays true (the file's data has not arrived yet:
// used by the "p" pending-list dialog to resolve a line/range draft's
// anchor once its file has loaded), and is dropped once loading finishes —
// found or not, so a target that never resolves (an outdated draft whose
// line no longer exists) does not keep retrying on every future,
// unrelated rebuild. The cursor side is only adopted for an Add/Del target
// (its kind says the side); see JumpToAnchor for a deliberate side.
func (dv *DiffView) JumpToLine(side model.DiffSide, line int) {
	dv.pendingCursorSet = true
	dv.pendingCursorSide = side
	dv.pendingCursorLine = line
	dv.pendingCursorAdopt = false
	dv.pendingThreadSet = false // only one pending target at a time
	dv.lastWidth = -1
}

// JumpToAnchor is JumpToLine for an anchor whose side is a deliberate choice
// (a saved draft's): when the line is a Context one, which shows on both
// sides of the split layout, the cursor also moves to side's column. JumpToLine
// leaves that choice alone, since a search match carries no side of its own.
func (dv *DiffView) JumpToAnchor(side model.DiffSide, line int) {
	dv.JumpToLine(side, line)
	dv.pendingCursorAdopt = true
}

// JumpToThread arranges for the cursor to land on the thread-header row for
// id, as soon as the current file's rendered rows contain one — the same
// survive-a-still-loading-rebuild, drop-once-it-is-not lifecycle JumpToLine
// uses (see its own doc comment). Used by the "t" review-threads dialog to
// resolve a chosen thread once its file has loaded.
func (dv *DiffView) JumpToThread(id string) {
	dv.pendingThreadSet = true
	dv.pendingThreadID = id
	dv.pendingThreadEdge = 0
	dv.pendingCursorSet = false // only one pending target at a time
	dv.lastWidth = -1
}

// JumpToThreadEdge arranges for the cursor to land on the first (dir > 0)
// or last (dir < 0) thread-header row in *display* order, once the current
// file's rendered rows contain any — the same lifecycle as JumpToThread.
// Display order matters here: bucketThreads/build render outdated threads
// first, then file-level threads, then inline threads in hunk order, so
// the caller cannot derive "first thread" from a raw
// model.ReviewThread.Line comparison without independently reimplementing
// that same skip-the-outdated-ones bucketing. App.stepThread uses this to
// land ]c/[c on the right end once it crosses into a neighbouring file.
func (dv *DiffView) JumpToThreadEdge(dir int) {
	dv.pendingThreadSet = true
	dv.pendingThreadID = ""
	if dir < 0 {
		dv.pendingThreadEdge = -1
	} else {
		dv.pendingThreadEdge = 1
	}
	dv.pendingCursorSet = false // only one pending target at a time
	dv.lastWidth = -1
}

// NewDiffView creates an empty DiffView.
func NewDiffView() *DiffView {
	return &DiffView{
		Box:          tview.NewBox(),
		folded:       map[string]bool{},
		lastWidth:    -1,
		curLineHunk:  -1,
		curLineLine:  -1,
		cursorSide:   model.DiffSideRight,
		gutterDigits: 1,
	}
}

// SetFile replaces the displayed file and forces the next Draw to rebuild
// its rows. Folded state is kept per thread ID across calls (see the
// folded field): a thread ID seen for the first time defaults to folded
// when already resolved, open otherwise; a thread ID already present in
// the map (from an earlier SetFile, possibly for the same file re-rendered
// after highlighting completed) keeps whatever the user last chose.
func (dv *DiffView) SetFile(f DiffFile) {
	pathChanged := f.Path != dv.file.Path
	if pathChanged {
		// A cursor/selection position is only ever meaningful relative to
		// the file it was set on: diffRowKey is positional (hunk/line
		// indices, or a thread ID), so restoring it by identity against a
		// *different* file could land on an arbitrary, unrelated row, and
		// a leftover Selection() would silently report the new file's own
		// lines as still selected. resetCursor tells rebuild to skip that
		// identity-based restore entirely for this rebuild, in addition to
		// resetting everything else here that rebuild does not own.
		dv.EndVisual()
		dv.cursor, dv.offset, dv.hOffset = 0, 0, 0
		dv.curLineHunk, dv.curLineLine = -1, -1
		dv.resetCursor = true
	}
	dv.file = f
	switch {
	case len(f.Hunks) > 0:
		dv.gutterDigits = gutterDigitWidth(f.Hunks)
	case pathChanged:
		dv.gutterDigits = 1
	}
	// else: a placeholder (Loading) for the same file keeps the width, like
	// wordRanges, so it cannot flip the drawn layout and end a selection.
	dv.updateWordRanges(f.Hunks)
	for _, t := range f.Threads {
		if _, ok := dv.folded[t.ID]; !ok {
			dv.folded[t.ID] = t.IsResolved
		}
	}
	dv.lastWidth = -1
}

// updateWordRanges recomputes wordRanges for a non-empty hunks unless it is
// the slice they were last computed for.
func (dv *DiffView) updateWordRanges(hunks []diff.Hunk) {
	if len(hunks) == 0 {
		// A loading or error placeholder renders no line rows, so the cached
		// ranges are never read meanwhile; keeping them lets the same hunks
		// returning afterwards reuse them.
		return
	}
	if &hunks[0] == dv.wordHunks && len(hunks) == dv.wordHunksLen {
		return
	}
	budget := wordBudget
	dv.wordRanges = make([]map[int][][]int, len(hunks))
	for i, h := range hunks {
		dv.wordRanges[i] = diff.IntraLineRanges(h.Lines, &budget)
	}
	dv.wordHunks, dv.wordHunksLen = &hunks[0], len(hunks)
}

// SetImageSource installs the factory that supplies, for the review comment
// with the given node ID, the ImageSource its body's images are drawn from
// (nil for no images: they stay text placeholders). It takes a factory
// rather than a source because the images belong to the comment, which only
// internal/ui and internal/store know how to address; DiffView imports
// neither. The next Draw rebuilds the rows.
func (dv *DiffView) SetImageSource(factory func(subjectID string) ImageSource) {
	dv.images = factory
	dv.lastWidth = -1
}

// imageSourceFor returns the ImageSource for the comment with the given
// ID, or nil when no factory is installed.
func (dv *DiffView) imageSourceFor(id string) ImageSource {
	if dv.images == nil {
		return nil
	}
	return dv.images(id)
}

// SetThreadFolded sets id's folded state directly (used by the Files tab
// after opening the thread comment URL, for example, though today only
// ToggleFoldAtCursor/FoldAll drive it).
func (dv *DiffView) SetThreadFolded(id string, folded bool) {
	dv.folded[id] = folded
	dv.lastWidth = -1
}

// FoldAll sets every known thread's folded state to folded (zM folds all,
// zR unfolds all).
func (dv *DiffView) FoldAll(folded bool) {
	for _, t := range dv.file.Threads {
		dv.folded[t.ID] = folded
	}
	dv.lastWidth = -1
}

// ToggleFoldAtCursor toggles the folded state of the thread under the
// cursor (za); a no-op when the cursor is not on a thread header row.
func (dv *DiffView) ToggleFoldAtCursor() {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindThreadHeader {
		return
	}
	dv.folded[row.threadID] = !dv.folded[row.threadID]
	dv.lastWidth = -1
}

// currentRow returns the row under the cursor, if any.
func (dv *DiffView) currentRow() (diffRow, bool) {
	if len(dv.selectable) == 0 {
		return diffRow{}, false
	}
	return dv.rows[dv.selectable[dv.cursor]], true
}

// setCursor moves the cursor to the i-th selectable row, clamping i, and
// refreshes curLineHunk/curLineLine.
func (dv *DiffView) setCursor(i int) {
	if len(dv.selectable) == 0 {
		return
	}
	dv.cursor = clamp(i, 0, len(dv.selectable)-1)
	dv.ensureVisible()
	dv.refreshLineTracking()
}

func (dv *DiffView) refreshLineTracking() {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindLine {
		return
	}
	if dv.pinnedBlank(row) {
		return // nothing on the selection's side to extend it to
	}
	if h := dv.activeHalf(row); h.line >= 0 {
		dv.curLineHunk, dv.curLineLine = row.hunk, h.line
	}
}

// MoveBy moves the cursor by n selectable rows (negative moves up). While a
// side-pinned selection is open, rows with nothing on the pinned side are not
// stops: the cursor goes on past them, or stays when none is left.
func (dv *DiffView) MoveBy(n int) {
	if !dv.inVisual || !dv.splitActive {
		dv.setCursor(dv.cursor + n)
		return
	}
	dir := 1
	if n < 0 {
		dir, n = -1, -n
	}
	pos := dv.cursor
	for ; n > 0; n-- {
		next, ok := dv.stopFrom(pos+dir, dir)
		if !ok {
			break
		}
		pos = next
	}
	dv.setCursor(pos)
}

// stopFrom returns the first selectable index from i (inclusive) going in dir
// that is a valid stop for the open selection.
func (dv *DiffView) stopFrom(i, dir int) (int, bool) {
	for ; i >= 0 && i < len(dv.selectable); i += dir {
		if !dv.pinnedBlank(dv.rows[dv.selectable[i]]) {
			return i, true
		}
	}
	return 0, false
}

// MoveTop moves the cursor to the first selectable row.
func (dv *DiffView) MoveTop() {
	if i, ok := dv.stopFrom(0, 1); ok {
		dv.setCursor(i)
	}
}

// MoveBottom moves the cursor to the last selectable row.
func (dv *DiffView) MoveBottom() {
	if i, ok := dv.stopFrom(len(dv.selectable)-1, -1); ok {
		dv.setCursor(i)
	}
}

// MoveHalfPage moves the cursor by however many selectable rows together
// span roughly half of the view's visible height in display lines,
// matching widget.ListView.MoveHalfPage's own semantics.
func (dv *DiffView) MoveHalfPage(dir int) {
	_, _, _, height := dv.GetInnerRect()
	dv.moveByDisplayLines(dir, height/2, height)
}

// MovePage moves the cursor by a full page instead of half; otherwise
// identical to MoveHalfPage.
func (dv *DiffView) MovePage(dir int) {
	_, _, _, height := dv.GetInnerRect()
	dv.moveByDisplayLines(dir, height, height)
}

// moveByDisplayLines is MoveHalfPage/MovePage's shared walk: it moves the
// cursor by however many selectable rows together span roughly target
// display lines in a pane height lines tall.
func (dv *DiffView) moveByDisplayLines(dir, target, height int) {
	if len(dv.selectable) == 0 {
		return
	}
	if target < 1 {
		target = 1
	}

	startRow := dv.selectable[dv.cursor]
	lines, steps := 0, 0
	for i := 1; lines < target; i++ {
		var idx int
		if dir >= 0 {
			idx = startRow + i
		} else {
			idx = startRow - i
		}
		if idx < 0 || idx >= len(dv.rows) {
			break
		}
		lines += dv.rows[idx].displayLines(height)
		// Only rows MoveBy would stop on count as steps: it skips the rest.
		if dv.rows[idx].selectable() && !dv.pinnedBlank(dv.rows[idx]) {
			steps++
		}
	}
	if steps == 0 {
		steps = 1
	}
	if dir < 0 {
		steps = -steps
	}
	dv.MoveBy(steps)
}

// NextThread moves the cursor to the next thread-header row after the
// current position, if any (]c). Returns true when the cursor moved; false
// (a no-op) at the last one, or when the file has none at all — the
// router's own dispatch falls forward into App.stepThread when false, to
// cross ]c into the next file that has a thread.
func (dv *DiffView) NextThread() bool {
	for i := dv.cursor + 1; i < len(dv.selectable); i++ {
		if dv.rows[dv.selectable[i]].kind == rowKindThreadHeader {
			dv.setCursor(i)
			return true
		}
	}
	return false
}

// PrevThread moves the cursor to the previous thread-header row before the
// current position, if any ([c). Returns true when the cursor moved; false
// at the first one, or when the file has none at all — see NextThread's
// own doc comment for how the router uses this.
func (dv *DiffView) PrevThread() bool {
	for i := dv.cursor - 1; i >= 0; i-- {
		if dv.rows[dv.selectable[i]].kind == rowKindThreadHeader {
			dv.setCursor(i)
			return true
		}
	}
	return false
}

// StartVisual begins a visual line selection anchored at the cursor's
// current line; a no-op when the cursor is not on a diff line (for example
// a thread header), per the "V on a thread row is a no-op" rule.
func (dv *DiffView) StartVisual() {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindLine {
		return
	}
	h := dv.activeHalf(row)
	dv.visualSide = model.DiffSideRight
	if row.isSplit() {
		dv.visualSide = sideOf(dv.activeIdx(row))
	}
	dv.inVisual = true
	dv.visualAnchorHunk, dv.visualAnchorLine = row.hunk, h.line
}

// EndVisual cancels an active visual selection (Esc).
func (dv *DiffView) EndVisual() { dv.inVisual = false }

// InVisual reports whether a visual selection is currently active.
func (dv *DiffView) InVisual() bool { return dv.inVisual }

// Selection returns the contiguous run of diff.Lines currently selected in
// visual mode, from the anchor to curLineHunk/curLineLine (the most recent
// line the cursor actually rested on — see those fields' doc comment for
// why this, rather than the literal current row, is used: it lets the
// selection keep behaving sensibly even if the cursor has since moved onto
// a non-line row). A selection that would cross into a different hunk than
// the anchor's is clamped to that hunk's own first or last line instead
// (GitHub range comments cannot span hunks). ok is false when no visual
// selection is active.
func (dv *DiffView) Selection() ([]diff.Line, bool) {
	hunk, lo, hi, ok := dv.selectionRange()
	if !ok {
		return nil, false
	}
	lines := dv.file.Hunks[hunk].Lines[lo : hi+1]
	if !dv.splitActive {
		return lines, true
	}
	// A split selection is pinned to one side: the other side's lines are
	// not part of it even though they sit between its ends.
	out := make([]diff.Line, 0, len(lines))
	for _, l := range lines {
		if diff.OnSide(l, dv.visualSide) {
			out = append(out, l)
		}
	}
	return out, true
}

// selectionRange computes the active visual selection's anchor hunk index
// and its clamped, inclusive [lo, hi] *line-index* range (into that hunk's
// own Lines slice) — the shared logic behind both Selection() (which slices
// Lines with it) and Draw's per-row highlighting (which compares a row's
// own index-based line field against it): both must derive the range the
// same way, since diff.Line.OldNo/NewNo are file line numbers, not Lines
// indices, and comparing a row index against a line number would silently
// misalign whenever OldStart != 1 or the range's first line has no old
// number (an Add line). ok is false when no visual selection is active.
func (dv *DiffView) selectionRange() (hunk, lo, hi int, ok bool) {
	if !dv.inVisual || dv.curLineHunk < 0 {
		return 0, 0, 0, false
	}
	anchorHunk, anchorLine := dv.visualAnchorHunk, dv.visualAnchorLine
	curHunk, curLine := dv.curLineHunk, dv.curLineLine

	if anchorHunk < 0 || anchorHunk >= len(dv.file.Hunks) {
		return 0, 0, 0, false
	}
	hunkLines := dv.file.Hunks[anchorHunk].Lines

	switch {
	case curHunk > anchorHunk:
		curLine = len(hunkLines) - 1
	case curHunk < anchorHunk:
		curLine = 0
	}

	lo, hi = anchorLine, curLine
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 || hi >= len(hunkLines) {
		return 0, 0, 0, false
	}
	return anchorHunk, lo, hi, true
}

// CursorLine returns the diff.Line under the cursor, if the cursor is
// currently on a line row.
func (dv *DiffView) CursorLine() (diff.Line, bool) {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindLine {
		return diff.Line{}, false
	}
	if row.hunk < 0 || row.hunk >= len(dv.file.Hunks) {
		return diff.Line{}, false
	}
	h := dv.file.Hunks[row.hunk]
	li := dv.activeHalf(row).line
	if li < 0 || li >= len(h.Lines) {
		return diff.Line{}, false
	}
	return h.Lines[li], true
}

// CursorThread returns the review thread under the cursor, if the cursor is
// currently on a thread header row.
func (dv *DiffView) CursorThread() (model.ReviewThread, bool) {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindThreadHeader {
		return model.ReviewThread{}, false
	}
	for _, t := range dv.file.Threads {
		if t.ID == row.threadID {
			return t, true
		}
	}
	return model.ReviewThread{}, false
}

// CursorGap returns the new-side start line of the collapsed region under
// the cursor, if the cursor is currently on a gap row (see
// diffBuilder.build/diff.ExpandGap).
func (dv *DiffView) CursorGap() (gapStart int, ok bool) {
	row, ok := dv.currentRow()
	if !ok || row.kind != rowKindGap {
		return 0, false
	}
	return row.gapStart, true
}

// SetWrap turns soft-wrapping of long diff lines on or off. While on, a line
// wider than its content width (the pane less its own gutter) is chopped
// into several display lines instead of truncated, and one taller than the
// pane is capped at the pane height with a trailing "…". Horizontal scrolling
// is suspended while on; turning wrap off restores the earlier position.
func (dv *DiffView) SetWrap(wrap bool) {
	dv.wrap = wrap
	dv.lastWidth = -1
}

// ToggleWrap flips SetWrap's state.
func (dv *DiffView) ToggleWrap() {
	dv.SetWrap(!dv.wrap)
}

// Wrap reports whether soft-wrapping is on.
func (dv *DiffView) Wrap() bool {
	return dv.wrap
}

// SetSplit selects the side-by-side (true) or the unified (false) layout. The
// split layout is only used while each half is wide enough (splitFits); Split
// still reports the requested value and SplitShown the layout drawn. When the
// drawn layout does change, the next rebuild ends visual mode (a selection is
// pinned to one side only in split) and keeps the cursor row at its screen
// position (see reanchor); when it does not, neither happens.
func (dv *DiffView) SetSplit(split bool) {
	if split == dv.split {
		return
	}
	dv.split = split
	dv.lastWidth = -1
}

// SplitShown reports whether the split layout is what is drawn: requested and
// wide enough for the last drawn width.
func (dv *DiffView) SplitShown() bool {
	return dv.split && splitFits(dv.drawWidth, dv.gutterDigits)
}

// CommentSide reports which side of the diff a comment on the cursor line (or
// the open selection) belongs to when that is the column the user is in, not
// the line's kind: explicit is true only while the split layout is shown (a
// context line shows on both sides, so its kind does not say which). The side
// is the selection's pinned one while selecting, else the cursor's half.
func (dv *DiffView) CommentSide() (side model.DiffSide, explicit bool) {
	if !dv.splitActive {
		return model.DiffSideRight, false
	}
	if dv.inVisual {
		return dv.visualSide, true
	}
	if row, ok := dv.currentRow(); ok && row.kind == rowKindLine {
		return sideOf(dv.activeIdx(row)), true
	}
	return dv.cursorSide, true
}

// ToggleSplit flips SetSplit's state.
func (dv *DiffView) ToggleSplit() { dv.SetSplit(!dv.split) }

// Split reports whether the split layout is requested.
func (dv *DiffView) Split() bool { return dv.split }

// SetCursorSide sets which half of a split row the cursor is in (the old side
// for left, the new for right); on a row whose half on that side is blank the
// other half is used. Ignored while a selection is open, which is pinned to
// the side it started on. In the unified layout it only records the
// preference.
func (dv *DiffView) SetCursorSide(side model.DiffSide) {
	if dv.inVisual {
		return
	}
	if dv.searchCurrentSet && side != dv.cursorSide {
		dv.lastWidth = -1 // the current match's column on a context row follows
	}
	dv.cursorSide = side
	dv.refreshLineTracking()
}

// reanchorOffset scrolls so the cursor row sits reanchorLines display lines
// below the top, as far as whole rows allow.
func (dv *DiffView) reanchorOffset(height int) {
	if len(dv.selectable) == 0 {
		return
	}
	off, lines := dv.selectable[dv.cursor], 0
	for off > 0 {
		n := dv.rows[off-1].displayLines(height)
		if lines+n > dv.reanchorLines {
			break
		}
		lines += n
		off--
	}
	dv.offset = off
}

// ScrollHorizontal adjusts the diff content's horizontal scroll position by
// delta cells (zh/zl), clamped to [0, the longest line's overflow past the
// last-drawn content width]. While wrapping scrolling is suspended: it
// changes nothing (the stored position is kept for when wrapping is turned
// off) and reports false.
func (dv *DiffView) ScrollHorizontal(delta int) bool {
	if dv.wrap {
		return false
	}
	dv.hOffset = clamp(dv.hOffset+delta, 0, dv.maxHOffset)
	return true
}

// HOffset returns the diff content's current horizontal scroll position, in
// cells (0 when not scrolled, and always 0 while wrapping, though the
// scrolled position is remembered and returns once wrapping is turned off).
func (dv *DiffView) HOffset() int {
	if dv.wrap {
		return 0
	}
	return dv.hOffset
}

// ensureVisible adjusts offset so the cursor's row is fully within the
// view's current height, matching widget.ListView.ensureVisible.
func (dv *DiffView) ensureVisible() {
	if len(dv.selectable) == 0 {
		dv.offset = 0
		return
	}
	cursorRow := dv.selectable[dv.cursor]
	if cursorRow < dv.offset {
		dv.offset = cursorRow
		return
	}

	_, _, _, height := dv.GetInnerRect()
	if height <= 0 {
		return
	}
	for dv.offset < cursorRow {
		lines := 0
		for i := dv.offset; i <= cursorRow; i++ {
			lines += dv.rows[i].displayLines(height)
		}
		if lines <= height {
			return
		}
		dv.offset++
	}
}

// Draw rebuilds rows (when the inner width has changed since the last
// rebuild — see rebuild) and renders the visible rows.
func (dv *DiffView) Draw(screen tcell.Screen) {
	dv.DrawForSubclass(screen, dv)
	fx, y, fullWidth, height := dv.GetInnerRect()
	x, width := frameInset(fx, fullWidth)
	dv.drawWidth = width
	if width != dv.lastWidth {
		dv.rebuild(width)
		dv.lastWidth = width
	}
	if dv.reanchor {
		dv.reanchor = false
		dv.reanchorOffset(height)
	}
	// Rows are clamped to the pane height at draw time, so a height change
	// needs no rebuild, only the cursor row kept in view.
	dv.ensureVisible()
	if width <= 0 || height <= 0 {
		return
	}

	cursorRowIdx := -1
	if len(dv.selectable) > 0 {
		cursorRowIdx = dv.selectable[dv.cursor]
	}
	selHunk, selLo, selHi, hasSel := dv.selectionRange()
	// half is h's index in row. A selection is one-sided in split, so only its
	// pinned column is tinted, a context row's other half included.
	selected := func(row diffRow, h diffHalf, half int) bool {
		if !hasSel || row.kind != rowKindLine || row.hunk != selHunk || h.line < selLo || h.line > selHi {
			return false
		}
		return !row.isSplit() || sideOf(half) == dv.visualSide
	}

	line := y
	for i := dv.offset; i < len(dv.rows) && line < y+height; i++ {
		row := dv.rows[i]
		isCursorRow := i == cursorRowIdx

		n := row.displayLines(height)
		if row.isSplit() {
			active := dv.frameIdx(row)
			sel := [2]bool{selected(row, row.halves[0], 0), selected(row, row.halves[1], 1)}
			for pl := 0; pl < n && line < y+height; pl++ {
				dv.drawSplitLine(screen, fx, line, fullWidth, n, row, pl, sel, isCursorRow, active)
				line++
			}
			continue
		}

		half := row.primary()
		bgStyle := theme.DiffLineStyle(half.kind, selected(row, half, 0))
		_, bg, _ := bgStyle.Decompose()
		for pl := 0; pl < n && line < y+height; pl++ {
			for cx := fx; cx < fx+fullWidth; cx++ {
				screen.SetContent(cx, line, ' ', nil, bgStyle)
			}
			if row.kind == rowKindLine {
				dv.drawHalf(screen, x, line, width, n, half, pl, bg)
			} else if pl < len(row.lines) {
				DrawSpans(screen, x, line, width, withBackground(row.lines[pl], bg))
			}
			if isCursorRow {
				drawCursorFrame(screen, fx, line, fullWidth, bg)
			}
			line++
		}
	}
}

// halfBackground is the background style of one half of a line row: a blank
// half takes the filler, any other the tint of its line kind.
func halfBackground(h diffHalf, selected bool) tcell.Style {
	if h.line < 0 {
		return theme.Base.Background(theme.DiffFillerBg)
	}
	return theme.DiffLineStyle(h.kind, selected)
}

// drawSplitLine draws physical line pl (of n) of a split row: the old half,
// the "│" separator, the new half, each half on its own background. The
// cursor bars go around the active half only: its outer reserved column and
// the separator column.
func (dv *DiffView) drawSplitLine(screen tcell.Screen, fx, y, fullWidth, n int, row diffRow, pl int, selected [2]bool, cursor bool, active int) {
	x, width := frameInset(fx, fullWidth)
	leftW, rightW := splitWidths(width)
	sepX := x + leftW

	var styles [2]tcell.Style
	for i, h := range row.halves {
		styles[i] = halfBackground(h, selected[i])
	}
	for cx := fx; cx < fx+fullWidth; cx++ {
		st := styles[1]
		if cx < sepX {
			st = styles[0]
		}
		screen.SetContent(cx, y, ' ', nil, st)
	}
	screen.SetContent(sepX, y, '│', nil, theme.Muted)

	_, leftBG, _ := styles[0].Decompose()
	_, rightBG, _ := styles[1].Decompose()
	dv.drawHalf(screen, x, y, leftW, n, row.halves[0], pl, leftBG)
	dv.drawHalf(screen, sepX+1, y, rightW, n, row.halves[1], pl, rightBG)

	if cursor {
		_, bg, _ := styles[active].Decompose()
		if active == 0 {
			drawCursorBars(screen, fx, sepX, y, bg)
		} else {
			drawCursorBars(screen, sepX, fx+fullWidth-1, y, bg)
		}
	}
}

// drawHalf draws physical line pl of one half (width cells wide at x) of a
// rowKindLine row shown on shown display lines: on the first line its gutter
// (never scrolled), then the half's content line pl with the
// horizontal-scroll offset applied, with a "‹" marker when scrolled.
// Continuation lines (pl > 0) leave the gutter blank on the row background,
// and a half with fewer lines than pl draws nothing. When the row is cut
// short (to the pane height, or at rebuild) its last drawn line ends with a
// muted "…".
func (dv *DiffView) drawHalf(screen tcell.Screen, x, y, width, shown int, half diffHalf, pl int, bg tcell.Color) {
	if pl >= len(half.content) {
		return
	}
	gutterSpans := withBackground(half.gutter, bg)
	gw := SpanWidth(half.gutter)
	if gw > width {
		gw = width
	}
	if pl == 0 {
		DrawSpans(screen, x, y, gw, gutterSpans)
	}
	if gw >= width {
		return
	}

	contentX := x + gw
	contentWidth := width - gw
	hOffset := dv.HOffset()
	if hOffset > 0 {
		markerStyle := theme.Muted.Background(bg)
		screen.SetContent(contentX, y, '‹', nil, markerStyle)
		contentX++
		contentWidth--
	}
	if contentWidth <= 0 {
		return
	}
	DrawSpansOffset(screen, contentX, y, contentWidth, hOffset, withBackgroundKeepingWords(half.content[pl], bg))
	if pl == shown-1 && (len(half.content) > shown || half.truncated) {
		drawCutEllipsis(screen, contentX+contentWidth-1, y, half.content[pl], contentWidth, bg)
	}
}

// drawCutEllipsis draws "…" in the content's last cell, x. The line starts at
// x-(width-1); if a double-width rune of it covers x, its left cell is
// blanked so no half rune is left beside the ellipsis. The "…" replaces a
// cell of the line, so when that cell is inside an emphasised word both the
// "…" and a blanked half keep the word's background instead of bg.
func drawCutEllipsis(screen tcell.Screen, x, y int, line []Span, width int, bg tcell.Color) {
	col := 0
	cellBG := bg
scan:
	for _, s := range line {
		for _, c := range spanCells(s) {
			if col+c.width > width-1 {
				// This cell covers the last column.
				if _, spanBG, _ := s.Style.Decompose(); theme.IsDiffWordBg(spanBG) {
					cellBG = spanBG
				}
				if c.width == 2 && col+1 == width-1 {
					screen.SetContent(x-1, y, ' ', nil, theme.Muted.Background(cellBG))
				}
				break scan
			}
			col += c.width
		}
	}
	screen.SetContent(x, y, ellipsisRune, nil, theme.Muted.Background(cellBG))
}

// rebuild recomputes every row of the current file at width, preserving the
// cursor by logical identity (see diffRowKey) the same way
// widget.ListView.SetRows preserves it by ListRow.ID.
func (dv *DiffView) rebuild(width int) {
	if width < 1 {
		width = 1
	}

	var oldKey diffRowKey
	hadSelection := len(dv.selectable) > 0 && !dv.resetCursor
	if hadSelection {
		oldKey = dv.keyForRow(dv.rows[dv.selectable[dv.cursor]])
	}
	oldCursor := dv.cursor
	dv.resetCursor = false

	digits := dv.gutterDigits
	split := dv.split && splitFits(width, digits)
	layoutChanged := split != dv.splitActive
	if layoutChanged {
		// A selection is only side-pinned in the split layout, so it ends with
		// the layout, whether by SetSplit or a resize across the fallback.
		dv.EndVisual()
	}
	dv.splitActive = split

	if layoutChanged && hadSelection {
		// The row count differs between layouts: keep the cursor row on its
		// screen line, however the layout came to change.
		_, _, _, height := dv.GetInnerRect()
		lines := 0
		for i := dv.offset; i < dv.selectable[oldCursor]; i++ {
			lines += dv.rows[i].displayLines(height)
		}
		dv.reanchor, dv.reanchorLines = true, lines
	}

	b := dv.buildRows(width, split, digits, dv.cursorColumn())
	// The gutter is the same width for every line of a file bar a wider draft
	// marker, so one sample line's gutter (blank draft column included)
	// stands in for all of them here.
	// In the split layout the narrower (left) half bounds the scroll.
	contentWidth := max(width-SpanWidth(gutterSpansFor(diff.Line{}, digits, "")), 1)
	if split {
		left, _ := splitWidths(width)
		contentWidth = max(left-splitGutterWidth(digits), 1)
	}
	dv.maxHOffset = maxHOffsetFor(b.longestContent, contentWidth)
	// While wrapping hOffset is neither applied (see HOffset) nor
	// clamped: maxHOffset follows the pane, and a resize while wrapped must
	// not lose the position that turning wrap off brings back.
	if !dv.wrap {
		dv.hOffset = clamp(dv.hOffset, 0, dv.maxHOffset)
	}

	dv.cursor = clamp(oldCursor, 0, len(dv.selectable)-1)
	if hadSelection {
		for i, idx := range dv.selectable {
			if rowMatchesKey(dv.rows[idx], oldKey) {
				dv.cursor = i
				if layoutChanged {
					dv.adoptSide(dv.rows[idx], oldKey.line)
				}
				break
			}
		}
	}
	dv.applyPendingCursor()
	dv.leavePinnedBlank()
	dv.refreshLineTracking()
	// The current match on a context row was styled in the column the cursor
	// had before it resolved (a jump or a restore can change it). Build once
	// more rather than restyle the spans: the rows are identical otherwise,
	// and this happens only when such a match exists and the column differs.
	if b.currentOnContext {
		if col := dv.cursorColumn(); col != b.currentSide {
			dv.buildRows(width, split, digits, col)
		}
	}
}

// buildRows rebuilds dv.rows and dv.selectable for the layout, returning the
// builder (for its longest content and current-match bookkeeping).
func (dv *DiffView) buildRows(width int, split bool, digits int, currentSide model.DiffSide) *diffBuilder {
	b := &diffBuilder{dv: dv, width: width, split: split, gutterWidth: digits, currentSide: currentSide}
	b.build()
	dv.rows = b.rows
	dv.selectable = dv.selectable[:0]
	for i, r := range dv.rows {
		if r.selectable() {
			dv.selectable = append(dv.selectable, i)
		}
	}
	return b
}

// cursorColumn is the column the cursor is drawn in: its row's frame half in
// the split layout, else the preferred side.
func (dv *DiffView) cursorColumn() model.DiffSide {
	if row, ok := dv.currentRow(); ok && row.isSplit() {
		return sideOf(dv.frameIdx(row))
	}
	return dv.cursorSide
}

// CursorRowHasLine reports whether the cursor's row shows line (an index into
// hunk's lines) in either of its halves, which CursorLine alone cannot say on
// a split row: it reports the one half the cursor is in.
func (dv *DiffView) CursorRowHasLine(hunk, line int) bool {
	row, ok := dv.currentRow()
	return ok && row.kind == rowKindLine && row.hunk == hunk && row.halfOfLine(line) >= 0
}

// leavePinnedBlank moves a cursor a jump left on a row with nothing on the
// selection's pinned side to the nearest row MoveBy could stop on (forward
// first), so the cursor, its frame and the selection's end agree.
func (dv *DiffView) leavePinnedBlank() {
	if len(dv.selectable) == 0 || !dv.pinnedBlank(dv.rows[dv.selectable[dv.cursor]]) {
		return
	}
	if i, ok := dv.stopFrom(dv.cursor+1, 1); ok {
		dv.cursor = i
	} else if i, ok := dv.stopFrom(dv.cursor-1, -1); ok {
		dv.cursor = i
	}
}

// applyPendingCursor resolves a JumpToLine or JumpToThread/JumpToThreadEdge
// target against the just-rebuilt rows, if one is pending, taking priority
// over rebuild's own by-identity restore above (a caller requesting a jump
// wants it honoured even when a selectable row of the same identity as the
// old cursor also still exists). See JumpToLine's own doc comment for why
// the target survives across rebuilds while the file is still loading, and
// is dropped once it is not, regardless of whether it was ever found — the
// same rule applies to a pending thread target below.
func (dv *DiffView) applyPendingCursor() {
	if dv.pendingCursorSet {
		dv.applyPendingLineCursor()
	}
	if dv.pendingThreadSet {
		dv.applyPendingThreadCursor()
	}
}

func (dv *DiffView) applyPendingLineCursor() {
	if hi, li, ok := diff.LocateLine(dv.file.Hunks, dv.pendingCursorSide, dv.pendingCursorLine); ok {
		for i, idx := range dv.selectable {
			r := dv.rows[idx]
			if r.kind == rowKindLine && r.hunk == hi && r.halfOfLine(li) >= 0 {
				dv.cursor = i
				switch {
				case dv.inVisual:
				case dv.pendingCursorAdopt:
					dv.cursorSide = dv.pendingCursorSide
				default:
					dv.adoptSide(r, li)
				}
				dv.pendingCursorSet = false
				break
			}
		}
	}
	if !dv.file.Loading {
		dv.pendingCursorSet = false
	}
}

// applyPendingThreadCursor resolves a JumpToThread (by ID) or
// JumpToThreadEdge (pendingThreadEdge != 0: first/last in display order,
// i.e. selectable order — see JumpToThreadEdge's own doc comment) target.
func (dv *DiffView) applyPendingThreadCursor() {
	if dv.pendingThreadEdge != 0 {
		found := -1
		for i, idx := range dv.selectable {
			if dv.rows[idx].kind != rowKindThreadHeader {
				continue
			}
			found = i
			if dv.pendingThreadEdge > 0 {
				break // first match, scanning forward
			}
			// dir < 0: keep overwriting so found ends on the last match.
		}
		if found >= 0 {
			dv.cursor = found
			dv.pendingThreadSet = false
		}
	} else {
		for i, idx := range dv.selectable {
			r := dv.rows[idx]
			if r.kind == rowKindThreadHeader && r.threadID == dv.pendingThreadID {
				dv.cursor = i
				dv.pendingThreadSet = false
				break
			}
		}
	}
	if !dv.file.Loading {
		dv.pendingThreadSet = false
	}
}

// maxHOffsetFor returns the furthest hOffset that still leaves at least one
// content column on screen: drawHalf only ever spends a cell on the "‹"
// marker once hOffset > 0, so the content actually visible while scrolled is
// contentWidth-1 cells wide, one narrower than the unscrolled (hOffset == 0)
// case contentWidth itself describes. Without
// the "+1" below, the longest line's very last column would never scroll
// into view — hOffset would already be clamped one cell short of it.
func maxHOffsetFor(longestContent, contentWidth int) int {
	overflow := longestContent - contentWidth
	if overflow <= 0 {
		return 0
	}
	return overflow + 1
}

// diffBuilder accumulates rows for one rebuild pass.
type diffBuilder struct {
	dv    *DiffView
	width int
	// split builds two-half line rows (see splitFits for when it may).
	split bool
	// currentSide is the column the search's current match is styled in on a
	// split context row (the cursor's, see cursorColumn); currentOnContext
	// records that the build had such a match, so rebuild knows whether a
	// column that turned out different needs another pass.
	currentSide      model.DiffSide
	currentOnContext bool

	rows           []diffRow
	gutterWidth    int
	longestContent int
}

// wordBudget caps the total work diff.IntraLineRanges may spend in one
// SetFile, in units of one per input byte tokenised plus one per DP cell, so
// a file with thousands of changed lines cannot stall the UI goroutine.
const wordBudget = 2_000_000

func (b *diffBuilder) build() {
	f := b.dv.file

	b.rows = append(b.rows, b.fileHeaderRow(f))

	outdated, fileThreads, inline := bucketThreads(f)

	if len(outdated) > 0 {
		b.rows = append(b.rows, diffRow{kind: rowKindOutdatedHeader, lines: [][]Span{{{Text: "Outdated threads", Style: theme.Muted}}}})
		for _, t := range outdated {
			b.appendThread(t)
		}
	}
	for _, t := range fileThreads {
		b.appendThread(t)
	}

	if !f.Loading && f.Err == nil && f.HasPatch {
		// A file whose Status is Added/Removed has its entire content in
		// the patch already (there is no "other side" to reveal), so gap
		// rows never apply to it, regardless of HeadKnown/HeadLineCount.
		gapsEligible := len(f.Hunks) > 0 &&
			f.Status != model.FileStatusAdded && f.Status != model.FileStatusRemoved

		if gapsEligible {
			if first, _ := diff.NewRange(f.Hunks[0]); first > 1 {
				b.appendGapRow(1, first-1)
			}
		}

		for hi, h := range f.Hunks {
			if gapsEligible && hi > 0 {
				_, lastPrev := diff.NewRange(f.Hunks[hi-1])
				first, _ := diff.NewRange(h)
				if first > lastPrev+1 {
					b.appendGapRow(lastPrev+1, first-lastPrev-1)
				}
			}
			b.rows = append(b.rows, b.hunkHeaderRow(h))
			if b.split {
				b.appendSplitLines(f, hi, h, inline)
				continue
			}
			for li, l := range h.Lines {
				side, _ := diff.Anchor(l)
				gutter := gutterSpansFor(l, b.gutterWidth, b.draftMark(f, hi, li, side))
				c := b.contentFor(f, hi, li, l)
				b.rows = append(b.rows, diffRow{kind: rowKindLine, hunk: hi, halves: []diffHalf{b.lineHalf(f, hi, li, l, c, true, gutter, b.width)}})
				for _, t := range inline[hunkLineKey{hi, li}] {
					b.appendThread(t)
				}
			}
		}

		if gapsEligible {
			_, last := diff.NewRange(f.Hunks[len(f.Hunks)-1])
			switch {
			case !f.HeadKnown:
				b.appendGapRow(last+1, 0) // count unknown until fetched
			case f.HeadLineCount > last:
				b.appendGapRow(last+1, f.HeadLineCount-last)
			}
		}
	}
}

// appendGapRow appends one collapsed-region row for the gap starting at
// new-side line gapStart, gapCount lines long. gapCount == 0 means "not yet
// known" (only ever the trailing gap, before the file's head content has
// been fetched - a real gap is never empty, see diffBuilder.build's own
// non-empty conditions), rendered as an "expand" prompt with no count.
func (b *diffBuilder) appendGapRow(gapStart, gapCount int) {
	text := "⋯ expand ⋯"
	if gapCount > 0 {
		text = fmt.Sprintf("⋯ %d lines hidden ⋯", gapCount)
	}
	b.rows = append(b.rows, diffRow{
		kind:     rowKindGap,
		gapStart: gapStart,
		gapCount: gapCount,
		lines:    [][]Span{{{Text: text, Style: theme.Muted}}},
	})
}

// appendSplitLines appends hunk hi's lines as side-by-side rows, each followed
// by the threads anchored to its old line then its new one (once, for a
// context line that faces itself).
func (b *diffBuilder) appendSplitLines(f DiffFile, hi int, h diff.Hunk, inline map[hunkLineKey][]model.ReviewThread) {
	leftW, rightW := splitWidths(b.width)
	for _, p := range diff.SideBySide(h.Lines) {
		var left, right diffHalf
		if p.Left == p.Right {
			// A context line: one content for both halves, the search's current
			// match styled in the cursor's column only.
			c := b.contentFor(f, hi, p.Left, h.Lines[p.Left])
			onLeft := b.currentSide == model.DiffSideLeft
			if b.dv.searchCurrentSet && b.dv.searchCurrentPath == f.Path &&
				b.dv.searchCurrentHunk == hi && b.dv.searchCurrentLine == p.Left {
				b.currentOnContext = true
			}
			left = b.splitHalf(f, hi, h, p.Left, c, onLeft, leftW, model.DiffSideLeft)
			right = b.splitHalf(f, hi, h, p.Right, c, !onLeft, rightW, model.DiffSideRight)
		} else {
			left = b.splitHalfOf(f, hi, h, p.Left, leftW, model.DiffSideLeft)
			right = b.splitHalfOf(f, hi, h, p.Right, rightW, model.DiffSideRight)
		}
		b.rows = append(b.rows, diffRow{kind: rowKindLine, hunk: hi, halves: []diffHalf{left, right}})
		anchors := []int{p.Left}
		if p.Right != p.Left {
			anchors = append(anchors, p.Right)
		}
		for _, li := range anchors {
			for _, t := range inline[hunkLineKey{hi, li}] {
				b.appendThread(t)
			}
		}
	}
}

// splitHalfOf is splitHalf building the content of line li itself, or a blank
// half for li == -1.
func (b *diffBuilder) splitHalfOf(f DiffFile, hi int, h diff.Hunk, li, width int, side model.DiffSide) diffHalf {
	if li < 0 {
		return diffHalf{line: -1}
	}
	return b.splitHalf(f, hi, h, li, b.contentFor(f, hi, li, h.Lines[li]), true, width, side)
}

// splitHalf builds line li of h (li >= 0) as the side half, width cells wide,
// from its prebuilt content c.
func (b *diffBuilder) splitHalf(f DiffFile, hi int, h diff.Hunk, li int, c lineContent, current bool, width int, side model.DiffSide) diffHalf {
	l := h.Lines[li]
	gutter := splitGutterSpans(l, diff.NumberOn(l, side), b.gutterWidth, b.draftMark(f, hi, li, side))
	return b.lineHalf(f, hi, li, l, c, current, gutter, width)
}

type hunkLineKey struct{ hunk, line int }

// bucketThreads splits f.Threads into: outdated/unlocatable (rendered in
// the "Outdated threads" block), file-level (rendered right after the file
// header), and inline (keyed by the hunk/line they anchor to).
func bucketThreads(f DiffFile) (outdated, fileThreads []model.ReviewThread, inline map[hunkLineKey][]model.ReviewThread) {
	inline = map[hunkLineKey][]model.ReviewThread{}
	for _, t := range f.Threads {
		switch t.SubjectType {
		case model.ThreadSubjectFile:
			fileThreads = append(fileThreads, t)
		default:
			hi, li, ok := diff.LocateThread(f.Hunks, t)
			if !ok {
				outdated = append(outdated, t)
				continue
			}
			key := hunkLineKey{hi, li}
			inline[key] = append(inline[key], t)
		}
	}
	return outdated, fileThreads, inline
}

// fileHeaderRow renders the status glyph, path (with a rename note), diff
// stats, and — when applicable — a loading/error/no-patch line.
func (b *diffBuilder) fileHeaderRow(f DiffFile) diffRow {
	if f.Path == "" {
		// Nothing selected at all (no file open, possibly no pull request
		// either): the usual glyph/stats header and its !HasPatch fallback
		// ("Patch not available") would both misrepresent a non-existent
		// file as a real, patchless one.
		msg := "No file selected"
		switch {
		case f.Err != nil:
			msg = f.Err.Error()
		case f.Loading:
			msg = "Loading…"
		}
		return diffRow{kind: rowKindFileHeader, lines: [][]Span{{{Text: msg, Style: theme.Muted}}}}
	}

	title := fmt.Sprintf("%s %s", FileStatusGlyph(f.Status), f.Path)
	if f.Status == model.FileStatusRenamed && f.PreviousPath != "" && f.PreviousPath != f.Path {
		title += fmt.Sprintf(" (renamed from %s)", f.PreviousPath)
	}
	title += fmt.Sprintf("  +%d −%d", f.Additions, f.Deletions)

	lines := [][]Span{{{Text: title, Style: theme.Base.Bold(true)}}}
	switch {
	case f.Err != nil:
		lines = append(lines, []Span{{Text: "Error: " + f.Err.Error(), Style: theme.Error}})
	case f.Loading:
		lines = append(lines, []Span{{Text: "Loading…", Style: theme.Muted}})
	case !f.HasPatch:
		lines = append(lines, []Span{{Text: "Patch not available (binary or too large)", Style: theme.Muted}})
	}
	return diffRow{kind: rowKindFileHeader, lines: lines}
}

func (b *diffBuilder) hunkHeaderRow(h diff.Hunk) diffRow {
	text := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		text += " " + h.Section
	}
	return diffRow{kind: rowKindHunkHeader, lines: [][]Span{{{Text: text, Style: theme.Muted}}}}
}

// draftMark is the draft-gutter glyph for line lineIdx of hunk hunkIdx, or "".
// side is the side the draft must be on: the column in the split layout, in
// the unified one the side "c" would anchor the line to (diff.Anchor), so a
// draft that "c" could not reopen there shows no marker.
func (b *diffBuilder) draftMark(f DiffFile, hunkIdx, lineIdx int, side model.DiffSide) string {
	if f.DraftLines[DraftAnchor{Hunk: hunkIdx, Line: lineIdx, Side: side}] {
		return f.DraftMarker
	}
	return ""
}

// lineContent is a line's content spans (syntax and word emphasis) before the
// search highlight, with the search matches in its text. A context line shows
// on both sides of the split layout, so both halves share one.
type lineContent struct {
	spans   []Span
	matches [][]int
}

// contentFor builds line lineIdx of hunk hunkIdx's lineContent.
func (b *diffBuilder) contentFor(f DiffFile, hunkIdx, lineIdx int, l diff.Line) lineContent {
	content := contentSpansFor(f, hunkIdx, lineIdx, l)
	if ranges := b.dv.wordRanges[hunkIdx][lineIdx]; len(ranges) > 0 {
		// Before the search highlight, which then replaces a matched piece's
		// whole style and so leaves search looking the same with or without
		// emphasis.
		wordBG, _ := theme.DiffWordBg(l.Kind)
		content = restyleRanges(content, l.Text, ranges, func(s tcell.Style) tcell.Style { return s.Background(wordBG) })
	}
	var matches [][]int
	if b.dv.searchRE != nil {
		matches = b.dv.searchRE.FindAllStringIndex(l.Text, -1)
	}
	return lineContent{spans: content, matches: matches}
}

// lineHalf builds the half of line lineIdx of hunk hunkIdx that is width
// cells wide and starts with gutter, folding in the search highlight of c
// (the current match is styled only when current), and tracks the longest
// content seen for maxHOffsetFor.
func (b *diffBuilder) lineHalf(f DiffFile, hunkIdx, lineIdx int, l diff.Line, c lineContent, current bool, gutter []Span, width int) diffHalf {
	content := c.spans
	switch {
	case len(c.matches) == 0:
	case current:
		content = b.dv.highlightSearchRanges(content, f.Path, hunkIdx, lineIdx, l.Text, c.matches)
	default:
		content = highlightRanges(content, l.Text, c.matches, theme.SearchMatch)
	}
	w := SpanWidth(content)
	if w > b.longestContent {
		b.longestContent = w
	}
	lines := [][]Span{content}
	truncated := false
	if b.dv.wrap {
		// A drafted line's marker can make its gutter wider than the others',
		// so each line wraps at its own width.
		// Below 2 cells a wide rune would not fit its line at all.
		if cw := width - SpanWidth(gutter); cw >= 2 && w > cw {
			lines, truncated = chopSpansLimit(content, cw, maxWrapLines)
		}
	}
	return diffHalf{
		line:      lineIdx,
		kind:      l.Kind,
		gutter:    gutter,
		content:   lines,
		truncated: truncated,
	}
}

// maxWrapLines bounds how many display lines a wrapped line is chopped into
// at rebuild, so a huge minified line costs only cell allocation and chunking
// for that many lines (SpanWidth and the search regexp still scan all of it).
// What keeps a row reachable is the pane height clamp in displayLines, not
// this.
const maxWrapLines = 200

// highlightSearchRanges applies theme.SearchMatch to every range in ranges
// (as returned by dv.searchRE.FindAllStringIndex against text, the line's
// own diff.Line.Text), except the one matching dv.searchCurrent*'s own
// (path, hunk, line, start, end) coordinates, if any — that one gets
// theme.SearchCurrent instead. Splitting the ranges and calling
// highlightRanges twice is safe: each call only ever splits spans further
// without changing their concatenated text, so the second call's spans
// still reconstruct text exactly, same as the first's.
func (dv *DiffView) highlightSearchRanges(spans []Span, path string, hunkIdx, lineIdx int, text string, ranges [][]int) []Span {
	current := -1
	if dv.searchCurrentSet && dv.searchCurrentPath == path &&
		dv.searchCurrentHunk == hunkIdx && dv.searchCurrentLine == lineIdx {
		for i, r := range ranges {
			if r[0] == dv.searchCurrentStart && r[1] == dv.searchCurrentEnd {
				current = i
				break
			}
		}
	}
	if current < 0 {
		return highlightRanges(spans, text, ranges, theme.SearchMatch)
	}

	others := make([][]int, 0, len(ranges)-1)
	others = append(others, ranges[:current]...)
	others = append(others, ranges[current+1:]...)
	spans = highlightRanges(spans, text, others, theme.SearchMatch)
	return highlightRanges(spans, text, ranges[current:current+1], theme.SearchCurrent)
}

// gutterSpansFor renders one line's "old-no new-no marker draft-mark "
// gutter, each number right-aligned to width digits and blank on the side
// the line has no number for (an Add line has no old number; a Del line has
// no new one). draftMark is the draft-gutter glyph for this line ("" for
// none — see DiffFile.DraftLines/DraftMarker); the column itself is always
// reserved, blank otherwise, so every line's gutter stays the same width.
func gutterSpansFor(l diff.Line, width int, draftMark string) []Span {
	oldStr := blankOrNumber(l.Kind != diff.Add, l.OldNo, width)
	newStr := blankOrNumber(l.Kind != diff.Del, l.NewNo, width)

	marker, markerStyle := markerFor(l.Kind)

	return []Span{
		{Text: oldStr, Style: theme.Muted},
		{Text: " ", Style: theme.Muted},
		{Text: newStr, Style: theme.Muted},
		{Text: " ", Style: theme.Muted},
		{Text: marker, Style: markerStyle},
		{Text: draftColumn(draftMark), Style: theme.Accent},
		{Text: " ", Style: theme.Base},
	}
}

// splitGutterSpans renders a split half's "number marker draft-mark " gutter:
// no, the one line number the half shows (old on the left, new on the
// right), right-aligned to width digits.
func splitGutterSpans(l diff.Line, no, width int, draftMark string) []Span {
	marker, markerStyle := markerFor(l.Kind)
	return []Span{
		{Text: blankOrNumber(true, no, width), Style: theme.Muted},
		{Text: " ", Style: theme.Muted},
		{Text: marker, Style: markerStyle},
		{Text: draftColumn(draftMark), Style: theme.Accent},
		{Text: " ", Style: theme.Base},
	}
}

func markerFor(k diff.LineKind) (string, tcell.Style) {
	switch k {
	case diff.Add:
		return "+", theme.Success
	case diff.Del:
		return "-", theme.Error
	case diff.Context:
	}
	return " ", theme.Base
}

func draftColumn(mark string) string {
	if mark == "" {
		return " "
	}
	return mark
}

// minSplitContent is the least content width of a split half. Below it the
// layout falls back to unified: lines would be cut to a few characters, and
// the file tree already takes 30 columns of a typical terminal.
const minSplitContent = 12

// splitWidths divides an inner width into the old and new halves around the
// one-column separator; the new half gets the odd column.
func splitWidths(width int) (left, right int) {
	left = (width - 1) / 2
	return left, width - 1 - left
}

// splitGutterWidth is a split half's gutter width for digits-wide numbers.
func splitGutterWidth(digits int) int {
	return SpanWidth(splitGutterSpans(diff.Line{}, 0, digits, ""))
}

// splitFits reports whether both halves of an inner width keep minSplitContent
// columns for content.
func splitFits(width, digits int) bool {
	left, _ := splitWidths(width)
	return left-splitGutterWidth(digits) >= minSplitContent
}

func blankOrNumber(present bool, n, width int) string {
	if !present {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, n)
}

// contentSpansFor renders one line's content: syntax-highlighted spans from
// DiffFile.Tokens when present for (hunkIdx, lineIdx), otherwise the raw
// line text in the plain base style.
func contentSpansFor(f DiffFile, hunkIdx, lineIdx int, l diff.Line) []Span {
	if hunkIdx < len(f.Tokens) && lineIdx < len(f.Tokens[hunkIdx]) {
		tokens := f.Tokens[hunkIdx][lineIdx]
		if tokens != nil {
			spans := make([]Span, len(tokens))
			for i, t := range tokens {
				spans[i] = Span{Text: t.Text, Style: theme.TokenStyle(t.Type)}
			}
			return spans
		}
	}
	return []Span{{Text: l.Text, Style: theme.Base}}
}

// gutterDigitWidth returns the number of digits needed to right-align the
// largest old/new line number across every hunk, at least 1.
func gutterDigitWidth(hunks []diff.Hunk) int {
	max := 0
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.OldNo > max {
				max = l.OldNo
			}
			if l.NewNo > max {
				max = l.NewNo
			}
		}
	}
	if max == 0 {
		return 1
	}
	return len(strconv.Itoa(max))
}

// appendThread appends t's header row (selectable) and, when unfolded, one
// row per comment.
func (b *diffBuilder) appendThread(t model.ReviewThread) {
	b.rows = append(b.rows, diffRow{
		kind:     rowKindThreadHeader,
		threadID: t.ID,
		lines:    [][]Span{threadHeaderSpans(t, b.dv.folded[t.ID])},
	})
	if b.dv.folded[t.ID] {
		return
	}
	for _, c := range t.Comments {
		b.rows = append(b.rows, diffRow{kind: rowKindThreadComment, lines: commentLines(c, b.width, b.dv.imageSourceFor(c.ID))})
	}
}

// threadHeaderSpans renders "▸/▾ @author · <relative> · status · N replies".
func threadHeaderSpans(t model.ReviewThread, folded bool) []Span {
	glyph := "▾"
	if folded {
		glyph = "▸"
	}
	author := "unknown"
	var created time.Time
	if len(t.Comments) > 0 {
		if t.Comments[0].Author.Login != "" {
			author = t.Comments[0].Author.Login
		}
		created = t.Comments[0].CreatedAt
	}

	var status []string
	if t.IsResolved {
		status = append(status, "resolved")
	}
	if t.IsOutdated {
		status = append(status, "outdated")
	}
	if len(status) == 0 {
		status = append(status, "open")
	}

	replies := len(t.Comments) - 1
	if replies < 0 {
		replies = 0
	}

	text := fmt.Sprintf("%s @%s · %s · %s · %d replies", glyph, author, relativeTimeShort(created), strings.Join(status, ", "), replies)
	return []Span{{Text: text, Style: theme.Base}}
}

// commentBorder is the left-border glyph indenting every line of a rendered
// review comment, matching diffview.nvim-style inline thread blocks.
const commentBorder = "│ "

// commentLines renders one review comment: an "@login · <relative>" header,
// its body rendered as Markdown to width (minus the border's own width), and
// a reactions summary line when it has any — all indented with
// commentBorder.
func commentLines(c model.ReviewComment, width int, images ImageSource) [][]Span {
	borderWidth := SpanWidth([]Span{{Text: commentBorder}})
	bodyWidth := width - borderWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}

	login := c.Author.Login
	if login == "" {
		login = "unknown"
	}
	header := fmt.Sprintf("@%s · %s", login, relativeTimeShort(c.CreatedAt))
	headerLine := []Span{{Text: commentBorder, Style: theme.Muted}, {Text: header, Style: theme.Base}}
	if c.State == model.ReviewCommentStatePending {
		// Reuses theme.Pending, the style every other "not final yet"
		// marker in gprt already uses (ReviewerStyle/RollupStyle's own
		// pending states), rather than introducing a separate badge style.
		headerLine = append(headerLine, Span{Text: " PENDING", Style: theme.Pending})
	}
	lines := [][]Span{headerLine}

	for _, l := range RenderMarkdown(c.Body, bodyWidth, images) {
		lines = append(lines, append([]Span{{Text: commentBorder, Style: theme.Muted}}, l...))
	}

	if r := reactionSummary(c.ReactionGroups); len(r) > 0 {
		lines = append(lines, append([]Span{{Text: commentBorder, Style: theme.Muted}}, r...))
	}
	return lines
}

// reactionSummary renders groups as a line of Spans (for example "👍 2  🎉
// 1"), bolding any group the viewer has themselves reacted with
// (ViewerHasReacted) so it is distinguishable from one they have not, or
// nil when there is nothing to show. Kept local to this package (rather
// than shared with internal/ui's near-identical reactionLine) since widget
// must not depend on internal/ui, and the two renderers are small enough
// that the duplication is cheaper than introducing a shared package for it.
func reactionSummary(groups []model.ReactionGroup) []Span {
	var spans []Span
	for _, g := range groups {
		if g.Count <= 0 {
			continue
		}
		if len(spans) > 0 {
			spans = append(spans, Span{Text: "  ", Style: theme.Muted})
		}
		style := theme.Muted
		if g.ViewerHasReacted {
			style = style.Bold(true)
		}
		spans = append(spans, Span{Text: fmt.Sprintf("%s %d", g.Content.Emoji(), g.Count), Style: style})
	}
	return spans
}

// relativeTimeShort renders t as a short "Ns"/"Nm"/"Nh"/"Nd" duration
// relative to time.Now(), or "" for a zero t. Kept local to this package
// for the same reason as reactionSummary — see its doc comment.
func relativeTimeShort(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	const day = 24 * time.Hour
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d/day))
	}
}
