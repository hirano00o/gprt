package store

import (
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func testPR() model.PullRequest {
	return model.PullRequest{
		ID:    "PR_1",
		Ref:   model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 42},
		Title: "Add the frobnicator",
		Author: model.User{
			Login: "alice",
		},
		Labels: []model.Label{{Name: "bug"}, {Name: "P1"}},
		ReviewRequests: []model.Reviewer{
			{Login: "bob", Kind: model.ReviewerKindUser},
			{Login: "core-team", Kind: model.ReviewerKindTeam},
		},
		UpdatedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
	}
}

func TestParsedFilter_Matches(t *testing.T) {
	pr := testPR()
	sec := model.Section{Name: "Mine", Kind: model.SectionKindMine}

	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"empty filter matches everything", "", true},
		{"repo substring match", "repo:acme/wid", true},
		{"repo substring mismatch", "repo:other", false},
		{"repo match is case-insensitive", "repo:ACME/WIDGETS", true},
		{"author exact match", "author:alice", true},
		{"author mismatch", "author:bob", false},
		{"reviewer matches a user", "reviewer:bob", true},
		{"reviewer matches a team slug", "reviewer:core-team", true},
		{"reviewer mismatch", "reviewer:nobody", false},
		{"label match", "label:bug", true},
		{"label match is case-insensitive", "label:BUG", true},
		{"label mismatch", "label:missing", false},
		{"free text matches title", "frobnicator", true},
		{"free text matches title case-insensitively", "FROBNICATOR", true},
		{"free text matches number", "#42", true},
		{"free text matches repo", "widgets", true},
		{"free text matches author", "alice", true},
		{"free text mismatch", "nonexistent", false},
		{"multiple qualifiers all must match", "repo:acme/widgets author:alice", true},
		{"multiple qualifiers any mismatch fails", "repo:acme/widgets author:bob", false},
		{"multiple free-text tokens all must match", "frobnicator alice", true},
		{"multiple free-text tokens any mismatch fails", "frobnicator nobody", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := parseFilter(tc.raw)
			if got := f.matches(pr, sec); got != tc.want {
				t.Errorf("parseFilter(%q).matches() = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseFilter_State(t *testing.T) {
	f := parseFilter("state:closed some text")
	if f.state != "closed" {
		t.Errorf("state = %q, want %q", f.state, "closed")
	}
	if len(f.text) != 2 || f.text[0] != "some" || f.text[1] != "text" {
		t.Errorf("text = %v, want [some text]", f.text)
	}
}
