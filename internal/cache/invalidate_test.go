package cache

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_InvalidatePR_OnlyAffectsThatPR(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	pr42 := model.PRRef{Repo: repo, Number: 42}
	pr43 := model.PRRef{Repo: repo, Number: 43}

	keyPR42 := Key{Host: "github.com", Login: "octocat", PR: &pr42, Rest: "detail"}
	keyPR43 := Key{Host: "github.com", Login: "octocat", PR: &pr43, Rest: "detail"}
	keySearch := Key{Host: "github.com", Login: "octocat", Rest: "search"}

	for _, k := range []Key{keyPR42, keyPR43, keySearch} {
		if err := store.Put(k, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
			t.Fatalf("Put(%+v) error = %v", k, err)
		}
	}

	if err := store.InvalidatePR("github.com", "octocat", pr42); err != nil {
		t.Fatalf("InvalidatePR() error = %v", err)
	}

	if _, ok, err := store.Get(keyPR42); ok || err != nil {
		t.Errorf("Get(pr42) = (ok=%v, err=%v) after InvalidatePR, want (false, nil)", ok, err)
	}
	if _, ok, err := store.Get(keyPR43); !ok || err != nil {
		t.Errorf("Get(pr43) = (ok=%v, err=%v) after InvalidatePR(pr42), want (true, nil) (untouched)", ok, err)
	}
	if _, ok, err := store.Get(keySearch); !ok || err != nil {
		t.Errorf("Get(search) = (ok=%v, err=%v) after InvalidatePR(pr42), want (true, nil) (untouched)", ok, err)
	}

	if _, err := os.Stat(store.path(keyPR42)); !os.IsNotExist(err) {
		t.Errorf("pr42 cache file still exists on disk after InvalidatePR, Stat error = %v", err)
	}
}

func TestStore_InvalidateSearch_OnlyAffectsSearchNamespace(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	pr := model.PRRef{Repo: repo, Number: 1}

	keyPR := Key{Host: "github.com", Login: "octocat", PR: &pr, Rest: "detail"}
	keySearch := Key{Host: "github.com", Login: "octocat", Rest: "search"}

	for _, k := range []Key{keyPR, keySearch} {
		if err := store.Put(k, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
			t.Fatalf("Put(%+v) error = %v", k, err)
		}
	}

	if err := store.InvalidateSearch("github.com", "octocat"); err != nil {
		t.Fatalf("InvalidateSearch() error = %v", err)
	}

	if _, ok, err := store.Get(keySearch); ok || err != nil {
		t.Errorf("Get(search) = (ok=%v, err=%v) after InvalidateSearch, want (false, nil)", ok, err)
	}
	if _, ok, err := store.Get(keyPR); !ok || err != nil {
		t.Errorf("Get(pr) = (ok=%v, err=%v) after InvalidateSearch, want (true, nil) (untouched)", ok, err)
	}
}

func TestStore_InvalidatePR_DifferentLoginUnaffected(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	pr := model.PRRef{Repo: repo, Number: 1}

	keyAlice := Key{Host: "github.com", Login: "alice", PR: &pr, Rest: "detail"}
	keyBob := Key{Host: "github.com", Login: "bob", PR: &pr, Rest: "detail"}

	for _, k := range []Key{keyAlice, keyBob} {
		if err := store.Put(k, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
			t.Fatalf("Put(%+v) error = %v", k, err)
		}
	}

	if err := store.InvalidatePR("github.com", "alice", pr); err != nil {
		t.Fatalf("InvalidatePR() error = %v", err)
	}

	if _, ok, err := store.Get(keyAlice); ok || err != nil {
		t.Errorf("Get(alice) = (ok=%v, err=%v) after InvalidatePR(alice), want (false, nil)", ok, err)
	}
	if _, ok, err := store.Get(keyBob); !ok || err != nil {
		t.Errorf("Get(bob) = (ok=%v, err=%v) after InvalidatePR(alice), want (true, nil) (different login untouched)", ok, err)
	}
}

func TestStore_InvalidatePR_RemoveAllFailurePropagated(t *testing.T) {
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

	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	pr := model.PRRef{Repo: repo, Number: 1}
	key := Key{Host: "github.com", Login: "octocat", PR: &pr, Rest: "detail"}

	if err := store.Put(key, Entry{Body: []byte("{}"), FetchedAt: time.Now()}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// The PR directory is <owner>/<name>/pr-<n>/<hash>.json; removing
	// write permission on <name> prevents unlinking pr-<n> itself, so
	// os.RemoveAll on the PR directory fails.
	prDir := store.path(key)
	nameDir := filepath.Dir(filepath.Dir(prDir))
	if err := os.Chmod(nameDir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(nameDir, 0o700) })

	err = store.InvalidatePR("github.com", "octocat", pr)
	if err == nil {
		t.Fatal("InvalidatePR() error = nil, want the os.RemoveAll failure to be propagated")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("InvalidatePR() error = %v, want it to wrap a permission error", err)
	}
}
