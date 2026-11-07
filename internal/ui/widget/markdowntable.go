package widget

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"

	"github.com/hirano00o/gprt/internal/ui/theme"
)

const (
	tableColumnSep = " │ "
	tableRuleSep   = "─┼─"
	// tableMinColumn is the narrowest a column is shrunk to while a wider one
	// remains; if the table still does not fit, columns shrink further.
	tableMinColumn = 3
)

// table renders t as aligned, single-line rows: a bold header, a rule, then
// the body. Cells are never wrapped; when the table is wider than width the
// widest columns are shrunk first and the cells that no longer fit are
// truncated with an ellipsis.
func (r *mdRenderer) table(t *extast.Table, width int) [][]Span {
	columns := len(t.Alignments)
	if columns == 0 {
		return nil
	}

	var rows [][][]Span
	var header [][]Span
	for rowNode := t.FirstChild(); rowNode != nil; rowNode = rowNode.NextSibling() {
		style := theme.Base
		_, isHeader := rowNode.(*extast.TableHeader)
		if isHeader {
			style = style.Bold(true)
		}
		cells := make([][]Span, columns)
		i := 0
		for cell := rowNode.FirstChild(); cell != nil && i < columns; cell = cell.NextSibling() {
			cells[i] = r.cellSpans(cell, style)
			i++
		}
		if isHeader {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	if header == nil {
		return nil
	}

	widths := columnWidths(append([][][]Span{header}, rows...), columns)
	shrinkColumns(widths, width)

	lines := [][]Span{tableRow(header, widths, t.Alignments, width)}
	lines = append(lines, tableRule(widths, width))
	for _, cells := range rows {
		lines = append(lines, tableRow(cells, widths, t.Alignments, width))
	}
	return lines
}

// cellSpans renders a table cell's inline content on a single line.
func (r *mdRenderer) cellSpans(cell ast.Node, style tcell.Style) []Span {
	spans := r.inline(cell, style)
	for i := range spans {
		spans[i].Text = strings.ReplaceAll(spans[i].Text, "\n", " ")
	}
	return spans
}

func columnWidths(rows [][][]Span, columns int) []int {
	widths := make([]int, columns)
	for _, cells := range rows {
		for i, c := range cells {
			widths[i] = max(widths[i], SpanWidth(c))
		}
	}
	return widths
}

// shrinkColumns narrows the widest column one cell at a time until the
// table (columns plus separators) fits width, or nothing can shrink more.
func shrinkColumns(widths []int, width int) {
	total := (len(widths) - 1) * SpanWidth([]Span{{Text: tableColumnSep}})
	for _, w := range widths {
		total += w
	}
	floor := tableMinColumn
	for total > width {
		widest := -1
		for i, w := range widths {
			if w > floor && (widest < 0 || w > widths[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			if floor == 1 {
				return
			}
			floor = 1
			continue
		}
		widths[widest]--
		total--
	}
}

// tableRow joins cells, each fitted to its column width and aligned, with
// column separators. The final truncation is a backstop for tables so
// narrow that even one-cell columns plus separators exceed width.
func tableRow(cells [][]Span, widths []int, aligns []extast.Alignment, width int) []Span {
	var row []Span
	for i, c := range cells {
		if i > 0 {
			row = append(row, Span{Text: tableColumnSep, Style: theme.Muted})
		}
		c = truncateSpans(c, widths[i])
		gap := widths[i] - SpanWidth(c)
		left := 0
		switch aligns[i] {
		case extast.AlignRight:
			left = gap
		case extast.AlignCenter:
			left = gap / 2
		}
		if left > 0 {
			row = append(row, Span{Text: strings.Repeat(" ", left), Style: theme.Base})
		}
		row = append(row, c...)
		if gap-left > 0 {
			row = append(row, Span{Text: strings.Repeat(" ", gap-left), Style: theme.Base})
		}
	}
	return truncateSpans(row, width)
}

func tableRule(widths []int, width int) []Span {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat(ruleRune, w)
	}
	return truncateSpans([]Span{{Text: strings.Join(parts, tableRuleSep), Style: theme.Muted}}, width)
}

// truncateSpans cuts spans to at most width columns, ending in an ellipsis
// when anything was cut; spans that already fit are returned unchanged.
func truncateSpans(spans []Span, width int) []Span {
	if SpanWidth(spans) <= width {
		return spans
	}
	if width < 1 {
		return nil
	}
	var cells []cell
	for _, s := range spans {
		cells = append(cells, spanCells(s)...)
	}
	kept := cells[:0:0]
	used := 0
	for _, c := range cells {
		if used+c.width > width-1 {
			break
		}
		kept = append(kept, c)
		used += c.width
	}
	style := spans[len(spans)-1].Style
	if len(kept) > 0 {
		style = kept[len(kept)-1].style
	}
	return append(cellsToSpans(kept), Span{Text: string(ellipsisRune), Style: style})
}
