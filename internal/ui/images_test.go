package ui

import (
	"bytes"
	"context"
	"image"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/inlineimg"
	"github.com/hirano00o/gprt/internal/kitty"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// fakeTty is a terminal that records what is written to it.
type fakeTty struct {
	mu  sync.Mutex
	buf bytes.Buffer
	ws  tcell.WindowSize
}

func (f *fakeTty) Start() error                          { return nil }
func (f *fakeTty) Stop() error                           { return nil }
func (f *fakeTty) Drain() error                          { return nil }
func (f *fakeTty) NotifyResize(func())                   {}
func (f *fakeTty) WindowSize() (tcell.WindowSize, error) { return f.ws, nil }
func (f *fakeTty) Read([]byte) (int, error)              { return 0, io.EOF }
func (f *fakeTty) Close() error                          { return nil }

func (f *fakeTty) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (f *fakeTty) output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// ttyScreen is a simulation screen that, like a real terminal, has a Tty.
// colors, when positive, overrides the number of colours it reports (the
// simulation screen always reports 256).
type ttyScreen struct {
	tcell.SimulationScreen
	tty    tcell.Tty
	colors int
}

func (s ttyScreen) Tty() (tcell.Tty, bool) { return s.tty, true }

func (s ttyScreen) Colors() int {
	if s.colors > 0 {
		return s.colors
	}
	return s.SimulationScreen.Colors()
}

func newFakeTty() *fakeTty {
	// 100x30 cells in 1000x600 pixels: 10x20 pixel cells.
	return &fakeTty{ws: tcell.WindowSize{Width: 100, Height: 30, PixelWidth: 1000, PixelHeight: 600}}
}

func kittyEnv(k string) string {
	if k == "KITTY_WINDOW_ID" {
		return "1"
	}
	return ""
}

func newTestScreen(t *testing.T, tty tcell.Tty) tcell.Screen {
	t.Helper()
	return newTestScreenColors(t, tty, 0)
}

func newTestScreenColors(t *testing.T, tty tcell.Tty, colors int) tcell.Screen {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Fini)
	sim.SetSize(100, 30)
	if tty == nil {
		return sim
	}
	return ttyScreen{SimulationScreen: sim, tty: tty, colors: colors}
}

// fakeImageStore answers Image from a table keyed by "subject|src".
type fakeImageStore struct {
	states map[string]store.ImageState
	asked  []string
}

func (f *fakeImageStore) Image(subjectID, src string) store.ImageState {
	f.asked = append(f.asked, subjectID+"|"+src)
	return f.states[subjectID+"|"+src]
}

func testPixels(w, h, natural int) inlineimg.Image {
	return inlineimg.Image{Pixels: image.NewNRGBA(image.Rect(0, 0, w, h)), NaturalWidth: natural}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestImageBackend_EnabledOnlyWhenConfiguredAndTheScreenHasATty(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		env      func(string) string
		tty      bool
		want     bool
	}{
		{"auto in kitty with a tty", "auto", kittyEnv, true, true},
		{"empty means auto", "", kittyEnv, true, true},
		{"auto elsewhere", "auto", func(string) string { return "" }, true, false},
		{"kitty forced anywhere", "kitty", func(string) string { return "" }, true, true},
		{"none wins over detection", "none", kittyEnv, true, false},
		{"no tty (simulation screen)", "kitty", kittyEnv, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tty tcell.Tty
			if tc.tty {
				tty = newFakeTty()
			}
			b := newImageBackend(&fakeImageStore{}, tc.protocol, tc.env, discardLogger())
			b.beforeDraw(newTestScreen(t, tty))
			if b.enabled != tc.want {
				t.Errorf("enabled = %v, want %v", b.enabled, tc.want)
			}
		})
	}
}

// TestImageBackend_NeedsA256ColourPalette: the image id travels in the
// cells' foreground colour as a palette index, which tcell drops (NO_COLOR,
// zero colours) or remaps (an 8 or 16 colour terminfo, e.g. TERM=screen
// inside tmux) on a terminal with fewer colours; the cells would show
// nothing or another image, so the text placeholder must stay.
func TestImageBackend_NeedsA256ColourPalette(t *testing.T) {
	for _, tc := range []struct {
		name   string
		colors int
		want   bool
	}{
		{"no colour", -1, false},
		{"8 colours", 8, false},
		{"16 colours", 16, false},
		{"88 colours", 88, false},
		{"256 colours", 256, true},
		{"truecolor", 1 << 24, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tty := newFakeTty()
			colors := tc.colors
			var screen tcell.Screen
			if colors < 0 {
				screen = noColorScreen{newTestScreen(t, tty)}
			} else {
				screen = newTestScreenColors(t, tty, colors)
			}
			b := newImageBackend(&fakeImageStore{}, "kitty", func(string) string { return "" }, discardLogger())
			b.beforeDraw(screen)
			if b.enabled != tc.want {
				t.Errorf("enabled = %v with %d colours, want %v", b.enabled, screen.Colors(), tc.want)
			}
		})
	}
}

// noColorScreen reports zero colours, as tcell does under NO_COLOR.
type noColorScreen struct{ tcell.Screen }

func (noColorScreen) Colors() int { return 0 }

func TestImageSubject_MapsStoreStatesToWidgetResults(t *testing.T) {
	const src = "https://e.com/a.png"
	st := &fakeImageStore{states: map[string]store.ImageState{
		"loading|" + src: {Status: store.ImageLoading},
		"loaded|" + src:  {Status: store.ImageLoaded, Image: testPixels(400, 200, 400)},
		"failed|" + src:  {Status: store.ImageFailed},
	}}
	b := newImageBackend(st, "kitty", func(string) string { return "" }, discardLogger())
	b.beforeDraw(newTestScreen(t, newFakeTty()))

	if got := b.sources()("loading").Image(src, 40, 0); got.Status != widget.ImageLoading {
		t.Errorf("loading = %+v", got)
	}
	if got := b.sources()("failed").Image(src, 40, 0); got.Status != widget.ImageText {
		t.Errorf("failed = %+v, want the text placeholder", got)
	}
	if got := b.sources()("unknown").Image(src, 40, 0); got.Status != widget.ImageText {
		t.Errorf("none = %+v, want the text placeholder", got)
	}
	// 10x20 pixel cells: 400 px natural width is 40 columns, 200 px of height 10 rows.
	if got := b.sources()("loaded").Image(src, 80, 0); got != (widget.ImageResult{Status: widget.ImageReady, ID: 16, Cols: 40, Rows: 10}) {
		t.Errorf("loaded = %+v, want a ready 40x10 placement with id 16", got)
	}
	if got := b.sources()("loaded").Image(src, 20, 0); got.Cols != 20 || got.ID != 17 {
		t.Errorf("loaded into 20 columns = %+v, want its own placement", got)
	}
}

func TestImageSubject_DisabledNeverConsultsTheStore(t *testing.T) {
	st := &fakeImageStore{}
	b := newImageBackend(st, "none", kittyEnv, discardLogger())
	b.beforeDraw(newTestScreen(t, newFakeTty()))

	if got := b.sources()("x").Image("https://e.com/a.png", 40, 0); got.Status != widget.ImageText {
		t.Errorf("Image() = %+v, want the text placeholder", got)
	}
	if len(st.asked) != 0 {
		t.Errorf("store consulted: %v (that would start downloads nobody will show)", st.asked)
	}
}

func TestImageBackend_CellSizeFromTheTtyWithFallback(t *testing.T) {
	st := &fakeImageStore{states: map[string]store.ImageState{
		"s|a": {Status: store.ImageLoaded, Image: testPixels(800, 400, 800)},
	}}
	for _, tc := range []struct {
		name     string
		ws       tcell.WindowSize
		wantCols int
		wantRows int
	}{
		{"real cell size", tcell.WindowSize{Width: 100, Height: 30, PixelWidth: 2000, PixelHeight: 1200}, 40, 10},
		{"no pixel size, as under tmux", tcell.WindowSize{Width: 100, Height: 30}, 100, 25},
		{"zero cells never divides by zero", tcell.WindowSize{PixelWidth: 100, PixelHeight: 100}, 100, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newImageBackend(st, "kitty", func(string) string { return "" }, discardLogger())
			b.beforeDraw(newTestScreen(t, &fakeTty{ws: tc.ws}))
			got := b.sources()("s").Image("a", 100, 0)
			if got.Cols != tc.wantCols || got.Rows != tc.wantRows {
				t.Errorf("placement = %dx%d, want %dx%d", got.Cols, got.Rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}

func TestImageBackend_FlushWritesQueuedImagesOnceAndCleanupDeletesThem(t *testing.T) {
	tty := newFakeTty()
	screen := newTestScreen(t, tty)
	st := &fakeImageStore{states: map[string]store.ImageState{
		"s|a": {Status: store.ImageLoaded, Image: testPixels(100, 100, 100)},
	}}
	b := newImageBackend(st, "kitty", func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux-1/default,1,0"
		}
		return ""
	}, discardLogger())

	b.beforeDraw(screen)
	b.sources()("s").Image("a", 40, 0)
	if tty.output() != "" {
		t.Fatal("placing an image wrote to the terminal; it must wait for the end of the frame")
	}
	b.flush(screen)
	out := tty.output()
	if !strings.HasPrefix(out, "\x1bPtmux;\x1b\x1b_Ga=T,U=1,f=100,t=d,i=16,") || !strings.Contains(out, ",q=2,") {
		t.Fatalf("flushed output is not a tmux-wrapped, quiet transmission: %.80q", out)
	}
	b.flush(screen)
	if tty.output() != out {
		t.Error("a second flush wrote the same image again")
	}

	b.cleanup()
	if got := tty.output()[len(out):]; got != "\x1bPtmux;\x1b\x1b_Ga=d,d=I,i=16,q=2\x1b\x1b\\\x1b\\" {
		t.Errorf("cleanup wrote %q, want the wrapped deletion of image 16", got)
	}
}

func TestImageBackend_DisabledFlushAndCleanupWriteNothing(t *testing.T) {
	tty := newFakeTty()
	screen := newTestScreen(t, tty)
	b := newImageBackend(&fakeImageStore{}, "none", kittyEnv, discardLogger())
	b.beforeDraw(screen)
	b.flush(screen)
	b.cleanup()
	if tty.output() != "" {
		t.Errorf("wrote %q with images disabled", tty.output())
	}
}

// recordingSources returns an imageSources that records the subject IDs it
// is asked for and answers each image as ready.
func recordingSources(asked *[]string) imageSources {
	return func(id string) widget.ImageSource {
		*asked = append(*asked, id)
		return readySource{}
	}
}

type readySource struct{}

func (readySource) Image(string, int, int) widget.ImageResult {
	return widget.ImageResult{Status: widget.ImageReady, ID: 3, Cols: 2, Rows: 1}
}

func TestBlocksUseTheirOwnNodeIDAsImageSubject(t *testing.T) {
	const img = "![x](https://e.com/a.png)"
	pr := &model.PullRequest{
		ID:   "PR_1",
		Body: img,
		Timeline: []model.TimelineItem{
			{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{ID: "IC_1", Body: img}},
			{Kind: model.TimelineKindReview, Review: &model.Review{ID: "R_1", Body: img}},
		},
	}
	var asked []string
	images := recordingSources(&asked)

	blocks := []widget.Block{descriptionBlock(pr, images)}
	blocks = append(blocks, conversationBlocks(pr, nil, images)...)
	for _, b := range blocks {
		if text := blockText(b, 40); !strings.Contains(text, kitty.Cell(0, 0)+kitty.Cell(0, 1)) {
			t.Errorf("block %s = %q, want the image's placeholder cells", b.ID, text)
		}
	}
	if got := strings.Join(asked, ","); got != "PR_1,IC_1,R_1" {
		t.Errorf("image subjects asked = %q, want PR_1,IC_1,R_1", got)
	}
}

func TestBlocksWithoutImageSourcesKeepTextPlaceholders(t *testing.T) {
	pr := &model.PullRequest{ID: "PR_1", Body: "![x](https://e.com/a.png)"}
	if got := blockText(descriptionBlock(pr, nil), 40); !strings.Contains(got, "[image: x]") {
		t.Errorf("description = %q, want the text placeholder", got)
	}
}

// gatedFetcher is a store.ImageFetcher whose downloads wait for gate.
type gatedFetcher struct {
	gate chan struct{}
}

func (f gatedFetcher) Fetch(ctx context.Context, _ string) (inlineimg.Image, error) {
	select {
	case <-f.gate:
	case <-ctx.Done():
		return inlineimg.Image{}, ctx.Err()
	}
	return testPixels(500, 250, 500), nil
}

const (
	appImgUUID   = "0b9a8c3e-1d2f-4a5b-9c6d-7e8f9a0b1c2d"
	appImgAsset  = "https://github.com/user-attachments/assets/" + appImgUUID
	appImgSigned = "https://private-user-images.githubusercontent.com/1/2-" + appImgUUID + ".png?jwt=x"
)

// imagePRFixture is a pull request whose description is one uploaded image
// and which has two comments after it.
func imagePRFixture(fake *fakeGitHub) model.PRRef {
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	pr.Body = `<img width="500" alt="shot" src="` + appImgAsset + `" />`
	pr.Timeline = []model.TimelineItem{
		{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{ID: "IC_1", Author: model.User{Login: "bob"}, Body: "first"}},
		{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{ID: "IC_2", Author: model.User{Login: "bob"}, Body: "second"}},
	}
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})
	fake.SetBodyHTML(pr.ID, `<p><img src="`+appImgSigned+`" width="500" alt="shot"></p>`)
	return ref
}

func TestAppDrawsImagesAndKeepsTheCursorAndCleansUpOnQuit(t *testing.T) {
	gate := make(chan struct{})
	tty := newFakeTty()
	app, done, fake, _ := newTestAppWith(t, nil, testAppOpts{
		images: gatedFetcher{gate: gate},
		getenv: kittyEnv,
		tty:    tty,
	})
	ref := imagePRFixture(fake)

	act(app.app, func() { app.showPreview(ref.Key()) })
	waitFor(t, app.app, func() bool { return detailSelectableIndex(app.prView.Rows(), "timeline:1") >= 0 })
	if got := query(app.app, func() string { return prViewText(app.prView) }); !strings.Contains(got, "[image: shot]") {
		t.Fatalf("before the download finishes the description = %q, want the text placeholder", got)
	}

	// Park the cursor on the last comment, then let the image arrive: the
	// rebuild that swaps the placeholder for the picture must not move it.
	act(app.app, func() { app.prView.SetCursor(detailSelectableIndex(app.prView.Rows(), "timeline:1")) })
	close(gate)
	waitFor(t, app.app, func() bool { return strings.Contains(prViewText(app.prView), "\U0010EEEE") })

	if id := query(app.app, app.prView.CurrentID); id != "timeline:1" {
		t.Errorf("cursor block = %q after the image loaded, want timeline:1 (unchanged)", id)
	}
	if strings.Contains(query(app.app, func() string { return prViewText(app.prView) }), "[image: shot]") {
		t.Error("the text placeholder is still shown beside the picture")
	}
	out := tty.output()
	// 10x20 pixel cells, 500 px natural width and a 500 px width attribute: 50 columns.
	if !regexp.MustCompile(`\x1b_Ga=T,U=1,f=100,t=d,i=16,c=50,r=\d+,q=2,m=0;`).MatchString(out) {
		t.Errorf("the image was not transmitted as expected: %.200q", out)
	}
	if strings.Count(out, "a=T,U=1") != 1 {
		t.Errorf("transmissions = %d, want exactly one", strings.Count(out, "a=T,U=1"))
	}

	act(app.app, app.quit)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("app did not stop")
	}
	if got := tty.output(); !strings.HasSuffix(got, "\x1b_Ga=d,d=I,i=16,q=2\x1b\\") {
		t.Errorf("terminal output does not end with the deletion of image 16: %.100q", got[max(0, len(got)-100):])
	}
}

func TestAppKeepsTextPlaceholdersWhenImagesAreOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(*config.Config)
		env    func(string) string
		tty    bool
	}{
		{"image_protocol none", func(c *config.Config) { c.ImageProtocol = "none" }, kittyEnv, true},
		{"auto outside kitty", nil, func(string) string { return "" }, true},
		{"no tty", nil, kittyEnv, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tty := newFakeTty()
			opts := testAppOpts{config: tc.config, getenv: tc.env, images: gatedFetcher{gate: make(chan struct{})}}
			if tc.tty {
				opts.tty = tty
			}
			app, _, fake, _ := newTestAppWith(t, nil, opts)
			ref := imagePRFixture(fake)

			act(app.app, func() { app.showPreview(ref.Key()) })
			waitFor(t, app.app, func() bool { return strings.Contains(prViewText(app.prView), "[image: shot]") })
			if strings.Contains(query(app.app, func() string { return prViewText(app.prView) }), "(loading…)") {
				t.Error("the placeholder says loading although nothing will be loaded")
			}
			if tty.output() != "" {
				t.Errorf("wrote %.60q to the terminal", tty.output())
			}
		})
	}
}
