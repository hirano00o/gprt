package ui

import (
	"log/slog"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/inlineimg"
	"github.com/hirano00o/gprt/internal/kitty"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// imageStore is the part of *store.Store the image backend uses.
type imageStore interface {
	Image(subjectID, src string) store.ImageState
}

// imageSources supplies, for the pull request, comment, or review with the
// given node ID, the widget.ImageSource its text's images are drawn from.
// A nil imageSources, or one returning nil, leaves images as text
// placeholders.
type imageSources func(subjectID string) widget.ImageSource

func (f imageSources) of(subjectID string) widget.ImageSource {
	if f == nil {
		return nil
	}
	return f(subjectID)
}

// imageBackend draws images with the kitty graphics protocol: it adapts the
// store's per-subject image state to widget.ImageSource, places decoded
// images with a kitty.Transmitter, and writes the transmissions to the
// terminal once per frame.
//
// Everything runs on the UI goroutine. The transmitter queues sequences while
// blocks build, which happens inside tview's draw; they are written from
// tview's after-draw hook (see flush) and removed when the program quits
// (see cleanup).
type imageBackend struct {
	store  imageStore
	tx     *kitty.Transmitter
	logger *slog.Logger

	// wanted is the configuration's verdict (image_protocol, resolved
	// against the environment); enabled additionally requires that the
	// screen has a terminal to write to and at least 256 colours, and is only
	// known once the first frame starts (see beforeDraw).
	wanted  bool
	enabled bool
	screen  tcell.Screen
}

// newImageBackend resolves protocol ("auto", "kitty", "none", or "" for
// auto) against the environment read through getenv.
func newImageBackend(st imageStore, protocol string, getenv func(string) string, logger *slog.Logger) *imageBackend {
	wanted := protocol == "kitty" || ((protocol == "auto" || protocol == "") && kitty.Detect(getenv))
	return &imageBackend{
		store:  st,
		tx:     kitty.NewTransmitter(kitty.InTmux(getenv)),
		logger: logger,
		wanted: wanted,
	}
}

// sources returns the imageSources for blocks to render with.
func (b *imageBackend) sources() imageSources {
	return func(subjectID string) widget.ImageSource {
		return imageSubject{backend: b, id: subjectID}
	}
}

// imageSubject is the widget.ImageSource of one text.
type imageSubject struct {
	backend *imageBackend
	id      string
}

// Image implements widget.ImageSource.
func (s imageSubject) Image(src string, maxCols, widthPx int) widget.ImageResult {
	b := s.backend
	if !b.enabled {
		return widget.ImageResult{}
	}
	switch st := b.store.Image(s.id, src); st.Status {
	case store.ImageLoading:
		return widget.ImageResult{Status: widget.ImageLoading}
	case store.ImageLoaded:
		return b.place(src, st.Image, maxCols, widthPx)
	default:
		return widget.ImageResult{}
	}
}

func (b *imageBackend) place(src string, img inlineimg.Image, maxCols, widthPx int) widget.ImageResult {
	p := b.tx.Place(src, img, maxCols, widthPx)
	return widget.ImageResult{Status: widget.ImageReady, ID: p.ID, Cols: p.Cols, Rows: p.Rows}
}

// beforeDraw runs at the start of every frame: it records the screen and
// decides whether images can be shown at all (they need the terminal itself;
// a simulation screen has none), and refreshes the cell size, which changes
// with the font size.
func (b *imageBackend) beforeDraw(screen tcell.Screen) {
	b.screen = screen
	tty, ok := screen.Tty()
	// The id travels as a 256-colour palette index in the cells' foreground,
	// which tcell drops under NO_COLOR and remaps on terminals with fewer
	// colours (TERM=screen/xterm inside tmux, whatever KITTY_WINDOW_ID says);
	// the cells would then show nothing or another image.
	b.enabled = b.wanted && ok && screen.Colors() >= 256
	if !b.enabled {
		return
	}
	cw, ch := 0, 0
	if ws, err := tty.WindowSize(); err == nil && ws.Width > 0 && ws.Height > 0 {
		cw, ch = ws.PixelWidth/ws.Width, ws.PixelHeight/ws.Height
	}
	b.tx.SetCellSize(cw, ch)
}

// flush writes the transmissions queued during the frame.
//
// tview's draw calls the after-draw hook after the whole tree has drawn (so
// every block has been built and has queued its images) and before it calls
// screen.Show, which is the only point where tcell writes cell updates to the
// terminal. Writing here therefore neither interleaves with tcell's own
// output, nor can the placeholder cells reach the terminal ahead of the image
// they refer to. Both run on the UI goroutine under tview's application lock.
func (b *imageBackend) flush(screen tcell.Screen) {
	if !b.enabled {
		return
	}
	tty, ok := screen.Tty()
	if !ok {
		return
	}
	if err := b.tx.Flush(tty); err != nil {
		b.logger.Warn("image transmission failed", "err", err)
	}
}

// cleanup deletes every image transmitted. It must run before the screen is
// finalised, while the terminal is still open for writing.
func (b *imageBackend) cleanup() {
	if !b.enabled || b.screen == nil {
		return
	}
	tty, ok := b.screen.Tty()
	if !ok {
		return
	}
	if err := b.tx.Cleanup(tty); err != nil {
		b.logger.Warn("image cleanup failed", "err", err)
	}
}
