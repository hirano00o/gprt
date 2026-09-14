package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_Branches_NullQueryWhenEmpty(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"repository": {
				"refs": {
					"nodes": [
						{"name": "main", "target": {"oid": "abc123"}},
						{"name": "feature/x", "target": {"oid": "def456"}}
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

	branches, rl, err := c.Branches(context.Background(), repositoryTestRef(), "", 50)
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if v, ok := gotVars["query"]; !ok || v != nil {
		t.Errorf("request variables[query] = %v, want an explicit null for an empty query", v)
	}
	if gotVars["first"] != float64(50) {
		t.Errorf("request variables[first] = %v, want 50", gotVars["first"])
	}
	want := []model.Branch{{Name: "main", HeadOID: "abc123"}, {Name: "feature/x", HeadOID: "def456"}}
	if len(branches) != len(want) || branches[0] != want[0] || branches[1] != want[1] {
		t.Errorf("Branches() = %+v, want %+v", branches, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("Branches() rate limit = %+v", rl)
	}
}

func TestClient_Branches_WithQuery(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {"repository": {"refs": {"nodes": [{"name": "feature/x", "target": {"oid": "def456"}}]}}},
		"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T01:00:00Z"}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	branches, _, err := c.Branches(context.Background(), repositoryTestRef(), "feat", 10)
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if gotVars["query"] != "feat" {
		t.Errorf("request variables[query] = %v, want %q", gotVars["query"], "feat")
	}
	if len(branches) != 1 || branches[0].Name != "feature/x" {
		t.Errorf("Branches() = %+v", branches)
	}
}

func TestClient_Branches_NoRepository(t *testing.T) {
	fixture := []byte(`{"data": {"repository": null}, "rateLimit": {"remaining": 4997, "resetAt": "2026-09-14T01:00:00Z"}}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	_, _, err := c.Branches(context.Background(), repositoryTestRef(), "", 50)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindUnknown {
		t.Fatalf("Branches() error = %v, want a KindUnknown *gh.Error", err)
	}
}
