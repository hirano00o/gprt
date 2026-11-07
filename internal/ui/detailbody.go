package ui

import (
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// descriptionBlock renders the pull request's body as GitHub Flavored
// Markdown (see widget.RenderMarkdown), or a muted "(no description)"
// placeholder when the body is blank or renders to nothing (only an HTML
// comment, only fence markers): a selectable block must never have zero
// lines. It is selectable, with the PR's own URL.
func descriptionBlock(pr *model.PullRequest) widget.Block {
	return widget.Block{
		ID:         "description",
		Selectable: true,
		URL:        pr.URL,
		Build: func(width int) [][]widget.Span {
			lines := widget.RenderMarkdown(pr.Body, width)
			if len(lines) == 0 {
				lines = [][]widget.Span{{{Text: "(no description)", Style: theme.Muted}}}
			}
			if reaction := reactionLine(pr.ReactionGroups); reaction != nil {
				lines = append(lines, reaction)
			}
			return lines
		},
	}
}
