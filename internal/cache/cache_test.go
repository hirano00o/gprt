package cache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_PutGet_MemoryRoundTrip(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	key := Key{Host: "github.com", Login: "octocat", Rest: "search:type:pr"}
	entry := Entry{Body: []byte(`{"total":1}`), ETag: `"abc123"`, FetchedAt: time.Now().Truncate(time.Second)}

	if err := store.Put(key, entry); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, ok, err := store.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if string(got.Body) != string(entry.Body) || got.ETag != entry.ETag || !got.FetchedAt.Equal(entry.FetchedAt) {
		t.Errorf("Get() = %+v, want %+v", got, entry)
	}
}

func TestStore_Get_MissReturnsFalse(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, ok, err := store.Get(Key{Host: "github.com", Login: "octocat", Rest: "nope"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if ok {
		t.Error("Get() ok = true, want false for a key that was never Put")
	}
}

func TestStore_Get_FallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	key := Key{
		Host:  "github.com",
		Login: "octocat",
		PR:    &model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}, Number: 42},
		Rest:  "pr-detail",
	}
	entry := Entry{Body: []byte(`{"title":"hello"}`), ETag: "", FetchedAt: time.Now().Truncate(time.Second)}

	writer, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := writer.Put(key, entry); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// A fresh Store over the same directory has an empty memory map, so
	// Get must read the entry back from disk.
	reader, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, ok, err := reader.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true (disk fallback)")
	}
	if string(got.Body) != string(entry.Body) || !got.FetchedAt.Equal(entry.FetchedAt) {
		t.Errorf("Get() = %+v, want %+v", got, entry)
	}
}

func TestNew_CreatesDirWithMode0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache-root")

	if _, err := New(dir); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	assertMode(t, dir, 0o700)
}
