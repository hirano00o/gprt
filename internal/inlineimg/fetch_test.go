package inlineimg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
)

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// newTLSFetcher returns a Fetcher trusting srv's certificate.
func newTLSFetcher(srv *httptest.Server) *Fetcher {
	return newFetcher(srv.Client())
}

func serve(t *testing.T, contentType string, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetcher_Fetch_DecodesAndNeverSendsCredentials(t *testing.T) {
	var gotAuth, gotCookie string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encodePNG(t, solid(2, 4, color.NRGBA{R: 10, G: 20, B: 30, A: 255})))
	}))
	defer srv.Close()

	img, err := newTLSFetcher(srv).Fetch(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if gotAuth != "" || gotCookie != "" {
		t.Errorf("credentials sent: Authorization=%q Cookie=%q", gotAuth, gotCookie)
	}
	if b := img.Pixels.Bounds(); b.Dx() != 2 || b.Dy() != 4 || img.NaturalWidth != 2 {
		t.Errorf("image = %v natural %d, want 2x4 natural 2", b, img.NaturalWidth)
	}
	if got := img.Pixels.NRGBAAt(1, 3); got != (color.NRGBA{R: 10, G: 20, B: 30, A: 255}) {
		t.Errorf("pixel = %v", got)
	}
}

func TestFetcher_Fetch_Formats(t *testing.T) {
	var jpg, gifb bytes.Buffer
	if err := jpeg.Encode(&jpg, solid(3, 3, color.NRGBA{A: 255}), nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifb, solid(3, 2, color.NRGBA{R: 255, A: 255}), nil); err != nil {
		t.Fatal(err)
	}
	// A 1x1 lossless WebP.
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, contentType string
		body              []byte
		wantW             int
	}{
		{"jpeg", "image/jpeg", jpg.Bytes(), 3},
		{"gif first frame", "image/gif", gifb.Bytes(), 3},
		{"webp", "image/webp", webp, 1},
		{"content type with parameters", "image/png; charset=binary", encodePNG(t, solid(5, 1, color.NRGBA{A: 255})), 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.contentType, tc.body)
			img, err := newTLSFetcher(srv).Fetch(context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if img.NaturalWidth != tc.wantW {
				t.Errorf("NaturalWidth = %d, want %d", img.NaturalWidth, tc.wantW)
			}
		})
	}
}

func TestFetcher_Fetch_Rejects(t *testing.T) {
	bomb := encodePNG(t, solid(1, 1, color.NRGBA{A: 255}))
	// Patch the IHDR to claim 10000x10000 (100 MP) and fix its CRC: the
	// pixel data is never read, so the file stays tiny.
	binary.BigEndian.PutUint32(bomb[16:], 10000)
	binary.BigEndian.PutUint32(bomb[20:], 10000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29]))

	tests := []struct {
		name, contentType string
		body              []byte
		maxBytes          int64
	}{
		{"not an image content type", "text/html", []byte("<html>"), 0},
		{"svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0},
		{"garbage bytes", "image/png", []byte("not a png"), 0},
		{"over the size limit", "image/png", encodePNG(t, solid(40, 40, color.NRGBA{A: 255})), 16},
		{"decompression bomb dimensions", "image/png", bomb, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.contentType, tc.body)
			f := newTLSFetcher(srv)
			if tc.maxBytes > 0 {
				f.maxBytes = tc.maxBytes
			}
			if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
				t.Error("Fetch() error = nil, want an error")
			}
		})
	}
}

func TestFetcher_Fetch_BadStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := newTLSFetcher(srv).Fetch(context.Background(), srv.URL); err == nil {
		t.Error("Fetch() error = nil, want an error for 404")
	}
}

func TestFetcher_Fetch_HTTPSOnly(t *testing.T) {
	hit := false
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encodePNG(t, solid(1, 1, color.NRGBA{A: 255})))
	}))
	defer plain.Close()

	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer tls.Close()

	f := newFetcher(tls.Client())
	if _, err := f.Fetch(context.Background(), plain.URL); err == nil {
		t.Error("Fetch(http://...) error = nil, want an https-only error")
	}
	if _, err := f.Fetch(context.Background(), tls.URL); err == nil {
		t.Error("Fetch(redirect to http://...) error = nil, want an https-only error")
	}
	if hit {
		t.Error("the plain-http server was contacted")
	}
	if _, err := f.Fetch(context.Background(), "::not a url"); err == nil {
		t.Error("Fetch(invalid url) error = nil, want an error")
	}
	if _, err := f.Fetch(context.Background(), "file:///etc/passwd"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("Fetch(file url) error = %v, want an https-only error", err)
	}
}

func TestFetcher_Fetch_DownscalesKeepingAspectAndNaturalWidth(t *testing.T) {
	tests := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"wide image is scaled to the maximum width", 2400, 1200, MaxWidth, 600},
		{"exactly the maximum is untouched", MaxWidth, 100, MaxWidth, 100},
		{"small image is untouched", 10, 6, 10, 6},
		{"extreme aspect keeps at least one row", 12000, 3, MaxWidth, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, "image/png", encodePNG(t, solid(tc.w, tc.h, color.NRGBA{B: 200, A: 255})))
			img, err := newTLSFetcher(srv).Fetch(context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if b := img.Pixels.Bounds(); b.Min != (image.Point{}) || b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Errorf("Pixels bounds = %v, want %dx%d at origin", b, tc.wantW, tc.wantH)
			}
			if img.NaturalWidth != tc.w {
				t.Errorf("NaturalWidth = %d, want %d", img.NaturalWidth, tc.w)
			}
		})
	}
}

// TestFetcher_Fetch_ErrorsNeverContainTheURL pins that no error mentions the
// request URL or its query: the URL carries a signed token, and the store
// logs these errors.
func TestFetcher_Fetch_ErrorsNeverContainTheURL(t *testing.T) {
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop?jwt=SECRET", http.StatusFound)
		default:
			http.Redirect(w, r, "http://127.0.0.1:1/x.png?jwt=SECRET", http.StatusFound)
		}
	}))
	defer redirecting.Close()
	notFound := httptest.NewTLSServer(http.NotFoundHandler())
	defer notFound.Close()

	tests := []struct {
		name string
		f    *Fetcher
		url  string
	}{
		{"connection refused", newFetcher(&http.Client{}), "https://127.0.0.1:1/x.png?jwt=SECRET"},
		{"request cannot be built", newFetcher(&http.Client{}), "https://exa mple.com/x.png?jwt=SECRET"},
		{"redirect to plain http is refused", newTLSFetcher(redirecting), redirecting.URL + "/start?jwt=SECRET"},
		{"too many redirects", newTLSFetcher(redirecting), redirecting.URL + "/loop?jwt=SECRET"},
		{"bad status", newTLSFetcher(notFound), notFound.URL + "/x.png?jwt=SECRET"},
		{"plain http", newFetcher(&http.Client{}), "http://127.0.0.1:1/x.png?jwt=SECRET"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.f.Fetch(context.Background(), tc.url)
			if err == nil {
				t.Fatal("Fetch() error = nil, want an error")
			}
			for _, leak := range []string{"SECRET", "jwt", "x.png", "127.0.0.1:1/"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q contains %q", err, leak)
				}
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				t.Errorf("error still wraps a *url.Error: %v", err)
			}
		})
	}
}

func TestIsPermanent(t *testing.T) {
	bomb := encodePNG(t, solid(1, 1, color.NRGBA{A: 255}))
	binary.BigEndian.PutUint32(bomb[16:], 5000)
	binary.BigEndian.PutUint32(bomb[20:], 5000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29]))

	notFound := httptest.NewTLSServer(http.NotFoundHandler())
	defer notFound.Close()

	permanent := map[string]*httptest.Server{
		"not an image":    serve(t, "text/html", []byte("<html>")),
		"svg":             serve(t, "image/svg+xml", []byte("<svg/>")),
		"corrupt":         serve(t, "image/png", []byte("not a png")),
		"too many pixels": serve(t, "image/png", bomb),
	}
	for name, srv := range permanent {
		t.Run(name, func(t *testing.T) {
			_, err := newTLSFetcher(srv).Fetch(context.Background(), srv.URL)
			if err == nil || !IsPermanent(err) {
				t.Errorf("Fetch() error = %v, want a permanent error", err)
			}
		})
	}
	t.Run("over the size limit", func(t *testing.T) {
		srv := serve(t, "image/png", encodePNG(t, solid(40, 40, color.NRGBA{A: 255})))
		f := newTLSFetcher(srv)
		f.maxBytes = 16
		if _, err := f.Fetch(context.Background(), srv.URL); err == nil || !IsPermanent(err) {
			t.Errorf("Fetch() error = %v, want a permanent error", err)
		}
	})

	t.Run("http status is transient", func(t *testing.T) {
		_, err := newTLSFetcher(notFound).Fetch(context.Background(), notFound.URL)
		if err == nil || IsPermanent(err) {
			t.Errorf("Fetch() error = %v, want a transient error", err)
		}
	})
	t.Run("network error is transient", func(t *testing.T) {
		_, err := newFetcher(&http.Client{}).Fetch(context.Background(), "https://127.0.0.1:1/x.png")
		if err == nil || IsPermanent(err) {
			t.Errorf("Fetch() error = %v, want a transient error", err)
		}
	})
}

func TestCheckConfig_PixelLimit(t *testing.T) {
	const side = 4000 // 16 MP exactly
	tests := []struct {
		name    string
		w, h    int
		wantErr bool
	}{
		{"exactly the limit", side, side, false},
		{"just above the limit", side + 1, side, true},
		{"huge", 100000, 100000, true},
		{"overflowing product", 1 << 40, 1 << 40, true},
		{"zero width", 0, 10, true},
		{"one pixel", 1, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkConfig(image.Config{Width: tc.w, Height: tc.h})
			if (err != nil) != tc.wantErr {
				t.Errorf("checkConfig(%dx%d) error = %v, wantErr %v", tc.w, tc.h, err, tc.wantErr)
			}
			if err != nil && !IsPermanent(err) {
				t.Errorf("checkConfig error %v is not permanent", err)
			}
		})
	}
}

// TestDecode_DoesNotAllocateAFullSizeCopy guards against converting a large
// decoded image into a second full-size NRGBA before scaling it down: for a
// 12 MP image that doubles the transient memory.
func TestDecode_DoesNotAllocateAFullSizeCopy(t *testing.T) {
	const w, h = 4000, 3000
	data := encodePNG(t, solid(w, h, color.NRGBA{R: 9, G: 8, B: 7, A: 255}))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	img, err := decode(data)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if img.NaturalWidth != w || img.Pixels.Bounds().Dx() != MaxWidth {
		t.Fatalf("decoded image = %v natural %d", img.Pixels.Bounds(), img.NaturalWidth)
	}
	decoded := uint64(w * h * 4)
	if got := after.TotalAlloc - before.TotalAlloc; got > decoded+decoded/2 {
		t.Errorf("decode allocated %d MB for a %d MB image, want no second full-size copy", got>>20, decoded>>20)
	}
}
