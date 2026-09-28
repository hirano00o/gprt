package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestCompareFiles_SuccessWithSlashBranchAndFileWithoutPatch covers both a
// base branch name containing '/' (a common branch-naming convention, e.g.
// "feature/foo") and a file the REST response omits "patch" for (e.g. a
// binary file).
func TestCompareFiles_SuccessWithSlashBranchAndFileWithoutPatch(t *testing.T) {
	fixture := map[string]any{
		"files": []map[string]any{
			{
				"filename":  "src/a.go",
				"status":    "modified",
				"additions": 3,
				"deletions": 1,
				"sha":       "abc123",
				"patch":     "@@ -1 +1 @@\n-old\n+new",
			},
			{
				"filename": "assets/b.png",
				"status":   "added",
				// no "patch" key: e.g. a binary file.
			},
		},
	}

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath (not Path, which always decodes %2F back to '/'
		// regardless of what was actually sent) is what tells apart a
		// literal '/' in the request line from an escaped %2F.
		wantPath := "/api/v3/repos/acme/widgets/compare/feature/foo...main"
		if got := r.URL.EscapedPath(); got != wantPath {
			t.Errorf("request path = %q, want %q (literal '/' in the base branch name, not %%2F)", got, wantPath)
		}
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1893456000")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fixture)
	})
	defer srv.Close()

	res, err := c.CompareFiles(t.Context(), testRepo(), "feature/foo", "main")
	if err != nil {
		t.Fatalf("CompareFiles() error = %v", err)
	}
	if res.Truncated {
		t.Error("Truncated = true, want false for a 2-file response")
	}
	if !res.RateLimit.Known || res.RateLimit.Remaining != 4999 {
		t.Errorf("RateLimit = %+v, want Known=true Remaining=4999", res.RateLimit)
	}
	if len(res.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(res.Files))
	}
	f0 := res.Files[0]
	if f0.Path != "src/a.go" || !f0.HasPatch || f0.Patch != "@@ -1 +1 @@\n-old\n+new" {
		t.Errorf("Files[0] = %+v, unexpected", f0)
	}
	f1 := res.Files[1]
	if f1.Path != "assets/b.png" || f1.HasPatch {
		t.Errorf("Files[1] = %+v, want HasPatch=false (no \"patch\" key in the fixture)", f1)
	}
}

func TestCompareFiles_NotFoundClassifiesError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
	})
	defer srv.Close()

	_, err := c.CompareFiles(t.Context(), testRepo(), "main", "missing-branch")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("error = %v, want a *gh.Error", err)
	}
	if ghErr.Kind != KindNotFound {
		t.Errorf("error Kind = %v, want %v", ghErr.Kind, KindNotFound)
	}
}

// TestCompareFiles_TruncatedAtMax asserts Truncated is set once the
// response's file count reaches compareFilesMax: see CompareResult.Truncated's
// doc comment for why a response at exactly that count cannot be told apart
// from one GitHub actually cut off.
func TestCompareFiles_TruncatedAtMax(t *testing.T) {
	files := make([]map[string]any, compareFilesMax)
	for i := range files {
		files[i] = map[string]any{"filename": fmt.Sprintf("f%d.go", i), "status": "modified"}
	}
	fixture := map[string]any{"files": files}

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fixture)
	})
	defer srv.Close()

	res, err := c.CompareFiles(t.Context(), testRepo(), "base", "head")
	if err != nil {
		t.Fatalf("CompareFiles() error = %v", err)
	}
	if !res.Truncated {
		t.Errorf("Truncated = false, want true at exactly compareFilesMax (%d) files", compareFilesMax)
	}
	if len(res.Files) != compareFilesMax {
		t.Errorf("len(Files) = %d, want %d", len(res.Files), compareFilesMax)
	}
}
