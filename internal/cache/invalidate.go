package cache

import (
	"fmt"
	"os"
	"strings"

	"github.com/hirano00o/gprt/internal/model"
)

// InvalidatePR removes every cached entry for one pull request: the
// corresponding memory entries are dropped and the PR's on-disk directory
// (see prDir in store.go) is deleted. Entries for other pull requests,
// other logins, and search results are left untouched. If the on-disk
// directory cannot be removed, that error is returned; the memory entries
// are dropped either way, since they are the more visible half of a stale
// hit.
func (s *Store) InvalidatePR(host, login string, ref model.PRRef) error {
	return s.invalidateDir(s.prDir(host, login, ref))
}

// InvalidateSearch removes every cached search/list entry for one
// host/login (see searchDir in store.go). Pull-request-scoped entries are
// left untouched.
func (s *Store) InvalidateSearch(host, login string) error {
	return s.invalidateDir(s.searchDir(host, login))
}

// invalidateDir drops every memory entry whose on-disk path is dir or lies
// under it, then removes dir from disk entirely. Both steps run under
// Store's single lock so that a concurrent Get cannot read the disk file
// after the memory sweep but before the removal and write a now-deleted
// entry straight back into memory (see the Store doc comment in store.go).
func (s *Store) invalidateDir(dir string) error {
	prefix := dir + string(os.PathSeparator)

	s.mu.Lock()
	defer s.mu.Unlock()

	for path := range s.mem {
		if path == dir || strings.HasPrefix(path, prefix) {
			delete(s.mem, path)
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cache: remove %s: %w", dir, err)
	}
	return nil
}
