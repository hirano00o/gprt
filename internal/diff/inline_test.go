package diff

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// inlineRanges runs inlineChanges without a shared budget.
func inlineRanges(old, new string) (oldRanges, newRanges [][]int) {
	oldRanges, newRanges = inlineChanges(old, new, nil)
	return oldRanges, newRanges
}

func lines(kinds string) []Line {
	out := make([]Line, 0, len(kinds))
	for _, k := range kinds {
		switch k {
		case 'c':
			out = append(out, Line{Kind: Context})
		case '+':
			out = append(out, Line{Kind: Add})
		case '-':
			out = append(out, Line{Kind: Del})
		}
	}
	return out
}

func TestSideBySide(t *testing.T) {
	tests := []struct {
		name  string
		lines []Line
		want  []Pair
	}{
		{"empty", nil, nil},
		{"context only", lines("cc"), []Pair{{0, 0}, {1, 1}}},
		{"one del one add", lines("-+"), []Pair{{0, 1}}},
		{"two dels one add", lines("--+"), []Pair{{0, 2}, {1, -1}}},
		{"one del two adds", lines("-++"), []Pair{{0, 1}, {-1, 2}}},
		{"add only", lines("c+c"), []Pair{{0, 0}, {-1, 1}, {2, 2}}},
		{"del only", lines("c-c"), []Pair{{0, 0}, {1, -1}, {2, 2}}},
		{"two blocks split by context", lines("-+c-+"), []Pair{{0, 1}, {2, 2}, {3, 4}}},
		{"add then del is not a pair", lines("+-"), []Pair{{-1, 0}, {1, -1}}},
		{
			"no newline flag does not affect pairing",
			[]Line{{Kind: Del}, {Kind: Add, NoNewline: true}},
			[]Pair{{0, 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SideBySide(tc.lines)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SideBySide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInlineRanges(t *testing.T) {
	tests := []struct {
		name         string
		old, new     string
		wantO, wantN [][]int
	}{
		{"one word changed", "foo(a, b)", "foo(a, c)", [][]int{{7, 8}}, [][]int{{7, 8}}},
		{"appended word", "foo(a)", "foo(a, b)", nil, [][]int{{5, 8}}},
		{"operator is one token", "if err != nil {", "if err == nil {", [][]int{{7, 9}}, [][]int{{7, 9}}},
		{"call gains an argument", "foo()", "foo(ctx)", nil, [][]int{{4, 7}}},
		{"empty string gains a letter", `foo("")`, `foo("x")`, nil, [][]int{{5, 6}}},
		{"index added to brackets", "foo[]", "foo[i]", nil, [][]int{{4, 5}}},
		{"padding left out of the range", "ab  x y z", "abc x y z", [][]int{{0, 2}}, [][]int{{0, 3}}},
		{"indentation-only change keeps its whitespace range", "\tfoo bar baz", "    foo bar baz", [][]int{{0, 1}}, [][]int{{0, 4}}},
		{"indentation and word change together", "    foo(a, b, c)", "        bar(a, b, c)", [][]int{{0, 7}}, [][]int{{0, 11}}},
		{"whitespace-only range outside the indentation is dropped", "f(a)    b", "f(ab)   b", [][]int{{2, 3}}, [][]int{{2, 4}}},
		{"sign change next to an operator character", "x=-1", "x=+1", [][]int{{2, 3}}, [][]int{{2, 3}}},
		{"logical operator before a negation", "a&&!b", "a||!b", [][]int{{1, 3}}, [][]int{{1, 3}}},
		{"accented word is one token", "naïve café ok today", "naïve cafés ok today", [][]int{{7, 12}}, [][]int{{7, 13}}},
		{"every word replaced around shared punctuation", "f(a, b, c)", "g(x, y, z)", nil, nil},
		{"punctuation-only lines fall back to non-whitespace bytes", "}", "})", nil, [][]int{{1, 2}}},
		{"less-than before a negative number", "if i<-1 {", "if i<0 {", [][]int{{5, 7}}, [][]int{{5, 6}}},
		{"three-character operator wins over two", "a...b", "a..b", [][]int{{1, 4}}, [][]int{{1, 3}}},
		{"triple equals wins over double", "a===b", "a==b", [][]int{{1, 4}}, [][]int{{1, 3}}},
		{"one Thai character", "ฉันไปตลาด", "ฉันไปตลอด", [][]int{{21, 24}}, [][]int{{21, 24}}},
		{"prolonged sound mark dropped", "サーバ起動です", "サバ起動です", [][]int{{3, 6}}, nil},
		{"halfwidth prolonged sound mark dropped", "ﾗｰﾒﾝ好き", "ﾗﾒﾝ好き", [][]int{{3, 6}}, nil},
		{"one Hangul syllable", "안녕하세요 세계", "안녕하세오 세계", [][]int{{12, 15}}, [][]int{{12, 15}}},
		{"punctuation", "a.b", "a->b", [][]int{{1, 2}}, [][]int{{1, 3}}},
		{"whitespace separated changes merge", "alpha one two omega", "alpha uno dos omega", [][]int{{6, 13}}, [][]int{{6, 13}}},
		{"one CJK char", "今日は天気がいいです", "今日は天気がよいです", [][]int{{18, 21}}, [][]int{{18, 21}}},
		{"combining mark not split", "résume done today", "rèsume done today", [][]int{{0, 8}}, [][]int{{0, 8}}},
		{"totally different", "alpha beta", "gamma delta", nil, nil},
		{"identical", "same text", "same text", nil, nil},
		{"every non-whitespace byte changed", "foo  bar", "baz  qux", nil, nil},
		{"both blank", "  ", "\t", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotO, gotN := inlineRanges(tc.old, tc.new)
			if !reflect.DeepEqual(gotO, tc.wantO) || !reflect.DeepEqual(gotN, tc.wantN) {
				t.Errorf("inlineRanges(%q, %q) = %v, %v; want %v, %v", tc.old, tc.new, gotO, gotN, tc.wantO, tc.wantN)
			}
			for _, side := range []struct {
				s  string
				rs [][]int
			}{{tc.old, gotO}, {tc.new, gotN}} {
				prev := 0
				for _, r := range side.rs {
					if r[0] < prev || r[1] <= r[0] || r[1] > len(side.s) {
						t.Errorf("range %v of %q is unsorted, empty or out of bounds", r, side.s)
					}
					if !utf8.RuneStart(side.s[r[0]]) || (r[1] < len(side.s) && !utf8.RuneStart(side.s[r[1]])) {
						t.Errorf("range %v of %q is not on rune boundaries", r, side.s)
					}
					prev = r[1]
				}
			}
		})
	}
}

// halfChangedWords builds a line of n words where every odd word is
// replaced when changed is true, so that the two lines are similar enough
// to be emphasised were it not for the DP size cap.
func halfChangedWords(n int, changed bool) string {
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
		if changed && i%2 == 1 {
			words[i] = fmt.Sprintf("x%d", i)
		}
	}
	return strings.Join(words, " ")
}

func TestInlineRangesDPCap(t *testing.T) {
	if o, n := inlineRanges(halfChangedWords(20, false), halfChangedWords(20, true)); o == nil || n == nil {
		t.Fatal("small pair must be emphasised, otherwise the cap case below proves nothing")
	}
	if o, n := inlineRanges(halfChangedWords(120, false), halfChangedWords(120, true)); o != nil || n != nil {
		t.Errorf("pair over the DP cap = %v, %v; want nil, nil", o, n)
	}
}

func TestIntraLineRanges(t *testing.T) {
	ls := []Line{
		{Kind: Context, Text: "ctx"},
		{Kind: Del, Text: "foo(a, b)"},
		{Kind: Add, Text: "foo(a, c)"},
		{Kind: Add, Text: "unpaired"},
	}
	budget := 1000
	got := IntraLineRanges(ls, &budget)
	want := map[int][][]int{1: {{7, 8}}, 2: {{7, 8}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IntraLineRanges = %v, want %v", got, want)
	}
	if budget >= 1000 {
		t.Errorf("budget = %d, want it decremented by the pair's DP size", budget)
	}
}

func TestIntraLineRangesBudgetExhaustionStopsFurtherPairs(t *testing.T) {
	// Each pair costs 14 bytes tokenised plus a 36 cell DP table (5+1 by 5+1).
	ls := []Line{
		{Kind: Del, Text: "a b c d"},
		{Kind: Add, Text: "a x c y"},
		{Kind: Context, Text: "ctx"},
		{Kind: Del, Text: "a b c d"},
		{Kind: Add, Text: "a x c y"},
	}
	budget := 60 // one pair costs 14 bytes + 36 cells = 50
	got := IntraLineRanges(ls, &budget)
	if len(got[0]) == 0 || len(got[1]) == 0 {
		t.Errorf("first pair must be emphasised, got %v", got)
	}
	if len(got[3]) != 0 || len(got[4]) != 0 {
		t.Errorf("second pair must be skipped once the budget runs out, got %v", got)
	}
}

func TestInlineChangesBudgetEdges(t *testing.T) {
	// 14 bytes to tokenise plus a 6 x 6 = 36 cell DP table (5 x 5 middle tokens).
	const old, new, cost = "a b c d", "a x c y", 14 + 36

	t.Run("pair exactly filling the budget is emphasised", func(t *testing.T) {
		budget := cost
		o, n := inlineChanges(old, new, &budget)
		if o == nil || n == nil || budget != 0 {
			t.Errorf("inlineChanges = %v, %v, budget=%d; want ranges, budget 0", o, n, budget)
		}
	})
	t.Run("pair one unit over the budget is skipped", func(t *testing.T) {
		budget := cost - 1
		o, n := inlineChanges(old, new, &budget)
		if o != nil || n != nil || budget >= 0 {
			t.Errorf("inlineChanges = %v, %v, budget=%d; want nil, nil, negative budget", o, n, budget)
		}
	})
	t.Run("pair over maxDPCells spends only its tokenisation bytes", func(t *testing.T) {
		a, b := halfChangedWords(120, false), halfChangedWords(120, true)
		budget := 1_000_000
		o, n := inlineChanges(a, b, &budget)
		if o != nil || n != nil || budget != 1_000_000-len(a)-len(b) {
			t.Errorf("inlineChanges = %v, %v, budget=%d; want nil, nil, budget %d", o, n, budget, 1_000_000-len(a)-len(b))
		}
	})
	t.Run("pair over the byte cap is dropped before tokenising", func(t *testing.T) {
		long := strings.Repeat("word ", maxPairBytes/5)
		budget := 1_000_000
		o, n := inlineChanges(long+"a", long+"b", &budget)
		if o != nil || n != nil || budget != 1_000_000 {
			t.Errorf("inlineChanges = %v, %v, budget=%d; want nil, nil, untouched budget", o, n, budget)
		}
	})
	t.Run("nil budget is unlimited", func(t *testing.T) {
		if o, n := inlineChanges(old, new, nil); o == nil || n == nil {
			t.Errorf("inlineChanges = %v, %v, want ranges", o, n)
		}
	})
}

func TestIntraLineRangesNilBudgetIsUnlimited(t *testing.T) {
	ls := []Line{{Kind: Del, Text: "foo(a, b)"}, {Kind: Add, Text: "foo(a, c)"}}
	if got := IntraLineRanges(ls, nil); len(got) != 2 {
		t.Errorf("IntraLineRanges(nil budget) = %v, want both lines emphasised", got)
	}
}

func TestIntraLineRangesTokenisationCostStopsLongPairs(t *testing.T) {
	// Each pair needs only a 2 x 2 DP table (4 cells) but 18 bytes to tokenise.
	ls := []Line{
		{Kind: Del, Text: "foo(a, b)"},
		{Kind: Add, Text: "foo(a, c)"},
		{Kind: Context},
		{Kind: Del, Text: "foo(a, b)"},
		{Kind: Add, Text: "foo(a, c)"},
	}
	budget := 30
	got := IntraLineRanges(ls, &budget)
	if len(got[0]) == 0 || len(got[3]) != 0 || len(got[4]) != 0 {
		t.Errorf("IntraLineRanges = %v, want only the first pair emphasised (22 of 30 units spent)", got)
	}
}

func TestInlineRangesSimilarityThreshold(t *testing.T) {
	// 2*2/(4+6) = 0.4 exactly: emphasised. 2*2/(4+7) is just under: not.
	if o, n := inlineRanges("aa xx", "aa yyyy"); !reflect.DeepEqual(o, [][]int{{3, 5}}) || !reflect.DeepEqual(n, [][]int{{3, 7}}) {
		t.Errorf("at the threshold = %v, %v; want emphasis", o, n)
	}
	if o, n := inlineRanges("aa xx", "aa yyyyy"); o != nil || n != nil {
		t.Errorf("just under the threshold = %v, %v; want nil, nil", o, n)
	}
}
