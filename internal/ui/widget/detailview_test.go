package widget

import "testing"

func widthReportingBlock(id string, selectable bool) Block {
	return Block{
		ID:         id,
		Selectable: selectable,
		Build: func(width int) [][]Span {
			if width <= 0 {
				return nil
			}
			// A fixed amount of "content" spread over ceil(content/width)
			// lines, so a narrower rect produces more lines: a simple,
			// deterministic stand-in for word-wrapped text without
			// depending on tview.WordWrap here.
			const content = 100
			n := (content + width - 1) / width
			lines := make([][]Span, n)
			for i := range lines {
				lines[i] = []Span{{Text: "x"}}
			}
			return lines
		},
	}
}

func TestDetailViewRebuildsOnWidthChange(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	dv.SetBlocks([]Block{widthReportingBlock("b1", true)})

	screen := newTestScreen(t, 40, 10)
	dv.Draw(screen)
	wideLines := len(dv.Rows()[0].Lines)

	dv.SetRect(0, 0, 10, 10)
	screen2 := newTestScreen(t, 10, 10)
	dv.Draw(screen2)
	narrowLines := len(dv.Rows()[0].Lines)

	if narrowLines <= wideLines {
		t.Fatalf("narrowing the rect did not rebuild with more lines: wide=%d narrow=%d", wideLines, narrowLines)
	}
}

func TestDetailViewDoesNotRebuildWhenWidthIsUnchanged(t *testing.T) {
	calls := 0
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	dv.SetBlocks([]Block{{
		ID:         "b1",
		Selectable: true,
		Build: func(width int) [][]Span {
			calls++
			return [][]Span{{{Text: "line"}}}
		},
	}})

	screen := newTestScreen(t, 40, 10)
	dv.Draw(screen)
	dv.Draw(screen)
	dv.Draw(screen)

	if calls != 1 {
		t.Fatalf("Build was called %d times across three same-width draws, want 1", calls)
	}
}

func TestDetailViewKeepsCursorByIDAcrossRebuild(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	dv.SetBlocks([]Block{
		widthReportingBlock("b1", true),
		widthReportingBlock("b2", true),
	})
	screen := newTestScreen(t, 40, 10)
	dv.Draw(screen)

	dv.MoveBy(1)
	if got := dv.CurrentID(); got != "b2" {
		t.Fatalf("CurrentID() after MoveBy(1) = %q, want b2", got)
	}

	dv.SetRect(0, 0, 10, 10)
	screen2 := newTestScreen(t, 10, 10)
	dv.Draw(screen2)

	if got := dv.CurrentID(); got != "b2" {
		t.Fatalf("cursor moved off b2 after a rebuild: CurrentID() = %q", got)
	}
}

func TestDetailViewCurrentURL(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	dv.SetBlocks([]Block{
		{ID: "b1", Selectable: true, URL: "https://example.com/1", Build: func(int) [][]Span { return [][]Span{{{Text: "a"}}} }},
		{ID: "b2", Selectable: true, URL: "https://example.com/2", Build: func(int) [][]Span { return [][]Span{{{Text: "b"}}} }},
	})
	screen := newTestScreen(t, 40, 10)
	dv.Draw(screen)

	if got := dv.CurrentURL(); got != "https://example.com/1" {
		t.Fatalf("CurrentURL() = %q, want the first block's URL", got)
	}
	dv.MoveBy(1)
	if got := dv.CurrentURL(); got != "https://example.com/2" {
		t.Fatalf("CurrentURL() after MoveBy(1) = %q, want the second block's URL", got)
	}
}

func TestDetailViewCurrentURLEmptyWhenNoBlocks(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	if got := dv.CurrentURL(); got != "" {
		t.Fatalf("CurrentURL() on an empty DetailView = %q, want empty", got)
	}
}

func TestDetailViewZeroWidthDoesNotPanic(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 0, 10) // zero width
	dv.SetBlocks([]Block{widthReportingBlock("b1", true)})

	screen := newTestScreen(t, 1, 10)
	dv.Draw(screen) // must not panic
}

func TestDetailViewNilBuildFuncProducesNoLines(t *testing.T) {
	dv := NewDetailView()
	dv.SetRect(0, 0, 40, 10)
	dv.SetBlocks([]Block{{ID: "b1", Selectable: false}})

	screen := newTestScreen(t, 40, 10)
	dv.Draw(screen) // must not panic

	if got := dv.Rows(); len(got) != 1 || got[0].ID != "b1" {
		t.Fatalf("Rows() = %#v, want one row with ID b1", got)
	}
}
