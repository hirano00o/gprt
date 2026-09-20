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

	"github.com/rivo/uniseg"

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
	state := -1
	rest := s
	for len(rest) > 0 {
		var cluster string
		var w int
		cluster, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if cluster == "\t" {
			spaces := width - (col % width)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
			continue
		}
		b.WriteString(cluster)
		col += w
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
