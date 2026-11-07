package inlineimg

import (
	"image"
	"image/color"
	"testing"
)

func TestDownscale_AveragesEachBox(t *testing.T) {
	// A 4x2 image of two 2x2 blocks, each with one black and three white pixels.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := range 2 {
		for x := range 4 {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	src.SetNRGBA(0, 0, color.NRGBA{A: 255})
	src.SetNRGBA(3, 1, color.NRGBA{A: 255})

	dst := downscale(src, 2, 1)
	for x := range 2 {
		if got := dst.NRGBAAt(x, 0); got != (color.NRGBA{R: 191, G: 191, B: 191, A: 255}) {
			t.Errorf("pixel %d = %v, want the average of three white and one black pixel", x, got)
		}
	}
}

func TestDownscale_WeightsColourByAlpha(t *testing.T) {
	// One opaque red and one fully transparent (but green) pixel: the
	// transparent one must not tint the result.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 0})

	got := downscale(src, 1, 1).NRGBAAt(0, 0)
	if got != (color.NRGBA{R: 255, A: 128}) {
		t.Errorf("pixel = %v, want red at half alpha", got)
	}
}

func TestDownscale_NonNRGBASources(t *testing.T) {
	ycc := image.NewYCbCr(image.Rect(0, 0, 4, 4), image.YCbCrSubsampleRatio420)
	for i := range ycc.Y {
		ycc.Y[i] = 128
	}
	for i := range ycc.Cb {
		ycc.Cb[i], ycc.Cr[i] = 128, 128
	}
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	for i := range gray.Pix {
		gray.Pix[i] = 100
	}
	for name, src := range map[string]image.Image{"ycbcr": ycc, "gray": gray} {
		t.Run(name, func(t *testing.T) {
			dst := downscale(src, 2, 2)
			if b := dst.Bounds(); b != image.Rect(0, 0, 2, 2) {
				t.Fatalf("bounds = %v", b)
			}
			if got := dst.NRGBAAt(1, 1); got.A != 255 || got.R != got.G || got.G != got.B || got.R == 0 {
				t.Errorf("pixel = %v, want an opaque grey", got)
			}
		})
	}
}

func TestDownscale_SourceWithOffsetBounds(t *testing.T) {
	src := image.NewNRGBA(image.Rect(10, 20, 14, 24))
	for y := 20; y < 24; y++ {
		for x := 10; x < 14; x++ {
			src.SetNRGBA(x, y, color.NRGBA{B: 200, A: 255})
		}
	}
	if got := downscale(src, 2, 2).NRGBAAt(1, 1); got != (color.NRGBA{B: 200, A: 255}) {
		t.Errorf("pixel = %v", got)
	}
}

func TestDownscale_PremultipliedRGBASource(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.SetRGBA(0, 0, color.RGBA{R: 127, A: 127}) // opaque-red at half alpha, premultiplied
	src.SetRGBA(1, 0, color.RGBA{R: 255, A: 255})
	got := downscale(src, 1, 1).NRGBAAt(0, 0)
	if got != (color.NRGBA{R: 255, A: 191}) {
		t.Errorf("pixel = %v, want pure red at 3/4 alpha", got)
	}
}
