// Package kitty speaks the part of the kitty graphics protocol gprt needs to
// show images in a terminal: transmitting an image once, then drawing it with
// Unicode placeholder cells, which are ordinary text cells and therefore
// scroll, wrap, and get redrawn by the UI (and by tmux) like any other text.
// It has no UI dependency: callers turn the returned strings into styled
// cells.
//
// See https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders.
package kitty

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// MaxCells is how many rows or columns a placement can span: the
	// protocol numbers them with the diacritics table, which has this many
	// entries.
	MaxCells = len(diacritics)

	// placeholderRune starts every placeholder cell.
	placeholderRune = '\U0010EEEE'

	// chunkSize is the largest base64 payload per escape sequence the
	// protocol allows.
	chunkSize = 4096

	esc = "\x1b"
	st  = esc + `\`
)

// Cell returns the text of the placeholder cell at (row, col) of a
// placement: the placeholder rune followed by the diacritics for the row and
// the column. Numbers outside the table are clamped to its ends.
func Cell(row, col int) string {
	return string(placeholderRune) + string(diacritic(row)) + string(diacritic(col))
}

func diacritic(n int) rune {
	return diacritics[min(max(n, 0), MaxCells-1)]
}

// Rows returns the placeholder text of a cols x rows placement, one string
// per row. Both dimensions are capped at MaxCells.
func Rows(cols, rows int) []string {
	cols, rows = min(cols, MaxCells), min(rows, MaxCells)
	out := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		for c := range cols {
			b.WriteString(Cell(r, c))
		}
		out[r] = b.String()
	}
	return out
}

// Detect reports whether the environment (getenv is os.Getenv) indicates a
// terminal that implements the kitty graphics protocol: kitty or Ghostty.
// The variables it reads survive inside tmux, unlike the terminal's own
// identification query, which tmux would swallow.
func Detect(getenv func(string) string) bool {
	if getenv("KITTY_WINDOW_ID") != "" || getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	if getenv("TERM_PROGRAM") == "ghostty" {
		return true
	}
	term := getenv("TERM")
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty")
}

// InTmux reports whether the process runs inside tmux, which swallows
// graphics escapes unless they are wrapped as a passthrough (see wrapTmux).
func InTmux(getenv func(string) string) bool {
	return getenv("TMUX") != ""
}

// transmitSequence returns the escape sequences that transmit png as image
// id and create a virtual placement of cols x rows cells for Unicode
// placeholders (U=1). q=2 suppresses the terminal's replies, which would
// otherwise arrive on stdin and be parsed as key presses.
func transmitSequence(id, cols, rows int, png []byte, tmux bool) string {
	payload := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	for first := true; first || payload != ""; first = false {
		chunk := payload[:min(len(payload), chunkSize)]
		payload = payload[len(chunk):]
		more := 0
		if payload != "" {
			more = 1
		}
		var seq string
		if first {
			seq = fmt.Sprintf("%s_Ga=T,U=1,f=100,t=d,i=%d,c=%d,r=%d,q=2,m=%d;%s%s", esc, id, cols, rows, more, chunk, st)
		} else {
			seq = fmt.Sprintf("%s_Gm=%d;%s%s", esc, more, chunk, st)
		}
		b.WriteString(passthrough(seq, tmux))
	}
	return b.String()
}

// deleteSequence returns the escape sequence deleting image id and freeing
// its data.
func deleteSequence(id int, tmux bool) string {
	return passthrough(fmt.Sprintf("%s_Ga=d,d=I,i=%d,q=2%s", esc, id, st), tmux)
}

func passthrough(seq string, tmux bool) string {
	if tmux {
		return wrapTmux(seq)
	}
	return seq
}

// wrapTmux wraps seq as a tmux DCS passthrough, which tmux forwards to the
// outer terminal verbatim (it requires `set -g allow-passthrough on`). Every
// ESC inside must be doubled, or tmux would end the passthrough at the first.
func wrapTmux(seq string) string {
	return esc + "Ptmux;" + strings.ReplaceAll(seq, esc, esc+esc) + st
}
