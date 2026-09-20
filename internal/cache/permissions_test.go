package cache

import (
	"os"
	"runtime"
	"testing"
)

// assertMode checks that the file or directory at path has exactly the
// given permission bits. It is a no-op on Windows, where POSIX permission
// bits are not meaningful.
func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", path, got, want)
	}
}
