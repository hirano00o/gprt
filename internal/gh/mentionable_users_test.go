package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func mentionableTestRepo() model.RepoRef {
	return model.RepoRef{Host: "example.com", Owner: "o", Name: "r"}
}

func TestClient_MentionableUsers_NullQueryWhenEmpty(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"repository": {
				"mentionableUsers": {
					"nodes": [
						{"id": "U_octocat", "login": "octocat", "name": "The Octocat"},
						{"id": "U_monalisa", "login": "monalisa", "name": ""}
					]
				}
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

	users, rl, err := c.MentionableUsers(context.Background(), mentionableTestRepo(), "", 100)
	if err != nil {
		t.Fatalf("MentionableUsers() error = %v", err)
	}
	if !containsAll(gotQuery, "query MentionableUsers", "mentionableUsers", "repository") {
		t.Errorf("request query = %q, want the MentionableUsers query document", gotQuery)
	}
	if gotVars["owner"] != "o" || gotVars["name"] != "r" || gotVars["first"] != float64(100) {
		t.Errorf("request variables = %+v, want owner=o name=r first=100", gotVars)
	}
	if v, ok := gotVars["query"]; !ok || v != nil {
		t.Errorf("request variables[query] = %v, want an explicit null for an empty query", v)
	}
	if len(users) != 2 ||
		users[0] != (model.User{ID: "U_octocat", Login: "octocat", Name: "The Octocat"}) ||
		users[1] != (model.User{ID: "U_monalisa", Login: "monalisa", Name: ""}) {
		t.Errorf("MentionableUsers() users = %+v, want IDs decoded from the response", users)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("MentionableUsers() rate limit = %+v, want Remaining=4999, Known=true", rl)
	}
}

func TestClient_MentionableUsers_WithQuery(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"repository": {"mentionableUsers": {"nodes": [{"login": "octocat", "name": "The Octocat"}]}},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-13T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	users, _, err := c.MentionableUsers(context.Background(), mentionableTestRepo(), "oct", 10)
	if err != nil {
		t.Fatalf("MentionableUsers() error = %v", err)
	}
	if gotVars["query"] != "oct" || gotVars["first"] != float64(10) {
		t.Errorf("request variables = %+v, want query=oct first=10", gotVars)
	}
	if len(users) != 1 || users[0].Login != "octocat" {
		t.Errorf("MentionableUsers() users = %+v", users)
	}
}

func TestClient_MentionableUsers_EmptyResult(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"repository": {"mentionableUsers": {"nodes": []}},
			"rateLimit": {"remaining": 4997, "resetAt": "2026-09-13T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	users, _, err := c.MentionableUsers(context.Background(), mentionableTestRepo(), "", 100)
	if err != nil {
		t.Fatalf("MentionableUsers() error = %v", err)
	}
	if len(users) != 0 {
		t.Errorf("MentionableUsers() users = %+v, want empty", users)
	}
}

func TestClient_MentionableUsers_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "Validation failed"}`))
	})
	defer srv.Close()

	_, _, err := c.MentionableUsers(context.Background(), mentionableTestRepo(), "", 100)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Fatalf("MentionableUsers() error = %v, want a KindValidation *gh.Error", err)
	}
}
