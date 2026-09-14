package gh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_Viewer(t *testing.T) {
	fixture, err := os.ReadFile("testdata/viewer.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotAuth, gotQuery string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotQuery = body.Query

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	user, rl, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer() error = %v", err)
	}

	if gotAuth != "token test-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token test-token")
	}
	if gotQuery == "" {
		t.Error("request carried an empty GraphQL query")
	}

	wantUser := model.User{ID: "U_octocat", Login: "octocat", Name: "The Octocat"}
	if user != wantUser {
		t.Errorf("Viewer() user = %+v, want %+v (ID decoded from the response)", user, wantUser)
	}

	wantResetAt, _ := time.Parse(time.RFC3339, "2026-09-11T12:00:00Z")
	wantRL := model.RateLimit{Remaining: 4999, ResetAt: wantResetAt, Known: true}
	if !rl.ResetAt.Equal(wantRL.ResetAt) || rl.Remaining != wantRL.Remaining || rl.Known != wantRL.Known {
		t.Errorf("Viewer() rate limit = %+v, want %+v", rl, wantRL)
	}
}

func TestClient_Viewer_NullRateLimitIsUnknown(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octocat","name":"The Octocat"},"rateLimit":null}}`))
	})
	defer srv.Close()

	_, rl, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer() error = %v", err)
	}
	if rl.Known {
		t.Errorf("Viewer() rate limit = %+v, want Known = false", rl)
	}
	if rl != (model.RateLimit{}) {
		t.Errorf("Viewer() rate limit = %+v, want the zero value", rl)
	}
}

func TestClient_Viewer_ErrorClassified(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	_, _, err := c.Viewer(context.Background())
	if err == nil {
		t.Fatal("Viewer() error = nil, want an error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("Viewer() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindAuth)
	}
}
