package gh

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMapPullRequestDetailNormalizesCRLF guards against a Windows-authored
// pull request body, comment, review, or commit message headline leaking
// "\r\n" line endings into model.PullRequest: gprt's own rendering (word
// wrap, line-by-line Markdown-lite parsing) only ever splits on "\n",
// so a stray "\r" would otherwise survive as a literal, visible control
// character at the end of every wrapped line.
func TestMapPullRequestDetailNormalizesCRLF(t *testing.T) {
	raw := `{
		"body": "line1\r\nline2",
		"latestReviews": {"nodes": [{"body": "review1\r\nreview2"}]},
		"reviews": {"nodes": [{"body": "pending1\r\npending2"}]},
		"reviewThreads": {"nodes": [{"comments": {"nodes": [{"body": "comment1\r\ncomment2"}]}}]},
		"timelineItems": {"nodes": [
			{"__typename": "IssueComment", "body": "issue1\r\nissue2"},
			{"__typename": "PullRequestReview", "body": "reviewtl1\r\nreviewtl2"},
			{"__typename": "PullRequestCommit", "commit": {"messageHeadline": "headline1\r\nheadline2"}}
		]}
	}`
	var node pullRequestDetailNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	pr := mapPullRequestDetail("example.com", node, "octocat")

	got := map[string]string{
		"PR body":                pr.Body,
		"latest review":          pr.LatestReviews[0].Body,
		"pending review":         pr.PendingReview.Body,
		"review thread comment":  pr.ReviewThreads[0].Comments[0].Body,
		"timeline issue comment": pr.Timeline[0].IssueComment.Body,
		"timeline review":        pr.Timeline[1].Review.Body,
		"commit headline":        pr.Timeline[2].Commit.Message,
	}
	for name, text := range got {
		if strings.Contains(text, "\r") {
			t.Errorf("%s = %q, want CRLF normalized to a bare LF", name, text)
		}
		if !strings.Contains(text, "\n") {
			t.Errorf("%s = %q, want the line break itself preserved", name, text)
		}
	}
}
