package widget

import (
	"strings"
	"testing"

	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/kitty"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

type imageCall struct {
	src             string
	maxCols, wantPx int
}

// fakeImages answers every Image call with result, recording the calls.
type fakeImages struct {
	result ImageResult
	calls  []imageCall
}

func (f *fakeImages) Image(src string, maxCols, widthPx int) ImageResult {
	f.calls = append(f.calls, imageCall{src, maxCols, widthPx})
	return f.result
}

var readyImage = ImageResult{Status: ImageReady, ID: 5, Cols: 3, Rows: 2}

func renderWith(body string, width int, images ImageSource) [][]Span {
	return RenderMarkdown(body, width, images)
}

func TestRenderMarkdown_StandaloneImageBecomesPlaceholderCells(t *testing.T) {
	const src = "https://github.com/user-attachments/assets/abc"
	tests := []struct {
		name    string
		body    string
		wantSrc string
		wantPx  int
	}{
		{"markdown image", "![shot](" + src + ")", src, 0},
		{"html block with width", `<img width="800" alt="shot" src="` + src + `" />`, src, 800},
		{"html block percent width is ignored", `<img width="50%" src="` + src + `">`, src, 0},
		{"linked markdown image", "[![shot](" + src + ")](https://example.com)", src, 0},
		{"html image wrapped in a link in a paragraph", `<a href="https://e.com"><img alt="x" src="` + src + `"></a>`, src, 0},
		{"html image single quoted", `<img src='` + src + `'>`, src, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeImages{result: readyImage}
			lines := renderWith(tc.body, 40, f)

			if len(f.calls) != 1 || f.calls[0] != (imageCall{tc.wantSrc, 40, tc.wantPx}) {
				t.Fatalf("Image calls = %+v, want one for %q with maxCols 40 and widthPx %d", f.calls, tc.wantSrc, tc.wantPx)
			}
			want := kitty.Rows(3, 2)
			if len(lines) != len(want) {
				t.Fatalf("got %d lines (%q), want %d image rows", len(lines), linesText(lines), len(want))
			}
			for i, line := range lines {
				if len(line) != 1 || line[0].Text != want[i] {
					t.Errorf("line %d = %q, want the placeholder row %q", i, spansText(line), want[i])
				}
				if fg, _, _ := line[0].Style.Decompose(); fg != tcell.PaletteColor(5) {
					t.Errorf("line %d foreground = %v, want palette colour 5 (the image id)", i, fg)
				}
				if w := SpanWidth(line); w != 3 {
					t.Errorf("line %d width = %d, want 3", i, w)
				}
			}
		})
	}
}

func TestRenderMarkdown_ImageStates(t *testing.T) {
	tests := []struct {
		name   string
		images ImageSource
		want   string
		style  tcell.Style
	}{
		{"nil source", nil, "[image: logo]", theme.Muted},
		{"loading", &fakeImages{result: ImageResult{Status: ImageLoading}}, "[image: logo] (loading…)", theme.Muted},
		{"failed or disabled", &fakeImages{result: ImageResult{Status: ImageText}}, "[image: logo]", theme.Muted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderWith("![logo](https://e.com/a.png)", 40, tc.images)
			if got := linesText(lines); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
			if lines[0][0].Style != tc.style {
				t.Errorf("style = %v, want muted", lines[0][0].Style)
			}
		})
	}
}

func TestRenderMarkdown_InlineImagesStayText(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"markdown image in text", "see ![x](https://e.com/a.png) here", "see [image: x] here"},
		{"html image in text", `see <img alt="x" src="https://e.com/a.png"> here`, "see [image: x] here"},
		{"two images on one line", "![a](https://e.com/a.png) ![b](https://e.com/b.png)", "[image: a] [image: b]"},
		{"image with text in an html block", `<p>logo <img alt="x" src="https://e.com/a.png"> here</p>`, "logo [image: x] here"},
		{"two images in one html line", `<img alt="a" src="a.png"> <img alt="b" src="b.png">`, "[image: a] [image: b]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeImages{result: readyImage}
			got := linesText(renderWith(tc.body, 60, f))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("lines = %q, want %q", got, tc.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("source consulted for an inline image: %+v", f.calls)
			}
		})
	}
}

func TestRenderMarkdown_ImageOnItsOwnLineInsideAParagraph(t *testing.T) {
	f := &fakeImages{result: readyImage}
	lines := renderWith("before\n![x](https://e.com/a.png)\nafter", 40, f)

	got := linesText(lines)
	rows := kitty.Rows(3, 2)
	want := []string{"before", rows[0], rows[1], "after"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func TestRenderMarkdown_ImageInListAndQuoteShrinksRoomAndIndents(t *testing.T) {
	f := &fakeImages{result: readyImage}
	lines := renderWith("- ![x](https://e.com/a.png)", 40, f)
	if len(f.calls) != 1 || f.calls[0].maxCols != 38 {
		t.Fatalf("list item calls = %+v, want maxCols 38 (width minus the bullet)", f.calls)
	}
	rows := kitty.Rows(3, 2)
	if got := linesText(lines); len(got) != 2 || got[0] != bulletMarker+rows[0] || got[1] != "  "+rows[1] {
		t.Errorf("list lines = %q", got)
	}
	for _, l := range lines {
		if w := SpanWidth(l); w > 40 {
			t.Errorf("line width %d exceeds 40", w)
		}
	}

	f = &fakeImages{result: readyImage}
	lines = renderWith("> ![x](https://e.com/a.png)", 40, f)
	if len(f.calls) != 1 || f.calls[0].maxCols != 38 {
		t.Fatalf("quote calls = %+v, want maxCols 38", f.calls)
	}
	if got := linesText(lines); len(got) != 2 || got[0] != quotePrefix+rows[0] {
		t.Errorf("quote lines = %q", got)
	}
}

func TestRenderMarkdown_ReadyImageWiderThanRoomIsClamped(t *testing.T) {
	f := &fakeImages{result: ImageResult{Status: ImageReady, ID: 1, Cols: 50, Rows: 1}}
	lines := renderWith("![x](https://e.com/a.png)", 10, f)
	if len(lines) != 1 || SpanWidth(lines[0]) != 10 {
		t.Errorf("lines = %q, want one row of 10 cells", linesText(lines))
	}
}

func TestRenderMarkdown_ImageSurroundingBlocksKeepBlankSeparation(t *testing.T) {
	f := &fakeImages{result: readyImage}
	got := linesText(renderWith("intro\n\n![x](https://e.com/a.png)\n\noutro", 40, f))
	rows := kitty.Rows(3, 2)
	want := []string{"intro", "", rows[0], rows[1], "", "outro"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// TestPlaceholderCellsAreOneColumn pins the width of every placeholder cell
// on each path that measures or draws text: the display-width library, the
// grapheme segmenter, DrawSpans, and the screen's own cell storage. A cell
// drawn as zero or two columns would shear the whole image.
func TestPlaceholderCellsAreOneColumn(t *testing.T) {
	scr := tcell.NewSimulationScreen("")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(kitty.MaxCells, 2)

	// Row r of the placement uses diacritic r for its row number, so drawing
	// two rows covers the whole table as row numbers and as column numbers.
	rows := kitty.Rows(kitty.MaxCells, 2)
	style := theme.Base.Foreground(tcell.PaletteColor(9))
	for y, row := range rows {
		clusters := 0
		for it := graphemes.FromString(row); it.Next(); {
			clusters++
			if w := displaywidth.String(it.Value()); w != 1 {
				t.Fatalf("cell %d of row %d has display width %d", clusters-1, y, w)
			}
		}
		if clusters != kitty.MaxCells {
			t.Fatalf("row %d has %d grapheme clusters, want %d", y, clusters, kitty.MaxCells)
		}
		if w := SpanWidth([]Span{{Text: row, Style: style}}); w != kitty.MaxCells {
			t.Fatalf("SpanWidth(row %d) = %d, want %d", y, w, kitty.MaxCells)
		}
		if drawn := DrawSpans(scr, 0, y, kitty.MaxCells, []Span{{Text: row, Style: style}}); drawn != kitty.MaxCells {
			t.Fatalf("DrawSpans(row %d) drew %d cells, want %d", y, drawn, kitty.MaxCells)
		}
	}
	for r := range kitty.MaxCells {
		cell := kitty.Cell(r, 0)
		it := graphemes.FromString(cell)
		if !it.Next() || it.Value() != cell || displaywidth.String(cell) != 1 {
			t.Fatalf("Cell(%d, 0) is not a single one-column cluster", r)
		}
	}
	scr.Show()

	for y := range 2 {
		for x := range kitty.MaxCells {
			want := kitty.Cell(y, x)
			got, gotStyle, width := scr.Get(x, y)
			if got != want || width != 1 {
				t.Fatalf("cell (%d,%d) = %q width %d, want %q width 1", x, y, got, width, want)
			}
			if fg, _, _ := gotStyle.Decompose(); fg != tcell.PaletteColor(9) {
				t.Fatalf("cell (%d,%d) foreground = %v, want the palette id colour", x, y, fg)
			}
		}
	}

	// tcell's own Put, which segments by its own rules, must agree.
	scr.Clear()
	rest, width := scr.Put(0, 0, kitty.Cell(3, 4)+kitty.Cell(3, 5), style)
	if width != 1 || rest != kitty.Cell(3, 5) {
		t.Errorf("Put consumed %q (width %d), want exactly the first cell", kitty.Cell(3, 4), width)
	}
}

func TestPlaceholderRowsSurviveSpanHelpers(t *testing.T) {
	style := theme.Base.Foreground(tcell.PaletteColor(7))
	row := kitty.Rows(6, 1)[0]
	spans := []Span{{Text: row, Style: style}}

	if got := WrapSpans(spans, 10); len(got) != 1 || spansText(got[0]) != row || got[0][0].Style != style {
		t.Errorf("WrapSpans changed a placeholder row: %q", linesText(got))
	}
	// A row wider than the room is split between cells, never inside one.
	chopped := chopSpans(spans, 4)
	if len(chopped) != 2 || spansText(chopped[0]) != kitty.Rows(4, 1)[0] {
		t.Errorf("chopSpans = %q", linesText(chopped))
	}
	if SpanWidth(chopped[1]) != 2 {
		t.Errorf("remainder width = %d, want 2", SpanWidth(chopped[1]))
	}
	for _, l := range chopped {
		for _, s := range l {
			if s.Style != style {
				t.Error("chopSpans lost the foreground colour")
			}
		}
	}

	bg := withBackground(spans, tcell.ColorRed)
	fg, gotBG, _ := bg[0].Style.Decompose()
	if bg[0].Text != row || fg != tcell.PaletteColor(7) || gotBG != tcell.ColorRed {
		t.Errorf("withBackground = %q fg %v bg %v, want text and fg kept, bg replaced", bg[0].Text, fg, gotBG)
	}

	// Truncation shows an ellipsis in the last cell and keeps whole cells before it.
	scr := tcell.NewSimulationScreen("")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(8, 1)
	if drawn := DrawSpans(scr, 0, 0, 4, spans); drawn != 4 {
		t.Errorf("truncated DrawSpans drew %d cells, want 4", drawn)
	}
	scr.Show()
	if got, _, _ := scr.Get(2, 0); got != kitty.Cell(0, 2) {
		t.Errorf("cell 2 = %q, want an intact placeholder cell", got)
	}
	if got, _, _ := scr.Get(3, 0); got != "…" {
		t.Errorf("cell 3 = %q, want the ellipsis", got)
	}
}
