package diff

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

// Pair is one row of a side-by-side layout: Left and Right are indices into
// the hunk's lines, or -1 when that side is empty on this row. Exported for
// the side-by-side diff layout in internal/ui/widget.
type Pair struct{ Left, Right int }

// SideBySide lays lines out as side-by-side rows. A context line faces
// itself. Within a run of Del lines immediately followed by a run of Add
// lines, the i-th Del shares a row with the i-th Add and the longer side's
// extras face -1; Adds with no preceding Dels are right-only rows and Dels
// not followed by Adds are left-only. NoNewline is a flag on a line, not a
// line of its own, so it does not affect pairing.
func SideBySide(lines []Line) []Pair {
	var out []Pair
	for i := 0; i < len(lines); {
		switch lines[i].Kind {
		case Context:
			out = append(out, Pair{i, i})
			i++
		case Add:
			out = append(out, Pair{-1, i})
			i++
		case Del:
			delEnd := i
			for delEnd < len(lines) && lines[delEnd].Kind == Del {
				delEnd++
			}
			addEnd := delEnd
			for addEnd < len(lines) && lines[addEnd].Kind == Add {
				addEnd++
			}
			for k := 0; k < max(delEnd-i, addEnd-delEnd); k++ {
				p := Pair{-1, -1}
				if i+k < delEnd {
					p.Left = i + k
				}
				if delEnd+k < addEnd {
					p.Right = delEnd + k
				}
				out = append(out, p)
			}
			i = addEnd
		}
	}
	return out
}

const (
	// maxDPCells bounds the LCS table of one inlineChanges call, so a pair
	// of very long lines costs a bounded amount of work.
	maxDPCells = 10_000
	// maxPairBytes bounds the combined length of a pair before it is even
	// tokenised: tokenising and comparing a minified line would otherwise
	// cost far more than the emphasis is worth.
	maxPairBytes = 4096
	// minSimilarity is the least share of matching word bytes for
	// a pair to be emphasised: lines sharing little would just be painted
	// whole, which tells the reader nothing the line tint does not.
	minSimilarity = 0.4
)

// tokenKind classifies a token: word and space tokens are runs that absorb
// following clusters of the same kind; anything else is a token of its own,
// either a letter of an unspaced script (kindUnspaced) or kindOther (an
// operator, a bracket, a quote, ...).
type tokenKind int

const (
	kindOther tokenKind = iota
	kindWord
	kindSpace
	kindUnspaced
)

// token is one comparison unit of a line: a run of letters/digits/underscore,
// a run of spaces/tabs, a multi-character operator, or a single grapheme
// cluster.
type token struct {
	start, end int
	kind       tokenKind
}

func (t token) space() bool { return t.kind == kindSpace }

// operators are the multi-character operators matched longest-first, so that
// "!=" -> "==" marks the whole operator. Every other punctuation character is
// a token of its own: merging arbitrary adjacent punctuation would turn "()"
// or "=-" into one token and mark more than what changed.
var operators = map[string]bool{
	"...": true, "===": true, "!==": true, "<<=": true, ">>=": true, "**=": true,
	"&&=": true, "||=": true, "??=": true, "&^=": true,
	"==": true, "!=": true, "<=": true, ">=": true, "&&": true, "||": true,
	"->": true, "=>": true, ":=": true, "++": true, "--": true, "+=": true,
	"-=": true, "*=": true, "/=": true, "%=": true, "&=": true, "|=": true,
	"^=": true, "<<": true, ">>": true, "::": true, "..": true, "**": true,
	"??": true, "&^": true,
}

// maxOperatorLen is the length of the longest entry of operators.
const maxOperatorLen = 3

// isUnspacedScript reports whether r is a letter of a script whose words are
// not separated by spaces (Han, Hiragana, Katakana, Hangul, Thai, Lao, Khmer,
// Myanmar). Such letters compare one grapheme at a time, so changing one
// character of a Japanese sentence marks only that character instead of the
// whole unbroken run. The katakana prolonged sound marks (U+30FC and its
// halfwidth form U+FF70) belong to no script but follow katakana.
func isUnspacedScript(r rune) bool {
	return r == '\u30fc' || r == '\uff70' ||
		unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul,
			unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

// clusterKind classifies one grapheme cluster by its first rune, so a
// combining mark stays part of its base letter's word.
func clusterKind(c string) tokenKind {
	if c == " " || c == "\t" {
		return kindSpace
	}
	r, _ := utf8.DecodeRuneInString(c)
	switch {
	case !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_':
		return kindOther
	case isUnspacedScript(r):
		return kindUnspaced
	}
	return kindWord
}

// tokenize splits s into tokens, walking grapheme clusters so that a
// combining mark is never separated from its base character.
func tokenize(s string) []token {
	var clusters []token // one per grapheme cluster; kind is clusterKind
	pos := 0
	for it := graphemes.FromString(s); it.Next(); {
		c := it.Value()
		clusters = append(clusters, token{pos, pos + len(c), clusterKind(c)})
		pos += len(c)
	}

	var toks []token
	for i := 0; i < len(clusters); {
		c := clusters[i]
		if n := operatorLen(s, clusters[i:]); n > 0 {
			toks = append(toks, token{c.start, clusters[i+n-1].end, kindOther})
			i += n
			continue
		}
		if last := len(toks) - 1; (c.kind == kindWord || c.kind == kindSpace) && last >= 0 && toks[last].kind == c.kind {
			toks[last].end = c.end
		} else {
			toks = append(toks, c)
		}
		i++
	}
	return toks
}

// operatorLen returns how many leading one-byte clusters of clusters spell
// the longest known operator, or 0.
func operatorLen(s string, clusters []token) int {
	for n := min(maxOperatorLen, len(clusters)); n >= 2; n-- {
		if clusters[n-1].end-clusters[0].start != n {
			continue // a cluster longer than one byte: not plain ASCII operator text
		}
		if operators[s[clusters[0].start:clusters[n-1].end]] {
			return n
		}
	}
	return 0
}

// inlineChanges returns the byte ranges ([start, end), sorted,
// non-overlapping, on grapheme boundaries) of old and new that differ.
// Both sides are tokenised, the common token prefix and suffix are
// trimmed and the middle is aligned by LCS; unmatched tokens become ranges,
// and two ranges separated only by whitespace merge. It returns nil, nil
// when the texts are identical, when the middle exceeds maxDPCells, or when
// the lines are too dissimilar (see minSimilarity) for word emphasis to
// carry information.
//
// budget, if non-nil, is the work left for a whole rebuild, in units of one
// input byte tokenised or one DP cell: the pair spends its byte length and
// then its table size, and when one does not fit the budget is set to -1 and
// the pair is skipped. A nil budget is unlimited.
func inlineChanges(old, new string, budget *int) (oldRanges, newRanges [][]int) {
	if old == new || len(old)+len(new) > maxPairBytes {
		return nil, nil
	}
	if !spend(budget, len(old)+len(new)) {
		return nil, nil
	}
	ot, nt := tokenize(old), tokenize(new)
	same := func(i, j int) bool { return old[ot[i].start:ot[i].end] == new[nt[j].start:nt[j].end] }

	prefix := 0
	for prefix < len(ot) && prefix < len(nt) && same(prefix, prefix) {
		prefix++
	}
	suffix := 0
	for suffix < len(ot)-prefix && suffix < len(nt)-prefix && same(len(ot)-1-suffix, len(nt)-1-suffix) {
		suffix++
	}
	om, nm := len(ot)-prefix-suffix, len(nt)-prefix-suffix

	oldMatched := make([]bool, len(ot))
	newMatched := make([]bool, len(nt))
	for i := range prefix {
		oldMatched[i], newMatched[i] = true, true
	}
	for i := range suffix {
		oldMatched[len(ot)-1-i], newMatched[len(nt)-1-i] = true, true
	}

	cells := (om + 1) * (nm + 1)
	if om > 0 && nm > 0 {
		if cells > maxDPCells {
			return nil, nil
		}
		if !spend(budget, cells) {
			return nil, nil
		}
		lcsMark(om, nm, func(i, j int) bool { return same(prefix+i, prefix+j) },
			func(i, j int) { oldMatched[prefix+i], newMatched[prefix+j] = true, true })
	}

	common, total := similarityBytes(ot, oldMatched, nt, true)
	if total == 0 {
		common, total = similarityBytes(ot, oldMatched, nt, false)
	}
	if total == 0 || float64(2*common)/float64(total) < minSimilarity {
		return nil, nil
	}
	return rangesOf(ot, oldMatched, old), rangesOf(nt, newMatched, new)
}

// spend takes n units from budget and reports whether they fit; when they do
// not it marks the budget exhausted (-1). A nil budget is unlimited.
func spend(budget *int, n int) bool {
	if budget == nil {
		return true
	}
	if n > *budget {
		*budget = -1
		return false
	}
	*budget -= n
	return true
}

// lcsMark runs an LCS over an om x nm grid and calls mark for each matched
// (i, j) pair.
func lcsMark(om, nm int, eq func(i, j int) bool, mark func(i, j int)) {
	w := nm + 1
	dp := make([]int, (om+1)*w)
	for i := om - 1; i >= 0; i-- {
		for j := nm - 1; j >= 0; j-- {
			switch {
			case eq(i, j):
				dp[i*w+j] = dp[(i+1)*w+j+1] + 1
			case dp[(i+1)*w+j] >= dp[i*w+j+1]:
				dp[i*w+j] = dp[(i+1)*w+j]
			default:
				dp[i*w+j] = dp[i*w+j+1]
			}
		}
	}
	for i, j := 0, 0; i < om && j < nm; {
		switch {
		case eq(i, j):
			mark(i, j)
			i++
			j++
		case dp[(i+1)*w+j] >= dp[i*w+j+1]:
			i++
		default:
			j++
		}
	}
}

// similarityBytes returns the matched bytes of the old side and the total
// bytes of both sides that count towards similarity: with wordsOnly, only
// word and unspaced-script tokens, so shared punctuation never makes
// otherwise unrelated lines look alike; without it, every non-whitespace
// token (for lines that have no words at all, such as "}" and "})").
func similarityBytes(ot []token, oldMatched []bool, nt []token, wordsOnly bool) (common, total int) {
	counts := func(t token) bool {
		if wordsOnly {
			return t.kind == kindWord || t.kind == kindUnspaced
		}
		return !t.space()
	}
	for i, t := range ot {
		if counts(t) {
			total += t.end - t.start
			if oldMatched[i] {
				common += t.end - t.start
			}
		}
	}
	for _, t := range nt {
		if counts(t) {
			total += t.end - t.start
		}
	}
	return common, total
}

// rangesOf turns the unmatched tokens into byte ranges, merging two ranges
// whose gap in s is only spaces/tabs, then trims spaces/tabs off both ends of
// each range (a tab-expanded column's padding is not a changed word). A
// whitespace-only range is kept only inside the line's indentation, where a
// changed indent is a real change; elsewhere it is dropped. A leading
// indentation stays in the one range with the word that follows it rather
// than being split off: the indentation changed with the word, and dropping
// it would hide that.
func rangesOf(toks []token, matched []bool, s string) [][]int {
	var out [][]int
	for i, t := range toks {
		if matched[i] {
			continue
		}
		last := len(out) - 1
		if last >= 0 && onlySpaces(s[out[last][1]:t.start]) {
			out[last][1] = t.end
			continue
		}
		out = append(out, []int{t.start, t.end})
	}
	kept := out[:0]
	for _, r := range out {
		text := s[r[0]:r[1]]
		if onlySpaces(text) {
			if onlySpaces(s[:r[0]]) {
				kept = append(kept, r)
			}
			continue
		}
		lead := 0
		if strings.TrimLeft(s[:r[0]], " \t") != "" {
			lead = len(text) - len(strings.TrimLeft(text, " \t"))
		}
		trail := len(text) - len(strings.TrimRight(text, " \t"))
		kept = append(kept, []int{r[0] + lead, r[1] - trail})
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func onlySpaces(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}

// IntraLineRanges maps a line index to the byte ranges of its changed words
// for every Del/Add pair SideBySide forms. budget is the work left for the
// whole call's caller (shared across hunks), in input bytes tokenised plus
// DP cells: each pair spends its cost, and once a pair does not fit no
// further pair is emphasised, so thousands of pairs cannot stall the UI
// goroutine. A nil budget is unlimited.
func IntraLineRanges(lines []Line, budget *int) map[int][][]int {
	var out map[int][][]int
	for _, p := range SideBySide(lines) {
		if p.Left < 0 || p.Right < 0 || p.Left == p.Right {
			continue
		}
		if budget != nil && *budget < 0 {
			break
		}
		o, n := inlineChanges(lines[p.Left].Text, lines[p.Right].Text, budget)
		if o == nil && n == nil {
			continue
		}
		if out == nil {
			out = map[int][][]int{}
		}
		if len(o) > 0 {
			out[p.Left] = o
		}
		if len(n) > 0 {
			out[p.Right] = n
		}
	}
	return out
}
