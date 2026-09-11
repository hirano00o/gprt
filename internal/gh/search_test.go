package gh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestClient_SearchPullRequests(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search_page1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotAuth string
	var gotVariables map[string]any
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotVariables = body.Variables

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	res, err := c.SearchPullRequests(context.Background(), "type:pr state:open", "")
	if err != nil {
		t.Fatalf("SearchPullRequests() error = %v", err)
	}

	if gotAuth != "token test-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token test-token")
	}
	if gotVariables["q"] != "type:pr state:open" {
		t.Errorf("variables[q] = %v, want %q", gotVariables["q"], "type:pr state:open")
	}
	if _, hasCursor := gotVariables["cursor"]; !hasCursor || gotVariables["cursor"] != nil {
		t.Errorf("variables[cursor] = %v, want nil for the first page", gotVariables["cursor"])
	}

	if len(res.Items) != 2 {
		t.Fatalf("Items = %d entries, want 2", len(res.Items))
	}
	if res.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", res.TotalCount)
	}
	if res.EndCursor != "cursor-1" {
		t.Errorf("EndCursor = %q, want %q", res.EndCursor, "cursor-1")
	}
	if !res.HasNextPage {
		t.Error("HasNextPage = false, want true")
	}

	wantResetAt, _ := time.Parse(time.RFC3339, "2026-09-11T13:00:00Z")
	if res.RateLimit.Remaining != 4990 || !res.RateLimit.ResetAt.Equal(wantResetAt) || !res.RateLimit.Known {
		t.Errorf("RateLimit = %+v, want {4990 %v true}", res.RateLimit, wantResetAt)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a clean page", res.Warnings)
	}
}

func TestClient_SearchPullRequests_NodeScopedErrorsKeepPageWithWarnings(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search_partial_error.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	res, err := c.SearchPullRequests(context.Background(), "q", "")
	if err != nil {
		t.Fatalf("SearchPullRequests() error = %v, want nil (node-scoped errors must not fail the page)", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("Items = %d, want 2 (both nodes have a non-empty ID)", len(res.Items))
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", res.Warnings)
	}
	if res.Warnings[0] != "You do not have permission to view this team" {
		t.Errorf("Warnings[0] = %q, want the GraphQL error message", res.Warnings[0])
	}
	// The affected review request degrades gracefully rather than being
	// dropped: requestedReviewer is null, so it maps to ReviewerKindOther.
	if len(res.Items[1].ReviewRequests) != 1 {
		t.Fatalf("PR_2 ReviewRequests = %v, want 1 entry", res.Items[1].ReviewRequests)
	}
}

func TestClient_SearchPullRequests_AllNodesSkippedWithNodeScopedErrorFails(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A single node in the page, entirely nulled out by GraphQL error
		// propagation, alongside a node-scoped error: this is a systemic
		// failure (everything on the page failed to resolve), not "the
		// page legitimately has zero results".
		body := `{"data":{"search":{"issueCount":1,"pageInfo":{"endCursor":"c1","hasNextPage":false},"nodes":[{}]},` +
			`"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},` +
			`"errors":[{"message":"insufficient scopes","path":["search","nodes",0,"reviewRequests"],"type":"INSUFFICIENT_SCOPES"}]}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	res, err := c.SearchPullRequests(context.Background(), "q", "")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("SearchPullRequests() error = %v (%T), want *gh.Error (all nodes failing is not a successful empty page)", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindAuth)
	}
	if res.Items != nil {
		t.Errorf("Items = %v, want nil", res.Items)
	}
}

func TestClient_SearchPullRequests_TopLevelErrorStillFails(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"search":null,"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},` +
			`"errors":[{"message":"SAML enforcement blocks this search","path":["search"],"type":"FORBIDDEN"}]}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	res, err := c.SearchPullRequests(context.Background(), "q", "")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("SearchPullRequests() error = %v (%T), want *gh.Error (a top-level error must still fail)", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindAuth)
	}
	if res.Items != nil {
		t.Errorf("Items = %v, want nil on a top-level failure", res.Items)
	}
}

func TestClient_SearchPullRequests_SkipsNonPullRequestOrEmptyIDNodes(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"search":{"issueCount":2,"pageInfo":{"endCursor":"c1","hasNextPage":false},` +
			`"nodes":[{"id":"PR_1","number":1,"title":"ok","repository":{"nameWithOwner":"acme/widgets"}},{}]},` +
			`"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}}}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	res, err := c.SearchPullRequests(context.Background(), "q", "")
	if err != nil {
		t.Fatalf("SearchPullRequests() error = %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("Items = %d, want 1 (the empty-ID node must be skipped)", len(res.Items))
	}
	if res.Items[0].ID != "PR_1" {
		t.Errorf("Items[0].ID = %q, want %q", res.Items[0].ID, "PR_1")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one (counting the skipped node)", res.Warnings)
	}
}

func TestClient_SearchPullRequests_CursorPassedThrough(t *testing.T) {
	var gotVariables map[string]any
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotVariables = body.Variables

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"search":{"issueCount":0,"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"rateLimit":{"remaining":100,"resetAt":"2026-09-11T00:00:00Z"}}}`))
	})
	defer srv.Close()

	if _, err := c.SearchPullRequests(context.Background(), "q", "page-2-cursor"); err != nil {
		t.Fatalf("SearchPullRequests() error = %v", err)
	}
	if gotVariables["cursor"] != "page-2-cursor" {
		t.Errorf("variables[cursor] = %v, want %q", gotVariables["cursor"], "page-2-cursor")
	}
}

func TestClient_SearchPullRequests_ErrorClassified(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	_, err := c.SearchPullRequests(context.Background(), "q", "")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("SearchPullRequests() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindNotFound {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindNotFound)
	}
}
