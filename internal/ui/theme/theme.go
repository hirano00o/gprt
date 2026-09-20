// Package theme owns gprt's dark colour scheme: the named tcell.Style
// values every widget draws with, a stable hash-based colour for
// free-form strings (repository and author names), styles derived from a
// pull request's review/check state, and the Unicode/Nerd Font icon sets.
// It has no dependency on gprt's store; only internal/model, for the
// state enums ReviewerStyle and RollupStyle switch on.
package theme

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The dark theme's base palette. Kept as unexported colours (rather than
// inlined in every style below) so the whole theme can be re-tuned in one
// place.
var (
	colorBg      = tcell.ColorBlack
	colorFg      = tcell.ColorWhite
	colorMuted   = tcell.ColorGray
	colorAccent  = tcell.ColorDeepSkyBlue
	colorError   = tcell.ColorRed
	colorSuccess = tcell.ColorLimeGreen
	colorWarning = tcell.ColorGold
	colorPending = tcell.ColorOrange
)

// Named styles every gprt widget draws with. Base is the default row style;
// the others layer a semantic meaning (Header for section headings, Cursor
// for the selected row's background, Stale for cache-shown-not-yet-verified
// data, and so on) on top of it.
var (
	Base    = tcell.StyleDefault.Foreground(colorFg).Background(colorBg)
	Header  = tcell.StyleDefault.Foreground(colorAccent).Background(colorBg).Bold(true)
	Cursor  = tcell.StyleDefault.Foreground(colorBg).Background(colorAccent)
	Stale   = tcell.StyleDefault.Foreground(colorMuted).Background(colorBg).Italic(true)
	Muted   = tcell.StyleDefault.Foreground(colorMuted).Background(colorBg)
	Accent  = tcell.StyleDefault.Foreground(colorAccent).Background(colorBg)
	Error   = tcell.StyleDefault.Foreground(colorError).Background(colorBg)
	Success = tcell.StyleDefault.Foreground(colorSuccess).Background(colorBg)
	Warning = tcell.StyleDefault.Foreground(colorWarning).Background(colorBg)
	Pending = tcell.StyleDefault.Foreground(colorPending).Background(colorBg)
)

// Apply sets tview's package-global Styles to gprt's dark theme. It must be
// called once, before any tview primitive is constructed: primitives read
// tview.Styles at construction time, not on every draw.
func Apply() {
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    colorBg,
		ContrastBackgroundColor:     colorMuted,
		MoreContrastBackgroundColor: colorAccent,
		BorderColor:                 colorMuted,
		TitleColor:                  colorAccent,
		GraphicsColor:               colorMuted,
		PrimaryTextColor:            colorFg,
		SecondaryTextColor:          colorAccent,
		TertiaryTextColor:           colorMuted,
		InverseTextColor:            colorBg,
		ContrastSecondaryTextColor:  colorFg,
	}
}
