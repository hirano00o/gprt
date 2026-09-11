// Package highlight tokenises diff line text for syntax colouring using
// chroma. It has no dependency on tcell/tview or store: it returns token
// kinds only, and the ui/theme package maps those kinds to tcell styles.
//
// Callers pass a file's hunk lines with the diff "+"/"-"/" " prefix already
// stripped and tabs already expanded (see internal/diff.ExpandTabs), so the
// text tokenised here is exactly what the file itself contains on that
// side.
package highlight

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// defaultMaxLineBytes and defaultMaxTotalBytes are applied when Options
// leaves the corresponding field at its zero value.
const (
	defaultMaxLineBytes  = 4096
	defaultMaxTotalBytes = 1 << 20 // 1 MiB
)

// Options configures a Highlighter.
type Options struct {
	// MaxLineBytes is the longest single line Lines will highlight.
	// Defaults to 4096 when zero or negative.
	MaxLineBytes int
	// MaxTotalBytes is the largest combined size of every line passed to
	// one Lines call that will be highlighted. Defaults to 1 MiB when zero
	// or negative.
	MaxTotalBytes int
}

// Token is one chroma-classified span of text.
type Token struct {
	Text string
	Type chroma.TokenType
}

// Highlighter tokenises source text for a set of known file extensions,
// caching the resolved lexer per extension so repeated calls for files of
// the same kind do not re-run chroma's filename matching every time.
//
// A Highlighter is safe for concurrent use, including concurrent Lines
// calls for different (or the same) paths: see Lines' doc comment for why
// sharing one *chroma.RegexLexer across goroutines is safe once its rules
// have compiled.
type Highlighter struct {
	opts Options

	mu     sync.Mutex
	lexers map[string]chroma.Lexer // nil value cached too: "no lexer for this extension"
}

// New builds a Highlighter from opts.
func New(opts Options) *Highlighter {
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = defaultMaxLineBytes
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = defaultMaxTotalBytes
	}
	return &Highlighter{opts: opts, lexers: make(map[string]chroma.Lexer)}
}

// Lines tokenises lines (a file's hunk lines, one entry per diff line, with
// any "+"/"-"/" " prefix already stripped) for path's file type, returning
// one token slice per input line in the same order.
//
// It returns (nil, nil) — meaning "render lines as plain text, unhighlighted"
// — rather than an error, in three cases: path's extension has no known
// chroma lexer (lexers.Match found nothing; lexers.Analyse, which sniffs
// file *content*, is deliberately never used as a fallback here since it is
// too slow to run on every hunk); any single line exceeds MaxLineBytes; or
// the combined size of lines exceeds MaxTotalBytes. All three are expected,
// routine outcomes for a diff viewer (binary-ish files, generated files,
// pathological single-line minified files), not failures.
//
// The whole of lines is tokenised as one text (joined with "\n") rather
// than one line at a time, so a token that legitimately spans multiple
// source lines (a block comment, a multi-line string) is classified
// correctly on every line it touches; chroma.SplitTokensIntoLines then
// re-splits the resulting token stream back into per-line groups. The
// result is defensively reshaped to exactly len(lines) groups (padding with
// empty slices, or folding any surplus group into the last line) so a
// caller can always index it by line position without a bounds check, even
// if a lexer's own newline handling (some force a trailing "\n" before
// tokenising) shifted the boundary by one somewhere along the way.
func (h *Highlighter) Lines(path string, lines []string) ([][]Token, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	total := 0
	for _, l := range lines {
		if len(l) > h.opts.MaxLineBytes {
			return nil, nil
		}
		total += len(l)
		if total > h.opts.MaxTotalBytes {
			return nil, nil
		}
	}

	lexer := h.lexerFor(path)
	if lexer == nil {
		return nil, nil
	}

	text := strings.Join(lines, "\n")
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil, err
	}

	grouped := chroma.SplitTokensIntoLines(it.Tokens())
	for i, line := range grouped {
		grouped[i] = stripTrailingNewline(line)
	}
	return reshape(grouped, len(lines)), nil
}

// stripTrailingNewline removes the trailing "\n" chroma.SplitTokensIntoLines
// leaves attached to the last token of every line but the (possibly
// omitted) final one: callers already know where one line ends and the next
// begins from len(lines) itself, so a bare newline character surviving into
// a Token's Text would be a rendering surprise, not useful information.
// Dropping it can empty out that last token entirely (a line ending exactly
// on a token boundary before the newline), in which case the now-empty
// token is removed rather than kept as a zero-length entry.
func stripTrailingNewline(line []chroma.Token) []chroma.Token {
	if len(line) == 0 {
		return line
	}
	last := &line[len(line)-1]
	if !strings.HasSuffix(last.Value, "\n") {
		return line
	}
	last.Value = strings.TrimSuffix(last.Value, "\n")
	if last.Value == "" {
		return line[:len(line)-1]
	}
	return line
}

// reshape adapts grouped (chroma's own line split of the tokenised text) to
// exactly n groups: short of n, it pads with nil (an unhighlighted, empty
// line); longer than n (for example because a lexer's EnsureNL config
// appended a trailing newline chroma then split off as its own trailing
// group), it folds every surplus group's tokens onto the last line rather
// than silently dropping them.
func reshape(grouped [][]chroma.Token, n int) [][]Token {
	out := make([][]Token, n)
	for i := range out {
		if i < len(grouped) {
			out[i] = convertTokens(grouped[i])
		}
	}
	for i := n; i < len(grouped); i++ {
		out[n-1] = append(out[n-1], convertTokens(grouped[i])...)
	}
	return out
}

// convertTokens maps chroma's own Token type to this package's Token.
func convertTokens(ts []chroma.Token) []Token {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Token, len(ts))
	for i, t := range ts {
		out[i] = Token{Text: t.Value, Type: t.Type}
	}
	return out
}

// lexerFor resolves and caches the lexer for path, keyed by its lowercased
// base name (see lexerCacheKey for why the *full* base name, not just the
// extension, is the correct cache granularity). A nil result (no lexer
// found for that key) is cached too, so a repeatedly referenced unknown
// filename does not re-run lexers.Match on every call.
//
// chroma concurrency: a resolved *chroma.RegexLexer compiles its rules
// exactly once, guarded by an internal sync.Once plus a mutex-checked
// "already compiled" flag (see (*RegexLexer).needRules/maybeCompile in
// chroma's regexp.go); each Tokenise call then allocates a fresh,
// call-local LexerState (text, state stack, mutator context) that is never
// shared between calls. The only thing two concurrent Tokenise calls on the
// same lexer instance touch after that first compile is the compiled
// *regexp2.Regexp values themselves, and dlclark/regexp2 documents "A
// Regexp is safe for concurrent use by multiple goroutines" once compiled.
// So one shared, cached lexer per filename - rather than one lexer per
// worker goroutine - is safe for the store's highlight worker pool; caching
// more entries than there are distinct extensions costs nothing extra,
// since every cache entry that resolves to the same lexer still points at
// the one shared, singleton *chroma.RegexLexer instance chroma's own
// registry already holds.
func (h *Highlighter) lexerFor(path string) chroma.Lexer {
	key := lexerCacheKey(path)

	h.mu.Lock()
	defer h.mu.Unlock()

	if lexer, ok := h.lexers[key]; ok {
		return lexer
	}

	var lexer chroma.Lexer
	if matched := lexers.Match(path); matched != nil {
		lexer = chroma.Coalesce(matched)
	}
	h.lexers[key] = lexer
	return lexer
}

// lexerCacheKey returns the cache key lexerFor keys its lexer map by: the
// lowercased *full base name* of path, not its extension. chroma's own
// lexers.Match resolves a lexer against the whole base name (some lexers
// register an exact filename glob alongside or instead of an extension
// glob - for example CMake matches both "*.cmake" and the literal
// "CMakeLists.txt"), so two files sharing an extension can legitimately
// resolve to different lexers ("CMakeLists.txt" -> CMake, "README.txt" ->
// plaintext, both ".txt"); keying the cache by extension alone would
// collapse them onto one shared, incorrect entry - whichever of the two
// happened to be highlighted first.
//
// This does not fix every quirk in chroma's own resolution, only this
// package's caching of it: lexers.Match itself has no notion of "prefer an
// exact filename over a glob" when multiple lexers match a name with equal
// declared Config.Priority (ties are broken by chroma.Lexers' registration
// order, which this package does not control) - for example a literal
// "go.mod" resolves to the AMPL lexer (registered filename glob "*.mod",
// default priority) rather than anything Go-specific, since chroma ships
// no dedicated "Go module" lexer with a more specific pattern. Such a file
// still renders (as AMPL, incorrectly, rather than plain text or Go), which
// is a chroma limitation to fix upstream, not a bug in this package.
func lexerCacheKey(path string) string {
	return strings.ToLower(filepath.Base(path))
}
