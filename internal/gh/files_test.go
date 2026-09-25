package gh

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func testRef() model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 42}
}

func testRepo() model.RepoRef {
	return model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}
}

func TestChangedFiles_SuccessWithLinkHeaderAndPatch(t *testing.T) {
	fixture := []map[string]any{
		{
			"filename":  "src/a.go",
			"status":    "modified",
			"additions": 3,
			"deletions": 1,
			"sha":       "abc123",
			"patch":     "@@ -1 +1 @@\n-old\n+new",
		},
		{
			"filename":          "src/b.go",
			"previous_filename": "src/old_b.go",
			"status":            "renamed",
			"additions":         0,
			"deletions":         0,
			"sha":               "def456",
			// no "patch" key: a rename with no content change.
		},
	}

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// newTestClient's Client is built against Host: "example.com",
		// which go-gh's own auth.IsEnterprise treats as a GHES host (only
		// "github.com" and its subdomains are not), so the REST base URL
		// takes the "/api/v3" form rather than the "api.<host>" form.
		wantPath := "/api/v3/repos/acme/widgets/pulls/42/files"
		if r.URL.Path != wantPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		if got := r.URL.Query().Get("page"); got != "1" {
			t.Errorf("page = %q, want 1", got)
		}
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("If-None-Match = %q, want empty when no etag is supplied", r.Header.Get("If-None-Match"))
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want %q", got, "application/vnd.github+json")
		}
		if got := r.Header.Get("Authorization"); got != "token test-token" {
			t.Errorf("Authorization = %q, want %q", got, "token test-token")
		}
		w.Header().Set("ETag", `"page1etag"`)
		w.Header().Set("Link", `<https://api.example.com/repos/acme/widgets/pulls/42/files?page=2>; rel="next", <https://api.example.com/repos/acme/widgets/pulls/42/files?page=2>; rel="last"`)
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1893456000")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fixture)
	})
	defer srv.Close()

	res, err := c.ChangedFiles(t.Context(), testRef(), 1, "")
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if res.NotModified {
		t.Error("NotModified = true, want false")
	}
	if res.ETag != `"page1etag"` {
		t.Errorf("ETag = %q, want %q", res.ETag, `"page1etag"`)
	}
	if !res.HasNext {
		t.Error("HasNext = false, want true (Link header has rel=\"next\")")
	}
	if !res.RateLimit.Known || res.RateLimit.Remaining != 4999 {
		t.Errorf("RateLimit = %+v, want Known=true Remaining=4999", res.RateLimit)
	}
	if !res.RateLimit.ResetAt.Equal(time.Unix(1893456000, 0)) {
		t.Errorf("RateLimit.ResetAt = %v, want %v", res.RateLimit.ResetAt, time.Unix(1893456000, 0))
	}

	if len(res.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(res.Files))
	}
	f0 := res.Files[0]
	if f0.Path != "src/a.go" || f0.Status != model.FileStatusModified || f0.Additions != 3 || f0.Deletions != 1 || f0.SHA != "abc123" {
		t.Errorf("Files[0] = %+v, unexpected", f0)
	}
	if !f0.HasPatch || f0.Patch != "@@ -1 +1 @@\n-old\n+new" {
		t.Errorf("Files[0] HasPatch/Patch = %v/%q, want true/the patch text", f0.HasPatch, f0.Patch)
	}

	f1 := res.Files[1]
	if f1.Path != "src/b.go" || f1.PreviousPath != "src/old_b.go" || f1.Status != model.FileStatusRenamed {
		t.Errorf("Files[1] = %+v, unexpected", f1)
	}
	if f1.HasPatch {
		t.Error("Files[1].HasPatch = true, want false (no \"patch\" key in the fixture)")
	}
}

func TestChangedFiles_NotModifiedSendsIfNoneMatchAndKeepsNoFiles(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"cached-etag"` {
			t.Errorf("If-None-Match = %q, want %q", got, `"cached-etag"`)
		}
		w.WriteHeader(http.StatusNotModified)
	})
	defer srv.Close()

	res, err := c.ChangedFiles(t.Context(), testRef(), 1, `"cached-etag"`)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if !res.NotModified {
		t.Error("NotModified = false, want true")
	}
	if len(res.Files) != 0 {
		t.Errorf("len(Files) = %d, want 0 on a 304", len(res.Files))
	}
}

// TestChangedFiles_NotModifiedWithoutIfNoneMatchIsAnError guards against a
// 304 being silently treated as a routine empty success when no
// If-None-Match header was ever sent: a 304 is only ever a valid response
// to a conditional request, so this can only be a protocol violation on
// the server's (or an intermediary's) part.
func TestChangedFiles_NotModifiedWithoutIfNoneMatchIsAnError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "" {
			t.Errorf("If-None-Match = %q, want empty (test calls ChangedFiles with etag=\"\")", got)
		}
		w.WriteHeader(http.StatusNotModified)
	})
	defer srv.Close()

	res, err := c.ChangedFiles(t.Context(), testRef(), 1, "")
	if err == nil {
		t.Fatalf("ChangedFiles() error = nil, res = %+v, want an error for an unsolicited 304", res)
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("error = %v, want a *gh.Error", err)
	}
	if ghErr.Kind != KindUnknown {
		t.Errorf("error Kind = %v, want %v", ghErr.Kind, KindUnknown)
	}
}

func TestChangedFiles_SecondPageRequestsPageParam(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want 2", got)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	defer srv.Close()

	if _, err := c.ChangedFiles(t.Context(), testRef(), 2, ""); err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
}

func TestChangedFiles_UnprocessableEntityClassifiesAsValidation(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "bad request"})
	})
	defer srv.Close()

	_, err := c.ChangedFiles(t.Context(), testRef(), 1, "")
	var ghErr *Error
	if err == nil {
		t.Fatal("ChangedFiles() error = nil, want a validation error")
	}
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Errorf("error = %v, want Kind=%v", err, KindValidation)
	}
}

func TestChangedFiles_NoLinkHeaderMeansNoNextPage(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	defer srv.Close()

	res, err := c.ChangedFiles(t.Context(), testRef(), 1, "")
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if res.HasNext {
		t.Error("HasNext = true, want false when the response has no Link header")
	}
}

func TestFileContent_SuccessSendsRawAcceptHeader(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/v3/repos/acme/widgets/git/blobs/abc123"
		if r.URL.Path != wantPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github.raw+json" {
			t.Errorf("Accept = %q, want %q (raw bytes, no base64 envelope)", got, "application/vnd.github.raw+json")
		}
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1893456000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("line one\nline two\nline three"))
	})
	defer srv.Close()

	res, err := c.FileContent(t.Context(), testRepo(), "abc123")
	if err != nil {
		t.Fatalf("FileContent() error = %v", err)
	}
	want := []string{"line one", "line two", "line three"}
	if !slicesEqual(res.Lines, want) {
		t.Errorf("Lines = %q, want %q", res.Lines, want)
	}
	if !res.RateLimit.Known || res.RateLimit.Remaining != 4999 {
		t.Errorf("RateLimit = %+v, want Known=true Remaining=4999", res.RateLimit)
	}
}

func TestFileContent_TrailingNewlineDropsFinalEmptyElement(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("line one\nline two\n"))
	})
	defer srv.Close()

	res, err := c.FileContent(t.Context(), testRepo(), "abc123")
	if err != nil {
		t.Fatalf("FileContent() error = %v", err)
	}
	want := []string{"line one", "line two"}
	if !slicesEqual(res.Lines, want) {
		t.Errorf("Lines = %q, want %q", res.Lines, want)
	}
}

func TestFileContent_EmptyBodyReturnsZeroLines(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	res, err := c.FileContent(t.Context(), testRepo(), "abc123")
	if err != nil {
		t.Fatalf("FileContent() error = %v", err)
	}
	if len(res.Lines) != 0 {
		t.Errorf("len(Lines) = %d, want 0 for an empty body", len(res.Lines))
	}
}

func TestFileContent_NotFoundClassifiesError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
	})
	defer srv.Close()

	_, err := c.FileContent(t.Context(), testRepo(), "missing-sha")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("error = %v, want a *gh.Error", err)
	}
	if ghErr.Kind != KindNotFound {
		t.Errorf("error Kind = %v, want %v", ghErr.Kind, KindNotFound)
	}
}

// TestFileContent_ContentLengthOverCapReturnsSizeErrorWithoutReadingBody
// asserts the too-large check uses the declared Content-Length header
// rather than reading the body: the handler declares a length far past
// maxBlobBytes but writes a short body, and the assertion on the error
// message's size figure only holds if FileContent trusted the header.
func TestFileContent_ContentLengthOverCapReturnsSizeErrorWithoutReadingBody(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short body, far less than the declared length"))
	})
	defer srv.Close()

	_, err := c.FileContent(t.Context(), testRepo(), "abc123")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("error = %v, want a *gh.Error", err)
	}
	if !strings.Contains(ghErr.Message, "20000000") {
		t.Errorf("error message = %q, want it to mention the declared size (20000000)", ghErr.Message)
	}
}

// TestFileContent_ChunkedResponseOverCapReturnsSizeError covers a response
// with no Content-Length header at all (chunked transfer encoding,
// resp.ContentLength == -1 client-side - common for the GitHub API): the
// declared-length check above cannot catch this, so the body must still be
// refused, not silently truncated, once actually read past maxBlobBytes.
// maxBlobBytes is temporarily shrunk (it is a var for exactly this reason)
// so the fixture body does not need to be a real 10 MiB.
func TestFileContent_ChunkedResponseOverCapReturnsSizeError(t *testing.T) {
	old := maxBlobBytes
	maxBlobBytes = 16
	defer func() { maxBlobBytes = old }()

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			// Commits the header now, with no Content-Length set, forcing
			// chunked transfer encoding for the write below.
			f.Flush()
		}
		_, _ = w.Write([]byte("this body is deliberately longer than the shrunk maxBlobBytes cap"))
	})
	defer srv.Close()

	_, err := c.FileContent(t.Context(), testRepo(), "abc123")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("error = %v, want a *gh.Error", err)
	}
	if ghErr.Kind != KindUnknown {
		t.Errorf("error Kind = %v, want %v", ghErr.Kind, KindUnknown)
	}
}

// slicesEqual reports whether a and b contain the same elements in order.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
