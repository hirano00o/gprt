package gh

import (
	"context"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_UpdatePullRequest_SendsExplicitNullForOmittedFields(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"updatePullRequest": {
				"pullRequest": {
					"title": "New title",
					"body": "",
					"baseRefName": "main",
					"labels": {"nodes": []},
					"updatedAt": "2026-09-14T02:00:00Z"
				}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	title := "New title"
	pr, err := c.UpdatePullRequest(context.Background(), "PR_1", UpdatePullRequestInput{Title: &title})
	if err != nil {
		t.Fatalf("UpdatePullRequest() error = %v", err)
	}
	if gotVars["id"] != "PR_1" || gotVars["title"] != "New title" {
		t.Errorf("request variables = %+v, want id=PR_1 title=%q", gotVars, "New title")
	}
	for _, key := range []string{"body", "baseRefName", "labelIds"} {
		if v, ok := gotVars[key]; !ok || v != nil {
			t.Errorf("request variables[%s] = %v, want an explicit null for an omitted field", key, v)
		}
	}
	if pr.Title != "New title" || pr.BaseRefName != "main" {
		t.Errorf("UpdatePullRequest() = %+v", pr)
	}
}

func TestClient_UpdatePullRequest_SendsLabelIDsAndBody(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"updatePullRequest": {
				"pullRequest": {
					"title": "t", "body": "new body", "baseRefName": "main",
					"labels": {"nodes": [{"id": "LA_1", "name": "bug", "color": "d73a4a"}]},
					"updatedAt": "2026-09-14T02:00:00Z"
				}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	body := "new body"
	labelIDs := []string{"LA_1"}
	pr, err := c.UpdatePullRequest(context.Background(), "PR_1", UpdatePullRequestInput{Body: &body, LabelIDs: &labelIDs})
	if err != nil {
		t.Fatalf("UpdatePullRequest() error = %v", err)
	}
	if gotVars["body"] != "new body" {
		t.Errorf("request variables[body] = %v, want %q", gotVars["body"], "new body")
	}
	gotLabelIDs, ok := gotVars["labelIds"].([]any)
	if !ok || len(gotLabelIDs) != 1 || gotLabelIDs[0] != "LA_1" {
		t.Errorf("request variables[labelIds] = %v, want [LA_1]", gotVars["labelIds"])
	}
	if len(pr.Labels) != 1 || pr.Labels[0].ID != "LA_1" {
		t.Errorf("UpdatePullRequest() Labels = %+v", pr.Labels)
	}
}

func TestClient_UpdatePullRequest_SendsEmptyLabelIDsToClear(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"updatePullRequest": {
				"pullRequest": {"title": "t", "body": "", "baseRefName": "main", "labels": {"nodes": []}, "updatedAt": "2026-09-14T02:00:00Z"}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	emptyLabelIDs := []string{}
	_, err := c.UpdatePullRequest(context.Background(), "PR_1", UpdatePullRequestInput{LabelIDs: &emptyLabelIDs})
	if err != nil {
		t.Fatalf("UpdatePullRequest() error = %v", err)
	}
	gotLabelIDs, ok := gotVars["labelIds"].([]any)
	if !ok || len(gotLabelIDs) != 0 {
		t.Errorf("request variables[labelIds] = %v, want an empty (non-nil) list to clear labels", gotVars["labelIds"])
	}
}

func TestClient_RequestReviewers_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"requestReviews": {
				"pullRequest": {
					"reviewRequests": {
						"nodes": [
							{"asCodeOwner": false, "requestedReviewer": {"__typename": "User", "id": "U_1", "login": "bob"}},
							{"asCodeOwner": false, "requestedReviewer": {"__typename": "Team", "id": "T_1", "slug": "core"}}
						]
					}
				}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	reviewers, err := c.RequestReviewers(context.Background(), "PR_1", []string{"U_1"}, []string{"T_1"}, false)
	if err != nil {
		t.Fatalf("RequestReviewers() error = %v", err)
	}
	if gotVars["union"] != false {
		t.Errorf("request variables[union] = %v, want false", gotVars["union"])
	}
	want := []model.Reviewer{
		{ID: "U_1", Login: "bob", Kind: model.ReviewerKindUser},
		{ID: "T_1", Login: "core", Kind: model.ReviewerKindTeam},
	}
	if len(reviewers) != 2 || reviewers[0] != want[0] || reviewers[1] != want[1] {
		t.Errorf("RequestReviewers() = %+v, want %+v", reviewers, want)
	}
}

// TestClient_RequestReviewers_NilSlicesSendEmptyArraysNotNull covers
// https://github.com/cli/cli/issues/7721: GitHub's requestReviews treats a
// null userIds/teamIds as "leave this kind of reviewer unspecified" but an
// empty array as "clear every reviewer of that kind". A nil Go slice
// (indistinguishable at the call site from an explicitly empty one) must
// therefore never round-trip to JSON null, or a caller asking to clear all
// reviewers of a kind would silently no-op instead.
func TestClient_RequestReviewers_NilSlicesSendEmptyArraysNotNull(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"requestReviews": {"pullRequest": {"reviewRequests": {"nodes": []}}}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	if _, err := c.RequestReviewers(context.Background(), "PR_1", nil, nil, false); err != nil {
		t.Fatalf("RequestReviewers() error = %v", err)
	}

	for _, key := range []string{"userIds", "teamIds"} {
		v, ok := gotVars[key].([]any)
		if !ok || v == nil {
			t.Errorf("request variables[%s] = %#v, want a non-nil empty array, not null", key, gotVars[key])
			continue
		}
		if len(v) != 0 {
			t.Errorf("request variables[%s] = %#v, want empty", key, v)
		}
	}
}

func TestClient_MarkReadyForReview_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"markPullRequestReadyForReview": {"pullRequest": {"isDraft": false}}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	isDraft, err := c.MarkReadyForReview(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("MarkReadyForReview() error = %v", err)
	}
	if isDraft {
		t.Errorf("MarkReadyForReview() isDraft = true, want false")
	}
}

func TestClient_ConvertToDraft_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"convertPullRequestToDraft": {"pullRequest": {"isDraft": true}}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	isDraft, err := c.ConvertToDraft(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("ConvertToDraft() error = %v", err)
	}
	if !isDraft {
		t.Errorf("ConvertToDraft() isDraft = false, want true")
	}
}
