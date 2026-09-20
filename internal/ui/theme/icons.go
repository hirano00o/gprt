package theme

// Icons is one glyph set gprt draws pull request state, review, and section
// markers with. Every field is exactly one grapheme cluster wide in a
// terminal using the matching font (a plain Unicode font for Unicode, a
// Nerd Font patched font for Nerd).
type Icons struct {
	Draft  string
	Open   string
	Merged string
	Closed string

	RollupSuccess string
	RollupFailure string
	RollupPending string
	RollupNone    string

	Loading string

	SectionMarker string
	// DraftMarker is the diff gutter glyph drawn on a line with a saved
	// comment draft anchored to it (see internal/ui/files.go's
	// draftLinesForFile).
	DraftMarker string
}

// Unicode returns gprt's default icon set: portable glyphs from the general
// Unicode block, readable in essentially any terminal font without a
// patched Nerd Font.
func Unicode() Icons {
	return Icons{
		Draft:  "◌",
		Open:   "●",
		Merged: "◆",
		Closed: "✖",

		RollupSuccess: "✓",
		RollupFailure: "✗",
		RollupPending: "◐",
		RollupNone:    "·",

		Loading: "…",

		SectionMarker: "▸",
		DraftMarker:   "✎",
	}
}

// Nerd returns gprt's icon set built from Nerd Font glyphs (the Font
// Awesome 4 subset every Nerd Fonts patched font includes, at its
// well-established Private Use Area code points), enabled by config's
// "icons: nerd". Falls back to Unicode's glyphs wherever no well-known
// Font Awesome equivalent exists.
func Nerd() Icons {
	return Icons{
		Draft:  "", // pencil
		Open:   "", // code-fork
		Merged: "", // check-circle
		Closed: "", // ban

		RollupSuccess: "", // check
		RollupFailure: "", // times
		RollupPending: "", // clock-o
		RollupNone:    "", // circle-o

		Loading: "", // hourglass-half

		SectionMarker: "", // caret-right
		// Reuses Draft's own pencil glyph: both mark "there is
		// unfinished, unsaved comment text here", one for a whole pull
		// request, the other for a single diff line.
		DraftMarker: "",
	}
}

// IconsFor selects an icon set from config's "icons" value: "nerd" selects
// Nerd, everything else (including the empty string) selects Unicode.
// config.Config.Validate already rejects any value other than "unicode" or
// "nerd", so the fallback here only matters for a zero-value Config, as
// tests construct directly.
func IconsFor(configIcons string) Icons {
	if configIcons == "nerd" {
		return Nerd()
	}
	return Unicode()
}
