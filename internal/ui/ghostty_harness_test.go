//go:build ghostty

package ui

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	// Registers the xterm-ghostty entry, which tcell's default database lacks.
	_ "github.com/gdamore/tcell/v2/terminfo/x/xterm_ghostty"
	"go.mitchellh.com/libghostty"
	syspng "go.mitchellh.com/libghostty/sys/png"
)

const (
	ghosttyCols = 100
	ghosttyRows = 30
	// The terminal reports 10x20 pixel cells, so images are sized from them
	// rather than from gprt's fallback.
	ghosttyCellW = 10
	ghosttyCellH = 20
)

// placeholderRune is the first rune of every kitty placeholder cell.
const placeholderRune = '\U0010EEEE'

var (
	placeholderUTF8 = []byte(string(placeholderRune))
	// continuationChunk starts every kitty escape sequence after a chunked
	// image's first.
	continuationChunk = []byte("\x1b_Gm=")
)

// pngDecoderOnce installs the PNG decoder the kitty graphics protocol needs;
// libghostty keeps it process-wide and it must be set before any terminal
// processes image data. pngDecoderErr keeps a failure visible to every test,
// not only the one that ran the Once.
var (
	pngDecoderOnce sync.Once
	pngDecoderErr  error
)

// vtTerm is a headless libghostty terminal. libghostty is not thread-safe,
// so every call into it goes through mu.
type vtTerm struct {
	mu   sync.Mutex
	term *libghostty.Terminal
	// continuationChunks counts the continuation sequences written, for
	// checks on what was sent (not what the terminal made of it).
	continuationChunks int
	// beforePlaceholder records, for every write that contains placeholder
	// cells, the ids of the images placed just before the terminal processed
	// it. beforePlaceholderErr is the last failure to read them.
	beforePlaceholder    [][]uint32
	beforePlaceholderErr error
}

// newVTTerm creates a terminal of ghosttyCols x ghosttyRows whose replies to
// queries (device attributes, kitty keyboard flags, ...) are handed to reply.
// reply runs inside VTWrite, i.e. with mu held, so it must not call back into
// the vtTerm.
func newVTTerm(t *testing.T, reply func([]byte)) *vtTerm {
	t.Helper()
	pngDecoderOnce.Do(func() { pngDecoderErr = libghostty.SysSetDecodePng(syspng.Decode) })
	if pngDecoderErr != nil {
		t.Fatalf("SysSetDecodePng: %v", pngDecoderErr)
	}
	term, err := libghostty.NewTerminal(
		libghostty.WithSize(ghosttyCols, ghosttyRows),
		libghostty.WithWritePty(func(_ *libghostty.Terminal, data []byte) { reply(data) }),
	)
	if err != nil {
		t.Fatalf("libghostty.NewTerminal: %v", err)
	}
	if err := term.Resize(ghosttyCols, ghosttyRows, ghosttyCellW, ghosttyCellH); err != nil {
		term.Close()
		t.Fatalf("Terminal.Resize: %v", err)
	}
	vt := &vtTerm{term: term}
	t.Cleanup(func() {
		vt.mu.Lock()
		defer vt.mu.Unlock()
		vt.term.Close()
		vt.term = nil
	})
	return vt
}

// write feeds bytes that the application printed into the terminal. It drops
// them once the terminal is freed, which only happens first when app.Run
// failed to stop in time: a cgo use-after-free would crash the test binary and
// hide that failure.
func (v *vtTerm) write(p []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.term == nil {
		return
	}
	v.continuationChunks += bytes.Count(p, continuationChunk)
	if bytes.Contains(p, placeholderUTF8) {
		ids, err := v.placedImageIDsLocked()
		if err != nil {
			v.beforePlaceholderErr = err
		}
		v.beforePlaceholder = append(v.beforePlaceholder, ids)
	}
	v.term.VTWrite(p)
}

// placedImageIDsLocked lists the image id of every placement in the terminal.
// The caller holds mu.
func (v *vtTerm) placedImageIDsLocked() ([]uint32, error) {
	kg, err := v.term.KittyGraphics()
	if err != nil {
		return nil, err
	}
	iter, err := libghostty.NewKittyGraphicsPlacementIterator()
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	if err := kg.PlacementIterator(iter); err != nil {
		return nil, err
	}
	var ids []uint32
	for iter.Next() {
		info, err := iter.Info()
		if err != nil {
			return nil, err
		}
		ids = append(ids, info.ImageID)
	}
	return ids, nil
}

// text returns the visible screen as plain text.
func (v *vtTerm) text() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	f, err := libghostty.NewFormatter(v.term, libghostty.WithFormatterFormat(libghostty.FormatterFormatPlain))
	if err != nil {
		return "formatter error: " + err.Error()
	}
	defer f.Close()
	s, err := f.FormatString()
	if err != nil {
		return "format error: " + err.Error()
	}
	return s
}

// ghosttyTty is a tcell.Tty wired to a vtTerm: what tcell writes goes to the
// terminal, and what the terminal replies is what tcell reads.
type ghosttyTty struct {
	vt *vtTerm

	mu   sync.Mutex
	cond *sync.Cond
	in   []byte
	wake bool // set by Drain/Stop/Close: Read must return instead of blocking
}

func newGhosttyTty() *ghosttyTty {
	g := &ghosttyTty{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// push queues bytes for tcell to read.
func (g *ghosttyTty) push(p []byte) {
	g.mu.Lock()
	g.in = append(g.in, p...)
	g.mu.Unlock()
	g.cond.Broadcast()
}

func (g *ghosttyTty) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.wake = false
	return nil
}

func (g *ghosttyTty) release() {
	g.mu.Lock()
	g.wake = true
	g.mu.Unlock()
	g.cond.Broadcast()
}

func (g *ghosttyTty) Stop() error  { g.release(); return nil }
func (g *ghosttyTty) Drain() error { g.release(); return nil }
func (g *ghosttyTty) Close() error { g.release(); return nil }

func (g *ghosttyTty) NotifyResize(func()) {}

func (g *ghosttyTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{
		Width: ghosttyCols, Height: ghosttyRows,
		PixelWidth: ghosttyCols * ghosttyCellW, PixelHeight: ghosttyRows * ghosttyCellH,
	}, nil
}

func (g *ghosttyTty) Write(p []byte) (int, error) {
	g.vt.write(p)
	return len(p), nil
}

// Read blocks until input is queued. Once released (Drain/Stop/Close) with
// nothing queued it returns io.EOF, which lets tcell's input loop exit
// because it is no longer running.
func (g *ghosttyTty) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for len(g.in) == 0 && !g.wake {
		g.cond.Wait()
	}
	if len(g.in) == 0 {
		return 0, io.EOF
	}
	n := copy(p, g.in)
	g.in = g.in[n:]
	return n, nil
}

// press encodes one key press the way a terminal would and queues it for
// tcell. With legacy false the encoder follows the terminal's current modes
// (including the kitty keyboard flags tcell enabled); with legacy true it is
// a fresh encoder with no flags, as on a terminal that never negotiated CSI-u.
// text is the character the key produces ("" for Ctrl combinations and
// special keys); unshifted is the key's unshifted codepoint (0 for none).
func (v *vtTerm) press(t *testing.T, legacy bool, key libghostty.Key, mods libghostty.Mods, text string, unshifted rune) []byte {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()

	enc, err := libghostty.NewKeyEncoder()
	if err != nil {
		t.Fatalf("NewKeyEncoder: %v", err)
	}
	defer enc.Close()
	if !legacy {
		if err := enc.SetFromTerminal(v.term); err != nil {
			t.Fatalf("SetFromTerminal: %v", err)
		}
	}
	ev, err := libghostty.NewKeyEvent()
	if err != nil {
		t.Fatalf("NewKeyEvent: %v", err)
	}
	defer ev.Close()
	for _, err := range []error{
		ev.SetAction(libghostty.KeyActionPress),
		ev.SetKey(key),
		ev.SetMods(mods),
		ev.SetUTF8(text),
		ev.SetUnshiftedCodepoint(unshifted),
	} {
		if err != nil {
			t.Fatalf("building key event: %v", err)
		}
	}
	out, err := enc.Encode(ev)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return out
}

// ghosttyApp is an App running on a real tcell screen over a libghostty
// terminal.
type ghosttyApp struct {
	app  *App
	fake *fakeGitHub
	vt   *vtTerm
	tty  *ghosttyTty
}

// newGhosttyApp starts the standard test App on a real terminfo screen
// (xterm-ghostty) attached to a headless libghostty terminal.
//
// opts tunes the App (images, getenv, config); setup, when non-nil, runs after
// the App is built and before it starts, to adjust its fake GitHub.
func newGhosttyApp(t *testing.T, opts testAppOpts, setup func(*fakeGitHub)) *ghosttyApp {
	t.Helper()
	t.Setenv("LC_ALL", "en_US.UTF-8") // tcell reads it before LC_CTYPE and LANG

	app, fake := buildTestApp(t, nil, opts)
	if setup != nil {
		setup(fake)
	}

	tty := newGhosttyTty()
	// Registered before startTestApp's cleanup, so (LIFO) the terminal is
	// freed only after app.Run has returned and tcell stopped writing.
	vt := newVTTerm(t, tty.push)
	tty.vt = vt

	ti, err := tcell.LookupTerminfo("xterm-ghostty")
	if err != nil {
		t.Fatalf("LookupTerminfo: %v", err)
	}
	screen, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		t.Fatalf("NewTerminfoScreenFromTtyTerminfo: %v", err)
	}
	// SetScreen runs screen.Init itself.
	app.app.SetScreen(screen)

	startTestApp(t, app)
	// startTestApp moves the cursor without a redraw, which the simulation
	// tests never see but the terminal grid does.
	act(app.app, func() { app.app.ForceDraw() })
	return &ghosttyApp{app: app, fake: fake, vt: vt, tty: tty}
}

// Text returns the terminal's visible screen as plain text.
func (g *ghosttyApp) Text() string { return g.vt.text() }

// Eventually polls the screen text until cond holds, failing the test with the
// last screen after five seconds.
func (g *ghosttyApp) Eventually(t *testing.T, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := g.Text()
		if cond(s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met; last screen:\n%s", strings.TrimRight(s, "\n "))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Press sends a key through the encoder (see vtTerm.press) into tcell's input
// and returns the bytes that were sent. After a legacy lone ESC, which tcell
// holds briefly as a possible sequence start, callers must wait for its effect
// before sending another key or tcell reads the pair as Alt-<key>.
func (g *ghosttyApp) Press(t *testing.T, legacy bool, key libghostty.Key, mods libghostty.Mods, text string, unshifted rune) []byte {
	t.Helper()
	out := g.vt.press(t, legacy, key, mods, text, unshifted)
	g.tty.push(out)
	return out
}

// Type sends each lowercase ASCII letter of s as a plain character key press.
func (g *ghosttyApp) Type(t *testing.T, legacy bool, s string) {
	t.Helper()
	for _, r := range s {
		key, err := libghostty.ParseKey("key_" + string(r))
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", r, err)
		}
		g.Press(t, legacy, key, 0, string(r), r)
	}
}

// cell returns the codepoint and width class of the viewport cell at (x, y);
// the codepoint is 0 for an empty cell.
func (v *vtTerm) cell(t *testing.T, x uint16, y uint32) (rune, libghostty.CellWide) {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	ref, err := v.term.GridRef(libghostty.Point{Tag: libghostty.PointTagViewport, X: x, Y: y})
	if err != nil {
		t.Fatalf("GridRef(%d,%d): %v", x, y, err)
	}
	c, err := ref.Cell()
	if err != nil {
		t.Fatalf("Cell(%d,%d): %v", x, y, err)
	}
	cp, err := c.Codepoint()
	if err != nil {
		t.Fatalf("Codepoint(%d,%d): %v", x, y, err)
	}
	w, err := c.Wide()
	if err != nil {
		t.Fatalf("Wide(%d,%d): %v", x, y, err)
	}
	return rune(cp), w
}

// KittyFlags reports the terminal's current kitty keyboard flags.
func (g *ghosttyApp) KittyFlags(t *testing.T) libghostty.KittyKeyFlags {
	t.Helper()
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	f, err := g.vt.term.KittyKeyboardFlags()
	if err != nil {
		t.Fatalf("KittyKeyboardFlags: %v", err)
	}
	return f
}

// kittyPlacement is one placement in the terminal's kitty graphics storage,
// with the size of the image it shows.
type kittyPlacement struct {
	ImageID                 uint32
	Virtual                 bool
	Cols, Rows              uint32
	ImageWidth, ImageHeight uint32
}

// KittyPlacements lists the placements in the terminal's kitty graphics
// storage.
func (g *ghosttyApp) KittyPlacements(t *testing.T) []kittyPlacement {
	t.Helper()
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	kg, err := g.vt.term.KittyGraphics()
	if err != nil {
		t.Fatalf("KittyGraphics: %v", err)
	}
	iter, err := libghostty.NewKittyGraphicsPlacementIterator()
	if err != nil {
		t.Fatalf("NewKittyGraphicsPlacementIterator: %v", err)
	}
	defer iter.Close()
	if err := kg.PlacementIterator(iter); err != nil {
		t.Fatalf("PlacementIterator: %v", err)
	}
	var out []kittyPlacement
	for iter.Next() {
		info, err := iter.Info()
		if err != nil {
			t.Fatalf("placement Info: %v", err)
		}
		p := kittyPlacement{ImageID: info.ImageID, Virtual: info.IsVirtual, Cols: info.Columns, Rows: info.Rows}
		if img := kg.Image(info.ImageID); img != nil {
			ii, err := img.Info()
			if err != nil {
				t.Fatalf("image Info: %v", err)
			}
			p.ImageWidth, p.ImageHeight = ii.Width, ii.Height
		}
		out = append(out, p)
	}
	return out
}

// PlaceholderForegrounds returns, for every placeholder cell on screen, the
// 256-colour palette index of its foreground: the image id gprt encoded.
// A cell whose foreground is not a palette colour yields -1.
func (g *ghosttyApp) PlaceholderForegrounds(t *testing.T) []int {
	t.Helper()
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	var out []int
	for y := uint32(0); y < ghosttyRows; y++ {
		for x := uint16(0); x < ghosttyCols; x++ {
			ref, err := g.vt.term.GridRef(libghostty.Point{Tag: libghostty.PointTagViewport, X: x, Y: y})
			if err != nil {
				t.Fatalf("GridRef(%d,%d): %v", x, y, err)
			}
			gr, err := ref.Graphemes()
			if err != nil {
				t.Fatalf("Graphemes(%d,%d): %v", x, y, err)
			}
			if len(gr) == 0 || gr[0] != placeholderRune {
				continue
			}
			style, err := ref.Style()
			if err != nil {
				t.Fatalf("Style(%d,%d): %v", x, y, err)
			}
			fg := style.FgColor()
			if fg.Tag != libghostty.StyleColorPalette {
				out = append(out, -1)
				continue
			}
			out = append(out, int(fg.Palette))
		}
	}
	return out
}

// ContinuationChunks returns how many kitty continuation sequences the
// application has written to the terminal.
func (g *ghosttyApp) ContinuationChunks() int {
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	return g.vt.continuationChunks
}

// ImageIDsBeforePlaceholderWrites returns, for each write that carried
// placeholder cells, the ids of the images placed in the terminal just before
// it.
func (g *ghosttyApp) ImageIDsBeforePlaceholderWrites(t *testing.T) [][]uint32 {
	t.Helper()
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	if g.vt.beforePlaceholderErr != nil {
		t.Fatalf("reading placements before a write: %v", g.vt.beforePlaceholderErr)
	}
	return slices.Clone(g.vt.beforePlaceholder)
}

// GraphemeClusterMode reports whether the terminal has mode 2027 (grapheme
// clusters) on, which decides how it measures emoji sequences.
func (g *ghosttyApp) GraphemeClusterMode(t *testing.T) bool {
	t.Helper()
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	on, err := g.vt.term.Mode(libghostty.ModeGraphemeCluster)
	if err != nil {
		t.Fatalf("Mode(2027): %v", err)
	}
	return on
}

// ActiveScreen reports which screen buffer (primary or alternate) is showing.
// It returns the error instead of failing the test, so it can be used on the
// UI goroutine.
func (g *ghosttyApp) ActiveScreen() (libghostty.TerminalScreen, error) {
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	return g.vt.term.ActiveScreen()
}

// KittyFlagsErr is KittyFlags for the UI goroutine: it returns the error.
func (g *ghosttyApp) KittyFlagsErr() (libghostty.KittyKeyFlags, error) {
	g.vt.mu.Lock()
	defer g.vt.mu.Unlock()
	return g.vt.term.KittyKeyboardFlags()
}

// Scribble writes bytes straight into the terminal, as a program other than
// gprt (an external editor) would while gprt is suspended.
func (g *ghosttyApp) Scribble(s string) { g.vt.write([]byte(s)) }
