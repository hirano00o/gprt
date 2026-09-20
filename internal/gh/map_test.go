package gh

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func loadSearchFixtureNodes(t *testing.T) []searchPullRequestNode {
	t.Helper()
	data, err := os.ReadFile("testdata/search_page1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var envelope struct {
		Data searchResponse `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return envelope.Data.Search.Nodes
}

func TestMapPullRequest_AllReviewerUnionBranches(t *testing.T) {
	nodes := loadSearchFixtureNodes(t)
	if len(nodes) != 2 {
		t.Fatalf("fixture has %d nodes, want 2", len(nodes))
	}

	got := mapPullRequest("example.com", nodes[0])

	createdAt, _ := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2026-09-10T00:00:00Z")
	submittedAt, _ := time.Parse(time.RFC3339, "2026-09-09T00:00:00Z")

	want := model.PullRequest{
		ID: "PR_1",
		Ref: model.PRRef{
			Repo:   model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"},
			Number: 101,
		},
		RepositoryID:   "REPO_1",
		Title:          "Add feature A",
		Author:         model.User{Login: "alice"},
		State:          model.PRStateOpen,
		IsDraft:        false,
		ReviewDecision: model.ReviewDecisionReviewRequired,
		BaseRefName:    "main",
		HeadRefName:    "feature-a",
		HeadOID:        "abc123",
		Additions:      10,
		Deletions:      2,
		ChangedFiles:   3,
		Labels:         []model.Label{{Name: "bug", Color: "d73a4a"}},
		ReviewRequests: []model.Reviewer{
			{Login: "bob", Kind: model.ReviewerKindUser, AsCodeOwner: true},
			{Login: "core", Kind: model.ReviewerKindTeam, AsCodeOwner: false},
			{Login: "some-bot", Kind: model.ReviewerKindOther, AsCodeOwner: false},
			{Login: "some-mannequin", Kind: model.ReviewerKindOther, AsCodeOwner: false},
			// EnterpriseTeam has no name: the query deliberately does not
			// select its "slug" field (an inline fragment on
			// EnterpriseTeam is a validation error on GHES, where that
			// type does not exist at all), so only __typename identifies
			// it, producing Other with an empty login rather than the
			// default branch's generic handling.
			{Kind: model.ReviewerKindOther, AsCodeOwner: false},
			{Kind: model.ReviewerKindOther, AsCodeOwner: false}, // null reviewer
		},
		LatestReviews: []model.Review{
			{ID: "REV_1", Author: model.User{Login: "bob"}, State: model.ReviewStateApproved, SubmittedAt: submittedAt},
		},
		RollupState:     model.StatusStateSuccess,
		ViewerDidAuthor: true,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		URL:             "https://example.com/acme/widgets/pull/101",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("mapPullRequest() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMapPullRequest_EmptyRollupAndConnections(t *testing.T) {
	nodes := loadSearchFixtureNodes(t)
	got := mapPullRequest("example.com", nodes[1])

	// The fixture's reviewDecision is JSON null (the real, nullable shape
	// GitHub returns when no decision has been reached), not "": decoding
	// null into the string field must leave it at its zero value rather
	// than erroring or panicking.
	if got.ReviewDecision != "" {
		t.Errorf("ReviewDecision = %q, want empty (reviewDecision was null)", got.ReviewDecision)
	}
	if got.RollupState != "" {
		t.Errorf("RollupState = %q, want empty (no rollup reported)", got.RollupState)
	}
	if len(got.Labels) != 0 {
		t.Errorf("Labels = %v, want empty", got.Labels)
	}
	if len(got.ReviewRequests) != 0 {
		t.Errorf("ReviewRequests = %v, want empty", got.ReviewRequests)
	}
	if len(got.LatestReviews) != 0 {
		t.Errorf("LatestReviews = %v, want empty", got.LatestReviews)
	}
	if !got.IsDraft {
		t.Error("IsDraft = false, want true")
	}
}

func TestMapRollupState_NoCommits(t *testing.T) {
	if got := mapRollupState(nil); got != "" {
		t.Errorf("mapRollupState(nil) = %q, want empty", got)
	}
}
