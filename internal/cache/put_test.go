package cache

import (
	"testing"
	"time"
)

func TestStore_Put_RejectsInvalidEntry(t *testing.T) {
	tests := []struct {
		name string
		e    Entry
	}{
		{"nil body", Entry{Body: nil, FetchedAt: time.Now()}},
		{"zero fetched at", Entry{Body: []byte("{}"), FetchedAt: time.Time{}}},
		{"nil body and zero fetched at", Entry{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
			if err := store.Put(key, tc.e); err == nil {
				t.Error("Put() error = nil, want an error for an entry that readDiskEntry would treat as corrupt")
			}
		})
	}
}

func TestStore_Put_AcceptsEmptyButNonNilBody(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	key := Key{Host: "github.com", Login: "octocat", Rest: "q"}
	e := Entry{Body: []byte{}, FetchedAt: time.Now()}
	if err := store.Put(key, e); err != nil {
		t.Fatalf("Put() error = %v, want nil ([]byte{} is a valid, non-nil body)", err)
	}
}
