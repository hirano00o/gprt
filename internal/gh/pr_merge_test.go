package gh

import (
	"context"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_MergePullRequest_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"mergePullRequest": {
				"pullRequest": {"state": "MERGED", "merged": true, "mergedAt": "2026-09-14T04:00:00Z"}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	headline := "Merge PR #1"
	pr, err := c.MergePullRequest(context.Background(), "PR_1", model.MergeMethodSquash, &headline, nil, "abc123")
	if err != nil {
		t.Fatalf("MergePullRequest() error = %v", err)
	}
	if gotVars["id"] != "PR_1" || gotVars["mergeMethod"] != "SQUASH" || gotVars["expectedHeadOid"] != "abc123" {
		t.Errorf("request variables = %+v", gotVars)
	}
	if gotVars["commitHeadline"] != "Merge PR #1" {
		t.Errorf("request variables[commitHeadline] = %v, want %q", gotVars["commitHeadline"], "Merge PR #1")
	}
	if v, ok := gotVars["commitBody"]; !ok || v != nil {
		t.Errorf("request variables[commitBody] = %v, want an explicit null", v)
	}
	if pr.State != model.PRStateMerged || !pr.Merged || pr.MergedAt.IsZero() {
		t.Errorf("MergePullRequest() = %+v", pr)
	}
}

func TestClient_ClosePullRequest_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"closePullRequest": {"pullRequest": {"state": "CLOSED"}}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	state, err := c.ClosePullRequest(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("ClosePullRequest() error = %v", err)
	}
	if state != model.PRStateClosed {
		t.Errorf("ClosePullRequest() = %v, want %v", state, model.PRStateClosed)
	}
}

func TestClient_ReopenPullRequest_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"reopenPullRequest": {"pullRequest": {"state": "OPEN"}}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	state, err := c.ReopenPullRequest(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("ReopenPullRequest() error = %v", err)
	}
	if state != model.PRStateOpen {
		t.Errorf("ReopenPullRequest() = %v, want %v", state, model.PRStateOpen)
	}
}
