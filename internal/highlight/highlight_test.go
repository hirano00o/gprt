package highlight

import (
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

// TestLines_FilesSharingAnExtensionResolveIndependently is the regression
// test for review item 14: the lexer cache is keyed by full base name, not
// extension, so two files sharing an extension but resolving to different
// chroma lexers (CMake's exact-filename match on "CMakeLists.txt" versus
// plaintext's "*.txt" glob, both ".txt") do not clobber each other's cache
// entry - whichever file happens to be highlighted first must not decide
// the other's lexer.
func TestLines_FilesSharingAnExtensionResolveIndependently(t *testing.T) {
	h := New(Options{})
	line := []string{"cmake_minimum_required(VERSION 3.10)"}

	cmakeTokens, err := h.Lines("CMakeLists.txt", line)
	if err != nil {
		t.Fatalf("Lines(CMakeLists.txt) error = %v", err)
	}
	readmeTokens, err := h.Lines("README.txt", line)
	if err != nil {
		t.Fatalf("Lines(README.txt) error = %v", err)
	}

	if tokensEqual(cmakeTokens, readmeTokens) {
		t.Errorf(
			"Lines(CMakeLists.txt, %q) and Lines(README.txt, %q) produced identical tokens %+v; "+
				"want CMake's lexer for the former and plaintext's for the latter, resolved independently despite sharing the \".txt\" extension",
			line[0], line[0], cmakeTokens,
		)
	}

	// Order must not matter: resolving README.txt first, then
	// CMakeLists.txt, must give the same (differing) results.
	readmeFirst, err := h.Lines("README.txt", line)
	if err != nil {
		t.Fatalf("Lines(README.txt) error = %v", err)
	}
	cmakeSecond, err := h.Lines("CMakeLists.txt", line)
	if err != nil {
		t.Fatalf("Lines(CMakeLists.txt) error = %v", err)
	}
	if tokensEqual(readmeFirst, cmakeSecond) {
		t.Errorf("Lines() results were identical regardless of resolution order; want them to differ by lexer regardless of order")
	}
}

// tokensEqual reports whether a and b hold the same token sequence.
func tokensEqual(a, b [][]Token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func TestLines_UnknownExtensionReturnsNil(t *testing.T) {
	h := New(Options{})
	tokens, err := h.Lines("file.no-such-ext-xyz", []string{"whatever"})
	if err != nil {
		t.Fatalf("Lines() error = %v, want nil", err)
	}
	if tokens != nil {
		t.Fatalf("Lines() = %v, want nil for an unknown extension", tokens)
	}
}

func TestLines_LineCountInvariant(t *testing.T) {
	h := New(Options{})
	lines := []string{
		"package main",
		"",
		"func main() {",
		`	println("hi")`,
		"}",
		"",
	}
	tokens, err := h.Lines("main.go", lines)
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}
	if len(tokens) != len(lines) {
		t.Fatalf("Lines() returned %d line-groups, want %d (one per input line)", len(tokens), len(lines))
	}
}

func TestLines_MultiLineCommentTokensOnBothLines(t *testing.T) {
	h := New(Options{})
	lines := []string{
		"/* This is a",
		"   multi-line comment */",
		"func main() {}",
	}
	tokens, err := h.Lines("main.go", lines)
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}
	if len(tokens) != len(lines) {
		t.Fatalf("Lines() returned %d line-groups, want %d", len(tokens), len(lines))
	}

	for i, want := range []int{0, 1} {
		if !anyTokenInComment(tokens[want]) {
			t.Errorf("line %d has no comment token: %+v (index %d)", want, tokens[want], i)
		}
	}
}

func anyTokenInComment(line []Token) bool {
	for _, tok := range line {
		if tok.Type.InCategory(chroma.Comment) {
			return true
		}
	}
	return false
}

func TestLines_MaxLineBytesSkipsHighlighting(t *testing.T) {
	h := New(Options{MaxLineBytes: 4})
	tokens, err := h.Lines("main.go", []string{"this line is too long"})
	if err != nil {
		t.Fatalf("Lines() error = %v, want nil", err)
	}
	if tokens != nil {
		t.Fatalf("Lines() = %v, want nil when a line exceeds MaxLineBytes", tokens)
	}
}

func TestLines_MaxTotalBytesSkipsHighlighting(t *testing.T) {
	h := New(Options{MaxTotalBytes: 4})
	tokens, err := h.Lines("main.go", []string{"aa", "bb", "cc"})
	if err != nil {
		t.Fatalf("Lines() error = %v, want nil", err)
	}
	if tokens != nil {
		t.Fatalf("Lines() = %v, want nil when the total exceeds MaxTotalBytes", tokens)
	}
}

func TestLines_EmptyInputReturnsNil(t *testing.T) {
	h := New(Options{})
	tokens, err := h.Lines("main.go", nil)
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}
	if tokens != nil {
		t.Fatalf("Lines() = %v, want nil for no input lines", tokens)
	}
}

func TestLines_ConcurrentCallsAreSafe(t *testing.T) {
	h := New(Options{})
	lines := []string{
		"package main",
		"",
		"func main() {",
		`	println("hi")`,
		"}",
	}

	var wg sync.WaitGroup
	paths := []string{"a.go", "b.py", "c.js", "d.go", "e.unknownext"}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		path := paths[i%len(paths)]
		go func() {
			defer wg.Done()
			if _, err := h.Lines(path, lines); err != nil {
				t.Errorf("Lines(%q) error = %v", path, err)
			}
		}()
	}
	wg.Wait()
}

func TestLines_DefaultsAppliedWhenOptionsZero(t *testing.T) {
	h := New(Options{})
	// A single, short line should highlight fine under the defaults
	// (4096 bytes/line, 1 MiB total) without hitting either limit.
	tokens, err := h.Lines("main.go", []string{"package main"})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}
	if len(tokens) != 1 || len(tokens[0]) == 0 {
		t.Fatalf("Lines() = %+v, want one highlighted line", tokens)
	}
}

func TestLines_JoinsWithNewlineNotPrefixes(t *testing.T) {
	h := New(Options{})
	// Callers pass lines without diff +/- prefixes (see package doc): make
	// sure Lines does not choke on, or misrender, a line that legitimately
	// starts with a character that would be a diff marker.
	tokens, err := h.Lines("main.go", []string{"+ is not a diff marker here", "package main"})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("Lines() returned %d line-groups, want 2", len(tokens))
	}
	var rebuilt strings.Builder
	for _, tok := range tokens[0] {
		rebuilt.WriteString(tok.Text)
	}
	if rebuilt.String() != "+ is not a diff marker here" {
		t.Errorf("rebuilt line 0 = %q, want the original text preserved", rebuilt.String())
	}
}
