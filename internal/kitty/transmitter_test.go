package kitty

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/inlineimg"
)

func testImage(w, h, natural int) inlineimg.Image {
	return inlineimg.Image{Pixels: image.NewNRGBA(image.Rect(0, 0, w, h)), NaturalWidth: natural}
}

func TestFit(t *testing.T) {
	cell := CellSize{W: 10, H: 20}
	tests := []struct {
		name               string
		pxW, pxH, natural  int
		cell               CellSize
		maxCols, wantPx    int
		wantCols, wantRows int
	}{
		{"fills the available width", 1000, 500, 1000, cell, 40, 0, 40, 10},
		{"natural width limits a small image", 100, 50, 100, cell, 40, 0, 10, 3},
		{"width attribute limits", 1000, 500, 1000, cell, 40, 200, 20, 5},
		{"width attribute larger than the room", 1000, 500, 1000, cell, 40, 4000, 40, 10},
		{"rows round up", 100, 100, 100, cell, 40, 0, 10, 5},
		{"tall image is capped at 30 rows and shrinks", 100, 1000, 3000, cell, 40, 0, 6, 30},
		{"never zero", 1, 1, 1, cell, 40, 0, 1, 1},
		{"zero room still gives one cell", 100, 100, 100, cell, 0, 0, 1, 1},
		{"fallback cell size 8x16", 800, 400, 800, CellSize{}, 200, 0, 100, 25},
		{"columns are capped at the diacritics table", 5000, 100, 5000, CellSize{W: 1, H: 1}, 1000, 0, 297, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := fit(tc.pxW, tc.pxH, tc.natural, tc.cell, tc.maxCols, tc.wantPx)
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("fit() = %dx%d, want %dx%d", cols, rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}

func decodeTransmitted(t *testing.T, seq string) image.Config {
	t.Helper()
	var payload strings.Builder
	for _, part := range strings.Split(strings.TrimSuffix(seq, "\x1b\\"), "\x1b\\") {
		_, data, _ := strings.Cut(part, ";")
		payload.WriteString(data)
	}
	raw, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("transmitted payload is not a PNG: %v", err)
	}
	return cfg
}

func TestTransmitter_PlaceAndFlush(t *testing.T) {
	tr := NewTransmitter(false)
	tr.SetCellSize(10, 20)

	img := testImage(400, 200, 400)
	p := tr.Place("https://x/a.png", img, 20, 0)
	if p.ID != firstID || p.Cols != 20 || p.Rows != 5 {
		t.Fatalf("Place() = %+v, want the first id, 20x5", p)
	}
	if again := tr.Place("https://x/a.png", img, 20, 0); again != p {
		t.Errorf("same key placed again = %+v, want %+v", again, p)
	}
	other := tr.Place("https://x/a.png", img, 10, 0)
	if other.ID != firstID+1 || other.Cols != 10 {
		t.Errorf("same src at another size = %+v, want a new id", other)
	}

	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if n := strings.Count(got, "a=T,U=1,f=100,t=d,"); n != 2 {
		t.Fatalf("transmissions = %d, want 2 (once per key)", n)
	}
	if !strings.Contains(got, "i=16,c=20,r=5,q=2") || !strings.Contains(got, "i=17,c=10,r=3,q=2") {
		t.Errorf("transmit headers missing: %.200q", got)
	}

	out.Reset()
	if err := tr.Flush(&out); err != nil || out.Len() != 0 {
		t.Errorf("second Flush wrote %d bytes (err %v), want nothing", out.Len(), err)
	}
}

func TestTransmitter_DownscalesPNGToPlacementWidth(t *testing.T) {
	tr := NewTransmitter(false)
	tr.SetCellSize(10, 20)

	// 20 columns of 10 px is 200 px, so a 1000 px wide image is sent at 200.
	tr.Place("big", testImage(1000, 500, 1000), 20, 0)
	// A small stored image is never upscaled.
	tr.Place("small", testImage(50, 25, 1000), 20, 0)

	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitAfter(out.String(), "\x1b\\")
	if len(parts) != 3 { // two single-chunk transmissions and the empty tail
		t.Fatalf("expected two transmissions, got %q", out.String())
	}
	if cfg := decodeTransmitted(t, parts[0]); cfg.Width != 200 || cfg.Height != 100 {
		t.Errorf("big PNG = %dx%d, want 200x100", cfg.Width, cfg.Height)
	}
	if cfg := decodeTransmitted(t, parts[1]); cfg.Width != 50 || cfg.Height != 25 {
		t.Errorf("small PNG = %dx%d, want 50x25", cfg.Width, cfg.Height)
	}
}

func TestTransmitter_FallbackCellSize(t *testing.T) {
	tr := NewTransmitter(false)
	tr.SetCellSize(0, 0) // tmux often cannot report pixel sizes
	p := tr.Place("a", testImage(800, 400, 800), 200, 0)
	if p.Cols != 100 || p.Rows != 25 {
		t.Errorf("Place() with fallback cell = %+v, want 100x25 (8x16 cells)", p)
	}
}

func TestTransmitter_ReusesIDsLeastRecentlyUsedFirstAndDeletesTheOldImage(t *testing.T) {
	tr := NewTransmitter(false)
	img := testImage(10, 10, 10)
	first := tr.Place("k0", img, 1, 0)
	for i := 1; i < maxIDs; i++ {
		tr.Place(keyName(i), img, 1, 0)
	}
	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "a=d") {
		t.Fatal("a delete was sent before any id was reused")
	}

	tr.Place("k0", img, 1, 0) // touch k0, so k1 is now the oldest
	p := tr.Place("brand-new", img, 1, 0)
	if p.ID != firstID+1 {
		t.Errorf("reused id = %d, want 17 (k1's, the least recently used); k0 had %d", p.ID, first.ID)
	}
	out.Reset()
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	del := strings.Index(got, "a=d,d=I,i=17,q=2")
	send := strings.Index(got, "a=T,U=1,f=100,t=d,i=17,")
	if del < 0 || send < 0 || del > send {
		t.Errorf("want the delete of id 17 before its new transmission, got %.300q", got)
	}
	if st := tr.Place("k0", img, 1, 0); st.ID != first.ID {
		t.Errorf("k0 id = %d, want it kept (%d)", st.ID, first.ID)
	}
}

func keyName(i int) string { return "k" + string(rune('A'+i/26)) + string(rune('a'+i%26)) + "x" }

func TestTransmitter_Tmux(t *testing.T) {
	tr := NewTransmitter(true)
	tr.Place("a", testImage(10, 10, 10), 1, 0)
	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "\x1bPtmux;\x1b\x1b_G") {
		t.Errorf("output is not tmux-wrapped: %.40q", out.String())
	}
}

func TestTransmitter_CleanupDeletesEveryLiveImage(t *testing.T) {
	tr := NewTransmitter(false)
	tr.Place("a", testImage(10, 10, 10), 1, 0)
	tr.Place("b", testImage(10, 10, 10), 1, 0)
	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil {
		t.Fatal(err)
	}
	out.Reset()

	if err := tr.Cleanup(&out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "\x1b_Ga=d,d=I,i=16,q=2\x1b\\\x1b_Ga=d,d=I,i=17,q=2\x1b\\"; got != want {
		t.Errorf("Cleanup wrote %q, want %q", got, want)
	}
	out.Reset()
	if err := tr.Cleanup(&out); err != nil || out.Len() != 0 {
		t.Errorf("second Cleanup wrote %q (err %v), want nothing", out.String(), err)
	}
}

func TestTransmitter_FlushErrorDropsPending(t *testing.T) {
	tr := NewTransmitter(false)
	tr.Place("a", testImage(10, 10, 10), 1, 0)
	if err := tr.Flush(errWriter{}); err == nil {
		t.Error("Flush() error = nil, want the write error")
	}
	var out bytes.Buffer
	if err := tr.Flush(&out); err != nil || out.Len() != 0 {
		t.Errorf("Flush() after a failure wrote %d bytes (err %v), want nothing queued", out.Len(), err)
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("tty gone") }
