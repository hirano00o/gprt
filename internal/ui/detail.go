// detail.go renders the PR tab's content as widget.Blocks for
// widget.DetailView: a header, the description, the checks list, and the
// conversation timeline, in that order. Every block builder is a pure
// function of model data (plus the current width, supplied by
// widget.DetailView at Draw time), so they are unit-tested directly without
// a tview.Application or screen.
package ui

import (
	"fmt"
	"log/slog"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// buildPRBlocks renders the PR tab's blocks for pr's detail. When pr is nil
// (nothing selected yet, or the detail failed to load), a single
// informational block is returned instead of the full layout. logger
// receives a warning if the timeline contains a malformed item (see
// conversationBlocks); a nil logger discards it.
func buildPRBlocks(pr *model.PullRequest, state store.DetailState, viewerLogin string, icons theme.Icons, logger *slog.Logger) []widget.Block {
	if pr == nil {
		return []widget.Block{emptyPRBlock(state)}
	}

	blocks := make([]widget.Block, 0, 2+len(pr.Checks)+len(pr.Timeline))
	blocks = append(blocks, headerBlock(pr, state, viewerLogin, icons))
	blocks = append(blocks, descriptionBlock(pr))
	blocks = append(blocks, checksBlocks(pr, icons)...)
	blocks = append(blocks, conversationBlocks(pr, logger)...)
	return blocks
}

// emptyPRBlock renders the PR tab's placeholder when no pull request detail
// is available to show: a standing detail-fetch error takes priority over
// "loading", which takes priority over the generic "nothing selected" hint.
func emptyPRBlock(state store.DetailState) widget.Block {
	text := "Select a pull request to preview it here."
	switch {
	case state.Err != nil:
		text = "Error loading pull request: " + state.Err.Error()
	case state.Loading:
		text = "Loading pull request..."
	}
	return widget.Block{
		ID: "empty",
		Build: func(int) [][]widget.Span {
			return [][]widget.Span{{{Text: text, Style: theme.Muted}}}
		},
	}
}

// headerBlock renders the pull request's title, state, base/head branches,
// author, timestamps, labels, review decision and reviewers, and diff
// stats. It is selectable, with the PR's own URL, so "o" on it opens the
// pull request in the browser.
func headerBlock(pr *model.PullRequest, state store.DetailState, viewerLogin string, icons theme.Icons) widget.Block {
	return widget.Block{
		ID:         "header",
		Selectable: true,
		URL:        pr.URL,
		Build: func(width int) [][]widget.Span {
			var lines [][]widget.Span

			titleLine := []widget.Span{{Text: fmt.Sprintf("#%d %s", pr.Ref.Number, pr.Title), Style: theme.Base.Bold(true)}}
			if state.Stale {
				titleLine = append(titleLine, widget.Span{Text: "  (cached)", Style: theme.Stale})
			}
			lines = append(lines, titleLine)

			lines = append(lines, []widget.Span{
				{Text: stateIcon(pr, icons) + " " + prStateLabel(pr), Style: theme.Base},
				{Text: " · ", Style: theme.Muted},
				{Text: string(pr.MergeStateStatus), Style: theme.Muted},
				{Text: " · ", Style: theme.Muted},
				{Text: pr.BaseRefName + " ← " + pr.HeadRefName, Style: theme.Base},
			})

			lines = append(lines, byLine(pr, viewerLogin))

			if len(pr.Labels) > 0 {
				lines = append(lines, labelSpans(pr.Labels))
			}

			if reviewLine := reviewSummaryLine(pr, viewerLogin); reviewLine != nil {
				lines = append(lines, reviewLine)
			}

			lines = append(lines, statsLine(pr))

			if state.Err != nil {
				// CurrentPR() only ever goes nil on OpenPR/ClosePR, never
				// on a failed fetch (see store.applyDetailResult), so a
				// standing error can coexist with an otherwise normal
				// header — for a background refresh that failed after
				// showing cached data, or a reload that failed outright.
				// Without this line, nothing on the PR tab would show
				// that the data on screen may now be stale.
				lines = append(lines, []widget.Span{{Text: "! " + state.Err.Error(), Style: theme.Error}})
			}
			for _, w := range state.Warnings {
				lines = append(lines, []widget.Span{{Text: "⚠ " + w, Style: theme.Warning}})
			}

			return lines
		},
	}
}

// prStateLabel returns the header's state word: DRAFT takes priority over
// OPEN for a draft pull request, matching stateIcon's own precedence.
func prStateLabel(pr *model.PullRequest) string {
	if pr.IsDraft && pr.State == model.PRStateOpen {
		return "DRAFT"
	}
	return string(pr.State)
}

// byLine renders "by @author · opened <relative> · updated <relative>",
// bolding the author's login when it is the viewer's own.
func byLine(pr *model.PullRequest, viewerLogin string) []widget.Span {
	authorStyle := theme.Base
	if pr.Author.Login != "" && pr.Author.Login == viewerLogin {
		authorStyle = authorStyle.Bold(true)
	}
	return []widget.Span{
		{Text: "by @" + pr.Author.Login, Style: authorStyle},
		{Text: withRelativeTime(" · opened", pr.CreatedAt), Style: theme.Muted},
		{Text: withRelativeTime(" · updated", pr.UpdatedAt), Style: theme.Muted},
	}
}

// labelSpans renders every label, each coloured by theme.ColorFor(name).
func labelSpans(labels []model.Label) []widget.Span {
	spans := make([]widget.Span, 0, max(0, len(labels)*2-1))
	for i, l := range labels {
		if i > 0 {
			spans = append(spans, widget.Span{Text: " ", Style: theme.Muted})
		}
		spans = append(spans, widget.Span{Text: l.Name, Style: theme.Base.Foreground(theme.ColorFor(l.Name))})
	}
	return spans
}

// reviewSummaryLine renders "review: <decision>  <reviewers>". Either half
// may be absent (no decision reached yet, or no reviewers/reviews at all);
// nil is returned when there is nothing at all to show.
func reviewSummaryLine(pr *model.PullRequest, viewerLogin string) []widget.Span {
	reviewers := reviewerSpans(pr, viewerLogin)
	if pr.ReviewDecision == "" && len(reviewers) == 0 {
		return nil
	}
	var spans []widget.Span
	if pr.ReviewDecision != "" {
		spans = append(spans, widget.Span{Text: "review: " + string(pr.ReviewDecision), Style: theme.Base})
		if len(reviewers) > 0 {
			spans = append(spans, widget.Span{Text: "  ", Style: theme.Base})
		}
	}
	spans = append(spans, reviewers...)
	return spans
}

// statsLine renders "+adds −dels · N files · M threads (K unresolved)"; the
// thread count is omitted entirely when there are no review threads.
func statsLine(pr *model.PullRequest) []widget.Span {
	text := fmt.Sprintf("+%d −%d · %d files", pr.Additions, pr.Deletions, pr.ChangedFiles)
	if n := len(pr.ReviewThreads); n > 0 {
		unresolved := 0
		for _, t := range pr.ReviewThreads {
			if !t.IsResolved {
				unresolved++
			}
		}
		text += fmt.Sprintf(" · %d threads (%d unresolved)", n, unresolved)
	}
	return []widget.Span{{Text: text, Style: theme.Muted}}
}

// wrapPlain word-wraps text to width using widget.WrapText (which treats
// every character literally, unlike tview.WordWrap — see its doc comment
// — and existing newlines as hard breaks), rendering every resulting line
// in a single style.
func wrapPlain(text string, width int, style tcell.Style) [][]widget.Span {
	if width <= 0 {
		width = 1
	}
	wrapped := widget.WrapText(text, width)
	lines := make([][]widget.Span, len(wrapped))
	for i, l := range wrapped {
		lines[i] = []widget.Span{{Text: l, Style: style}}
	}
	return lines
}

// chopChars splits text into fixed-width chunks of width runes each,
// without any word-boundary search — used for fenced code blocks, where
// word-wrapping would reflow content in a way that misrepresents the
// original source.
func chopChars(text string, width int) [][]widget.Span {
	if width <= 0 {
		width = 1
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return [][]widget.Span{{{Text: "", Style: theme.Muted}}}
	}
	var lines [][]widget.Span
	for i := 0; i < len(runes); i += width {
		end := i + width
		if end > len(runes) {
			end = len(runes)
		}
		lines = append(lines, []widget.Span{{Text: string(runes[i:end]), Style: theme.Muted}})
	}
	return lines
}

// reactionLine renders a single line summarising groups (for example "👍 2
// 🎉 1"), bolding any group the viewer has themselves reacted with
// (ViewerHasReacted) so it is distinguishable from one they have not, or
// nil when there is nothing to show.
func reactionLine(groups []model.ReactionGroup) []widget.Span {
	var spans []widget.Span
	for _, g := range groups {
		if g.Count <= 0 {
			continue
		}
		if len(spans) > 0 {
			spans = append(spans, widget.Span{Text: "  ", Style: theme.Muted})
		}
		style := theme.Muted
		if g.ViewerHasReacted {
			style = style.Bold(true)
		}
		spans = append(spans, widget.Span{Text: fmt.Sprintf("%s %d", g.Content.Emoji(), g.Count), Style: style})
	}
	return spans
}
