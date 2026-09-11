package ui

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// conversationBlocks renders one block per timeline item, in order:
// comments and reviews are selectable (their own URL opens in the
// browser), commits and events are muted, informational rows. A
// TimelineItem whose payload is nil despite its Kind (a malformed cache
// entry or mapping bug) renders as a muted, unselectable "(unavailable)"
// placeholder instead of panicking the draw path — logged once per item at
// Error level via logger, consistent with rows.go's policy for an orphan
// list row; a nil logger discards it (tests that do not care can pass
// one).
func conversationBlocks(pr *model.PullRequest, logger *slog.Logger) []widget.Block {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	blocks := make([]widget.Block, 0, len(pr.Timeline))
	for i, item := range pr.Timeline {
		id := fmt.Sprintf("timeline:%d", i)
		switch {
		case item.Kind == model.TimelineKindIssueComment && item.IssueComment != nil:
			blocks = append(blocks, issueCommentBlock(id, item.IssueComment))
		case item.Kind == model.TimelineKindReview && item.Review != nil:
			blocks = append(blocks, reviewBlock(id, item.Review))
		case item.Kind == model.TimelineKindCommit && item.Commit != nil:
			blocks = append(blocks, commitBlock(id, item.Commit))
		case item.Kind == model.TimelineKindEvent && item.Event != nil:
			blocks = append(blocks, eventBlock(id, item.Event))
		default:
			logger.Error("timeline item has a nil payload for its kind", "index", i, "kind", item.Kind)
			blocks = append(blocks, unavailableBlock(id))
		}
	}
	return blocks
}

// unavailableBlock renders a muted, unselectable placeholder for a
// timeline item that could not be rendered (see conversationBlocks).
func unavailableBlock(id string) widget.Block {
	return widget.Block{
		ID: id,
		Build: func(int) [][]widget.Span {
			return [][]widget.Span{{{Text: "(unavailable)", Style: theme.Muted}}}
		},
	}
}

// issueCommentBlock renders "@author commented <relative>" followed by the
// wrapped comment body and, when present, a reaction summary line.
func issueCommentBlock(id string, c *model.IssueComment) widget.Block {
	return widget.Block{
		ID:         id,
		Selectable: true,
		URL:        c.URL,
		Build: func(width int) [][]widget.Span {
			lines := [][]widget.Span{{{Text: withRelativeTime("@"+c.Author.Login+" commented", c.CreatedAt), Style: theme.Base}}}
			if strings.TrimSpace(c.Body) != "" {
				lines = append(lines, wrapPlain(c.Body, width, theme.Base)...)
			}
			if reaction := reactionLine(c.ReactionGroups); reaction != nil {
				lines = append(lines, reaction)
			}
			return lines
		},
	}
}

// reviewVerb renders review.State as the past-tense verb shown in the
// timeline header line.
func reviewVerb(state model.ReviewState) string {
	switch state {
	case model.ReviewStateApproved:
		return "approved"
	case model.ReviewStateChangesRequested:
		return "requested changes"
	case model.ReviewStateCommented:
		return "commented"
	case model.ReviewStateDismissed:
		return "dismissed"
	default:
		return strings.ToLower(strings.ReplaceAll(string(state), "_", " "))
	}
}

// reviewBlock renders "@author <verb> <relative>" followed by the review's
// body, if any, and a reaction summary line when present.
func reviewBlock(id string, r *model.Review) widget.Block {
	return widget.Block{
		ID:         id,
		Selectable: true,
		URL:        r.URL,
		Build: func(width int) [][]widget.Span {
			lines := [][]widget.Span{{{Text: withRelativeTime("@"+r.Author.Login+" "+reviewVerb(r.State), r.SubmittedAt), Style: theme.Base}}}
			if strings.TrimSpace(r.Body) != "" {
				lines = append(lines, wrapPlain(r.Body, width, theme.Base)...)
			}
			if reaction := reactionLine(r.ReactionGroups); reaction != nil {
				lines = append(lines, reaction)
			}
			return lines
		},
	}
}

// commitOIDShort returns oid's first 7 characters (GitHub's usual "short
// SHA" length), or the whole string if it is shorter than that.
func commitOIDShort(oid string) string {
	if len(oid) <= 7 {
		return oid
	}
	return oid[:7]
}

// commitHeadline returns message's first line: a commit's subsequent lines
// are its full description, which the timeline row does not show.
func commitHeadline(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return message[:i]
	}
	return message
}

// commitBlock renders a pushed commit as a single muted, non-selectable
// row: "⋅ <7-char oid> <message headline> — @author".
func commitBlock(id string, c *model.Commit) widget.Block {
	text := fmt.Sprintf("⋅ %s %s — @%s", commitOIDShort(c.OID), commitHeadline(c.Message), c.Author.Login)
	return widget.Block{
		ID: id,
		Build: func(int) [][]widget.Span {
			return [][]widget.Span{{{Text: text, Style: theme.Muted}}}
		},
	}
}

// eventVerb renders a readable description of a non-comment timeline
// event. internal/gh (see pull_request_map.go's timelineEvent, called from
// mapTimelineItem's event-type switch) already renders Detail as a
// complete, human-readable phrase for every
// event type that carries more than a bare actor/timestamp — for example
// `requested a review from bob`, `renamed from "Old" to "New"`, or
// `force-pushed to abc1234` — so it is used alone, never appended after a
// verb of our own (doing both would double-print, e.g. "requested review
// requested a review from bob"). Only the handful of event types gh always
// renders with an empty Detail get a verb here; an event type this
// package does not recognize at all (Detail empty, not one of the cases
// below) falls back to the raw GraphQL type name, so gprt never renders a
// blank line for one it does not know about yet.
func eventVerb(e *model.Event) string {
	if e.Detail != "" {
		return e.Detail
	}
	switch e.Type {
	case "ReadyForReviewEvent":
		return "marked ready for review"
	case "ConvertToDraftEvent":
		return "converted to draft"
	case "MergedEvent":
		return "merged"
	case "ClosedEvent":
		return "closed"
	case "ReopenedEvent":
		return "reopened"
	default:
		return e.Type
	}
}

// eventBlock renders a non-comment timeline event as a single muted,
// non-selectable row: "⋅ @actor <verb/detail> <relative>".
func eventBlock(id string, e *model.Event) widget.Block {
	text := withRelativeTime(fmt.Sprintf("⋅ @%s %s", e.Actor.Login, eventVerb(e)), e.At)
	return widget.Block{
		ID: id,
		Build: func(int) [][]widget.Span {
			return [][]widget.Span{{{Text: text, Style: theme.Muted}}}
		},
	}
}
