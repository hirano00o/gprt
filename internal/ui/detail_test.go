package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// blockText joins every span's text in every line of a block built at
// width, for substring assertions that do not care about styling.
func blockText(b widget.Block, width int) string {
	var sb strings.Builder
	for _, line := range b.Build(width) {
		for _, s := range line {
			sb.WriteString(s.Text)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func findBlock(blocks []widget.Block, id string) (widget.Block, bool) {
	for _, b := range blocks {
		if b.ID == id {
			return b, true
		}
	}
	return widget.Block{}, false
}

func TestBuildPRBlocksEmptyStateVariants(t *testing.T) {
	tests := []struct {
		name  string
		state store.DetailState
		want  string
	}{
		{"nothing selected", store.DetailState{}, "Select a pull request"},
		{"loading", store.DetailState{Loading: true}, "Loading"},
		{"error", store.DetailState{Err: errors.New("boom")}, "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks := buildPRBlocks(nil, tc.state, "octocat", theme.Unicode(), nil)
			if len(blocks) != 1 {
				t.Fatalf("len(blocks) = %d, want 1", len(blocks))
			}
			if got := blockText(blocks[0], 80); !strings.Contains(got, tc.want) {
				t.Errorf("empty-state block = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func fixtureHeaderPR() *model.PullRequest {
	return &model.PullRequest{
		Ref:              model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}, Number: 42},
		Title:            "Add widget support",
		Author:           model.User{Login: "alice"},
		State:            model.PRStateOpen,
		IsDraft:          false,
		MergeStateStatus: model.MergeStateStatusClean,
		BaseRefName:      "main",
		HeadRefName:      "alice/widgets",
		ReviewDecision:   model.ReviewDecisionReviewRequired,
		Labels:           []model.Label{{Name: "backend"}, {Name: "urgent"}},
		ReviewRequests:   []model.Reviewer{{Login: "bob", Kind: model.ReviewerKindUser}},
		LatestReviews:    []model.Review{{Author: model.User{Login: "carol"}, State: model.ReviewStateApproved}},
		Additions:        10,
		Deletions:        3,
		ChangedFiles:     2,
		ReviewThreads: []model.ReviewThread{
			{ID: "t1", IsResolved: true},
			{ID: "t2", IsResolved: false},
		},
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now().Add(-5 * time.Minute),
		URL:       "https://github.com/acme/widgets/pull/42",
	}
}

func TestHeaderBlockRendersMetadata(t *testing.T) {
	pr := fixtureHeaderPR()
	b := headerBlock(pr, store.DetailState{}, "alice", theme.Unicode())

	if !b.Selectable {
		t.Error("header block must be selectable so `o` can open the PR URL")
	}
	if b.URL != pr.URL {
		t.Errorf("header block URL = %q, want the PR URL %q", b.URL, pr.URL)
	}

	text := blockText(b, 120)
	for _, want := range []string{
		"#42 Add widget support",
		"OPEN",
		"CLEAN",
		"main",
		"alice/widgets",
		"alice",
		"backend",
		"urgent",
		"REVIEW_REQUIRED",
		"bob",
		"carol",
		"+10",
		"3",
		"2 files",
		"2 threads",
		"1 unresolved",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("header block text = %q, want it to contain %q", text, want)
		}
	}
}

func TestHeaderBlockDraftLabel(t *testing.T) {
	pr := fixtureHeaderPR()
	pr.IsDraft = true
	text := blockText(headerBlock(pr, store.DetailState{}, "alice", theme.Unicode()), 120)
	if !strings.Contains(text, "DRAFT") {
		t.Errorf("draft PR header = %q, want it to contain DRAFT", text)
	}
}

func TestHeaderBlockShowsStaleMarker(t *testing.T) {
	pr := fixtureHeaderPR()
	text := blockText(headerBlock(pr, store.DetailState{Stale: true}, "alice", theme.Unicode()), 120)
	if !strings.Contains(text, "cached") {
		t.Errorf("stale header = %q, want it to mention being cached", text)
	}
}

func TestLabelSpansEmptyDoesNotPanic(t *testing.T) {
	// headerBlock only calls labelSpans when len(pr.Labels) > 0; this
	// guards labelSpans itself against a negative slice capacity
	// (len(labels)*2-1 with zero labels) if that guard is ever removed
	// or bypassed by a future caller.
	if got := labelSpans(nil); len(got) != 0 {
		t.Errorf("labelSpans(nil) = %#v, want empty", got)
	}
}

func TestHeaderBlockShowsWarnings(t *testing.T) {
	pr := fixtureHeaderPR()
	state := store.DetailState{Warnings: []string{"3 timeline items omitted"}}
	text := blockText(headerBlock(pr, state, "alice", theme.Unicode()), 120)
	if !strings.Contains(text, "3 timeline items omitted") {
		t.Errorf("header with warnings = %q, want it to include the warning text", text)
	}
}

// TestHeaderBlockShowsStandingError covers both cases where CurrentPR()
// still shows a previously loaded pull request while DetailState().Err
// stands: a background refresh (auto-refresh or "R") that failed after
// showing cached data, and a reload that failed outright. Without this,
// the header (and so the whole PR tab, since it is only ever nil when
// CurrentPR() itself is nil) would give no indication that the data on
// screen may now be out of date.
func TestHeaderBlockShowsStandingError(t *testing.T) {
	pr := fixtureHeaderPR()
	state := store.DetailState{Err: errors.New("network unreachable")}
	b := headerBlock(pr, state, "alice", theme.Unicode())

	var errLine []widget.Span
	for _, line := range b.Build(120) {
		for _, s := range line {
			if strings.Contains(s.Text, "network unreachable") {
				errLine = line
			}
		}
	}
	if errLine == nil {
		t.Fatalf("header block text = %q, want a line mentioning the standing error", blockText(b, 120))
	}
	for _, s := range errLine {
		if s.Style == theme.Error {
			return
		}
	}
	t.Errorf("error line %+v has no theme.Error-styled span", errLine)
}

func TestDescriptionBlockBlankBody(t *testing.T) {
	pr := &model.PullRequest{Body: "", URL: "https://example.com/pr/1"}
	b := descriptionBlock(pr)
	if !b.Selectable || b.URL != pr.URL {
		t.Errorf("description block = %+v, want selectable with the PR URL", b)
	}
	if got := blockText(b, 80); !strings.Contains(got, "no description") {
		t.Errorf("blank body description = %q, want a (no description) placeholder", got)
	}
}

// TestDescriptionBlockOnlyFenceMarkersFallsBackToNoDescription guards
// against a zero-height selectable row: a body consisting only of fence
// markers (no lines of actual content, inside or outside the fence) makes
// every line in renderMarkdownLite's loop toggle inFence without ever
// appending anything, so the naive body-is-blank check (which only looks
// at whitespace) does not catch it.
func TestDescriptionBlockOnlyFenceMarkersFallsBackToNoDescription(t *testing.T) {
	pr := &model.PullRequest{Body: "```\n```"}
	lines := descriptionBlock(pr).Build(80)
	if len(lines) == 0 {
		t.Fatal("descriptionBlock produced zero lines for a fence-only body; a selectable row must never have zero height")
	}
	if got := blockText(descriptionBlock(pr), 80); !strings.Contains(got, "no description") {
		t.Errorf("fence-only body description = %q, want a (no description) placeholder", got)
	}
}

func TestDescriptionBlockHeadingIsBold(t *testing.T) {
	pr := &model.PullRequest{Body: "# Summary\nSome details here."}
	lines := descriptionBlock(pr).Build(80)
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatal("expected at least one rendered line")
	}
	_, _, attrs := lines[0][0].Style.Decompose()
	if attrs&tcell.AttrBold == 0 {
		t.Errorf("heading line style = %v, want bold", lines[0][0].Style)
	}
}

func TestDescriptionBlockCodeFenceDoesNotWordWrap(t *testing.T) {
	body := "before\n```\n" + strings.Repeat("x", 50) + "\n```\nafter"
	pr := &model.PullRequest{Body: body}
	lines := descriptionBlock(pr).Build(10)

	var codeLineText string
	for _, line := range lines {
		var sb strings.Builder
		for _, s := range line {
			sb.WriteString(s.Text)
		}
		if strings.Contains(sb.String(), "xxxxxxxxxx") {
			codeLineText = sb.String()
			break
		}
	}
	if codeLineText == "" {
		t.Fatal("expected to find a rendered code-fence line")
	}
	// Word-wrap would have broken the 50-x run into 10-column pieces too,
	// but the point of "no wrapping-induced reflow" is that the raw content
	// is preserved as a single chopped chunk without word-boundary
	// searching; a character-chopped line at exactly the given width is the
	// expected shape here.
	if len([]rune(codeLineText)) != 10 {
		t.Errorf("code-fence line length = %d, want exactly the wrap width (10)", len([]rune(codeLineText)))
	}
}

func TestDescriptionBlockKeepsBullets(t *testing.T) {
	pr := &model.PullRequest{Body: "- first\n* second"}
	text := blockText(descriptionBlock(pr), 80)
	if !strings.Contains(text, "- first") || !strings.Contains(text, "* second") {
		t.Errorf("description with bullets = %q, want the bullet markers kept", text)
	}
}

func TestDescriptionBlockReactions(t *testing.T) {
	pr := &model.PullRequest{
		Body:           "hello",
		ReactionGroups: []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 2}},
	}
	text := blockText(descriptionBlock(pr), 80)
	if !strings.Contains(text, "2") || !strings.Contains(text, model.ReactionThumbsUp.Emoji()) {
		t.Errorf("description with reactions = %q, want the thumbs-up count rendered", text)
	}
}

// TestReactionLineBoldsAGroupTheViewerReactedTo covers reactionLine
// distinguishing a reaction the viewer has themselves added from one they
// have not, via style rather than extra text (blockText's own
// text-only join cannot see that): the viewer's own group renders bold,
// every other group does not.
func TestReactionLineBoldsAGroupTheViewerReactedTo(t *testing.T) {
	spans := reactionLine([]model.ReactionGroup{
		{Content: model.ReactionThumbsUp, Count: 2, ViewerHasReacted: true},
		{Content: model.ReactionHooray, Count: 1},
	})

	var gotThumbsUp, gotHooray bool
	for _, s := range spans {
		switch {
		case strings.Contains(s.Text, model.ReactionThumbsUp.Emoji()):
			gotThumbsUp = true
			if s.Style != theme.Muted.Bold(true) {
				t.Errorf("thumbs-up span style = %+v, want theme.Muted.Bold(true) (ViewerHasReacted)", s.Style)
			}
		case strings.Contains(s.Text, model.ReactionHooray.Emoji()):
			gotHooray = true
			if s.Style != theme.Muted {
				t.Errorf("hooray span style = %+v, want plain theme.Muted (not the viewer's own reaction)", s.Style)
			}
		}
	}
	if !gotThumbsUp || !gotHooray {
		t.Fatalf("reactionLine spans = %+v, want both a thumbs-up and a hooray span", spans)
	}
}

func TestReactionLineOmitsZeroCountGroups(t *testing.T) {
	if spans := reactionLine([]model.ReactionGroup{{Content: model.ReactionEyes, Count: 0}}); spans != nil {
		t.Errorf("reactionLine with only a zero-count group = %+v, want nil", spans)
	}
}

func TestChecksBlocksSummaryLine(t *testing.T) {
	pr := &model.PullRequest{Checks: []model.Check{
		{Name: "build", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionSuccess},
		{Name: "lint", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionFailure, URL: "https://example.com/lint"},
		{Name: "deploy", Status: model.CheckStatusInProgress},
	}}
	blocks := checksBlocks(pr, theme.Unicode())
	summary, ok := findBlock(blocks, "checks-summary")
	if !ok {
		t.Fatal("no checks-summary block")
	}
	text := blockText(summary, 80)
	for _, want := range []string{"1 passed", "1 failed", "1 pending"} {
		if !strings.Contains(text, want) {
			t.Errorf("checks summary = %q, want it to contain %q", text, want)
		}
	}
}

func TestChecksBlocksSummaryLineIncludesSkippedAndOmitsZeroCounts(t *testing.T) {
	pr := &model.PullRequest{Checks: []model.Check{
		{Name: "build", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionSuccess},
		{Name: "build2", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionSuccess},
		{Name: "docs", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionSkipped},
	}}
	summary, ok := findBlock(checksBlocks(pr, theme.Unicode()), "checks-summary")
	if !ok {
		t.Fatal("no checks-summary block")
	}
	text := blockText(summary, 80)
	if !strings.Contains(text, "2 passed") {
		t.Errorf("checks summary = %q, want it to contain \"2 passed\"", text)
	}
	if !strings.Contains(text, "1 skipped") {
		t.Errorf("checks summary = %q, want it to contain \"1 skipped\"", text)
	}
	if strings.Contains(text, "failed") || strings.Contains(text, "pending") {
		t.Errorf("checks summary = %q, want zero-count categories (failed, pending) omitted", text)
	}
}

func TestChecksBlocksNoChecks(t *testing.T) {
	pr := &model.PullRequest{}
	blocks := checksBlocks(pr, theme.Unicode())
	summary, ok := findBlock(blocks, "checks-summary")
	if !ok {
		t.Fatal("no checks-summary block")
	}
	if got := blockText(summary, 80); !strings.Contains(got, "No checks") {
		t.Errorf("empty checks summary = %q, want \"No checks\"", got)
	}
	if len(blocks) != 1 {
		t.Errorf("len(blocks) = %d, want exactly the summary block when there are no checks", len(blocks))
	}
}

func TestChecksBlocksPerCheckRow(t *testing.T) {
	pr := &model.PullRequest{Checks: []model.Check{
		{Name: "lint", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionFailure, Workflow: "CI", IsRequired: true, URL: "https://example.com/lint"},
	}}
	blocks := checksBlocks(pr, theme.Unicode())
	row, ok := findBlock(blocks, "check:0")
	if !ok {
		t.Fatal("no check:0 block")
	}
	if !row.Selectable || row.URL != "https://example.com/lint" {
		t.Errorf("check row = %+v, want selectable with the check's URL", row)
	}
	text := blockText(row, 80)
	for _, want := range []string{"lint", "CI", "required"} {
		if !strings.Contains(text, want) {
			t.Errorf("check row text = %q, want it to contain %q", text, want)
		}
	}
}

func TestConversationBlocksIssueComment(t *testing.T) {
	pr := &model.PullRequest{Timeline: []model.TimelineItem{{
		Kind: model.TimelineKindIssueComment,
		IssueComment: &model.IssueComment{
			Author:         model.User{Login: "dave"},
			Body:           "thanks for the PR",
			CreatedAt:      time.Now().Add(-10 * time.Minute),
			ReactionGroups: []model.ReactionGroup{{Content: model.ReactionHeart, Count: 1}},
			URL:            "https://example.com/comment/1",
		},
	}}}
	blocks := conversationBlocks(pr, nil)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	b := blocks[0]
	if !b.Selectable || b.URL != "https://example.com/comment/1" {
		t.Errorf("issue comment block = %+v, want selectable with the comment URL", b)
	}
	text := blockText(b, 80)
	for _, want := range []string{"dave", "commented", "thanks for the PR", model.ReactionHeart.Emoji()} {
		if !strings.Contains(text, want) {
			t.Errorf("issue comment text = %q, want it to contain %q", text, want)
		}
	}
}

func TestConversationBlocksReviewVerbs(t *testing.T) {
	tests := []struct {
		state model.ReviewState
		want  string
	}{
		{model.ReviewStateApproved, "approved"},
		{model.ReviewStateChangesRequested, "requested changes"},
		{model.ReviewStateCommented, "commented"},
		{model.ReviewStateDismissed, "dismissed"},
	}
	for _, tc := range tests {
		t.Run(string(tc.state), func(t *testing.T) {
			pr := &model.PullRequest{Timeline: []model.TimelineItem{{
				Kind:   model.TimelineKindReview,
				Review: &model.Review{Author: model.User{Login: "erin"}, State: tc.state, Body: "lgtm", URL: "https://example.com/review/1"},
			}}}
			blocks := conversationBlocks(pr, nil)
			text := blockText(blocks[0], 80)
			if !strings.Contains(text, tc.want) {
				t.Errorf("review verb text = %q, want it to contain %q", text, tc.want)
			}
			if !strings.Contains(text, "lgtm") {
				t.Errorf("review text = %q, want the review body", text)
			}
			if !blocks[0].Selectable || blocks[0].URL != "https://example.com/review/1" {
				t.Errorf("review block = %+v, want selectable with the review URL", blocks[0])
			}
		})
	}
}

// TestConversationBlocksZeroTimestampsOmitRelativeTime guards
// issueCommentBlock/reviewBlock/eventBlock against a zero CreatedAt/
// SubmittedAt/At (for example a PENDING review, which has never been
// submitted) rendering relativeTime's enormous "2562047h ago" nonsense, or
// even just a dangling trailing space once relativeTime returns "".
func TestConversationBlocksZeroTimestampsOmitRelativeTime(t *testing.T) {
	pr := &model.PullRequest{Timeline: []model.TimelineItem{
		{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{Author: model.User{Login: "dave"}, Body: "hi"}},
		{Kind: model.TimelineKindReview, Review: &model.Review{Author: model.User{Login: "erin"}, State: model.ReviewStatePending}},
		{Kind: model.TimelineKindEvent, Event: &model.Event{Type: "MergedEvent", Actor: model.User{Login: "frank"}}},
	}}
	for i, b := range conversationBlocks(pr, nil) {
		lines := b.Build(80)
		header := lines[0][0].Text
		if strings.Contains(header, "ago") {
			t.Errorf("blocks[%d] header = %q, want no relative-time text for a zero timestamp", i, header)
		}
		if strings.HasSuffix(header, " ") {
			t.Errorf("blocks[%d] header = %q, want no trailing space when the relative time is omitted", i, header)
		}
	}
}

func TestConversationBlocksCommit(t *testing.T) {
	pr := &model.PullRequest{Timeline: []model.TimelineItem{{
		Kind:   model.TimelineKindCommit,
		Commit: &model.Commit{OID: "abcdef1234567890", Message: "Fix bug\n\nlonger body", Author: model.User{Login: "frank"}},
	}}}
	blocks := conversationBlocks(pr, nil)
	if blocks[0].Selectable {
		t.Error("a commit row must not be selectable")
	}
	text := blockText(blocks[0], 80)
	for _, want := range []string{"abcdef1", "Fix bug", "frank"} {
		if !strings.Contains(text, want) {
			t.Errorf("commit text = %q, want it to contain %q", text, want)
		}
	}
	if strings.Contains(text, "longer body") {
		t.Errorf("commit text = %q, want only the message headline, not the full body", text)
	}
}

// TestConversationBlocksEventVerbs uses Detail strings matching
// internal/gh/pull_request_map.go's actual rendered phrases exactly (see
// its timelineEvent/mapEvent switch): for every event type that carries a
// Detail, it is already a complete, human-readable phrase, not a fragment
// meant to be appended after a UI-side verb of our own — appending both
// would double-print (e.g. "requested review requested a review from
// bob"). Only the five event types gh always renders with an empty Detail
// (ReadyForReviewEvent, ConvertToDraftEvent, MergedEvent, ClosedEvent,
// ReopenedEvent) get a verb of eventVerb's own.
func TestConversationBlocksEventVerbs(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		detail string
		want   string
	}{
		{"ready for review", "ReadyForReviewEvent", "", "marked ready for review"},
		{"convert to draft", "ConvertToDraftEvent", "", "converted to draft"},
		{"merged", "MergedEvent", "", "merged"},
		{"closed", "ClosedEvent", "", "closed"},
		{"reopened", "ReopenedEvent", "", "reopened"},
		{"review requested", "ReviewRequestedEvent", "requested a review from bob", "requested a review from bob"},
		{"review request removed", "ReviewRequestRemovedEvent", "removed the review request for bob", "removed the review request for bob"},
		{"force pushed", "HeadRefForcePushedEvent", "force-pushed to abc1234", "force-pushed to abc1234"},
		{"renamed title", "RenamedTitleEvent", `renamed from "Old" to "New"`, `renamed from "Old" to "New"`},
		{"labeled", "LabeledEvent", "added the bug label", "added the bug label"},
		{"unlabeled", "UnlabeledEvent", "removed the bug label", "removed the bug label"},
		{"base ref changed", "BaseRefChangedEvent", "changed the base branch from main to develop", "changed the base branch from main to develop"},
		{"unknown type falls back to the raw type name", "SomeFutureEvent", "", "SomeFutureEvent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := &model.PullRequest{Timeline: []model.TimelineItem{{
				Kind:  model.TimelineKindEvent,
				Event: &model.Event{Type: tc.typ, Actor: model.User{Login: "grace"}, Detail: tc.detail},
			}}}
			blocks := conversationBlocks(pr, nil)
			if blocks[0].Selectable {
				t.Error("an event row must not be selectable")
			}
			text := blockText(blocks[0], 80)
			// The exact phrase must follow "@grace " directly, with
			// nothing else — such as a UI-side verb — in between; a
			// weaker substring-of-want check would not catch double
			// printing, since gh's own Detail phrase is itself still a
			// substring of "requested review requested a review from bob".
			wantPrefix := "@grace " + tc.want
			if !strings.Contains(text, wantPrefix) {
				t.Errorf("event text = %q, want it to contain %q immediately after the actor", text, wantPrefix)
			}
		})
	}
}

func TestConversationBlocksNilPayloadRendersUnavailableInsteadOfPanicking(t *testing.T) {
	pr := &model.PullRequest{Timeline: []model.TimelineItem{
		{Kind: model.TimelineKindIssueComment, IssueComment: nil},
		{Kind: model.TimelineKindReview, Review: nil},
		{Kind: model.TimelineKindCommit, Commit: nil},
		{Kind: model.TimelineKindEvent, Event: nil},
	}}

	blocks := conversationBlocks(pr, nil) // must not panic on a corrupt entry
	if len(blocks) != 4 {
		t.Fatalf("len(blocks) = %d, want 4 (one per timeline item, even a corrupt one)", len(blocks))
	}
	for i, b := range blocks {
		if b.Selectable {
			t.Errorf("blocks[%d] is selectable, want an unselectable placeholder for a corrupt item", i)
		}
		if got := blockText(b, 80); !strings.Contains(got, "unavailable") {
			t.Errorf("blocks[%d] text = %q, want it to mention being unavailable", i, got)
		}
	}
}
