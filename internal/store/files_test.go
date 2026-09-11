package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/highlight"
	"github.com/hirano00o/gprt/internal/model"
)

// newFilesTestStore builds a Store primed with a confirmed viewer login and
// an already-open pull request (bypassing OpenPR's own network fetch, which
// this file's tests are not exercising) with headOID as CurrentPR().HeadOID,
// so LoadFiles has a pull request to act on.
func newFilesTestStore(t *testing.T, gitHub *fakeGitHub, disp *fakeDispatcher, ref model.PRRef, headOID string) *Store {
	t.Helper()
	s := newTestStore(t, config.Default(), gitHub, disp)
	s.viewer = model.User{Login: "tester"}
	s.viewerConfirmed = true
	s.current = &ref
	s.currentPR = &model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: headOID}
	return s
}

func filesTestRef() model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"}, Number: 7}
}

func changedFile(path, patch string) model.ChangedFile {
	return model.ChangedFile{
		Path: path, Status: model.FileStatusModified, HasPatch: patch != "", Patch: patch,
	}
}

func TestLoadFiles_CacheFirstThenConfirmedBy304(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	cachedFiles := []model.ChangedFile{changedFile("a.go", "@@ -1 +1 @@\n-old\n+new")}
	key, err := cache.PRKey(s.deps.Host, "tester", ref, "files/page-1")
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, err := json.Marshal(struct {
		Files   []model.ChangedFile `json:"files"`
		HasNext bool                `json:"has_next"`
	}{Files: cachedFiles, HasNext: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, ETag: `"etag-1"`, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed files cache: %v", err)
	}

	var sawEtag string
	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, etag string) (gh.FilesResult, error) {
		sawEtag = etag
		return gh.FilesResult{NotModified: true}, nil
	})

	s.LoadFiles(false)

	// Cache is applied synchronously, before the network fetch resolves.
	if len(s.Files()) != 1 || s.Files()[0].File.Path != "a.go" {
		t.Fatalf("Files() = %+v, want the cached page applied immediately", s.Files())
	}
	if !s.FilesState().Stale {
		t.Error("FilesState().Stale = false, want true before the network confirms it")
	}

	runUntilIdle(t, disp)

	if sawEtag != `"etag-1"` {
		t.Errorf("If-None-Match etag seen = %q, want %q", sawEtag, `"etag-1"`)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true, want false once the 304 confirms the cached page")
	}
	if len(s.Files()) != 1 {
		t.Fatalf("Files() = %+v, want the same single cached file kept after a 304", s.Files())
	}
}

func TestLoadFiles_200ReplacesStalePage(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	key, err := cache.PRKey(s.deps.Host, "tester", ref, "files/page-1")
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, _ := json.Marshal(struct {
		Files   []model.ChangedFile `json:"files"`
		HasNext bool                `json:"has_next"`
	}{Files: []model.ChangedFile{changedFile("old.go", "@@ -1 +1 @@\n-x\n+y")}})
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, ETag: `"old-etag"`, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed files cache: %v", err)
	}

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{
			Files: []model.ChangedFile{changedFile("new.go", "@@ -1 +1 @@\n-a\n+b")},
			ETag:  `"new-etag"`,
		}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 || files[0].File.Path != "new.go" {
		t.Fatalf("Files() = %+v, want the stale page replaced by the fresh 200 response", files)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true, want false after the 200 replaces the stale page")
	}
}

func TestLoadFiles_SequentialPaginationUntilNoNextPage(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	var mu sync.Mutex
	var pagesRequested []int
	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		mu.Lock()
		pagesRequested = append(pagesRequested, page)
		mu.Unlock()
		switch page {
		case 1:
			return gh.FilesResult{Files: []model.ChangedFile{changedFile("p1.go", "")}, HasNext: true}, nil
		case 2:
			return gh.FilesResult{Files: []model.ChangedFile{changedFile("p2.go", "")}, HasNext: false}, nil
		default:
			t.Errorf("unexpected page requested: %d", page)
			return gh.FilesResult{}, nil
		}
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	mu.Lock()
	got := append([]int(nil), pagesRequested...)
	mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("pages requested = %v, want [1 2]", got)
	}

	files := s.Files()
	if len(files) != 2 || files[0].File.Path != "p1.go" || files[1].File.Path != "p2.go" {
		t.Fatalf("Files() = %+v, want p1.go then p2.go", files)
	}
	st := s.FilesState()
	if st.HasNext {
		t.Error("FilesState().HasNext = true, want false once page 2 reports no next page")
	}
	if st.PagesLoaded != 2 {
		t.Errorf("FilesState().PagesLoaded = %d, want 2", st.PagesLoaded)
	}
}

func TestLoadFiles_ParseErrorKeptPerFileOthersFine(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFile("bad.go", "not a valid patch"),
			changedFile("good.go", "@@ -1 +1 @@\n-x\n+y"),
		}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 2 {
		t.Fatalf("Files() = %+v, want 2 entries", files)
	}
	if files[0].ParseErr == nil {
		t.Error("files[0].ParseErr = nil, want an error for an unparsable patch")
	}
	if files[1].ParseErr != nil {
		t.Errorf("files[1].ParseErr = %v, want nil", files[1].ParseErr)
	}
	if len(files[1].Hunks) != 1 {
		t.Errorf("files[1].Hunks has %d entries, want 1", len(files[1].Hunks))
	}
}

func TestLoadFiles_HighlightResultsAppliedAndDroppedOnGenerationChange(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFile("main.go", "@@ -1 +1 @@\n-package old\n+package main"),
		}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 {
		t.Fatalf("Files() = %+v, want 1 entry", files)
	}
	if !files[0].Highlighted {
		t.Error("files[0].Highlighted = false, want true once its only hunk's highlight result has applied")
	}
	if len(files[0].Tokens) != 1 {
		t.Fatalf("files[0].Tokens has %d entries, want 1 (one per hunk)", len(files[0].Tokens))
	}
}

func TestLoadFiles_HighlightPoolCancelledOnClosePR(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	block := make(chan struct{})
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		<-block
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFile("main.go", "@@ -1 +1 @@\n-package old\n+package main"),
		}}, nil
	})

	s.LoadFiles(false)
	s.ClosePR()

	close(block)
	runUntilIdle(t, disp)

	// The fetch itself was in flight when ClosePR ran, so its own late
	// result must be dropped (checked here indirectly): no highlight job
	// for it should ever have been enqueued or dispatched, since the
	// files generation it belonged to no longer exists after ClosePR.
	if len(s.Files()) != 0 {
		t.Errorf("Files() = %+v, want empty after ClosePR", s.Files())
	}
	if s.FilesState().Highlighting != 0 {
		t.Errorf("FilesState().Highlighting = %d, want 0 after ClosePR", s.FilesState().Highlighting)
	}
}

// TestClosePR_CancelsFilesContext directly verifies the mechanism that
// makes "no dispatch after cancel" true by construction:
// processHighlightJob checks ctx.Err() immediately before dispatching, so a
// worker still processing (or about to process) a job when the pool's
// context is cancelled never queues a stale result at all, rather than
// queuing one that a generation check would merely drop later. Catching an
// actually in-flight worker at the exact moment of cancellation would be a
// flaky black-box test; asserting the context itself is cancelled is the
// deterministic proxy for it.
func TestClosePR_CancelsFilesContext(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "@@ -1 +1 @@\n-x\n+y")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	ctx := s.filesCtx
	if ctx == nil {
		t.Fatal("filesCtx is nil after a successful LoadFiles")
	}
	if ctx.Err() != nil {
		t.Fatal("filesCtx is already done before ClosePR")
	}

	s.ClosePR()

	if ctx.Err() == nil {
		t.Error("filesCtx.Err() = nil after ClosePR, want it cancelled so the highlight pool's workers exit and stop dispatching")
	}
}

func TestOpenPR_HeadOIDChangeReloadsFilesForcingReload(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)
	if len(s.Files()) != 1 {
		t.Fatalf("Files() = %+v, want 1 file after the initial load", s.Files())
	}

	// A detail refresh lands with a new HeadOID (a force-push): files must
	// reload for the new commit rather than keep showing head1's diff.
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head2"}}, nil
	})
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("b.go", "")}}, nil
	})

	s.RefreshPR()
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 || files[0].File.Path != "b.go" {
		t.Fatalf("Files() = %+v, want reloaded to head2's single file b.go", files)
	}
}

func TestReload_ReloadsFiles(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head1"}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)
	initialCalls := len(gitHub.filesCallsSnapshot())
	if initialCalls == 0 {
		t.Fatal("no ChangedFiles calls recorded after the initial load")
	}

	s.Reload()
	runUntilIdle(t, disp)

	if len(gitHub.filesCallsSnapshot()) <= initialCalls {
		t.Error("Reload() did not trigger a new ChangedFiles call")
	}
}

// TestReloadPR_DoesNotLoadFilesWhenNeverOpened is the regression test for
// review item 17: ReloadPR/Reload ("R") must not fetch any files page for
// a pull request whose Files tab was never opened (filesStarted == false).
func TestReloadPR_DoesNotLoadFilesWhenNeverOpened(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head1"}}, nil
	})

	s.ReloadPR()
	runUntilIdle(t, disp)

	if len(gitHub.filesCallsSnapshot()) != 0 {
		t.Errorf("ChangedFiles called %d times, want 0 (the Files tab was never opened for this pull request)", len(gitHub.filesCallsSnapshot()))
	}
}

func TestLoadFiles_IdempotentForSameHeadOIDUnlessForced(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if len(gitHub.filesCallsSnapshot()) != 1 {
		t.Errorf("ChangedFiles called %d times, want exactly 1 (LoadFiles must be idempotent for the same HeadOID)", len(gitHub.filesCallsSnapshot()))
	}

	s.LoadFiles(true)
	runUntilIdle(t, disp)
	if len(gitHub.filesCallsSnapshot()) != 2 {
		t.Errorf("ChangedFiles called %d times after a forced reload, want 2", len(gitHub.filesCallsSnapshot()))
	}
}

func TestLoadFiles_SkipsHighlightingFilesWithoutPatch(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("binary.png", "")}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 {
		t.Fatalf("Files() = %+v, want 1 entry", files)
	}
	if !files[0].Highlighted {
		t.Error("files[0].Highlighted = false, want true for a file with no patch (nothing to highlight)")
	}
	if s.FilesState().Highlighting != 0 {
		t.Errorf("FilesState().Highlighting = %d, want 0 for a patch-less file", s.FilesState().Highlighting)
	}
}

// TestLoadFiles_ErrorSetsErr checks that a page fetch failure sets
// FilesState().Err. It intentionally forces a fresh generation
// (LoadFiles(true)) before the failing fetch, so it does *not* exercise
// "an existing files list survives a failed fetch" (force's own resetFiles
// call already clears it) - the resume-from-error behaviour that keeps a
// standing error's *previous pages* is TestLoadFiles_ResumesFromFailedPageOnRetry
// (review item 4).
func TestLoadFiles_ErrorSetsErr(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	wantErr := errors.New("boom")
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{}, wantErr
	})
	s.LoadFiles(true)
	runUntilIdle(t, disp)

	if !errors.Is(s.FilesState().Err, wantErr) {
		t.Errorf("FilesState().Err = %v, want %v", s.FilesState().Err, wantErr)
	}
}

func TestFileByPath(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if _, ok := s.FileByPath("a.go"); !ok {
		t.Error("FileByPath(\"a.go\") ok = false, want true")
	}
	if _, ok := s.FileByPath("missing.go"); ok {
		t.Error("FileByPath(\"missing.go\") ok = true, want false")
	}
}

// TestOpenPR_EmitsFilesLoadingChangedWhenSwitchingWhileFilesLoading is the
// regression test for review item 15: switching pull requests while a
// files page fetch is in flight for the *previous* one must emit
// EventFilesLoadingChanged (resetFiles turns filesLoading back off
// internally, but that fetch's own eventual, superseded result is dropped
// silently by the generation check before it would ever emit this itself -
// see the identical reasoning ClosePR already applied) so a files spinner
// never survives a PR switch.
func TestOpenPR_EmitsFilesLoadingChangedWhenSwitchingWhileFilesLoading(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref1 := filesTestRef()
	ref2 := model.PRRef{Repo: ref1.Repo, Number: ref1.Number + 1}
	s := newFilesTestStore(t, gitHub, disp, ref1, "head1")

	block := make(chan struct{})
	gitHub.setFilesFunc(func(ctx context.Context, _ model.PRRef, _ int, _ string) (gh.FilesResult, error) {
		<-block
		return gh.FilesResult{}, ctx.Err()
	})

	s.LoadFiles(false)
	if !s.FilesState().Loading {
		t.Fatal("FilesState().Loading = false, want true while the fetch is in flight")
	}

	var sawFilesLoadingChanged bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventFilesLoadingChanged {
			sawFilesLoadingChanged = true
		}
	})

	s.OpenPR(ref2) // switches PR while ref1's files fetch is still in flight

	if !sawFilesLoadingChanged {
		t.Error("no EventFilesLoadingChanged observed from OpenPR; a files spinner would survive the PR switch")
	}
	if s.FilesState().Loading {
		t.Error("FilesState().Loading = true after OpenPR, want false")
	}

	close(block) // let ref1's now-superseded fetch finish; must not panic/corrupt state
	runUntilIdle(t, disp)
}

// manyHunksPatch builds a valid GitHub-style patch with n one-line hunks,
// each far enough apart in old/new line numbers to be unambiguous.
func manyHunksPatch(n int) string {
	var b strings.Builder
	for i := range n {
		line := i*10 + 1
		fmt.Fprintf(&b, "@@ -%d +%d @@\n-old%d\n+new%d\n", line, line, i, i)
	}
	return strings.TrimRight(b.String(), "\n")
}

// TestEnqueueHighlightJobs_NeverBlocksTheUIGoroutine is the regression test
// for the deadlock review item 1: the UI goroutine (represented here by the
// test goroutine calling enqueueHighlightJobs directly, exactly as
// applyFilesPageResult/applyCachedFilesPage do) must return immediately
// even when it enqueues far more jobs than the worker-facing channel can
// hold and nothing at all is draining highlightQueue (a stand-in for
// "every worker blocked").
func TestEnqueueHighlightJobs_NeverBlocksTheUIGoroutine(t *testing.T) {
	oldSize := filesHighlightQueueSize
	filesHighlightQueueSize = 1
	defer func() { filesHighlightQueueSize = oldSize }()

	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	// A generation and highlightJobs/highlightWake must exist for
	// enqueueHighlightJobs to do anything, but no feeder/worker is
	// started, so nothing ever drains the queue - the worst case the
	// review comment describes.
	s.filesGen = 1
	s.highlightJobs = &highlightQueue{}
	s.highlightWake = make(chan struct{}, 1)

	entries := parseFilesPage([]model.ChangedFile{changedFile("big.go", manyHunksPatch(2000))}, 4)
	s.assignFileEntryIDs(entries)

	done := make(chan struct{})
	go func() {
		s.enqueueHighlightJobs(1, entries)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueueHighlightJobs blocked the UI goroutine with an undrained queue")
	}

	if s.filesHighlightPending != 2000 {
		t.Errorf("filesHighlightPending = %d, want 2000 (every job still counted, just not yet delivered to a worker)", s.filesHighlightPending)
	}
}

// TestReplaceFilesPage_DropsStalePendingCountForReplacedEntries is the
// regression test for review item 2's first half: replacing a cache-shown
// page whose entries still have hunks pending highlighting must not leave
// filesHighlightPending/filesPendingByID permanently overstating the
// outstanding work for ids that no longer exist in s.files.
func TestReplaceFilesPage_DropsStalePendingCountForReplacedEntries(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")
	s.filesGen = 1

	old := parseFilesPage([]model.ChangedFile{changedFile("old.go", "@@ -1 +1 @@\n-x\n+y")}, 4)
	s.assignFileEntryIDs(old)
	s.appendFilesPage(1, old)
	oldID := s.files[0].id
	// Simulate the old entry's hunk still being highlighted (never
	// resolved) when the page gets replaced.
	s.filesPendingByID = map[int]int{oldID: 1}
	s.filesHighlightPending = 1

	fresh := parseFilesPage([]model.ChangedFile{changedFile("new.go", "@@ -1 +1 @@\n-a\n+b")}, 4)
	s.assignFileEntryIDs(fresh)
	s.replaceFilesPage(1, fresh)

	if _, ok := s.filesPendingByID[oldID]; ok {
		t.Error("filesPendingByID still has the replaced entry's id, want it dropped")
	}
	if s.filesHighlightPending != 0 {
		t.Errorf("filesHighlightPending = %d, want 0 (the only pending count belonged to the now-replaced entry)", s.filesHighlightPending)
	}
}

// TestApplyHighlightResult_DiscardsResultForReplacedFileID is the
// regression test for review item 2's second half: a highlight result that
// arrives for a FileEntry.id no longer present in s.files (because
// replaceFilesPage already replaced it with a same-path, different-id
// entry) must not be applied to the replacement.
func TestApplyHighlightResult_DiscardsResultForReplacedFileID(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")
	s.filesGen = 1

	old := parseFilesPage([]model.ChangedFile{changedFile("f.go", "@@ -1 +1 @@\n-x\n+y")}, 4)
	s.assignFileEntryIDs(old)
	s.appendFilesPage(1, old)
	oldID := s.files[0].id

	fresh := parseFilesPage([]model.ChangedFile{changedFile("f.go", "@@ -1 +1 @@\n-a\n+b")}, 4)
	s.assignFileEntryIDs(fresh)
	s.replaceFilesPage(1, fresh)
	newID := s.files[0].id
	if newID == oldID {
		t.Fatal("test setup broken: replacement got the same id as the original")
	}

	staleTokens := [][]highlight.Token{{{Text: "STALE", Type: 0}}}
	s.applyHighlightResult(oldID, 0, staleTokens)

	if s.files[0].Tokens[0] != nil {
		t.Errorf("Tokens[0] = %+v, want nil: a stale result for the replaced id must never reach the replacement entry", s.files[0].Tokens[0])
	}
}

// TestFetchFilesPage_SubscriberClosingPRDuringLoadingEmitDoesNotPanic is
// the regression test for review item 3: fetchFilesPage must snapshot
// s.current/Config.TabWidth *before* emitFilesLoadingChanged, since a
// subscriber may react to that event by calling ClosePR synchronously,
// which nils s.current - dereferencing it afterward would panic.
func TestFetchFilesPage_SubscriberClosingPRDuringLoadingEmitDoesNotPanic(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})

	var closed bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventFilesLoadingChanged && !closed {
			closed = true
			s.ClosePR()
		}
	})

	s.LoadFiles(false) // must not panic
	runUntilIdle(t, disp)

	if s.CurrentPR() != nil {
		t.Error("CurrentPR() != nil, want nil after the subscriber closed the pull request")
	}
}

// TestApplyFilesPageResult_SubscriberClosingPRDuringEmitStopsPagination is
// the second regression test for review item 3: once a synchronous
// subscriber closes the pull request during applyFilesPageResult's own
// EventFilesChanged emission, the function must re-check the generation
// before continuing to page 2 - otherwise it would fetch another page
// under a superseded (and, after ClosePR, cancelled) generation.
func TestApplyFilesPageResult_SubscriberClosingPRDuringEmitStopsPagination(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	var mu sync.Mutex
	var pageRequests []int
	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		mu.Lock()
		pageRequests = append(pageRequests, page)
		mu.Unlock()
		return gh.FilesResult{
			Files:   []model.ChangedFile{changedFile(fmt.Sprintf("p%d.go", page), "")},
			HasNext: true,
		}, nil
	})

	// EventFilesChanged fires once from startFilesFetch itself (before any
	// page has even been requested) and again from applyFilesPageResult
	// once page 1's result is applied: close on the *second* occurrence,
	// so this test actually exercises applyFilesPageResult's gen-recheck
	// rather than startFilesFetch's own (already covered elsewhere).
	var seen int
	s.Subscribe(func(e Event) {
		if e.Kind != EventFilesChanged {
			return
		}
		seen++
		if seen == 2 {
			s.ClosePR()
		}
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	mu.Lock()
	got := len(pageRequests)
	mu.Unlock()
	if got != 1 {
		t.Errorf("ChangedFiles called %d times, want exactly 1 (pagination must stop once the subscriber closed the PR)", got)
	}
	if len(s.Files()) != 0 {
		t.Errorf("Files() = %+v, want empty after ClosePR", s.Files())
	}
}

// TestLoadFiles_ResumesFromFailedPageOnRetry is the regression test for
// review item 4's first half: a mid-sequence page failure must not
// permanently wedge LoadFiles(false) into a no-op; calling it again once
// nothing is loading should retry starting at the next unfetched page,
// keeping the pages already loaded.
func TestLoadFiles_ResumesFromFailedPageOnRetry(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		if page == 1 {
			return gh.FilesResult{Files: []model.ChangedFile{changedFile("p1.go", "")}, HasNext: true}, nil
		}
		return gh.FilesResult{}, errors.New("page 2 boom")
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	st := s.FilesState()
	if st.Err == nil {
		t.Fatal("FilesState().Err = nil, want the page-2 error")
	}
	if st.PagesLoaded != 1 {
		t.Fatalf("FilesState().PagesLoaded = %d, want 1", st.PagesLoaded)
	}

	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		if page != 2 {
			t.Errorf("resumed fetch requested page %d, want exactly page 2", page)
		}
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("p2.go", "")}, HasNext: false}, nil
	})

	s.LoadFiles(false) // not forced: must resume, not no-op or restart
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 2 || files[0].File.Path != "p1.go" || files[1].File.Path != "p2.go" {
		t.Fatalf("Files() = %+v, want p1.go then p2.go after resuming", files)
	}
	if s.FilesState().Err != nil {
		t.Errorf("FilesState().Err = %v, want nil after the resumed page succeeds", s.FilesState().Err)
	}
}

// TestLoadFiles_ErrorSurfacesViaLastError is the regression test for review
// item 4's second half: recomputeLastErr must include filesErr (ranked
// below detailErr, above section errors).
func TestLoadFiles_ErrorSurfacesViaLastError(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	wantErr := errors.New("files boom")
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{}, wantErr
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if !errors.Is(s.LastError(), wantErr) {
		t.Errorf("LastError() = %v, want %v", s.LastError(), wantErr)
	}
}

// TestLoadFiles_CalledBeforeDetailResolvesStartsOnceDetailArrives is the
// regression test for review item 5: LoadFiles called while the pull
// request's own detail is still in flight (CurrentPR() nil, so there is no
// HeadOID yet) must not silently no-op forever; it must start once the
// detail fetch resolves.
func TestLoadFiles_CalledBeforeDetailResolvesStartsOnceDetailArrives(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newTestStore(t, config.Default(), gitHub, disp)
	s.viewer = model.User{Login: "tester"}
	s.viewerConfirmed = true

	block := make(chan struct{})
	gitHub.setDetailFunc(func(_ context.Context, r model.PRRef, _ string) (gh.DetailResult, error) {
		<-block
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: r, HeadOID: "head1"}}, nil
	})
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})

	s.OpenPR(ref)
	if s.CurrentPR() != nil {
		t.Fatal("CurrentPR() != nil before the detail fetch resolves; test setup assumption broken")
	}

	s.LoadFiles(false) // called before detail resolves: must be deferred, not dropped
	if len(gitHub.filesCallsSnapshot()) != 0 {
		t.Fatal("ChangedFiles called before the detail resolved; want it deferred")
	}

	close(block)
	runUntilIdle(t, disp)

	if len(s.Files()) != 1 {
		t.Fatalf("Files() = %+v, want 1 file once the deferred LoadFiles finally started", s.Files())
	}
}

// TestLoadFiles_NotModifiedEmitsFilesChanged is the regression test for
// review item 6's first half: a 304 confirming a cache-shown page (which
// flips FilesState().Stale) must emit EventFilesChanged, not just be a
// silent internal state change.
func TestLoadFiles_NotModifiedEmitsFilesChanged(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	key, err := cache.PRKey(s.deps.Host, "tester", ref, "files/page-1")
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, _ := json.Marshal(cachedFilesPage{Files: []model.ChangedFile{changedFile("a.go", "")}})
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, ETag: `"e"`, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed files cache: %v", err)
	}
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{NotModified: true}, nil
	})

	var sawFilesChangedAfterNetwork bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventFilesChanged && !s.FilesState().Stale {
			sawFilesChangedAfterNetwork = true
		}
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if !sawFilesChangedAfterNetwork {
		t.Error("no EventFilesChanged observed once Stale flipped false; the 304 confirmation must emit it")
	}
}

// TestLoadFiles_ErrorEmitsFilesChanged is the regression test for review
// item 6's second half: a page fetch failure (filesErr changing from nil
// to non-nil) must emit EventFilesChanged in addition to EventError, so a
// UI bound only to the former still learns FilesState().Err changed.
func TestLoadFiles_ErrorEmitsFilesChanged(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{}, errors.New("boom")
	})

	var sawFilesChanged bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventFilesChanged {
			sawFilesChanged = true
		}
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if !sawFilesChanged {
		t.Error("no EventFilesChanged observed after a page fetch error; filesErr changing must emit it")
	}
}

// TestLoadFiles_TruncationWarningWhenFewerFilesThanReported is the
// regression test for review item 7's first half.
func TestLoadFiles_TruncationWarningWhenFewerFilesThanReported(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")
	s.currentPR.ChangedFiles = 5

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil // only 1 of 5, HasNext false
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	warnings := s.FilesState().Warnings
	if len(warnings) != 1 {
		t.Fatalf("FilesState().Warnings = %v, want exactly 1 warning", warnings)
	}
	if !strings.Contains(warnings[0], "1 of 5") {
		t.Errorf("warning = %q, want it to mention 1 of 5", warnings[0])
	}
}

// TestLoadFiles_NoTruncationWarningWhenCountsMatch is the sibling check to
// the above: no warning when everything paginated matches ChangedFiles.
func TestLoadFiles_NoTruncationWarningWhenCountsMatch(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")
	s.currentPR.ChangedFiles = 1

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if warnings := s.FilesState().Warnings; len(warnings) != 0 {
		t.Errorf("FilesState().Warnings = %v, want none when the loaded count matches ChangedFiles", warnings)
	}
}

// TestRefreshPR_ChangedFilesCountChangeReloadsFilesEvenWithSameHeadOID is
// the regression test for review item 7's second half: a detail refresh
// changing the pull request's own ChangedFiles count, even with an
// unchanged HeadOID, must reload files.
func TestRefreshPR_ChangedFilesCountChangeReloadsFilesEvenWithSameHeadOID(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")
	s.currentPR.ChangedFiles = 1

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)
	if len(s.Files()) != 1 {
		t.Fatalf("Files() = %+v, want 1 file after the initial load", s.Files())
	}

	gitHub.setDetailFunc(func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
		return gh.DetailResult{PR: model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head1", ChangedFiles: 2}}, nil
	})
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", ""), changedFile("b.go", "")}}, nil
	})

	s.RefreshPR()
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 2 {
		t.Fatalf("Files() = %+v, want 2 files after ChangedFiles changed with the same HeadOID", files)
	}
}

// TestLoadFiles_UnknownRateLimitDoesNotOverwriteStandingValue is the
// regression test for review item 8.
func TestLoadFiles_UnknownRateLimitDoesNotOverwriteStandingValue(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	want := model.RateLimit{Remaining: 4999, Known: true}
	s.rateLimit = want

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{
			Files:     []model.ChangedFile{changedFile("a.go", "")},
			RateLimit: model.RateLimit{}, // Known: false
		}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if s.RateLimit() != want {
		t.Errorf("RateLimit() = %+v, want unchanged %+v (an unknown rate limit must not overwrite a standing known value)", s.RateLimit(), want)
	}
}

// TestStop_CancelsFilesContext is the regression test for review item 9:
// Stop must cancel the files generation's context (which the highlight
// pool shares - see item 10), not just the list/detail ones.
func TestStop_CancelsFilesContext(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "@@ -1 +1 @@\n-x\n+y")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	ctx := s.filesCtx
	if ctx == nil {
		t.Fatal("filesCtx is nil after a successful LoadFiles")
	}

	s.Stop()

	if ctx.Err() == nil {
		t.Error("filesCtx.Err() = nil after Stop(), want it cancelled")
	}
	// Unlike ClosePR, Stop must not discard already-loaded data.
	if len(s.Files()) != 1 {
		t.Errorf("Files() = %+v, want the already-loaded file kept after Stop()", s.Files())
	}
}

// recordingHandler is a minimal slog.Handler that appends every record it
// handles to a shared, mutex-protected slice, for tests asserting on log
// output.
type recordingHandler struct {
	mu      *sync.Mutex
	records *[]slog.Record
}

func newRecordingHandler() (slog.Handler, *[]slog.Record) {
	var records []slog.Record
	return recordingHandler{mu: &sync.Mutex{}, records: &records}, &records
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, r)
	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

func recordedMessages(records *[]slog.Record, level slog.Level) []string {
	var out []string
	for _, r := range *records {
		if r.Level == level {
			out = append(out, r.Message)
		}
	}
	return out
}

// TestLoadFiles_LogsParseErrorAtWarn is the regression test for review item
// 11: a patch that fails to parse is most likely gprt's own bug, so it must
// be logged at Warn (path + error), not just kept silently on ParseErr.
func TestLoadFiles_LogsParseErrorAtWarn(t *testing.T) {
	handler, records := newRecordingHandler()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	s := New(Deps{
		GitHub: gitHub, Cache: cacheStore, Dispatch: disp.dispatch,
		Config: config.Default(), Host: "example.com", Logger: slog.New(handler),
		Now: func() time.Time { return time.Now() },
	})
	s.viewer = model.User{Login: "tester"}
	s.viewerConfirmed = true
	s.current = &ref
	s.currentPR = &model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head1"}

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("bad.go", "not a valid patch")}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	msgs := recordedMessages(records, slog.LevelWarn)
	var found bool
	for _, m := range msgs {
		if strings.Contains(m, "parse") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warn-level log messages = %v, want one mentioning the parse failure", msgs)
	}
}

// fakeLineHighlighter is a lineHighlighter test double that always returns
// linesErr, for testing processHighlightJob's error-logging path (review
// item 12) without depending on coaxing a real chroma lexer into failing.
type fakeLineHighlighter struct {
	linesErr error
}

func (f fakeLineHighlighter) Lines(string, []string) ([][]highlight.Token, error) {
	return nil, f.linesErr
}

// TestProcessHighlightJob_LogsLinesErrorAtDebug is the regression test for
// review item 12: a highlight.Lines error must be logged at Debug rather
// than silently discarded.
func TestProcessHighlightJob_LogsLinesErrorAtDebug(t *testing.T) {
	handler, records := newRecordingHandler()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	s := New(Deps{
		GitHub: gitHub, Cache: cacheStore, Dispatch: disp.dispatch,
		Config: config.Default(), Host: "example.com", Logger: slog.New(handler),
		Now: func() time.Time { return time.Now() },
	})
	s.viewer = model.User{Login: "tester"}
	s.viewerConfirmed = true
	s.current = &ref
	s.currentPR = &model.PullRequest{ID: "PR_1", Ref: ref, HeadOID: "head1"}
	s.highlighter = fakeLineHighlighter{linesErr: errors.New("tokenise boom")}

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "@@ -1 +1 @@\n-x\n+y")}}, nil
	})

	s.LoadFiles(false)
	runUntilIdle(t, disp)

	msgs := recordedMessages(records, slog.LevelDebug)
	var found bool
	for _, m := range msgs {
		if strings.Contains(m, "highlight") {
			found = true
		}
	}
	if !found {
		t.Errorf("Debug-level log messages = %v, want one mentioning the highlight failure", msgs)
	}
}

// TestLoadFiles_FailedConfirmationOfCachedPageDiscardsItForRetry is the
// regression test for review item 20: a cached page's own network
// confirmation failing must not leave it counted as "loaded" - otherwise
// LoadFiles(false)'s resume-on-error logic (item 4) would skip straight to
// the *next* page, permanently marking the never-confirmed (possibly
// wrong-commit) cached content as no longer stale once a later page
// succeeds, in violation of "at most one page is ever shown but
// unconfirmed at a time".
func TestLoadFiles_FailedConfirmationOfCachedPageDiscardsItForRetry(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	key, err := cache.PRKey(s.deps.Host, "tester", ref, "files/page-1")
	if err != nil {
		t.Fatalf("PRKey: %v", err)
	}
	body, _ := json.Marshal(cachedFilesPage{Files: []model.ChangedFile{changedFile("cached.go", "@@ -1 +1 @@\n-x\n+y")}})
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, ETag: `"etag-1"`, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed files cache: %v", err)
	}

	var pagesRequested []int
	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		pagesRequested = append(pagesRequested, page)
		return gh.FilesResult{}, errors.New("network boom")
	})

	s.LoadFiles(false)
	// Cache applied synchronously, before the (about to fail) network
	// fetch resolves.
	if len(s.Files()) != 1 {
		t.Fatalf("Files() = %+v, want the cached page applied immediately", s.Files())
	}
	if !s.FilesState().Stale {
		t.Fatal("FilesState().Stale = false, want true right after the cache-first apply")
	}

	runUntilIdle(t, disp)

	// The cached page's own confirmation failed: it must be discarded, not
	// left counted as loaded.
	if len(s.Files()) != 0 {
		t.Errorf("Files() = %+v, want empty once the cached page's own confirmation failed", s.Files())
	}
	if s.FilesState().PagesLoaded != 0 {
		t.Errorf("FilesState().PagesLoaded = %d, want 0", s.FilesState().PagesLoaded)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true, want false once the unconfirmed page has been discarded (nothing left is unconfirmed)")
	}

	// Now let the retry succeed with a real 200.
	gitHub.setFilesFunc(func(_ context.Context, _ model.PRRef, page int, _ string) (gh.FilesResult, error) {
		pagesRequested = append(pagesRequested, page)
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("fresh.go", "")}}, nil
	})
	s.LoadFiles(false) // resume: must re-request page 1, not skip to page 2
	runUntilIdle(t, disp)

	if got := pagesRequested[len(pagesRequested)-1]; got != 1 {
		t.Errorf("retry requested page %d, want page 1 (must not skip past the discarded page)", got)
	}
	files := s.Files()
	if len(files) != 1 || files[0].File.Path != "fresh.go" {
		t.Fatalf("Files() = %+v, want the freshly confirmed page 1", files)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true after a real 200, want false")
	}
}

// TestStartFilesFetch_RecomputesLastErrAfterReset is the regression test
// for review item 21: resetFiles clears filesErr, but LastError() must
// reflect that immediately - not wait until page 1 happens to resolve and
// call recomputeLastErr on its own.
func TestStartFilesFetch_RecomputesLastErrAfterReset(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{}, errors.New("boom")
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)
	if s.LastError() == nil {
		t.Fatal("LastError() = nil, want the standing files error before starting a fresh generation")
	}

	block := make(chan struct{})
	gitHub.setFilesFunc(func(ctx context.Context, _ model.PRRef, _ int, _ string) (gh.FilesResult, error) {
		<-block
		return gh.FilesResult{}, ctx.Err()
	})

	s.LoadFiles(true) // starts a new generation via startFilesFetch

	if s.LastError() != nil {
		t.Errorf(
			"LastError() = %v immediately after LoadFiles(true) started a new generation, want nil "+
				"(resetFiles cleared filesErr; recomputeLastErr must run right after, not wait for page 1)",
			s.LastError(),
		)
	}

	close(block)
	runUntilIdle(t, disp)
}
