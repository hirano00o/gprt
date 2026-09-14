package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_PullRequestTemplates_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"repository": {
				"pullRequestTemplates": [
					{"filename": "PULL_REQUEST_TEMPLATE.md", "body": "## Summary\n"},
					{"filename": null, "body": "Untitled"}
				]
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	templates, rl, err := c.PullRequestTemplates(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("PullRequestTemplates() error = %v", err)
	}
	want := []model.PullRequestTemplate{
		{Filename: "PULL_REQUEST_TEMPLATE.md", Body: "## Summary\n"},
		{Filename: "", Body: "Untitled"},
	}
	if len(templates) != len(want) || templates[0] != want[0] || templates[1] != want[1] {
		t.Errorf("PullRequestTemplates() = %+v, want %+v", templates, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("PullRequestTemplates() rate limit = %+v", rl)
	}
}

func TestClient_PullRequestTemplates_Empty(t *testing.T) {
	fixture := []byte(`{
		"data": {"repository": {"pullRequestTemplates": []}},
		"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T01:00:00Z"}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	templates, _, err := c.PullRequestTemplates(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("PullRequestTemplates() error = %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("PullRequestTemplates() = %+v, want empty", templates)
	}
}

func TestClient_PullRequestTemplates_NoRepository(t *testing.T) {
	fixture := []byte(`{"data": {"repository": null}, "rateLimit": {"remaining": 4997, "resetAt": "2026-09-14T01:00:00Z"}}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	_, _, err := c.PullRequestTemplates(context.Background(), repositoryTestRef())
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindUnknown {
		t.Fatalf("PullRequestTemplates() error = %v, want a KindUnknown *gh.Error", err)
	}
}
