package gh

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestClient_BodyHTML_MapsNodesByID(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"nodes": [
				{"id": "PR_1", "bodyHTML": "<p>pr</p>"},
				null,
				{"id": "IC_1", "bodyHTML": "<img src=\"https://x/y.png\">"},
				{"id": "PRR_1", "bodyHTML": ""},
				{"id": "PRRC_1", "bodyHTML": "<p>c</p>"}
			],
			"rateLimit": {"remaining": 4990, "resetAt": "2026-09-13T01:00:00Z"}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	got, rl, err := c.BodyHTML(context.Background(), []string{"PR_1", "gone", "IC_1", "PRR_1", "PRRC_1"})
	if err != nil {
		t.Fatalf("BodyHTML() error = %v", err)
	}
	if !containsAll(gotQuery, "query BodyHTML", "nodes(ids: $ids)", "... on PullRequest", "... on IssueComment",
		"... on PullRequestReview ", "... on PullRequestReviewComment", "bodyHTML") {
		t.Errorf("request query = %q, want the BodyHTML query document", gotQuery)
	}
	if ids, _ := gotVars["ids"].([]any); len(ids) != 5 {
		t.Errorf("request ids = %v, want 5", gotVars["ids"])
	}
	want := map[string]string{
		"PR_1": "<p>pr</p>", "IC_1": `<img src="https://x/y.png">`, "PRR_1": "", "PRRC_1": "<p>c</p>",
	}
	if len(got) != len(want) {
		t.Fatalf("BodyHTML() = %v, want %v", got, want)
	}
	for id, html := range want {
		if got[id] != html {
			t.Errorf("BodyHTML()[%q] = %q, want %q", id, got[id], html)
		}
	}
	if !rl.Known || rl.Remaining != 4990 {
		t.Errorf("rate limit = %+v, want Remaining=4990", rl)
	}
}

func TestClient_BodyHTML_ChunksAtOneHundred(t *testing.T) {
	var sizes []int
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, vars := decodeRequest(t, r)
		ids, _ := vars["ids"].([]any)
		sizes = append(sizes, len(ids))
		w.Header().Set("Content-Type", "application/json")
		nodes := ""
		for i, id := range ids {
			if i > 0 {
				nodes += ","
			}
			nodes += fmt.Sprintf(`{"id": %q, "bodyHTML": "b-%v"}`, id, id)
		}
		_, _ = fmt.Fprintf(w, `{"data": {"nodes": [%s]}}`, nodes)
	})
	defer srv.Close()

	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("N_%d", i)
	}
	got, _, err := c.BodyHTML(context.Background(), ids)
	if err != nil {
		t.Fatalf("BodyHTML() error = %v", err)
	}
	if fmt.Sprint(sizes) != "[100 100 50]" {
		t.Errorf("request sizes = %v, want [100 100 50]", sizes)
	}
	if len(got) != 250 || got["N_249"] != "b-N_249" {
		t.Errorf("BodyHTML() returned %d entries, N_249 = %q", len(got), got["N_249"])
	}
}

func TestClient_BodyHTML_NoIDsMakesNoRequest(t *testing.T) {
	c, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected request for an empty id list")
	})
	defer srv.Close()
	got, _, err := c.BodyHTML(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("BodyHTML(nil) = %v, %v; want empty, nil", got, err)
	}
}

func TestClient_BodyHTML_GraphQLError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors": [{"message": "boom"}]}`))
	})
	defer srv.Close()
	if _, _, err := c.BodyHTML(context.Background(), []string{"PR_1"}); err == nil {
		t.Error("BodyHTML() error = nil, want an error")
	}
}
