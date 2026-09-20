package cache

import (
	"os"
	"path/filepath"
)

// writeAtomic writes data to path without ever exposing a partially
// written file: it writes to a temp file in the same directory (created
// mode 0600 by os.CreateTemp, so no separate chmod is needed) and renames
// it into place. The parent directory is created with mode 0700 if it does
// not already exist.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
