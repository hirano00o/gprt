package drafts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestNew_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "drafts")
	if _, err := New(dir); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	info, err := statDir(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("New() did not create a directory at %s", dir)
	}
}

func TestStore_SaveLoad_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}

	if err := s.Save(k, "hello world"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, ok, err := s.Load(k)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("Load() ok = false, want true")
	}
	if got.Text != "hello world" {
		t.Errorf("Load().Text = %q, want %q", got.Text, "hello world")
	}
	if got.Key != k {
		t.Errorf("Load().Key = %+v, want %+v", got.Key, k)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Load().UpdatedAt is zero, want non-zero")
	}
}

func TestStore_Load_Missing(t *testing.T) {
	s := newTestStore(t)
	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}

	_, ok, err := s.Load(k)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ok {
		t.Fatal("Load() ok = true for a draft never saved, want false")
	}
}

func TestStore_Save_EmptyTextDeletes(t *testing.T) {
	s := newTestStore(t)
	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}

	if err := s.Save(k, "some text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := s.Save(k, ""); err != nil {
		t.Fatalf("Save(\"\") error = %v", err)
	}

	_, ok, err := s.Load(k)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ok {
		t.Error("Load() ok = true after saving empty text, want false (no empty draft files)")
	}
}

func TestStore_Delete_Missing(t *testing.T) {
	s := newTestStore(t)
	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}

	if err := s.Delete(k); err != nil {
		t.Errorf("Delete() on a never-saved key error = %v, want nil", err)
	}
}

func TestStore_Delete_RemovesSaved(t *testing.T) {
	s := newTestStore(t)
	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}

	if err := s.Save(k, "text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, ok, err := s.Load(k)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ok {
		t.Error("Load() ok = true after Delete, want false")
	}
}

func TestStore_List_OrderedByUpdatedAtDesc(t *testing.T) {
	s := newTestStore(t)
	pr := "github.com/o/r#1"

	times := []time.Time{
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
	anchors := []string{"a", "b", "c"}
	for i, anchor := range anchors {
		when := times[i]
		s.now = func() time.Time { return when }
		if err := s.Save(Key{PR: pr, Kind: KindComment, Anchor: anchor}, "text-"+anchor); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	list, err := s.List(pr)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List() returned %d drafts, want 3", len(list))
	}
	wantOrder := []string{"b", "c", "a"} // Sep 3, Sep 2, Sep 1
	for i, want := range wantOrder {
		if list[i].Key.Anchor != want {
			t.Errorf("List()[%d].Key.Anchor = %q, want %q", i, list[i].Key.Anchor, want)
		}
	}
}

func TestStore_List_EmptyForUnknownPR(t *testing.T) {
	s := newTestStore(t)
	list, err := s.List("github.com/o/r#999")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List() = %+v, want empty", list)
	}
}

func TestStore_List_CorruptFileTolerance(t *testing.T) {
	s := newTestStore(t)
	pr := "github.com/o/r#1"

	if err := s.Save(Key{PR: pr, Kind: KindComment, Anchor: "good"}, "text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	corruptPath := filepath.Join(s.prDir(pr), "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	list, err := s.List(pr)
	if err == nil {
		t.Error("List() error = nil, want a non-nil aggregated error for the corrupt file")
	}
	if len(list) != 1 {
		t.Fatalf("List() returned %d drafts, want 1 (corrupt file skipped)", len(list))
	}
	if list[0].Key.Anchor != "good" {
		t.Errorf("List()[0].Key.Anchor = %q, want %q", list[0].Key.Anchor, "good")
	}
}

func TestStore_Count(t *testing.T) {
	s := newTestStore(t)
	pr := "github.com/o/r#1"

	if n, err := s.Count(pr); err != nil || n != 0 {
		t.Fatalf("Count() = (%d, %v), want (0, nil)", n, err)
	}

	if err := s.Save(Key{PR: pr, Kind: KindComment, Anchor: "a"}, "text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := s.Save(Key{PR: pr, Kind: KindComment, Anchor: "b"}, "text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if n, err := s.Count(pr); err != nil || n != 2 {
		t.Fatalf("Count() = (%d, %v), want (2, nil)", n, err)
	}
}

func TestStore_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "state")
	s, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if perm := rootInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("root dir mode = %o, want 0700", perm)
	}

	k := Key{PR: "github.com/o/r#1", Kind: KindComment, Anchor: "issue"}
	if err := s.Save(k, "text"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	prInfo, err := os.Stat(s.prDir(k.PR))
	if err != nil {
		t.Fatalf("stat pr dir: %v", err)
	}
	if perm := prInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("pr dir mode = %o, want 0700", perm)
	}

	fileInfo, err := os.Stat(s.filePath(k))
	if err != nil {
		t.Fatalf("stat draft file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("draft file mode = %o, want 0600", perm)
	}
}

func TestStore_ConcurrentSaves(t *testing.T) {
	s := newTestStore(t)
	pr := "github.com/o/r#1"

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			k := Key{PR: pr, Kind: KindComment, Anchor: fmt.Sprintf("anchor-%d", i%4)}
			if err := s.Save(k, fmt.Sprintf("text-%d", i)); err != nil {
				t.Errorf("Save() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	if _, err := s.List(pr); err != nil {
		t.Errorf("List() error = %v", err)
	}
}
