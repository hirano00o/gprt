package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_AddReaction_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addReaction": {
				"reactionGroups": [
					{"content": "THUMBS_UP", "viewerHasReacted": true, "reactors": {"totalCount": 3}},
					{"content": "HEART", "viewerHasReacted": false, "reactors": {"totalCount": 1}}
				]
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-13T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	groups, rl, err := c.AddReaction(context.Background(), "PR_1", model.ReactionThumbsUp)
	if err != nil {
		t.Fatalf("AddReaction() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation AddReaction", "addReaction", "subjectId") {
		t.Errorf("request query = %q, want the AddReaction mutation document", gotQuery)
	}
	if gotVars["subjectId"] != "PR_1" || gotVars["content"] != "THUMBS_UP" {
		t.Errorf("request variables = %+v, want subjectId=PR_1 content=THUMBS_UP", gotVars)
	}
	if len(groups) != 2 || groups[0].Content != model.ReactionThumbsUp || groups[0].Count != 3 || !groups[0].ViewerHasReacted {
		t.Errorf("AddReaction() groups = %+v", groups)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("AddReaction() rate limit = %+v, want Remaining=4999, Known=true", rl)
	}
}

func TestClient_AddReaction_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "Validation failed"}`))
	})
	defer srv.Close()

	_, _, err := c.AddReaction(context.Background(), "PR_1", model.ReactionThumbsUp)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Fatalf("AddReaction() error = %v, want a KindValidation *gh.Error", err)
	}
}

// TestClient_AddReaction_UnprocessableGraphQLError exercises the other
// shape GitHub uses for a validation failure besides an HTTP 422: an HTTP
// 200 response whose GraphQL "errors" array carries an item with
// type: "UNPROCESSABLE" (mirrors
// TestClient_SubmitReview_UnprocessableGraphQLError in review_test.go).
func TestClient_AddReaction_UnprocessableGraphQLError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data": null,
			"errors": [{"type": "UNPROCESSABLE", "message": "Content is not a valid reaction content"}]
		}`))
	})
	defer srv.Close()

	_, _, err := c.AddReaction(context.Background(), "PR_1", model.ReactionThumbsUp)
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("AddReaction() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindValidation {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindValidation)
	}
	if ghErr.Message != "Content is not a valid reaction content" {
		t.Errorf("Message = %q, want GitHub's message kept verbatim", ghErr.Message)
	}
}

func TestClient_RemoveReaction_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"removeReaction": {
				"reactionGroups": [
					{"content": "THUMBS_UP", "viewerHasReacted": false, "reactors": {"totalCount": 2}}
				]
			},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-13T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	groups, rl, err := c.RemoveReaction(context.Background(), "IC_1", model.ReactionThumbsUp)
	if err != nil {
		t.Fatalf("RemoveReaction() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation RemoveReaction", "removeReaction", "subjectId") {
		t.Errorf("request query = %q, want the RemoveReaction mutation document", gotQuery)
	}
	if gotVars["subjectId"] != "IC_1" || gotVars["content"] != "THUMBS_UP" {
		t.Errorf("request variables = %+v, want subjectId=IC_1 content=THUMBS_UP", gotVars)
	}
	if len(groups) != 1 || groups[0].ViewerHasReacted {
		t.Errorf("RemoveReaction() groups = %+v, want ViewerHasReacted=false", groups)
	}
	if !rl.Known || rl.Remaining != 4998 {
		t.Errorf("RemoveReaction() rate limit = %+v, want Remaining=4998, Known=true", rl)
	}
}

func TestClient_RemoveReaction_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "Validation failed"}`))
	})
	defer srv.Close()

	_, _, err := c.RemoveReaction(context.Background(), "IC_1", model.ReactionThumbsUp)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Fatalf("RemoveReaction() error = %v, want a KindValidation *gh.Error", err)
	}
}
