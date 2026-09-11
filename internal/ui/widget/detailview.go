package widget

import "github.com/gdamore/tcell/v2"

// Block is one block-structured, width-dependent unit of a DetailView: a PR
// tab header, a paragraph of the description, a check row, a timeline entry,
// and so on. Build renders the block's lines for the given inner width (the
// primitive re-invokes it only when that width actually changes); it may be
// nil, which renders as a block with no lines.
type Block struct {
	// ID identifies the block stably across a rebuild, the same way
	// ListRow.ID does: the cursor stays on the same block (by ID) when
	// SetBlocks triggers a rebuild at a new width.
	ID string
	// Selectable marks the block as one the cursor can land on.
	Selectable bool
	// URL is what the "open in browser" action (global.open_browser) opens
	// when the cursor is on this block; empty when the block has nothing
	// of its own to open.
	URL string
	// Build renders the block's content at the given inner width.
	Build func(width int) [][]Span
}

// DetailView is a ListView of width-dependent Blocks: content that needs to
// know the available width to wrap or lay itself out (the PR tab's header,
// description, checks, and conversation). Draw rebuilds every block's lines
// — via SetRows, which preserves the cursor by ID — whenever the inner width
// has changed since the last rebuild; SetBlocks itself does no rendering
// work; it stores blocks and invalidates the last-seen width so the next
// Draw rebuilds unconditionally, matching the primitive's "rebuild lazily,
// on the width that will actually be drawn with" design.
type DetailView struct {
	*ListView

	blocks    []Block
	lastWidth int
}

// NewDetailView creates an empty DetailView.
func NewDetailView() *DetailView {
	return &DetailView{ListView: NewListView(), lastWidth: -1}
}

// SetBlocks replaces the displayed blocks and forces the next Draw to
// rebuild them, regardless of whether the inner width happens to be
// unchanged: the blocks themselves (not just the width) may have changed.
func (dv *DetailView) SetBlocks(blocks []Block) {
	dv.blocks = blocks
	dv.lastWidth = -1
}

// CurrentURL returns the URL of the block currently under the cursor, or ""
// when there are no blocks or the current one has none.
func (dv *DetailView) CurrentURL() string {
	id := dv.CurrentID()
	if id == "" {
		return ""
	}
	for _, b := range dv.blocks {
		if b.ID == id {
			return b.URL
		}
	}
	return ""
}

// Draw rebuilds every block's lines, via SetRows, when the inner width has
// changed since the last rebuild, then renders like a plain ListView.
func (dv *DetailView) Draw(screen tcell.Screen) {
	_, _, width, _ := dv.GetInnerRect()
	if width != dv.lastWidth {
		dv.rebuild(width)
		dv.lastWidth = width
	}
	dv.ListView.Draw(screen)
}

// rebuild renders every block at width and applies the result via SetRows,
// which keeps the cursor on whichever block ID it was on before.
func (dv *DetailView) rebuild(width int) {
	rows := make([]ListRow, len(dv.blocks))
	for i, b := range dv.blocks {
		var lines [][]Span
		if b.Build != nil {
			lines = b.Build(width)
		}
		rows[i] = ListRow{ID: b.ID, Selectable: b.Selectable, Lines: lines}
	}
	dv.SetRows(rows)
}
