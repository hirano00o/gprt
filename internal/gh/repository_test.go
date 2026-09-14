package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func repositoryTestRef() model.RepoRef {
	return model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
}

func TestClient_Repository_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"repository": {
				"id": "R_1",
				"defaultBranchRef": {"name": "main"},
				"mergeCommitAllowed": true,
				"squashMergeAllowed": true,
				"rebaseMergeAllowed": false,
				"deleteBranchOnMerge": true,
				"viewerPermission": "WRITE"
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

	info, rl, err := c.Repository(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("Repository() error = %v", err)
	}
	if gotVars["owner"] != "acme" || gotVars["name"] != "widgets" {
		t.Errorf("request variables = %+v, want owner=acme name=widgets", gotVars)
	}
	want := model.RepositoryInfo{
		ID:                  "R_1",
		Ref:                 repositoryTestRef(),
		DefaultBranch:       "main",
		MergeCommitAllowed:  true,
		SquashMergeAllowed:  true,
		RebaseMergeAllowed:  false,
		DeleteBranchOnMerge: true,
		ViewerPermission:    "WRITE",
	}
	if info != want {
		t.Errorf("Repository() = %+v, want %+v", info, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("Repository() rate limit = %+v, want Remaining=4999 Known=true", rl)
	}
}

func TestClient_Repository_NoDefaultBranch(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"repository": {
				"id": "R_2",
				"defaultBranchRef": null,
				"mergeCommitAllowed": true,
				"squashMergeAllowed": true,
				"rebaseMergeAllowed": true,
				"deleteBranchOnMerge": false,
				"viewerPermission": "READ"
			},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	info, _, err := c.Repository(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("Repository() error = %v", err)
	}
	if info.DefaultBranch != "" {
		t.Errorf("Repository() DefaultBranch = %q, want empty for a null defaultBranchRef", info.DefaultBranch)
	}
}

func TestClient_Repository_NotFound(t *testing.T) {
	fixture := []byte(`{"data": {"repository": null}, "rateLimit": {"remaining": 4997, "resetAt": "2026-09-14T01:00:00Z"}}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	_, _, err := c.Repository(context.Background(), repositoryTestRef())
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindNotFound {
		t.Fatalf("Repository() error = %v, want a KindNotFound *gh.Error", err)
	}
}
