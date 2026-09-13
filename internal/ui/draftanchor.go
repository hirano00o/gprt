// draftanchor.go formats and parses the drafts.Key.Anchor form used for a
// line/range review-comment composer target: "path:SIDE:line" for a single
// line, "path:SIDE:start-line" for a range. The Files tab's draft gutter
// marker (see files.go's draftLinesForFile) parses these back into a
// location it can render against the diff.
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hirano00o/gprt/internal/model"
)

// formatLineAnchor renders path/side/startLine/line as a drafts.Key.Anchor
// for a line or range review comment: "path:SIDE:line" when startLine is 0
// or equal to line (a single-line comment, matching
// draftThreadFromRange/threadInputFromRange's own single-line convention in
// internal/store/review.go), otherwise "path:SIDE:start-line".
func formatLineAnchor(path string, side model.DiffSide, startLine, line int) string {
	if startLine != 0 && startLine != line {
		return fmt.Sprintf("%s:%s:%d-%d", path, side, startLine, line)
	}
	return fmt.Sprintf("%s:%s:%d", path, side, line)
}

// parseLineAnchor parses formatLineAnchor's output back into its parts. It
// splits from the right (path may itself contain ":"), so only the last two
// ":"-separated segments are interpreted as side and line/range; ok is false
// for anything else, including a side that is not LEFT/RIGHT or a
// non-numeric line/range.
func parseLineAnchor(anchor string) (path string, side model.DiffSide, startLine, line int, ok bool) {
	lastColon := strings.LastIndex(anchor, ":")
	if lastColon < 0 {
		return "", "", 0, 0, false
	}
	lineOrRange := anchor[lastColon+1:]
	rest := anchor[:lastColon]

	prevColon := strings.LastIndex(rest, ":")
	if prevColon < 0 {
		return "", "", 0, 0, false
	}
	sideStr := rest[prevColon+1:]
	path = rest[:prevColon]

	side = model.DiffSide(sideStr)
	if side != model.DiffSideLeft && side != model.DiffSideRight {
		return "", "", 0, 0, false
	}

	if s, l, ok := strings.Cut(lineOrRange, "-"); ok {
		start, err1 := strconv.Atoi(s)
		end, err2 := strconv.Atoi(l)
		if err1 != nil || err2 != nil {
			return "", "", 0, 0, false
		}
		return path, side, start, end, true
	}

	n, err := strconv.Atoi(lineOrRange)
	if err != nil {
		return "", "", 0, 0, false
	}
	return path, side, 0, n, true
}
