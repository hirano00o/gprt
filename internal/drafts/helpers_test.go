package drafts

import (
	"os"
	"testing"
)

// newTestStore builds a Store rooted at a fresh t.TempDir().
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

// statDir is a thin os.Stat wrapper so tests read naturally as "stat the
// directory", without importing os directly in every test file.
func statDir(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
