package kitty

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"

	"golang.org/x/image/draw"

	"github.com/hirano00o/gprt/internal/inlineimg"
)

const (
	// firstID and maxIDs bound the image ids. The id is carried by the
	// cells' foreground colour as a 256-colour palette index, which survives
	// tmux where a 24-bit colour might not. Indices 0-15 are skipped: tcell
	// and tmux emit them as the basic SGR 30-37/90-97 colours, which tmux may
	// remap, so only 16-255 reach the terminal as an unambiguous index.
	firstID = 16
	maxIDs  = 256 - firstID

	// MaxRows caps a placement's height so one image never fills the pane.
	MaxRows = 30

	// Pixel size of a cell when the terminal reports none, which is common
	// inside tmux.
	fallbackCellW, fallbackCellH = 8, 16
)

// CellSize is the size of one terminal cell in pixels. A zero value means
// unknown.
type CellSize struct{ W, H int }

func (c CellSize) orFallback() CellSize {
	if c.W <= 0 || c.H <= 0 {
		return CellSize{W: fallbackCellW, H: fallbackCellH}
	}
	return c
}

// Placement is where and how big a transmitted image is drawn: the cells
// carry ID in their foreground colour, and span Cols x Rows.
type Placement struct {
	ID, Cols, Rows int
}

// fit chooses the placement size in cells for an image whose stored pixels
// are pxW x pxH and whose original width was natural pixels. Columns are the
// least of the room (maxCols), the natural size, the requested width in
// pixels (wantPx, 0 for none), and the diacritics table; rows follow from
// the aspect ratio, and when they exceed MaxRows the columns shrink to
// match. Both are at least 1.
func fit(pxW, pxH, natural int, cell CellSize, maxCols, wantPx int) (cols, rows int) {
	cell = cell.orFallback()
	pxW, pxH = max(pxW, 1), max(pxH, 1)
	cols = min(maxCols, natural/cell.W, MaxCells)
	if wantPx > 0 {
		cols = min(cols, wantPx/cell.W)
	}
	cols = max(cols, 1)

	rowsFor := func(cols int) int {
		return max(1, (cols*cell.W*pxH+pxW*cell.H-1)/(pxW*cell.H))
	}
	rows = rowsFor(cols)
	if rows > MaxRows {
		cols = max(1, min(cols, MaxRows*cell.H*pxW/(pxH*cell.W)))
		rows = min(rowsFor(cols), MaxRows)
	}
	return cols, rows
}

// placementKey identifies a transmitted image: the same picture at another
// size is another image, transmitted at that size.
type placementKey struct {
	src        string
	cols, rows int
}

// Transmitter allocates image ids, encodes images, and queues the escape
// sequences that transmit them. It writes nothing itself: the owner calls
// Flush at a moment when nothing else is writing to the terminal.
//
// A Transmitter is owned by the UI goroutine and is not safe for concurrent
// use.
type Transmitter struct {
	tmux bool
	cell CellSize

	ids   map[placementKey]int
	order []placementKey // least recently used first
	// pending holds sequences queued by Place, in order; a delete of a
	// reused id always precedes the transmission that replaces it.
	pending []string
}

// NewTransmitter returns a Transmitter; tmux wraps every sequence as a tmux
// passthrough (see InTmux).
func NewTransmitter(tmux bool) *Transmitter {
	return &Transmitter{tmux: tmux, ids: map[placementKey]int{}}
}

// SetCellSize records the terminal's cell size in pixels, 0 when unknown.
func (t *Transmitter) SetCellSize(w, h int) {
	t.cell = CellSize{W: w, H: h}
}

// Place returns the placement for src drawn within maxCols columns (and, if
// wantPx is positive, no wider than that many pixels), queueing the
// transmission of the image the first time this size is seen. The least
// recently placed image gives up its id when all ids are in use.
//
// Cells already on screen keep referring to an id by number, so a cell built
// before its id was reused would show the newer image; that takes more than
// maxIDs distinct images alive at once.
func (t *Transmitter) Place(src string, img inlineimg.Image, maxCols, wantPx int) Placement {
	b := img.Pixels.Bounds()
	cols, rows := fit(b.Dx(), b.Dy(), img.NaturalWidth, t.cell, maxCols, wantPx)
	key := placementKey{src: src, cols: cols, rows: rows}
	if id, ok := t.ids[key]; ok {
		t.touch(key)
		return Placement{ID: id, Cols: cols, Rows: rows}
	}

	id := firstID + len(t.order)
	if len(t.order) >= maxIDs {
		oldest := t.order[0]
		id = t.ids[oldest]
		delete(t.ids, oldest)
		t.order = t.order[1:]
		t.pending = append(t.pending, deleteSequence(id, t.tmux))
	}
	t.ids[key] = id
	t.order = append(t.order, key)

	if data, err := encodePNG(img.Pixels, cols*t.cell.orFallback().W); err == nil {
		t.pending = append(t.pending, transmitSequence(id, cols, rows, data, t.tmux))
	}
	return Placement{ID: id, Cols: cols, Rows: rows}
}

func (t *Transmitter) touch(key placementKey) {
	for i, k := range t.order {
		if k == key {
			t.order = append(append(t.order[:i:i], t.order[i+1:]...), key)
			return
		}
	}
}

// Flush writes the queued sequences to w. They are dropped even when the
// write fails: a terminal that cannot take them now will not later, and
// retrying every frame would only repeat the error.
func (t *Transmitter) Flush(w io.Writer) error {
	if len(t.pending) == 0 {
		return nil
	}
	data := strings.Join(t.pending, "")
	t.pending = nil
	if _, err := io.WriteString(w, data); err != nil {
		return fmt.Errorf("kitty: write images: %w", err)
	}
	return nil
}

// Cleanup writes the deletion of every image this Transmitter transmitted.
// Call it before the terminal is restored, so no image outlives the program.
func (t *Transmitter) Cleanup(w io.Writer) error {
	var b strings.Builder
	for _, key := range t.order {
		b.WriteString(deleteSequence(t.ids[key], t.tmux))
	}
	t.order, t.ids, t.pending = nil, map[placementKey]int{}, nil
	if b.Len() == 0 {
		return nil
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("kitty: delete images: %w", err)
	}
	return nil
}

// encodePNG encodes pixels as a PNG no wider than maxWidth pixels (never
// enlarging it).
func encodePNG(pixels *image.NRGBA, maxWidth int) ([]byte, error) {
	var src image.Image = pixels
	if b := pixels.Bounds(); b.Dx() > maxWidth && maxWidth > 0 {
		dst := image.NewNRGBA(image.Rect(0, 0, maxWidth, max(1, b.Dy()*maxWidth/b.Dx())))
		draw.BiLinear.Scale(dst, dst.Bounds(), pixels, b, draw.Src, nil)
		src = dst
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, src); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
