// Package diff parses GitHub's per-file, header-less unified diff patches
// (as returned by the REST "list pull request files" endpoint) into
// structured hunks and lines, and locates review threads and comment
// anchors within them. It is pure: no network, no UI, no dependency on
// store or ui.
package diff

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/hirano00o/gprt/internal/model"
)

// LineKind identifies which side(s) of a diff a Line belongs to.
type LineKind int

// Known line kinds.
const (
	// Context is an unchanged line present on both sides of the diff.
	Context LineKind = iota
	// Add is a line only present on the new (head) side.
	Add
	// Del is a line only present on the old (base) side.
	Del
)

// Line is one line of a hunk's body. OldNo/NewNo are 0 when the line has no
// number on that side (an Add line has no OldNo; a Del line has no NewNo).
type Line struct {
	Kind      LineKind
	OldNo     int
	NewNo     int
	Text      string
	NoNewline bool
}

// Hunk is one "@@ ... @@" section of a patch.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Section  string
	Lines    []Line
}

// noNewlineMarker is the literal line git/GitHub emits immediately after a
// line that has no trailing newline in the file it belongs to. It carries no
// line-number information of its own: it only sets NoNewline on the Line
// that precedes it.
const noNewlineMarker = `\ No newline at end of file`

// hunkHeaderRE matches a hunk header: "@@ -a[,b] +c[,d] @@[ section]". The
// count after a comma is optional (GitHub omits it, defaulting to 1, when a
// side has exactly one line - see hunkCount).
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

// Parse parses a GitHub-style patch into its hunks. An empty patch (no
// changes reported, for example a pure rename) returns (nil, nil). Any line
// that is neither a recognised hunk header, a body line prefixed with " "/
// "+"/"-", nor the no-newline marker is a parse error naming its 1-based
// line number within patch.
func Parse(patch string) ([]Hunk, error) {
	if patch == "" {
		return nil, nil
	}

	lines := strings.Split(patch, "\n")
	var hunks []Hunk
	var cur *Hunk
	var oldNo, newNo int

	for i, raw := range lines {
		lineNo := i + 1

		// strings.Split leaves one trailing empty element when patch ends
		// in "\n" (git/GitHub patches normally do not, but tolerate it
		// rather than misreport it as a malformed line): it carries no
		// content of its own.
		if raw == "" && i == len(lines)-1 {
			continue
		}

		if strings.HasPrefix(raw, "@@ ") || raw == "@@" {
			h, err := parseHunkHeader(raw, lineNo)
			if err != nil {
				return nil, err
			}
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			cur = &h
			oldNo, newNo = h.OldStart, h.NewStart
			continue
		}

		if cur == nil {
			return nil, fmt.Errorf("diff: line %d: content before any hunk header: %q", lineNo, raw)
		}

		if raw == noNewlineMarker {
			if len(cur.Lines) == 0 {
				return nil, fmt.Errorf("diff: line %d: %q has no preceding line", lineNo, noNewlineMarker)
			}
			cur.Lines[len(cur.Lines)-1].NoNewline = true
			continue
		}

		line, err := parseBodyLine(raw, lineNo, &oldNo, &newNo)
		if err != nil {
			return nil, err
		}
		cur.Lines = append(cur.Lines, line)
	}

	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks, nil
}

// parseHunkHeader parses a single "@@ ... @@" header line.
func parseHunkHeader(raw string, lineNo int) (Hunk, error) {
	m := hunkHeaderRE.FindStringSubmatch(raw)
	if m == nil {
		return Hunk{}, fmt.Errorf("diff: line %d: invalid hunk header: %q", lineNo, raw)
	}

	oldStart, err := strconv.Atoi(m[1])
	if err != nil {
		return Hunk{}, fmt.Errorf("diff: line %d: invalid old start: %w", lineNo, err)
	}
	newStart, err := strconv.Atoi(m[3])
	if err != nil {
		return Hunk{}, fmt.Errorf("diff: line %d: invalid new start: %w", lineNo, err)
	}
	oldLines, err := hunkCount(m[2], lineNo)
	if err != nil {
		return Hunk{}, err
	}
	newLines, err := hunkCount(m[4], lineNo)
	if err != nil {
		return Hunk{}, err
	}

	return Hunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Section:  strings.TrimPrefix(m[5], " "),
	}, nil
}

// hunkCount parses a hunk header's optional ",<count>" group, defaulting to
// 1 when it was omitted (GitHub's patches omit it for a one-line side, for
// example "@@ -1 +1 @@").
func hunkCount(raw string, lineNo int) (int, error) {
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("diff: line %d: invalid hunk count: %w", lineNo, err)
	}
	return n, nil
}

// parseBodyLine parses one hunk body line (prefixed " ", "+", or "-"),
// advancing *oldNo/*newNo as appropriate for its kind.
func parseBodyLine(raw string, lineNo int, oldNo, newNo *int) (Line, error) {
	if raw == "" {
		return Line{}, fmt.Errorf("diff: line %d: empty line inside a hunk", lineNo)
	}

	var kind LineKind
	switch raw[0] {
	case ' ':
		kind = Context
	case '+':
		kind = Add
	case '-':
		kind = Del
	default:
		return Line{}, fmt.Errorf("diff: line %d: unrecognised line prefix %q", lineNo, raw[:1])
	}

	line := Line{Kind: kind, Text: raw[1:]}
	switch kind {
	case Context:
		line.OldNo, line.NewNo = *oldNo, *newNo
		*oldNo++
		*newNo++
	case Add:
		line.NewNo = *newNo
		*newNo++
	case Del:
		line.OldNo = *oldNo
		*oldNo++
	}
	return line, nil
}

// ExpandTabs replaces every tab in s with spaces up to the next multiple of
// width columns, tracking display column width (not byte or rune count) so
// wide runes (for example CJK characters) advance the column correctly. A
// non-positive width is treated as 1 (each tab becomes a single space up to
// the next column, which is a no-op tab stop but never divides by zero or
// produces a negative repeat count).
func ExpandTabs(s string, width int) string {
	if width <= 0 {
		width = 1
	}

	var b strings.Builder
	col := 0
	it := graphemes.FromString(s)
	for it.Next() {
		cluster := it.Value()
		if cluster == "\t" {
			spaces := width - (col % width)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
			continue
		}
		b.WriteString(cluster)
		col += displaywidth.String(cluster)
	}
	return b.String()
}

// Anchor returns the diff side and line number a single Line comment
// attaches to: a deletion anchors to the old (LEFT) side; every other kind
// (addition or context) anchors to the new (RIGHT) side, matching GitHub's
// own review-comment placement rule.
func Anchor(l Line) (model.DiffSide, int) {
	if l.Kind == Del {
		return model.DiffSideLeft, l.OldNo
	}
	return model.DiffSideRight, l.NewNo
}

// ErrMixedSides is returned by RangeAnchor when lines contains both
// deletion and non-deletion lines: GitHub's review API accepts only a
// single-sided range comment.
var ErrMixedSides = errors.New("diff: range comment cannot mix left and right sides")

// Range is the anchor for a multi-line ("V" visual range) comment.
type Range struct {
	StartSide model.DiffSide
	Side      model.DiffSide
	StartLine int
	Line      int
}

// RangeAnchor computes the anchor for a contiguous range of lines within a
// single hunk (the caller guarantees contiguity). It requires at least two
// lines and a single side: a range made up entirely of deletion lines
// anchors to LEFT using old line numbers; a range with no deletion lines
// anchors to RIGHT using new line numbers; a range mixing the two returns
// ErrMixedSides.
func RangeAnchor(lines []Line) (Range, error) {
	if len(lines) < 2 {
		return Range{}, fmt.Errorf("diff: RangeAnchor requires at least 2 lines, got %d", len(lines))
	}

	allDel, anyDel := true, false
	for _, l := range lines {
		if l.Kind == Del {
			anyDel = true
		} else {
			allDel = false
		}
	}

	first, last := lines[0], lines[len(lines)-1]
	switch {
	case allDel:
		return Range{
			StartSide: model.DiffSideLeft, Side: model.DiffSideLeft,
			StartLine: first.OldNo, Line: last.OldNo,
		}, nil
	case !anyDel:
		return Range{
			StartSide: model.DiffSideRight, Side: model.DiffSideRight,
			StartLine: first.NewNo, Line: last.NewNo,
		}, nil
	default:
		return Range{}, ErrMixedSides
	}
}

// LocateThread finds the (hunk index, line index) of the diff line a review
// thread is anchored to, so the UI can render it inline. It returns
// ok == false for a file-level thread (SubjectType FILE, rendered at the
// file header instead), an outdated thread (IsOutdated, whose anchor no
// longer corresponds to a line in the current diff), or a thread whose line
// cannot be found in hunks at all.
func LocateThread(hunks []Hunk, t model.ReviewThread) (hunk, line int, ok bool) {
	if t.IsOutdated || t.SubjectType == model.ThreadSubjectFile {
		return 0, 0, false
	}
	return LocateLine(hunks, t.Side, t.Line)
}

// NewRange returns the inclusive [first, last] range of new-side (head-file)
// line numbers h's lines occupy. A side with NewLines == 0 (h adds no
// lines, for example a pure deletion) has no lines of its own; by
// unified-diff convention it is then positioned immediately after
// NewStart, so its (empty) range is (NewStart+1, NewStart) - first > last
// signals emptiness to callers doing gap-boundary arithmetic (ExpandGap,
// and widget.diffBuilder's gap-row placement, which reuses this exact
// convention rather than duplicating it).
func NewRange(h Hunk) (first, last int) {
	if h.NewLines == 0 {
		return h.NewStart + 1, h.NewStart
	}
	return h.NewStart, h.NewStart + h.NewLines - 1
}

// oldRange is NewRange's old-side counterpart. It is unexported: only
// ExpandGap's delta arithmetic needs it here - callers outside this package
// identify a gap by its new-side start line only (see ExpandGap's doc
// comment for why).
func oldRange(h Hunk) (first, last int) {
	if h.OldLines == 0 {
		return h.OldStart + 1, h.OldStart
	}
	return h.OldStart, h.OldStart + h.OldLines - 1
}

// ExpandGap returns hunks with the collapsed region ("gap") starting at
// new-side line gapStart filled in with Context lines drawn from headLines
// (the full head-side file content; headLines[i] is new-side line i+1).
//
// A gap is identified by gapStart, the new-side line number of its first
// hidden line, rather than by which hunks border it: expanding one gap can
// merge two hunks into one, shifting the index of every later hunk, while
// line numbers never change. The three possible gaps are: before the first
// hunk ([1, NewRange(hunks[0]).first-1]), between hunks i and i+1
// ([NewRange(hunks[i]).last+1, NewRange(hunks[i+1]).first-1]), and after the
// last hunk ([NewRange(hunks[last]).last+1, len(headLines)]).
//
// The trailing gap may legitimately be empty (the diff already reaches the
// end of the file): ExpandGap then returns hunks unchanged (a copy) rather
// than an error, since the caller (store.Store) cannot distinguish "empty"
// from "unknown" without first knowing len(headLines), which it only learns
// from this call. Any other gapStart that matches none of hunks' gaps is an
// error.
//
// hunks and its Lines slices are never mutated: the result is built from
// copies, so a caller that also holds a reference to the original hunks
// keeps seeing the pre-expansion diff.
func ExpandGap(hunks []Hunk, gapStart int, headLines []string) ([]Hunk, error) {
	if len(hunks) == 0 {
		return nil, fmt.Errorf("diff: ExpandGap: no hunks to expand")
	}

	if first0, _ := NewRange(hunks[0]); first0 > 1 && gapStart == 1 {
		if err := checkHeadLinesReach(first0-1, headLines); err != nil {
			return nil, err
		}
		return expandBeforeFirst(hunks, headLines), nil
	}

	for i := 0; i < len(hunks)-1; i++ {
		_, lastI := NewRange(hunks[i])
		firstNext, _ := NewRange(hunks[i+1])
		if firstNext > lastI+1 && gapStart == lastI+1 {
			if err := checkHeadLinesReach(firstNext-1, headLines); err != nil {
				return nil, err
			}
			return expandBetween(hunks, i, headLines), nil
		}
	}

	if _, lastLast := NewRange(hunks[len(hunks)-1]); gapStart == lastLast+1 {
		if lastLast >= len(headLines) {
			return copyHunks(hunks), nil
		}
		return expandTrailing(hunks, headLines), nil
	}

	return nil, fmt.Errorf("diff: ExpandGap: no gap starts at new-side line %d", gapStart)
}

// checkHeadLinesReach reports an error, rather than letting contextLines
// panic on an out-of-range index later, when headLines is too short to
// cover a gap that needs new-side line need (the before-first and between
// paths only - the trailing path's own end is always len(headLines) by
// construction, never past it).
func checkHeadLinesReach(need int, headLines []string) error {
	if need > len(headLines) {
		return fmt.Errorf("diff: ExpandGap: head content has %d lines, gap needs line %d", len(headLines), need)
	}
	return nil
}

// copyHunks returns a shallow copy of hunks: a new backing slice, holding
// the same (unmutated) Hunk values.
func copyHunks(hunks []Hunk) []Hunk {
	out := make([]Hunk, len(hunks))
	copy(out, hunks)
	return out
}

// contextLines builds the Context lines that fill a gap spanning new-side
// lines [start, end] (inclusive; end < start yields none), using headLines
// for text and delta (NewNo - OldNo, constant across a single gap) to
// derive each line's old-side number.
func contextLines(start, end, delta int, headLines []string) []Line {
	if end < start {
		return nil
	}
	lines := make([]Line, 0, end-start+1)
	for newNo := start; newNo <= end; newNo++ {
		lines = append(lines, Line{
			Kind:  Context,
			OldNo: newNo - delta,
			NewNo: newNo,
			Text:  headLines[newNo-1],
		})
	}
	return lines
}

// recount derives OldLines/NewLines from lines' actual side membership, so
// a hunk's header stays consistent after Context lines are inserted.
func recount(lines []Line) (oldLines, newLines int) {
	for _, l := range lines {
		if l.Kind != Add {
			oldLines++
		}
		if l.Kind != Del {
			newLines++
		}
	}
	return oldLines, newLines
}

// expandBeforeFirst fills the gap before hunks[0] by prepending Context
// lines to it. The merged hunk starts at line 1 on both sides (delta is 0
// throughout the preamble: nothing has changed yet).
func expandBeforeFirst(hunks []Hunk, headLines []string) []Hunk {
	out := copyHunks(hunks)
	h0 := out[0]
	first0, _ := NewRange(h0)

	lines := make([]Line, 0, first0-1+len(h0.Lines))
	lines = append(lines, contextLines(1, first0-1, 0, headLines)...)
	lines = append(lines, h0.Lines...)
	oldLines, newLines := recount(lines)

	out[0] = Hunk{OldStart: 1, OldLines: oldLines, NewStart: 1, NewLines: newLines, Section: h0.Section, Lines: lines}
	return out
}

// expandBetween fills the gap between hunks[i] and hunks[i+1], merging them
// into a single hunk in the returned slice.
func expandBetween(hunks []Hunk, i int, headLines []string) []Hunk {
	hi, hNext := hunks[i], hunks[i+1]
	_, lastNewI := NewRange(hi)
	_, lastOldI := oldRange(hi)
	delta := lastNewI - lastOldI
	firstNext, _ := NewRange(hNext)

	gap := contextLines(lastNewI+1, firstNext-1, delta, headLines)
	lines := make([]Line, 0, len(hi.Lines)+len(gap)+len(hNext.Lines))
	lines = append(lines, hi.Lines...)
	lines = append(lines, gap...)
	lines = append(lines, hNext.Lines...)
	oldLines, newLines := recount(lines)

	firstOld, _ := oldRange(hi)
	firstNew, _ := NewRange(hi)
	merged := Hunk{
		OldStart: firstOld, OldLines: oldLines,
		NewStart: firstNew, NewLines: newLines,
		Section: hi.Section, Lines: lines,
	}

	out := make([]Hunk, 0, len(hunks)-1)
	out = append(out, hunks[:i]...)
	out = append(out, merged)
	out = append(out, hunks[i+2:]...)
	return out
}

// expandTrailing fills the gap after the last hunk by appending Context
// lines to it. OldStart/NewStart are taken from oldRange/NewRange's own
// first, not last.OldStart/NewStart directly, for the same reason
// expandBetween does: when the last hunk's old or new side is itself empty
// (a pure addition or deletion, NewLines/OldLines == 0), its own
// OldStart/NewStart is only the position the empty side sits *after* (see
// NewRange's doc comment) - the header's Start must be the first line
// actually present in Lines, which the appended gap's Context lines become
// once that side had none of its own.
func expandTrailing(hunks []Hunk, headLines []string) []Hunk {
	out := copyHunks(hunks)
	last := out[len(out)-1]
	_, lastNew := NewRange(last)
	_, lastOld := oldRange(last)
	delta := lastNew - lastOld

	gap := contextLines(lastNew+1, len(headLines), delta, headLines)
	lines := make([]Line, 0, len(last.Lines)+len(gap))
	lines = append(lines, last.Lines...)
	lines = append(lines, gap...)
	oldLines, newLines := recount(lines)

	firstOld, _ := oldRange(last)
	firstNew, _ := NewRange(last)
	out[len(out)-1] = Hunk{
		OldStart: firstOld, OldLines: oldLines,
		NewStart: firstNew, NewLines: newLines,
		Section: last.Section, Lines: lines,
	}
	return out
}

// LocateLine finds the (hunk index, line index) of the diff line anchored at
// side/lineNo — the same location model.ReviewThread's own Side/Line fields
// name (LocateThread delegates here), and what a saved line/range-comment
// draft's anchor (see internal/ui's draft anchor helpers) parses back into.
// LEFT matches a Del/Context line by OldNo; RIGHT matches an Add/Context
// line by NewNo. ok is false when no line matches (for example an outdated
// anchor whose line no longer exists in the current diff).
func LocateLine(hunks []Hunk, side model.DiffSide, lineNo int) (hunk, line int, ok bool) {
	for hi, h := range hunks {
		for li, l := range h.Lines {
			switch side {
			case model.DiffSideLeft:
				if l.OldNo == lineNo && (l.Kind == Del || l.Kind == Context) {
					return hi, li, true
				}
			case model.DiffSideRight:
				if l.NewNo == lineNo && (l.Kind == Add || l.Kind == Context) {
					return hi, li, true
				}
			}
		}
	}
	return 0, 0, false
}
