package gh

import (
	"context"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_ViewerRepositories_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"viewer": {
				"repositories": {
					"nodes": [
						{"id": "R_1", "nameWithOwner": "acme/widgets", "isArchived": false, "defaultBranchRef": {"name": "main"}},
						{"id": "R_2", "nameWithOwner": "acme/gizmos", "isArchived": true, "defaultBranchRef": null}
					]
				}
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	repos, rl, err := c.ViewerRepositories(context.Background(), 50)
	if err != nil {
		t.Fatalf("ViewerRepositories() error = %v", err)
	}
	if gotVars["first"] != float64(50) {
		t.Errorf("request variables[first] = %v, want 50", gotVars["first"])
	}
	want := []model.RepositorySummary{
		{Ref: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, ID: "R_1", DefaultBranch: "main", IsArchived: false},
		{Ref: model.RepoRef{Host: "example.com", Owner: "acme", Name: "gizmos"}, ID: "R_2", DefaultBranch: "", IsArchived: true},
	}
	if len(repos) != len(want) || repos[0] != want[0] || repos[1] != want[1] {
		t.Errorf("ViewerRepositories() = %+v, want %+v", repos, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("ViewerRepositories() rate limit = %+v", rl)
	}
}

func TestClient_ViewerRepositories_Empty(t *testing.T) {
	fixture := []byte(`{
		"data": {"viewer": {"repositories": {"nodes": []}}},
		"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T01:00:00Z"}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	repos, _, err := c.ViewerRepositories(context.Background(), 50)
	if err != nil {
		t.Fatalf("ViewerRepositories() error = %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("ViewerRepositories() = %+v, want empty", repos)
	}
}
