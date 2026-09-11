package theme

import (
	"hash/fnv"

	"github.com/gdamore/tcell/v2"
)

// palette is the 12 colours ColorFor picks from. Chosen to be mutually
// distinguishable on a dark background and visually separate from the
// theme's own semantic colours (Accent, Error, Success, Warning, Pending)
// so a repository or author name never looks like a status indicator.
var palette = []tcell.Color{
	tcell.ColorTomato,
	tcell.ColorOrange,
	tcell.ColorGold,
	tcell.ColorYellowGreen,
	tcell.ColorLimeGreen,
	tcell.ColorMediumSeaGreen,
	tcell.ColorTurquoise,
	tcell.ColorSteelBlue,
	tcell.ColorCornflowerBlue,
	tcell.ColorMediumPurple,
	tcell.ColorOrchid,
	tcell.ColorHotPink,
}

// ColorFor returns a stable colour for key, picked from a fixed 12-colour
// palette by an FNV-1a hash. The same key always maps to the same colour
// within one gprt process (and across processes, since the hash and
// palette are both fixed) — used to give each repository and author a
// consistent colour across the whole list without configuration.
func ColorFor(key string) tcell.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // hash.Hash.Write never returns an error
	return palette[h.Sum32()%uint32(len(palette))]
}
