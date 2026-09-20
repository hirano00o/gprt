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
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-14T05:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	headline := "Merge PR #1"
	pr, rl, err := c.MergePullRequest(context.Background(), "PR_1", model.MergeMethodSquash, &headline, nil, "abc123")
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
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("MergePullRequest() rate limit = %+v", rl)
	}
}

func TestClient_ClosePullRequest_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"closePullRequest": {"pullRequest": {"state": "CLOSED"}},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T05:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	state, rl, err := c.ClosePullRequest(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("ClosePullRequest() error = %v", err)
	}
	if state != model.PRStateClosed {
		t.Errorf("ClosePullRequest() = %v, want %v", state, model.PRStateClosed)
	}
	if !rl.Known || rl.Remaining != 4998 {
		t.Errorf("ClosePullRequest() rate limit = %+v", rl)
	}
}

func TestClient_ReopenPullRequest_Success(t *testing.T) {
	fixture := []byte(`{
		"data": {
			"reopenPullRequest": {"pullRequest": {"state": "OPEN"}},
			"rateLimit": {"remaining": 4997, "resetAt": "2026-09-14T05:00:00Z"}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	state, rl, err := c.ReopenPullRequest(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("ReopenPullRequest() error = %v", err)
	}
	if state != model.PRStateOpen {
		t.Errorf("ReopenPullRequest() = %v, want %v", state, model.PRStateOpen)
	}
	if !rl.Known || rl.Remaining != 4997 {
		t.Errorf("ReopenPullRequest() rate limit = %+v", rl)
	}
}
