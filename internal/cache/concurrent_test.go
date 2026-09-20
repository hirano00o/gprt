package cache

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

func TestStore_ConcurrentPutGet(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const (
		goroutines = 16
		iterations = 50
	)

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := Key{Host: "github.com", Login: "octocat", Rest: fmt.Sprintf("q-%d", g)}
			for i := range iterations {
				body := fmt.Appendf(nil, `{"i":%d}`, i)
				if err := store.Put(key, Entry{Body: body, FetchedAt: time.Now()}); err != nil {
					t.Errorf("Put() error = %v", err)
					return
				}
				if _, ok, err := store.Get(key); err != nil {
					t.Errorf("Get() error = %v", err)
					return
				} else if !ok {
					t.Errorf("Get() ok = false immediately after Put()")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestStore_ConcurrentGetAndInvalidatePR guards against the race where Get
// (memory miss -> disk read -> populate memory) interleaves with
// InvalidatePR (memory sweep -> RemoveAll): if Get's disk read happened to
// run between InvalidatePR's memory sweep and its RemoveAll, Get would put
// the just-invalidated entry straight back into memory. Get and
// InvalidatePR must therefore never observe each other mid-operation.
func TestStore_ConcurrentGetAndInvalidatePR(t *testing.T) {
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

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _, _ = store.Get(key)
			}
		}
	}()

	// Give the reader goroutine a chance to start racing Get against the
	// invalidation below.
	time.Sleep(time.Millisecond)

	if err := store.InvalidatePR("github.com", "octocat", pr); err != nil {
		t.Fatalf("InvalidatePR() error = %v", err)
	}

	close(stop)
	wg.Wait()

	// Get and InvalidatePR share one lock for their entire duration, so by
	// the time InvalidatePR above returned, no concurrent Get could still
	// be resurrecting the entry: it must be gone right now.
	if _, ok, err := store.Get(key); ok || err != nil {
		t.Errorf("Get() = (ok=%v, err=%v) after InvalidatePR returned, want (false, nil)", ok, err)
	}
}

// TestStore_ConcurrentPutAndInvalidatePR guards against the mirror-image
// race: a Put in flight when InvalidatePR runs could otherwise write its
// disk file (recreating the just-deleted directory) after
// InvalidatePR's RemoveAll, resurrecting an entry the invalidation was
// meant to remove. Put now holds Store's lock across its own memory-and-
// disk write, the same way Get does, so it can never straddle an
// invalidation. Because goroutine scheduling can't force one exact
// interleaving, this asserts what the locking guarantees structurally: no
// data race (caught by -race); that memory and disk agree once the race
// has settled (neither can resurrect the other); and, on top of that, a
// definitive final Put/Get round-trip is still perfectly coherent —
// proving the store never ends up in a torn state, which the old code
// could produce.
func TestStore_ConcurrentPutAndInvalidatePR(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	pr := model.PRRef{Repo: repo, Number: 1}
	key := Key{Host: "github.com", Login: "octocat", PR: &pr, Rest: "detail"}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				body := fmt.Appendf(nil, `{"i":%d}`, i)
				_ = store.Put(key, Entry{Body: body, FetchedAt: time.Now()})
				i++
			}
		}
	}()

	time.Sleep(time.Millisecond)

	if err := store.InvalidatePR("github.com", "octocat", pr); err != nil {
		t.Fatalf("InvalidatePR() error = %v", err)
	}

	close(stop)
	wg.Wait()

	// Get and InvalidatePR share one lock for their entire duration, so
	// whichever of the racing Put calls and the single InvalidatePR call
	// finished last fully determines the visible state: memory and disk
	// must never disagree about whether the key exists. A Put that
	// completed before InvalidatePR started must not leave a disk file
	// that a fresh (memory-empty) Store would later resurrect.
	path := store.path(key)
	store.mu.Lock()
	_, inMem := store.mem[path]
	store.mu.Unlock()
	_, statErr := os.Stat(path)
	onDisk := statErr == nil
	if inMem != onDisk {
		t.Fatalf("memory and disk disagree after the race settled: inMem=%v onDisk=%v", inMem, onDisk)
	}

	final := Entry{Body: []byte(`{"final":true}`), FetchedAt: time.Now()}
	if err := store.Put(key, final); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, ok, err := store.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || string(got.Body) != string(final.Body) {
		t.Errorf("Get() = (%+v, %v) after the race settled, want the definitive final entry %+v", got, ok, final)
	}
}
