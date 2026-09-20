package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestClient_Labels_SinglePage(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"repository": {
				"labels": {
					"nodes": [{"id": "LA_1", "name": "bug", "color": "d73a4a"}],
					"pageInfo": {"hasNextPage": false, "endCursor": null}
				}
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)

	var calls int
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	labels, rl, err := c.Labels(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("Labels() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no further page)", calls)
	}
	if v, ok := gotVars["after"]; !ok || v != nil {
		t.Errorf("request variables[after] = %v, want an explicit null on the first page", v)
	}
	want := []model.Label{{ID: "LA_1", Name: "bug", Color: "d73a4a"}}
	if len(labels) != 1 || labels[0] != want[0] {
		t.Errorf("Labels() = %+v, want %+v", labels, want)
	}
	if !rl.Known || rl.Remaining != 4999 {
		t.Errorf("Labels() rate limit = %+v", rl)
	}
}

func TestClient_Labels_Paginates(t *testing.T) {
	page1 := []byte(`{
		"data": {
			"repository": {
				"labels": {
					"nodes": [{"id": "LA_1", "name": "bug", "color": "d73a4a"}],
					"pageInfo": {"hasNextPage": true, "endCursor": "cursor-1"}
				}
			},
			"rateLimit": {"remaining": 4999, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)
	page2 := []byte(`{
		"data": {
			"repository": {
				"labels": {
					"nodes": [{"id": "LA_2", "name": "enhancement", "color": "a2eeef"}],
					"pageInfo": {"hasNextPage": false, "endCursor": null}
				}
			},
			"rateLimit": {"remaining": 4998, "resetAt": "2026-09-14T01:00:00Z"}
		}
	}`)

	var calls int
	var gotAfters []any
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, vars := decodeRequest(t, r)
		gotAfters = append(gotAfters, vars["after"])
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write(page1)
		} else {
			_, _ = w.Write(page2)
		}
	})
	defer srv.Close()

	labels, rl, err := c.Labels(context.Background(), repositoryTestRef())
	if err != nil {
		t.Fatalf("Labels() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if gotAfters[0] != nil || gotAfters[1] != "cursor-1" {
		t.Errorf("after cursors = %+v, want [nil, cursor-1]", gotAfters)
	}
	if len(labels) != 2 || labels[0].Name != "bug" || labels[1].Name != "enhancement" {
		t.Errorf("Labels() = %+v", labels)
	}
	if !rl.Known || rl.Remaining != 4998 {
		t.Errorf("Labels() rate limit = %+v, want the final page's rate limit", rl)
	}
}

func TestClient_Labels_NoRepository(t *testing.T) {
	fixture := []byte(`{"data": {"repository": null}, "rateLimit": {"remaining": 4997, "resetAt": "2026-09-14T01:00:00Z"}}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	_, _, err := c.Labels(context.Background(), repositoryTestRef())
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindUnknown {
		t.Fatalf("Labels() error = %v, want a KindUnknown *gh.Error", err)
	}
}
