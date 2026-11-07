package inlineimg

import (
	"image"
	"image/color"
)

// downscale shrinks src to dw x dh pixels by averaging the box of source
// pixels each destination pixel covers, weighting colours by alpha.
//
// It is hand-written because x/image's scalers first build an intermediate
// buffer of destination width x source height float64 quadruples (115 MB for
// a 4000x3000 source scaled to 1200 wide), which would defeat the pixel
// limit's purpose. A box filter needs no intermediate and is the standard
// choice for shrinking.
func downscale(src image.Image, dw, dh int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	at := pixelReader(src)

	for dy := range dh {
		y0 := dy * sh / dh
		y1 := min(max(y0+1, (dy+1)*sh/dh), sh)
		for dx := range dw {
			x0 := dx * sw / dw
			x1 := min(max(x0+1, (dx+1)*sw/dw), sw)

			var r, g, bl, a, n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					c := at(b.Min.X+x, b.Min.Y+y)
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					n++
				}
			}
			if a == 0 {
				continue // fully transparent: the zero pixel
			}
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: uint8((r + a/2) / a),
				G: uint8((g + a/2) / a),
				B: uint8((bl + a/2) / a),
				A: uint8((a + n/2) / n),
			})
		}
	}
	return dst
}

// pixelReader returns an accessor for src's pixels as non-premultiplied
// colours, with fast paths for the types the decoders produce (the generic
// path boxes every pixel into a color.Color, which allocates).
func pixelReader(src image.Image) func(x, y int) color.NRGBA {
	switch s := src.(type) {
	case *image.NRGBA:
		return s.NRGBAAt
	case *image.RGBA:
		return func(x, y int) color.NRGBA {
			c := s.RGBAAt(x, y)
			switch c.A {
			case 255:
				return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}
			case 0:
				return color.NRGBA{}
			}
			a := uint32(c.A)
			return color.NRGBA{
				R: uint8(uint32(c.R) * 255 / a), G: uint8(uint32(c.G) * 255 / a), B: uint8(uint32(c.B) * 255 / a), A: c.A,
			}
		}
	case *image.YCbCr:
		return func(x, y int) color.NRGBA {
			c := s.YCbCrAt(x, y)
			r, g, b := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			return color.NRGBA{R: r, G: g, B: b, A: 255}
		}
	default:
		return func(x, y int) color.NRGBA {
			return color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
		}
	}
}
