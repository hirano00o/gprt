package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

// TestStore_Path_ExactOnDiskLayout pins the documented disk layout exactly,
// using only safe (unescaped) path segments so the expected path can be
// built with a plain filepath.Join rather than by re-implementing
// sanitize.
func TestStore_Path_ExactOnDiskLayout(t *testing.T) {
	store := &Store{dir: "/cache"}

	t.Run("PR-scoped key", func(t *testing.T) {
		repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
		pr := model.PRRef{Repo: repo, Number: 42}
		key := Key{Host: "github.com", Login: "octocat", PR: &pr, Rest: "detail"}

		got := store.path(key)
		hash := sha256.Sum256([]byte("detail"))
		want := filepath.Join(
			// "repos" sits between login and owner so the PR tree can
			// never overlap the search tree, even for an owner literally
			// named "search" (see TestStore_PRDirAndSearchDirNeverOverlap).
			"/cache", "v1", "github.com", "octocat", "repos", "hirano00o", "gprt", "pr-42",
			hex.EncodeToString(hash[:])+".json",
		)
		if got != want {
			t.Errorf("path() = %q, want %q", got, want)
		}
	})

	t.Run("search key", func(t *testing.T) {
		key := Key{Host: "github.com", Login: "octocat", Rest: "search:type:pr"}

		got := store.path(key)
		hash := sha256.Sum256([]byte("search:type:pr"))
		want := filepath.Join(
			"/cache", "v1", "github.com", "octocat", "search",
			hex.EncodeToString(hash[:])+".json",
		)
		if got != want {
			t.Errorf("path() = %q, want %q", got, want)
		}
	})
}

// TestStore_PRDirAndSearchDirNeverOverlap guards against a real GitHub
// owner (or the sanitized form of any other field) ever being named
// "search": prDir and searchDir must always land under disjoint fixed
// segments, so InvalidateSearch's os.RemoveAll("<login>/search") can never
// also delete that owner's PR caches.
func TestStore_PRDirAndSearchDirNeverOverlap(t *testing.T) {
	store := &Store{dir: "/cache"}

	repo := model.RepoRef{Host: "github.com", Owner: "search", Name: "gprt"}
	pr := model.PRRef{Repo: repo, Number: 1}

	prDir := store.prDir("github.com", "octocat", pr)
	searchDir := store.searchDir("github.com", "octocat")

	if prDir == searchDir || strings.HasPrefix(prDir, searchDir+string(filepath.Separator)) {
		t.Errorf("prDir (owner=%q) = %q lies under searchDir = %q, want disjoint trees", repo.Owner, prDir, searchDir)
	}
}
