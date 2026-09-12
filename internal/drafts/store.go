package drafts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Draft is one saved draft's text, as returned by Store.Load/Store.List.
type Draft struct {
	Key       Key
	Text      string
	UpdatedAt time.Time
}

// fileFormat is the on-disk JSON shape of one draft file: Key is kept
// structured (rather than Key.String()'s flattened form) so PR/Kind/Anchor
// round-trip exactly regardless of what characters they contain.
type fileFormat struct {
	Key       Key       `json:"key"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists drafts as small JSON files under a directory, one file
// per Key. It is safe for concurrent use.
type Store struct {
	mu  sync.Mutex
	dir string
	// now is overridden by tests for deterministic UpdatedAt ordering;
	// New always sets it to time.Now.
	now func() time.Time
}

// New builds a Store rooted at dir, creating it (mode 0700) if it does not
// already exist.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("drafts: create dir: %w", err)
	}
	return &Store{dir: dir, now: time.Now}, nil
}

// sanitisePR returns a filesystem-safe directory name for pr: every byte
// outside [A-Za-z0-9.-] becomes "_", followed by a short content hash. The
// hash suffix (not just the sanitised text) is what actually guarantees
// two different pr values never share a directory: a GitHub owner/repo
// name may itself legitimately contain "_", so two distinct
// "host/owner/name#number" strings could otherwise sanitise to the exact
// same text (for example "h/a_b/c#1" and "h/a/b_c#1" both become
// "h_a_b_c_1") - a real collision that would make Store.List(pr) return
// another pull request's drafts. The plain-text prefix is kept purely for
// human-readable directory listings; only the hash needs to be trusted for
// correctness.
func sanitisePR(pr string) string {
	var b []byte
	for i := 0; i < len(pr); i++ {
		c := pr[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	sum := sha256.Sum256([]byte(pr))
	return string(b) + "-" + hex.EncodeToString(sum[:4])
}

// prDir returns the directory holding every draft for pr.
func (s *Store) prDir(pr string) string {
	return filepath.Join(s.dir, sanitisePR(pr))
}

// filePath returns the on-disk path for k's draft file: its content hash
// (over Key.String()) as the file name, under k.PR's directory.
func (s *Store) filePath(k Key) string {
	sum := sha256.Sum256([]byte(k.String()))
	return filepath.Join(s.prDir(k.PR), hex.EncodeToString(sum[:])+".json")
}

// Save writes text as k's draft, atomically. An empty text deletes the
// draft instead (see Delete): gprt never keeps an on-disk file for an
// empty draft, so a cleared composer leaves nothing behind to restore
// later.
func (s *Store) Save(k Key, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if text == "" {
		return s.deleteLocked(k)
	}

	data, err := json.Marshal(fileFormat{Key: k, Text: text, UpdatedAt: s.now()})
	if err != nil {
		return fmt.Errorf("drafts: marshal: %w", err)
	}
	if err := writeAtomic(s.filePath(k), data); err != nil {
		return fmt.Errorf("drafts: save %s: %w", k, err)
	}
	return nil
}

// Load reads k's draft, returning (Draft{}, false, nil) when none exists.
func (s *Store) Load(k Key) (Draft, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(k)
}

func (s *Store) loadLocked(k Key) (Draft, bool, error) {
	data, err := os.ReadFile(s.filePath(k))
	if errors.Is(err, os.ErrNotExist) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, fmt.Errorf("drafts: load %s: %w", k, err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return Draft{}, false, fmt.Errorf("drafts: decode %s: %w", k, err)
	}
	return Draft(ff), true, nil
}

// Delete removes k's draft. A missing draft is not an error.
func (s *Store) Delete(k Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(k)
}

func (s *Store) deleteLocked(k Key) error {
	err := os.Remove(s.filePath(k))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("drafts: delete %s: %w", k, err)
}

// List returns every draft for pr, sorted by UpdatedAt descending (most
// recently edited first). A pr with no drafts at all returns (nil, nil).
// A draft file that fails to read or decode is skipped rather than
// failing the whole call; every such failure is joined into the returned
// error (via errors.Join) so a caller can log it once instead of once per
// corrupt file.
func (s *Store) List(pr string) ([]Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.prDir(pr))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drafts: list %s: %w", pr, err)
	}

	var drafts []Draft
	var errs []error
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.prDir(pr), e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("drafts: read %s: %w", path, err))
			continue
		}
		var ff fileFormat
		if err := json.Unmarshal(data, &ff); err != nil {
			errs = append(errs, fmt.Errorf("drafts: decode %s: %w", path, err))
			continue
		}
		drafts = append(drafts, Draft(ff))
	}

	sort.Slice(drafts, func(i, j int) bool { return drafts[i].UpdatedAt.After(drafts[j].UpdatedAt) })
	return drafts, errors.Join(errs...)
}

// Count returns how many drafts exist for pr: a plain directory listing
// of its ".json" entries, deliberately never reading or decoding any of
// them the way List does — callers such as gprt's own status bar call
// Count on every render, far more often than they call List, so it must
// stay cheap regardless of how many drafts exist or whether any of them
// happen to be corrupt (which List reports as an aggregated error but
// Count does not: a corrupt file still counts as "one draft" here).
func (s *Store) Count(pr string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.prDir(pr))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("drafts: count %s: %w", pr, err)
	}

	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			n++
		}
	}
	return n, nil
}
