package gh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_AddIssueComment_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addComment": {
				"commentEdge": {
					"node": {
						"id": "IC_1",
						"author": {"login": "octocat"},
						"body": "hello",
						"createdAt": "2026-09-12T00:00:00Z",
						"updatedAt": "2026-09-12T00:00:00Z",
						"url": "https://github.com/o/r/pull/1#issuecomment-1",
						"reactionGroups": [],
						"viewerCanUpdate": true,
						"viewerCanDelete": true
					}
				}
			},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-12T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotQuery = body.Query
		gotVars = body.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	comment, rl, err := c.AddIssueComment(context.Background(), "PR_1", "hello")
	if err != nil {
		t.Fatalf("AddIssueComment() error = %v", err)
	}

	if !containsAll(gotQuery, "mutation AddComment", "addComment", "subjectId", "commentEdge") {
		t.Errorf("request query = %q, want the AddComment mutation document", gotQuery)
	}
	if gotVars["id"] != "PR_1" || gotVars["body"] != "hello" {
		t.Errorf("request variables = %+v, want id=PR_1 body=hello", gotVars)
	}

	wantCreated, _ := time.Parse(time.RFC3339, "2026-09-12T00:00:00Z")
	want := model.IssueComment{
		ID:              "IC_1",
		Author:          model.User{Login: "octocat"},
		Body:            "hello",
		CreatedAt:       wantCreated,
		UpdatedAt:       wantCreated,
		URL:             "https://github.com/o/r/pull/1#issuecomment-1",
		ViewerCanUpdate: true,
		ViewerCanDelete: true,
	}
	if comment.ID != want.ID || comment.Author != want.Author || comment.Body != want.Body ||
		!comment.CreatedAt.Equal(want.CreatedAt) || !comment.UpdatedAt.Equal(want.UpdatedAt) ||
		comment.URL != want.URL || comment.ViewerCanUpdate != want.ViewerCanUpdate ||
		comment.ViewerCanDelete != want.ViewerCanDelete {
		t.Errorf("AddIssueComment() comment = %+v, want %+v", comment, want)
	}
	if !rl.Known || rl.Remaining != 4998 {
		t.Errorf("AddIssueComment() rate limit = %+v, want Remaining=4998, Known=true", rl)
	}
}

func TestClient_AddIssueComment_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "Body can't be blank"}`))
	})
	defer srv.Close()

	_, _, err := c.AddIssueComment(context.Background(), "PR_1", "")
	if err == nil {
		t.Fatal("AddIssueComment() error = nil, want an error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("AddIssueComment() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindValidation {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindValidation)
	}
	if ghErr.Message != "Body can't be blank" {
		t.Errorf("Message = %q, want %q", ghErr.Message, "Body can't be blank")
	}
}

func TestClient_UpdateIssueComment_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"updateIssueComment": {
				"issueComment": {
					"id": "IC_1",
					"author": {"login": "octocat"},
					"body": "edited",
					"createdAt": "2026-09-12T00:00:00Z",
					"updatedAt": "2026-09-12T00:05:00Z",
					"url": "https://github.com/o/r/pull/1#issuecomment-1",
					"reactionGroups": [],
					"viewerCanUpdate": true,
					"viewerCanDelete": true
				}
			},
			"rateLimit": {"remaining": 4997, "resetAt": "2026-09-12T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotVars = body.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	comment, rl, err := c.UpdateIssueComment(context.Background(), "IC_1", "edited")
	if err != nil {
		t.Fatalf("UpdateIssueComment() error = %v", err)
	}
	if gotVars["id"] != "IC_1" || gotVars["body"] != "edited" {
		t.Errorf("request variables = %+v, want id=IC_1 body=edited", gotVars)
	}
	if comment.Body != "edited" {
		t.Errorf("UpdateIssueComment() comment.Body = %q, want %q", comment.Body, "edited")
	}
	if !rl.Known || rl.Remaining != 4997 {
		t.Errorf("UpdateIssueComment() rate limit = %+v, want Remaining=4997, Known=true", rl)
	}
}

func TestClient_UpdateIssueComment_AuthError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	_, _, err := c.UpdateIssueComment(context.Background(), "IC_1", "edited")
	if err == nil {
		t.Fatal("UpdateIssueComment() error = nil, want an error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("UpdateIssueComment() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindAuth)
	}
}

func TestClient_DeleteIssueComment_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"deleteIssueComment": {"clientMutationId": null},
			"rateLimit": {"remaining": 4996, "resetAt": "2026-09-12T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery = body.Query
		gotVars = body.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	rl, err := c.DeleteIssueComment(context.Background(), "IC_1")
	if err != nil {
		t.Fatalf("DeleteIssueComment() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation DeleteIssueComment", "deleteIssueComment") {
		t.Errorf("request query = %q, want the DeleteIssueComment mutation document", gotQuery)
	}
	if gotVars["id"] != "IC_1" {
		t.Errorf("request variables = %+v, want id=IC_1", gotVars)
	}
	if !rl.Known || rl.Remaining != 4996 {
		t.Errorf("DeleteIssueComment() rate limit = %+v, want Remaining=4996, Known=true", rl)
	}
}

func TestClient_DeleteIssueComment_NotFoundError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	_, err := c.DeleteIssueComment(context.Background(), "IC_missing")
	if err == nil {
		t.Fatal("DeleteIssueComment() error = nil, want an error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("DeleteIssueComment() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindNotFound {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindNotFound)
	}
}

// containsAll reports whether s contains every one of substrs.
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
