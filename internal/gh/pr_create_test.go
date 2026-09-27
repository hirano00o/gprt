package gh

import (
	"context"
	"net/http"
	"testing"
)

func TestClient_CreatePullRequest_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"createPullRequest": {
				"pullRequest": {
					"id": "PR_new",
					"number": 7,
					"repository": {"nameWithOwner": "acme/widgets"},
					"url": "https://example.com/acme/widgets/pull/7"
				}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	in := CreatePullRequestInput{
		RepositoryID: "R_1",
		BaseRefName:  "main",
		HeadRefName:  "feature/x",
		Title:        "Add feature X",
		Body:         "Description",
		Draft:        true,
	}
	pr, err := c.CreatePullRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("CreatePullRequest() error = %v", err)
	}
	if gotVars["repositoryId"] != "R_1" || gotVars["baseRefName"] != "main" || gotVars["headRefName"] != "feature/x" ||
		gotVars["title"] != "Add feature X" || gotVars["body"] != "Description" || gotVars["draft"] != true {
		t.Errorf("request variables = %+v", gotVars)
	}
	if pr.ID != "PR_new" || pr.Ref.Number != 7 || pr.Ref.Repo != repositoryTestRef() || pr.URL != "https://example.com/acme/widgets/pull/7" {
		t.Errorf("CreatePullRequest() = %+v", pr)
	}
}
