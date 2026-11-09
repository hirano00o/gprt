//go:build ghostty

package ui

import (
	"context"
	"image"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/inlineimg"
	"github.com/hirano00o/gprt/internal/model"
)

// staticFetcher is a store.ImageFetcher that answers every download with img.
type staticFetcher struct{ img inlineimg.Image }

func (f staticFetcher) Fetch(context.Context, string) (inlineimg.Image, error) { return f.img, nil }

// noisePixels returns a w x h image of seeded random pixels, which PNG cannot
// compress: its transmission needs many 4096-byte chunks.
func noisePixels(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.UintN(256))
	}
	return img
}

// newGhosttyImageApp opens the image pull request's description, whose only
// content is one image served by the fake fetcher, in a kitty-detected
// terminal, and waits until its placeholder cells are on screen.
func newGhosttyImageApp(t *testing.T, img inlineimg.Image) *ghosttyApp {
	t.Helper()
	var ref model.PRRef
	g := newGhosttyApp(t, testAppOpts{
		images: staticFetcher{img: img},
		getenv: kittyEnv, // KITTY_WINDOW_ID set, no TMUX: raw, unwrapped sequences
	}, func(f *fakeGitHub) { ref = imagePRFixture(f) })

	act(g.app.app, func() { g.app.showPreview(ref.Key()) })
	g.Eventually(t, func(s string) bool { return strings.Contains(s, string(placeholderRune)) })
	return g
}

// onlyPlacement fails unless the terminal holds exactly one placement.
func onlyPlacement(t *testing.T, g *ghosttyApp) kittyPlacement {
	t.Helper()
	ps := g.KittyPlacements(t)
	if len(ps) != 1 {
		t.Fatalf("placements = %+v, want exactly one\n%s", ps, g.Text())
	}
	return ps[0]
}

// TestGhosttyImageIsAVirtualPlacementOfGprtsSize: a description that is one
// image becomes one virtual placement of the cols x rows gprt computed, and
// every placeholder cell on screen carries the image's id as its 256-colour
// foreground.
func TestGhosttyImageIsAVirtualPlacementOfGprtsSize(t *testing.T) {
	// 500x250 pixels shown at its natural 500 px in 10x20 pixel cells: 50
	// columns, and 250/500*50*10/20 = 12.5 rows, rounded up.
	g := newGhosttyImageApp(t, testPixels(500, 250, 500))

	p := onlyPlacement(t, g)
	if !p.Virtual {
		t.Errorf("placement is not virtual (U=1 lost?): %+v", p)
	}
	if p.Cols != 50 || p.Rows != 13 {
		t.Fatalf("placement = %dx%d cells, want 50x13", p.Cols, p.Rows)
	}
	if p.ImageID != 16 {
		t.Errorf("image id = %d, want 16, the first id internal/kitty hands out", p.ImageID)
	}

	fgs := g.PlaceholderForegrounds(t)
	// Whole rows, not cols x rows: how much of the image fits depends on the
	// layout around it, which this test is not about.
	if len(fgs) == 0 || len(fgs)%int(p.Cols) != 0 {
		t.Errorf("placeholder cells on screen = %d, want whole rows of %d", len(fgs), p.Cols)
	}
	for _, fg := range fgs {
		if fg != int(p.ImageID) {
			t.Fatalf("a placeholder cell has foreground palette index %d, want the image id %d", fg, p.ImageID)
		}
	}
}

// TestGhosttyChunkedImageIsRebuiltInTheTerminal: an image too big for one
// 4096-byte escape sequence still arrives whole.
func TestGhosttyChunkedImageIsRebuiltInTheTerminal(t *testing.T) {
	// 200x100 noise: a PNG of tens of kilobytes, so dozens of chunks.
	g := newGhosttyImageApp(t, inlineimg.Image{Pixels: noisePixels(200, 100), NaturalWidth: 200})

	if n := g.ContinuationChunks(); n < 2 {
		t.Fatalf("continuation chunks sent = %d, want several: the image is too small to test chunking", n)
	}
	p := onlyPlacement(t, g)
	if p.ImageWidth != 200 || p.ImageHeight != 100 {
		t.Errorf("image in the terminal is %dx%d pixels, want 200x100", p.ImageWidth, p.ImageHeight)
	}
	if p.Cols != 20 || p.Rows != 5 {
		t.Errorf("placement = %dx%d cells, want 20x5", p.Cols, p.Rows)
	}
}

// TestGhosttyImageReachesTheTerminalBeforeItsPlaceholders guards the ordering
// documented on imageBackend.flush: whenever cells carrying placeholders reach
// the terminal, the image they name is already stored there.
func TestGhosttyImageReachesTheTerminalBeforeItsPlaceholders(t *testing.T) {
	g := newGhosttyImageApp(t, testPixels(500, 250, 500))
	// The id the cells name, not the placement's: both must match, but the
	// ordering claim is about the image the cells refer to.
	fgs := g.PlaceholderForegrounds(t)
	if len(fgs) == 0 || fgs[0] < 0 {
		t.Fatalf("placeholder foregrounds = %v, want palette indexes", fgs)
	}
	id := uint32(fgs[0])

	before := g.ImageIDsBeforePlaceholderWrites(t)
	if len(before) == 0 {
		t.Fatal("no write carried placeholder cells")
	}
	for i, ids := range before {
		if !slices.Contains(ids, id) {
			t.Errorf("write %d put placeholders on screen while the terminal stored images %v, not %d", i, ids, id)
		}
	}
}
