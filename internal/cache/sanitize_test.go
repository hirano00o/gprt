package cache

import (
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"letters and digits", "github.com", "github.com"},
		{"underscore dash dot", "my_repo-name.go", "my_repo-name.go"},
		{"login with digits", "octocat123", "octocat123"},
		{"path traversal hex-encoded", "../../etc/passwd", "=" + hex.EncodeToString([]byte("../../etc/passwd"))},
		{"slash hex-encoded", "owner/name", "=" + hex.EncodeToString([]byte("owner/name"))},
		{"space hex-encoded", "my repo", "=" + hex.EncodeToString([]byte("my repo"))},
		{"empty string hex-encoded", "", "=" + hex.EncodeToString([]byte(""))},
		{"single dot hex-encoded", ".", "=" + hex.EncodeToString([]byte("."))},
		{"double dot hex-encoded", "..", "=" + hex.EncodeToString([]byte(".."))},
		{
			"a literal segment that looks escaped is itself escaped",
			"=deadbeef",
			"=" + hex.EncodeToString([]byte("=deadbeef")),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitize(tc.input); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitize_NeverProducesAnEmptySegment(t *testing.T) {
	// filepath.Join silently drops empty path elements, which would let
	// distinct keys collapse onto the same on-disk path (see
	// TestStore_Path_EmptyFieldsDoNotCollide). sanitize must therefore
	// never return "".
	for _, input := range []string{"", ".", "..", "safe", "un/safe"} {
		if got := sanitize(input); got == "" {
			t.Errorf("sanitize(%q) = \"\", want a non-empty segment", input)
		}
	}
}

func TestStore_Path_EmptyFieldsDoNotCollide(t *testing.T) {
	store := &Store{dir: "/cache"}

	pathA := store.path(Key{Host: "", Login: "alice", Rest: "q"})
	pathB := store.path(Key{Host: "alice", Login: "", Rest: "q"})

	if pathA == pathB {
		t.Errorf("path(Host:\"\",Login:alice) == path(Host:alice,Login:\"\") = %q, want distinct paths", pathA)
	}
}

func TestStore_Path_TraversalSegmentsDoNotEscapeCacheRoot(t *testing.T) {
	store := &Store{dir: "/cache"}

	repo := model.RepoRef{Host: "github.com", Owner: "..", Name: ".."}
	pr := model.PRRef{Repo: repo, Number: 1}
	path := store.path(Key{Host: "github.com", Login: "octocat", PR: &pr, Rest: "detail"})

	if !filepath.IsAbs(path) {
		t.Fatalf("path() = %q, want an absolute path", path)
	}
	if filepath.Clean(path) != path {
		t.Errorf("path() = %q is not filepath.Clean, want it already clean (no residual ..)", path)
	}
	rel, err := filepath.Rel("/cache", path)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	if rel == ".." || len(rel) >= 2 && rel[:2] == ".." {
		t.Errorf("path() = %q escapes the cache root /cache", path)
	}
}

func TestStore_Put_FilePermissions(t *testing.T) {
	// New must create dir itself (with mode 0700) rather than reuse a
	// pre-existing directory, so this test gives it a path that does not
	// exist yet, matching how the real cache directory is resolved by
	// config.CacheDir().
	dir := filepath.Join(t.TempDir(), "cache")
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
	if err := store.Put(key, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	path := store.path(key)
	assertMode(t, path, 0o600)
	assertMode(t, dir, 0o700)
}

func TestStore_Put_SanitizesPathSegmentsWithSlashes(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A login or repo owner containing a path separator must not be able
	// to escape the intended cache directory.
	key := Key{Host: "github.com", Login: "../../evil", Rest: "q"}
	if err := store.Put(key, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, ok, err := store.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || string(got.Body) != "{}" {
		t.Errorf("Get() = %+v, %v, want the stored entry", got, ok)
	}
}
