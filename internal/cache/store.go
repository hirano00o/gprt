package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

// Store is a memory-backed, disk-persisted cache of Entry values keyed by
// Key. A memory map sits in front of the disk for fast repeat reads within
// a single process run; the disk survives across runs.
//
// Store serialises every operation — Get, Put, and invalidateDir (used by
// InvalidatePR/InvalidateSearch) — behind a single mutex held across their
// entire body, including disk I/O, rather than only guarding the memory
// map. This is coarser than strictly necessary, but it is what closes two
// real races: a Get that misses memory, reads a stale disk file, and
// writes the result back to memory could otherwise interleave with a
// concurrent invalidation and resurrect an entry it just removed; a Put in
// flight during an invalidation could otherwise write its disk file after
// the invalidation's RemoveAll and resurrect the directory it just
// deleted. gprt is a single-user CLI touching small JSON files, so paying
// for a coarse lock (rather than a more elaborate per-key scheme) is an
// acceptable trade-off for that guarantee.
type Store struct {
	dir string

	mu  sync.Mutex
	mem map[string]Entry // keyed by the entry's on-disk path
}

// New creates (if needed) dir with mode 0700 and returns a Store rooted at
// it.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cache: create dir %s: %w", dir, err)
	}
	return &Store{dir: dir, mem: make(map[string]Entry)}, nil
}

// Get returns the cached entry for k. It checks memory first, then falls
// back to disk, populating memory on a disk hit. The whole operation runs
// under Store's single lock (see the Store doc comment), so it cannot
// observe a partially completed invalidation.
//
// A missing file is reported as (Entry{}, false, nil), not an error. A
// corrupt file — invalid JSON, or JSON that decodes but is missing a body
// or a fetch time (as a truncated or half-written file might produce) — is
// treated the same way and is also removed from disk so it does not keep
// failing on every lookup; if that removal itself fails, its error is
// returned. Any other read error (for example a permission error) is
// returned as-is.
func (s *Store) Get(k Key) (Entry, bool, error) {
	path := s.path(k)

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.mem[path]; ok {
		return e, true, nil
	}

	e, ok, err := readDiskEntry(path)
	if err != nil || !ok {
		return Entry{}, false, err
	}

	s.mem[path] = e
	return e, true, nil
}

// Put stores e in memory and atomically persists it to disk (a temp file
// written alongside the destination, then renamed into place, mode 0600).
// e is rejected — before anything is written — if it has a nil Body or a
// zero FetchedAt, the same two conditions readDiskEntry treats as file
// corruption; accepting either here would let Put write an entry a later
// Get would immediately discard.
//
// The whole operation (the memory write and the disk write) runs under
// Store's single lock, like Get and invalidateDir: without that, a Put in
// flight when InvalidatePR/InvalidateSearch runs could write its disk file
// after RemoveAll and resurrect a directory the invalidation just deleted.
// (writeAtomic's temp-file-then-rename means a concurrent Get can never
// observe a half-written file at the destination path, so that is not a
// separate risk.) Marshalling happens before the lock is taken to keep the
// critical section to memory-and-disk I/O only.
func (s *Store) Put(k Key, e Entry) error {
	if e.Body == nil || e.FetchedAt.IsZero() {
		return errors.New("cache: Put: Body must be non-nil and FetchedAt must be non-zero")
	}

	path := s.path(k)
	data, err := json.Marshal(diskEntry{ETag: e.ETag, FetchedAt: e.FetchedAt, Body: e.Body})
	if err != nil {
		return fmt.Errorf("cache: marshal entry: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.mem[path] = e
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("cache: write %s: %w", path, err)
	}
	return nil
}

// diskEntry is Entry's on-disk JSON representation. Body is base64-encoded
// automatically by encoding/json's []byte handling.
type diskEntry struct {
	ETag      string    `json:"etag"`
	FetchedAt time.Time `json:"fetched_at"`
	Body      []byte    `json:"body"`
}

// readDiskEntry reads and validates the cache file at path, without
// touching Store's memory map or lock.
func readDiskEntry(path string) (Entry, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}

	var de diskEntry
	corrupt := json.Unmarshal(data, &de) != nil
	if !corrupt && (de.Body == nil || de.FetchedAt.IsZero()) {
		// Valid JSON but missing the fields a real entry always has (for
		// example {} or null from a truncated write): treat it the same
		// as a parse failure rather than serving a hollow "hit".
		corrupt = true
	}
	if !corrupt {
		return Entry{Body: de.Body, ETag: de.ETag, FetchedAt: de.FetchedAt}, true, nil
	}

	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return Entry{}, false, fmt.Errorf("cache: remove corrupt file %s: %w", path, rmErr)
	}
	return Entry{}, false, nil
}

// path computes the on-disk location for k. Pull-request-scoped keys
// (k.PR != nil) live under prDir(k.Host, k.Login, *k.PR); everything else
// (search and list results) lives under searchDir(k.Host, k.Login).
func (s *Store) path(k Key) string {
	filename := hashHex(k.Rest) + ".json"

	if k.PR != nil {
		return filepath.Join(s.prDir(k.Host, k.Login, *k.PR), filename)
	}
	return filepath.Join(s.searchDir(k.Host, k.Login), filename)
}

// prDir returns the on-disk directory holding every cached entry for one
// pull request: <dir>/v1/<host>/<login>/repos/<owner>/<name>/pr-<n>. The
// fixed "repos" segment keeps this tree disjoint from searchDir's "search"
// tree at the same level: without it, a repository literally owned by an
// account named "search" would put prDir under .../<login>/search/..., and
// InvalidateSearch's os.RemoveAll(searchDir) would delete that owner's PR
// caches along with the search cache. Shared by path (here) and
// InvalidatePR (invalidate.go) so the layout is defined in exactly one
// place.
func (s *Store) prDir(host, login string, ref model.PRRef) string {
	return filepath.Join(
		s.dir, "v1", sanitize(host), sanitize(login), "repos",
		sanitize(ref.Repo.Owner), sanitize(ref.Repo.Name), fmt.Sprintf("pr-%d", ref.Number),
	)
}

// searchDir returns the on-disk directory holding every cached search/list
// entry for one host/login: <dir>/v1/<host>/<login>/search. Shared by path
// (here) and InvalidateSearch (invalidate.go).
func (s *Store) searchDir(host, login string) string {
	return filepath.Join(s.dir, "v1", sanitize(host), sanitize(login), "search")
}

// safeSegment matches path segments that can be used verbatim on disk.
// "." and ".." are excluded even though they match the character class,
// since either would otherwise be a no-op or a traversal segment once
// joined into a path.
var safeSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// sanitizeEscape marks a segment as hex-encoded. It cannot collide with a
// literal safe segment because '=' is outside safeSegment's character
// class, so no unescaped segment ever starts with it.
const sanitizeEscape = "="

// sanitize returns s unchanged if it is safe to use as a single path
// segment, or sanitizeEscape followed by its hex encoding otherwise. It
// never returns "": filepath.Join silently drops empty segments, which
// would otherwise let differently-shaped keys (for example an empty Host
// versus an empty Login) collide onto the same path.
func sanitize(s string) string {
	if s != "." && s != ".." && safeSegment.MatchString(s) {
		return s
	}
	return sanitizeEscape + hex.EncodeToString([]byte(s))
}

// hashHex returns the hex-encoded SHA-256 hash of s.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
