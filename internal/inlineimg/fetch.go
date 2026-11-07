package inlineimg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register the GIF decoder (first frame only)
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register the WebP decoder
)

const (
	fetchTimeout = 15 * time.Second
	// maxBytes bounds the encoded size read from the network.
	maxBytes = 10 << 20
	// maxPixels bounds the decoded size: 16 MP decodes to at most 64 MB (a
	// 40 MP PNG took over 500 MB, and two downloads run at once). The header
	// is checked first, so a small file claiming enormous dimensions (a
	// decompression bomb) is rejected before any pixel memory is allocated.
	maxPixels = 16_000_000
	// MaxWidth is the widest image kept; wider ones are downscaled before
	// caching. 1200 px is 150 columns of 8 px cells, or about 75 of the 16 px
	// cells of a hi-dpi display: more than an image is ever drawn across, and
	// a 1200x800 stored copy is 3.8 MB, which keeps the store's image cache
	// around 200 MB at most.
	MaxWidth     = 1200
	maxRedirects = 5
)

// Image is a decoded, possibly downscaled image.
type Image struct {
	// Pixels holds the image at no more than MaxWidth pixels wide, with its
	// bounds at the origin.
	Pixels *image.NRGBA
	// NaturalWidth is the width in pixels of the image as downloaded, before
	// any downscaling: it sizes the image on screen, so a small icon is not
	// blown up to fill the pane.
	NaturalWidth int
}

// Fetcher downloads and decodes images.
//
// It deliberately has its own plain http.Client and takes none from the
// GitHub API layer: an image URL comes from user-written text (a camo
// canonical URL or an arbitrary host), so no request made here may carry the
// GitHub token.
type Fetcher struct {
	client   *http.Client
	maxBytes int64
}

// NewFetcher returns a Fetcher using a fresh http.Client.
func NewFetcher() *Fetcher {
	return newFetcher(&http.Client{})
}

// newFetcher wraps a copy of base with the timeout and the https-only
// redirect policy. base exists so tests can supply a client trusting an
// httptest certificate.
func newFetcher(base *http.Client) *Fetcher {
	c := *base
	c.Timeout = fetchTimeout
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("inlineimg: too many redirects")
		}
		return requireHTTPS(req)
	}
	return &Fetcher{client: &c, maxBytes: maxBytes}
}

func requireHTTPS(req *http.Request) error {
	if req.URL.Scheme != "https" {
		return permanent(fmt.Errorf("inlineimg: refusing non-https url scheme %q", req.URL.Scheme))
	}
	return nil
}

// permanentError marks a failure that retrying cannot fix: the content
// itself is unusable (not an image, unsupported, too large), as opposed to a
// network error or an HTTP error status, which a fresh URL may cure.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent implements the interface IsPermanent looks for.
func (e *permanentError) Permanent() bool { return true }

func permanent(err error) error { return &permanentError{err: err} }

// IsPermanent reports whether err, returned by Fetch, is a failure no retry
// can fix. Everything else (network errors, HTTP error statuses) is
// transient: the signed URL may simply have expired.
func IsPermanent(err error) bool {
	var p interface{ Permanent() bool }
	return errors.As(err, &p) && p.Permanent()
}

// withoutURL returns err without the request URL. The net/http client wraps
// every transport, redirect-policy, and request-building error in a
// *url.Error whose text includes the full URL, and the URL of a signed image
// carries a bearer token in its query. Fetch's errors are logged, so none may
// contain it.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Fetch downloads rawURL (https only, an image/* content type, at most the
// size and pixel limits) and decodes png, jpeg, gif (first frame) or webp.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Image{}, fmt.Errorf("inlineimg: bad request: %w", withoutURL(err))
	}
	if err := requireHTTPS(req); err != nil {
		return Image{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Image{}, fmt.Errorf("inlineimg: fetch: %w", withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Image{}, fmt.Errorf("inlineimg: fetch: status %s", resp.Status)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || !strings.HasPrefix(mt, "image/") {
		return Image{}, permanent(fmt.Errorf("inlineimg: content type %q is not an image", resp.Header.Get("Content-Type")))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("inlineimg: read: %w", withoutURL(err))
	}
	if int64(len(data)) > f.maxBytes {
		return Image{}, permanent(fmt.Errorf("inlineimg: image is larger than %d bytes", f.maxBytes))
	}
	return decode(data)
}

// checkConfig rejects an image whose header claims an unusable size.
func checkConfig(cfg image.Config) error {
	// Division, not multiplication: the product of two huge claimed sizes
	// could overflow.
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxPixels/cfg.Height {
		return permanent(fmt.Errorf("inlineimg: image size %dx%d is out of range", cfg.Width, cfg.Height))
	}
	return nil
}

// decode checks the image's dimensions from its header, then decodes it and
// scales it down to at most MaxWidth.
func decode(data []byte) (Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, permanent(fmt.Errorf("inlineimg: unsupported image: %w", err))
	}
	if err := checkConfig(cfg); err != nil {
		return Image{}, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, permanent(fmt.Errorf("inlineimg: decode: %w", err))
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// The destination is allocated at its final size only: allocating a
	// full-size NRGBA first (to convert, then scale) would double the
	// transient memory of a large image.
	if w <= MaxWidth {
		if nrgba, ok := src.(*image.NRGBA); ok && b.Min == (image.Point{}) {
			return Image{Pixels: nrgba, NaturalWidth: w}, nil
		}
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return Image{Pixels: dst, NaturalWidth: w}, nil
	}
	return Image{Pixels: downscale(src, MaxWidth, max(1, h*MaxWidth/w)), NaturalWidth: w}, nil
}
