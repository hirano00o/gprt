package theme

import (
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
)

// defaultHighlightStyleName is used until SetHighlightStyle is called (or a
// caller passes an empty config.HighlightStyle to it); it matches
// config.Default's own "highlight_style" default.
const defaultHighlightStyleName = "github-dark"

var (
	highlightMu     sync.Mutex
	highlightStyle  = buildHighlightStyle(defaultHighlightStyleName)
	tokenStyleCache = map[chroma.TokenType]tcell.Style{}
)

// SetHighlightStyle configures the chroma style TokenStyle derives its
// per-token-type styles from (config's "highlight_style", for example
// "github-dark"). An unknown name falls back to chroma's own default style
// (styles.Get's own behaviour) rather than an error, since a diff view with
// merely-unfamiliar colours is far less disruptive than one that fails to
// start. Safe to call at any time; every previously cached TokenStyle
// result is invalidated.
func SetHighlightStyle(name string) {
	highlightMu.Lock()
	defer highlightMu.Unlock()
	if name == "" {
		name = defaultHighlightStyleName
	}
	highlightStyle = buildHighlightStyle(name)
	tokenStyleCache = map[chroma.TokenType]tcell.Style{}
}

// buildHighlightStyle resolves name to a chroma.Style and clears its
// Background entry: gprt draws diff-line backgrounds itself (theme.Base,
// theme.DiffLineStyle), so a chroma style's own background colour must
// never leak through and fight with those. Overriding the Background entry
// with an empty, NoInherit StyleEntry — rather than simply never reading
// it — is necessary because chroma.Style.Get resolves an unset field by
// falling through to its parent style's own entry for that same token type
// (see the parent chroma.Style this Builder wraps); NoInherit stops that
// fallback for Background specifically, without affecting how any other
// token type inherits its own foreground/bold/italic from Background,
// Text, or its category (chroma.StyleEntry.Inherit only ever refers to the
// entry being resolved's own NoInherit flag, not an ancestor's).
func buildHighlightStyle(name string) *chroma.Style {
	base := styles.Get(name)
	built, err := base.Builder().
		AddEntry(chroma.Background, chroma.StyleEntry{NoInherit: true}).
		Build()
	if err != nil {
		// AddEntry stores entry.String() and Build re-parses every entry
		// back through ParseStyleEntry, so this round-trips rather than
		// bypassing the string parser: StyleEntry{NoInherit: true}.String()
		// is exactly "noinherit", which ParseStyleEntry always accepts.
		// Build can only fail here if chroma's own String()/ParseStyleEntry
		// pair stops round-tripping that value; treat that as a programmer
		// error, not a reachable runtime condition.
		panic("theme: unexpected chroma style build error: " + err.Error())
	}
	return built
}

// TokenStyle returns the tcell.Style a highlight.Token of type t should be
// drawn with: the configured chroma style's foreground colour (theme.Base's
// foreground when the style has none for t) plus bold/italic, with the
// background always theme.Base's — never chroma's own — so a diff row's
// background (added/removed/cursor/selection) always shows through
// unaffected by syntax colouring. Results are cached per token type.
func TokenStyle(t chroma.TokenType) tcell.Style {
	highlightMu.Lock()
	defer highlightMu.Unlock()

	if s, ok := tokenStyleCache[t]; ok {
		return s
	}

	entry := highlightStyle.Get(t)
	s := Base
	if entry.Colour.IsSet() {
		s = s.Foreground(tcell.NewRGBColor(int32(entry.Colour.Red()), int32(entry.Colour.Green()), int32(entry.Colour.Blue())))
	}
	if entry.Bold == chroma.Yes {
		s = s.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		s = s.Italic(true)
	}

	tokenStyleCache[t] = s
	return s
}
