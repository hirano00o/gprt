package ui

import (
	"strings"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// descriptionBlock renders the pull request's body with light Markdown
// styling: "#" headings bold, fenced (```) code blocks muted and
// character-wrapped (never word-wrapped, which would reflow source code),
// "-"/"* " bullets kept as literal text, and a blank body rendered as a
// muted placeholder. It is selectable, with the PR's own URL.
func descriptionBlock(pr *model.PullRequest) widget.Block {
	return widget.Block{
		ID:         "description",
		Selectable: true,
		URL:        pr.URL,
		Build: func(width int) [][]widget.Span {
			lines := renderMarkdownLite(pr.Body, width)
			if reaction := reactionLine(pr.ReactionGroups); reaction != nil {
				lines = append(lines, reaction)
			}
			return lines
		},
	}
}

// renderMarkdownLite renders body's lines with gprt's light Markdown
// styling (see descriptionBlock's doc comment), or a muted placeholder when
// body is blank.
func renderMarkdownLite(body string, width int) [][]widget.Span {
	if strings.TrimSpace(body) == "" {
		return [][]widget.Span{{{Text: "(no description)", Style: theme.Muted}}}
	}

	var out [][]widget.Span
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
		case inFence:
			out = append(out, chopChars(raw, width)...)
		case strings.HasPrefix(raw, "#"):
			out = append(out, wrapPlain(raw, width, theme.Base.Bold(true))...)
		case raw == "":
			out = append(out, []widget.Span{{Text: "", Style: theme.Base}})
		default:
			// Plain text and "-"/"* " bullet lines both word-wrap the same
			// way: a bullet's marker is already literal text at the start
			// of raw, so wrapping it like any other line keeps it intact.
			out = append(out, wrapPlain(raw, width, theme.Base)...)
		}
	}
	if len(out) == 0 {
		// A body of only fence markers (no content line inside or outside
		// the fence) toggles inFence on every line without ever appending
		// anything: the blank-body check above only looks at whitespace,
		// so it does not catch this case. Falling through with zero lines
		// would leave descriptionBlock's selectable row at zero height.
		return [][]widget.Span{{{Text: "(no description)", Style: theme.Muted}}}
	}
	return out
}
