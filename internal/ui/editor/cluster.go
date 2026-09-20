package editor

import (
	"unicode"

	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/gdamore/tcell/v2"
)

// modCtrl is tcell.ModCtrl, aliased locally so files in this package do not
// each need their own tcell import purely for modifier comparisons.
const modCtrl = tcell.ModCtrl

// cluster is one grapheme cluster's byte range within a Text's buffer,
// plus the class of its first rune (used by word motions) and its display
// width (used to tell a wide, CJK-style cluster apart from an ordinary
// one — see wordClassAt).
type cluster struct {
	start, end int
	first      rune
	width      int
}

// clusters segments s into its grapheme clusters. Every byte offset Vim
// ever passes to Text.SetCursor/Replace/Select is either 0, len(s), or one
// of a returned cluster's start/end — all grapheme-cluster boundaries.
func clusters(s string) []cluster {
	var out []cluster
	it := graphemes.FromString(s)
	for it.Next() {
		value := it.Value()
		var first rune
		for _, r := range value {
			first = r
			break
		}
		out = append(out, cluster{
			start: it.Start(),
			end:   it.End(),
			first: first,
			width: displaywidth.String(value),
		})
	}
	return out
}

// indexAtOrAfter returns the index into cs of the cluster starting at or
// immediately after the byte offset pos (pos is assumed to already be a
// cluster boundary), or len(cs) if pos is at or past the end of the text.
func indexAtOrAfter(cs []cluster, pos int) int {
	for i, c := range cs {
		if c.start >= pos {
			return i
		}
	}
	return len(cs)
}

// wordClass classifies a cluster for word-motion purposes (w/b/e):
// wordClassSpace for whitespace, wordClassWide for a double-width
// (typically CJK) cluster — each such cluster is its own word, matching
// vim's own multibyte handling, where consecutive CJK characters do not
// merge into a single "word" the way consecutive ASCII letters do —
// wordClassWord for a letter/digit/underscore, and wordClassOther for
// everything else (punctuation).
type wordClass int

const (
	wordClassSpace wordClass = iota
	wordClassWide
	wordClassWord
	wordClassOther
)

func wordClassOf(c cluster) wordClass {
	switch {
	case unicode.IsSpace(c.first):
		return wordClassSpace
	case c.width >= 2:
		return wordClassWide
	case c.first == '_' || unicode.IsLetter(c.first) || unicode.IsDigit(c.first):
		return wordClassWord
	default:
		return wordClassOther
	}
}

// snapBoundary returns the byte offset in s of the cluster boundary at or
// immediately before pos, so a caller building an undo entry from a raw
// byte-length diff never hands Text an offset that lands inside a
// multi-byte grapheme cluster.
func snapBoundary(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}
	best := 0
	for _, c := range clusters(s) {
		if c.start > pos {
			break
		}
		best = c.start
	}
	return best
}
