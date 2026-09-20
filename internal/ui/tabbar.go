package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// tabBarView is a one-line row of labelled tabs, drawing the active tab
// with theme.Header and every other tab with theme.Muted. It never handles
// input: the router calls SetActive directly, matching every other widget
// in this package.
type tabBarView struct {
	*tview.Box

	labels []string
	active int
}

// newTabBarView creates a tabBarView with the given labels, in order.
func newTabBarView(labels []string) *tabBarView {
	return &tabBarView{Box: tview.NewBox(), labels: labels}
}

// SetActive highlights labels[i]. Out-of-range values are ignored.
func (tb *tabBarView) SetActive(i int) {
	if i < 0 || i >= len(tb.labels) {
		return
	}
	tb.active = i
}

// Draw renders the tab bar.
func (tb *tabBarView) Draw(screen tcell.Screen) {
	tb.DrawForSubclass(screen, tb)
	x, y, width, height := tb.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	spans := make([]widget.Span, 0, len(tb.labels))
	for i, label := range tb.labels {
		style := theme.Muted
		if i == tb.active {
			style = theme.Header
		}
		spans = append(spans, widget.Span{Text: "[" + label + "] ", Style: style})
	}
	widget.DrawSpans(screen, x, y, width, spans)
}
