package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// checksBlocks renders the checks summary line, followed by one selectable
// row per check when there are any (a pull request with no checks at all
// gets only the summary block, reading "No checks").
func checksBlocks(pr *model.PullRequest, icons theme.Icons) []widget.Block {
	summary := model.Summarize(pr.Checks)
	blocks := []widget.Block{checksSummaryBlock(summary)}
	for i, c := range pr.Checks {
		blocks = append(blocks, checkRowBlock(i, c, icons))
	}
	return blocks
}

// checksSummaryBlock renders "Checks · X passed · Y failed · Z pending ·
// W skipped" (a category is omitted entirely when its count is zero, so a
// pull request with no skipped checks does not read "· 0 skipped"), or
// "No checks" when there are none at all.
func checksSummaryBlock(summary model.ChecksSummary) widget.Block {
	text := "No checks"
	if summary.Total > 0 {
		var parts []string
		for _, p := range []struct {
			count int
			label string
		}{
			{summary.Success, "passed"},
			{summary.Failure, "failed"},
			{summary.Pending, "pending"},
			{summary.Skipped, "skipped"},
		} {
			if p.count > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", p.count, p.label))
			}
		}
		text = "Checks · " + strings.Join(parts, " · ")
	}
	return widget.Block{
		ID: "checks-summary",
		Build: func(int) [][]widget.Span {
			return [][]widget.Span{{{Text: text, Style: theme.Base.Bold(true)}}}
		},
	}
}

// checkRowBlock renders one check: a status icon coloured by its outcome,
// its name, its workflow (muted), and a "required" marker when applicable.
// index disambiguates the block ID when two checks share a name.
func checkRowBlock(index int, c model.Check, icons theme.Icons) widget.Block {
	return widget.Block{
		ID:         fmt.Sprintf("check:%d", index),
		Selectable: true,
		URL:        c.URL,
		Build: func(int) [][]widget.Span {
			spans := []widget.Span{
				{Text: checkIcon(c, icons) + " ", Style: checkStyle(c)},
				{Text: c.Name, Style: theme.Base},
			}
			if c.Workflow != "" {
				spans = append(spans, widget.Span{Text: " · " + c.Workflow, Style: theme.Muted})
			}
			if c.IsRequired {
				spans = append(spans, widget.Span{Text: " (required)", Style: theme.Muted})
			}
			return [][]widget.Span{spans}
		},
	}
}

// checkStyle colours a single check's icon the same way theme.RollupStyle
// colours the pull request's overall rollup: pending until completed, then
// by conclusion (success, muted for skipped/neutral, error otherwise).
func checkStyle(c model.Check) tcell.Style {
	if c.Status != model.CheckStatusCompleted {
		return theme.Pending
	}
	switch c.Conclusion {
	case model.CheckConclusionSuccess:
		return theme.Success
	case model.CheckConclusionSkipped, model.CheckConclusionNeutral:
		return theme.Muted
	default:
		return theme.Error
	}
}

// checkIcon picks a single check's icon from the same glyph set used for
// the pull request's overall rollup icon, so a check row matches the icon
// set the user configured (Unicode or Nerd Font).
func checkIcon(c model.Check, icons theme.Icons) string {
	if c.Status != model.CheckStatusCompleted {
		return icons.RollupPending
	}
	switch c.Conclusion {
	case model.CheckConclusionSuccess:
		return icons.RollupSuccess
	case model.CheckConclusionSkipped, model.CheckConclusionNeutral:
		return icons.RollupNone
	default:
		return icons.RollupFailure
	}
}
