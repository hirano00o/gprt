package cache

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStore_Get_CorruptJSONTreatedAsMiss(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
	path := store.path(key)

	if err := writeAtomic(path, []byte("not valid json{{{")); err != nil {
		t.Fatalf("writeAtomic() error = %v", err)
	}

	_, ok, err := store.Get(key)
	if err != nil {
		t.Errorf("Get() error = %v, want nil (a corrupt file is a miss, not an error)", err)
	}
	if ok {
		t.Error("Get() ok = true, want false for a corrupt cache file")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt file at %s was not removed, Stat error = %v", path, err)
	}
}

func TestStore_Get_EmptyBodyTreatedAsCorrupt(t *testing.T) {
	// A JSON object that decodes successfully but has no body or fetch
	// time (as a truncated or half-written file might) must not be
	// accepted as a valid cache hit.
	tests := []struct {
		name string
		json string
	}{
		{"empty object", `{}`},
		{"null", `null`},
		{"null body", `{"etag":"","fetched_at":"2026-01-01T00:00:00Z","body":null}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
			path := store.path(key)
			if err := writeAtomic(path, []byte(tc.json)); err != nil {
				t.Fatalf("writeAtomic() error = %v", err)
			}

			_, ok, err := store.Get(key)
			if err != nil {
				t.Errorf("Get() error = %v, want nil", err)
			}
			if ok {
				t.Errorf("Get() ok = true for %q, want false", tc.json)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("file at %s was not removed, Stat error = %v", path, err)
			}
		})
	}
}

func TestStore_Get_ReadErrorPropagated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
	path := store.path(key)
	if err := writeAtomic(path, []byte(`{"body":"e30=","fetched_at":"2026-01-01T00:00:00Z"}`)); err != nil {
		t.Fatalf("writeAtomic() error = %v", err)
	}
	// Making the file unreadable turns the next Get into a genuine read
	// error rather than a "file does not exist" miss.
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, ok, err := store.Get(key)
	if ok {
		t.Error("Get() ok = true, want false")
	}
	if err == nil {
		t.Error("Get() error = nil, want a permission error")
	}
}

func TestStore_Get_CorruptFileRemoveFailurePropagated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
	path := store.path(key)
	if err := writeAtomic(path, []byte("not valid json{{{")); err != nil {
		t.Fatalf("writeAtomic() error = %v", err)
	}

	// Removing write permission on the parent directory makes unlinking
	// the corrupt file itself fail.
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, ok, err := store.Get(key)
	if ok {
		t.Error("Get() ok = true, want false")
	}
	if err == nil {
		t.Error("Get() error = nil, want the os.Remove failure to be propagated")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("Get() error = %v, want it to wrap a permission error", err)
	}
}
