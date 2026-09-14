package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_Teams_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"organization": {
				"teams": {"nodes": [{"id": "T_1", "slug": "core", "name": "Core Team"}]}
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

	teams, rl, err := c.Teams(context.Background(), "acme", "", 25)
	if err != nil {
		t.Fatalf("Teams() error = %v", err)
	}
	if gotVars["login"] != "acme" || gotVars["first"] != float64(25) {
		t.Errorf("request variables = %+v, want login=acme first=25", gotVars)
	}
	if v, ok := gotVars["query"]; !ok || v != nil {
		t.Errorf("request variables[query] = %v, want an explicit null for an empty query", v)
	}
	want := []model.Team{{ID: "T_1", Slug: "core", Name: "Core Team"}}
	if len(teams) != 1 || teams[0] != want[0] {
		t.Errorf("Teams() = %+v, want %+v", teams, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("Teams() rate limit = %+v", rl)
	}
}

// TestClient_Teams_UserOwnedRepository_ReturnsEmptyWithoutError covers a
// user-owned repository's org login: GitHub resolves "organization(login:)"
// to null accompanied by a NOT_FOUND-typed, "organization"-scoped GraphQL
// error (there is no organization by that login, only a user account) -
// this is expected, not a failure, and must not surface as one.
func TestClient_Teams_UserOwnedRepository_ReturnsEmptyWithoutError(t *testing.T) {
	fixture := []byte(`{
		"data": {"organization": null, "rateLimit": {"remaining": 4996, "resetAt": "2026-09-14T01:00:00Z"}},
		"errors": [{
			"type": "NOT_FOUND",
			"path": ["organization"],
			"message": "Could not resolve to an Organization with the login of 'octocat'."
		}]
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	teams, rl, err := c.Teams(context.Background(), "octocat", "", 25)
	if err != nil {
		t.Fatalf("Teams() error = %v, want nil for a user-owned login", err)
	}
	if len(teams) != 0 {
		t.Errorf("Teams() = %+v, want empty", teams)
	}
	if !rl.Known || rl.Remaining != 4996 {
		t.Errorf("Teams() rate limit = %+v, want the rateLimit sibling field still decoded", rl)
	}
}

func TestClient_Teams_OtherGraphQLError_Fails(t *testing.T) {
	fixture := []byte(`{
		"data": {"organization": null},
		"errors": [{"type": "FORBIDDEN", "path": ["organization"], "message": "SAML enforcement"}]
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	_, _, err := c.Teams(context.Background(), "acme", "", 25)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindAuth {
		t.Fatalf("Teams() error = %v, want a KindAuth *gh.Error", err)
	}
}
