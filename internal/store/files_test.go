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

// changedFileWithSHA is changedFile plus a head blob SHA, for ExpandGap's
// tests (a file with no SHA has nothing for ExpandGap to fetch from - see
// TestExpandGap_EmptySHAReturnsSyncError).
func changedFileWithSHA(path, patch, sha string) model.ChangedFile {
	cf := changedFile(path, patch)
	cf.SHA = sha
	return cf
}

// expandGapFixturePatch is a three-hunk patch, one Context line each, with
// two gaps between hunks (new-side lines [2,4] and [6,8]) and one trailing
// gap ([10,10]) against expandGapFixtureHeadLines' 10-line head content.
const expandGapFixturePatch = "@@ -1 +1 @@\n context1\n@@ -5 +5 @@\n context5\n@@ -9 +9 @@\n context9"

func expandGapFixtureHeadLines() []string {
	return []string{
		"context1", "line2", "line3", "line4", "context5",
		"line6", "line7", "line8", "context9", "line10",
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

// TestLoadFiles_ForcesReloadWithoutCacheWhenHeadOIDAlreadyChanged is the
// regression test for a UI-layer bug: internal/ui's Files tab calls
// LoadFiles(false) on every EventPRChanged while it is visible, so that
// call can land *after* CurrentPR().HeadOID has already moved (a
// force-push) but before whatever caller is meant to force a reload for
// it gets a chance to run. Since a cached files page is keyed only by
// page number, not by HeadOID, LoadFiles(false) falling through to its
// usual cache-first behaviour in that situation would risk briefly
// showing the *previous* commit's diff with no indication that it does
// not match what is actually open. LoadFiles must recognise
// filesHeadOID != CurrentPR().HeadOID itself and force a reload
// regardless of its own force argument.
func TestLoadFiles_ForcesReloadWithoutCacheWhenHeadOIDAlreadyChanged(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("a.go", "")}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)
	if files := s.Files(); len(files) != 1 || files[0].File.Path != "a.go" {
		t.Fatalf("Files() = %+v, want the initial single file a.go", files)
	}

	// CurrentPR() now reports a different HeadOID (as if a detail refresh
	// had already applied a force-push) *before* LoadFiles(false) is
	// called again — mirroring internal/ui's own onPRChangedForFiles,
	// which calls LoadFiles(false) unconditionally on every EventPRChanged
	// while the Files tab is visible, not just the ones caused by a
	// force-push.
	s.currentPR.HeadOID = "head2"
	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{changedFile("b.go", "")}}, nil
	})

	s.LoadFiles(false)

	// Checked synchronously, before the real (head2) network fetch below
	// has any chance to resolve: applyCachedFilesPage runs synchronously
	// within LoadFiles' own call stack, so a bugged, cache-first
	// LoadFiles(false) would already have shown head1's cached a.go here,
	// marked FilesState().Stale, before the goroutine below ever runs.
	if files := s.Files(); len(files) != 0 {
		t.Fatalf("Files() = %+v immediately after LoadFiles(false) with a changed HeadOID, want empty (no stale head1 cache shown) until head2's own fetch resolves", files)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true immediately after LoadFiles(false) with a changed HeadOID, want it never set: a cache-first page is keyed by page number only, so it could belong to head1, not head2")
	}

	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 || files[0].File.Path != "b.go" {
		t.Fatalf("Files() = %+v, want reloaded to head2's single file b.go even though LoadFiles was called with force=false", files)
	}
	if s.FilesState().Stale {
		t.Error("FilesState().Stale = true after the head2 reload resolved, want it cleared (no cache-first page was ever shown for head2)")
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

// TestExpandGap_FetchesMergesHunksReHighlightsAndEmits is the primary
// end-to-end path: a fresh ExpandGap call for a file whose head content is
// not yet known fetches it over the network exactly once, then reuses that
// same fetch for every other gap in the same file (a second, independent
// gap, then the trailing gap) with no further network call, ending with
// every hunk merged into one and fully re-highlighted.
func TestExpandGap_FetchesMergesHunksReHighlightsAndEmits(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	files := s.Files()
	if len(files) != 1 || len(files[0].Hunks) != 3 {
		t.Fatalf("Files() = %+v, want 1 file with 3 hunks before expansion", files)
	}
	if !files[0].Highlighted || len(files[0].Tokens) != 3 {
		t.Fatalf("files[0] = %+v, want Highlighted=true Tokens len=3 before expansion", files[0])
	}

	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		return gh.FileContentResult{Lines: expandGapFixtureHeadLines()}, nil
	})

	var events []EventKind
	s.Subscribe(func(e Event) { events = append(events, e.Kind) })

	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("ExpandGap(gapStart=2) error = %v", err)
	}
	runUntilIdle(t, disp)

	if calls := gitHub.fileContentCallsSnapshot(); len(calls) != 1 || calls[0].sha != "sha-abc" {
		t.Fatalf("FileContent calls = %+v, want exactly 1 call for sha-abc", calls)
	}
	var sawFilesChanged bool
	for _, k := range events {
		if k == EventFilesChanged {
			sawFilesChanged = true
		}
	}
	if !sawFilesChanged {
		t.Error("no EventFilesChanged observed after ExpandGap")
	}

	files = s.Files()
	if len(files[0].Hunks) != 2 {
		t.Fatalf("Hunks after first expand = %d, want 2 (hunk 0 and 1 merged, hunk 2 untouched)", len(files[0].Hunks))
	}
	if got := len(files[0].Hunks[0].Lines); got != 5 {
		t.Errorf("merged hunk has %d lines, want 5 (1 + 3 revealed + 1)", got)
	}
	if len(files[0].Tokens) != 2 || !files[0].Highlighted {
		t.Errorf("files[0] = %+v, want Tokens len=2 Highlighted=true after re-highlighting", files[0])
	}

	// Second gap, same file: no further network call, since the whole
	// head content was already fetched once.
	if err := s.ExpandGap("sample.go", 6); err != nil {
		t.Fatalf("ExpandGap(gapStart=6) error = %v", err)
	}
	runUntilIdle(t, disp)
	if len(gitHub.fileContentCallsSnapshot()) != 1 {
		t.Errorf("FileContent called %d times after a second gap, want still 1", len(gitHub.fileContentCallsSnapshot()))
	}
	files = s.Files()
	if len(files[0].Hunks) != 1 {
		t.Fatalf("Hunks after second expand = %d, want 1 (fully merged with hunk 2)", len(files[0].Hunks))
	}

	// Trailing gap: still no further network call.
	if err := s.ExpandGap("sample.go", 10); err != nil {
		t.Fatalf("ExpandGap(gapStart=10) error = %v", err)
	}
	runUntilIdle(t, disp)
	if len(gitHub.fileContentCallsSnapshot()) != 1 {
		t.Errorf("FileContent called %d times after the trailing gap, want still 1", len(gitHub.fileContentCallsSnapshot()))
	}
	files = s.Files()
	if len(files[0].Hunks) != 1 || len(files[0].Hunks[0].Lines) != 10 {
		t.Fatalf("Hunks after trailing expand = %+v, want 1 hunk covering all 10 lines", files[0].Hunks)
	}
}

// TestApplyExpandGap_TrailingGapAlreadyEmptyEmitsEventButNoReHighlight
// covers head content that turns out exactly as long as the diff's last
// visible line: diff.ExpandGap reports the trailing gap already empty
// (hunks unchanged). EventFilesChanged must still fire (the UI now knows
// HeadLineCount and can hide the trailing gap row), but there is nothing to
// re-highlight: the entry's id must stay the same (no replace-and-reassign,
// unlike a real expansion), and Tokens must be untouched.
func TestApplyExpandGap_TrailingGapAlreadyEmptyEmitsEventButNoReHighlight(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	before := s.Files()
	if len(before) != 1 || len(before[0].Hunks) != 3 {
		t.Fatalf("Files() = %+v, want 1 file with 3 hunks", before)
	}
	beforeID := before[0].id
	beforeTokensLen := len(before[0].Tokens)

	// Exactly 9 lines: the diff's last hunk already ends at new-side line
	// 9, so the trailing gap (starting at 10) is empty.
	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		return gh.FileContentResult{Lines: expandGapFixtureHeadLines()[:9]}, nil
	})

	var events []EventKind
	s.Subscribe(func(e Event) { events = append(events, e.Kind) })

	if err := s.ExpandGap("sample.go", 10); err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	runUntilIdle(t, disp)

	var sawFilesChanged bool
	for _, k := range events {
		if k == EventFilesChanged {
			sawFilesChanged = true
		}
	}
	if !sawFilesChanged {
		t.Error("no EventFilesChanged observed for the already-empty trailing gap")
	}

	after := s.Files()
	if len(after) != 1 || len(after[0].Hunks) != 3 {
		t.Fatalf("Hunks after expand = %+v, want unchanged (still 3 hunks)", after[0].Hunks)
	}
	if after[0].id != beforeID {
		t.Errorf("FileEntry id changed (%d -> %d), want unchanged: a no-op expansion must not replace-and-reassign", beforeID, after[0].id)
	}
	if len(after[0].Tokens) != beforeTokensLen {
		t.Errorf("Tokens len changed %d -> %d, want unchanged", beforeTokensLen, len(after[0].Tokens))
	}
}

// TestExpandGap_CacheHitSkipsNetworkOnFreshStore covers a fresh Store (no
// in-memory HeadLines yet) whose blob is already on disk under the
// "blob/<sha>" cache key: ExpandGap must apply it synchronously, with no
// network call at all.
func TestExpandGap_CacheHitSkipsNetworkOnFreshStore(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-cached"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	key, err := cache.RepoKey(s.deps.Host, "tester", ref.Repo, "blob/sha-cached")
	if err != nil {
		t.Fatalf("RepoKey: %v", err)
	}
	body, err := json.Marshal(cachedBlob{Lines: expandGapFixtureHeadLines()})
	if err != nil {
		t.Fatalf("marshal cachedBlob: %v", err)
	}
	if err := s.deps.Cache.Put(key, cache.Entry{Body: body, FetchedAt: s.deps.Now()}); err != nil {
		t.Fatalf("seed blob cache: %v", err)
	}

	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	runUntilIdle(t, disp)

	if len(gitHub.fileContentCallsSnapshot()) != 0 {
		t.Errorf("FileContent called %d times, want 0 (blob already cached on disk)", len(gitHub.fileContentCallsSnapshot()))
	}
	if got := len(s.Files()[0].Hunks); got != 2 {
		t.Errorf("Hunks after cache-hit expand = %d, want 2", got)
	}
}

// TestExpandGap_InFlightFetchIsDeduplicated covers a second ExpandGap call
// for the same file while its head-content fetch is still in flight: it
// must not start a second, redundant network call.
func TestExpandGap_InFlightFetchIsDeduplicated(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	block := make(chan struct{})
	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		<-block
		return gh.FileContentResult{Lines: expandGapFixtureHeadLines()}, nil
	})

	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("first ExpandGap() error = %v", err)
	}
	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("second (in-flight) ExpandGap() error = %v, want nil (silently ignored)", err)
	}

	close(block)
	runUntilIdle(t, disp)

	if len(gitHub.fileContentCallsSnapshot()) != 1 {
		t.Errorf("FileContent called %d times, want exactly 1 (the second call was a dedup no-op)", len(gitHub.fileContentCallsSnapshot()))
	}
}

// TestExpandGap_GenerationMismatchDiscardsLateResult covers a files
// generation switch (ClosePR) while an ExpandGap fetch is still in flight:
// its late result must be discarded without panicking or corrupting the
// (now empty) files state.
func TestExpandGap_GenerationMismatchDiscardsLateResult(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	block := make(chan struct{})
	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		<-block
		return gh.FileContentResult{Lines: expandGapFixtureHeadLines()}, nil
	})

	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("ExpandGap() error = %v", err)
	}
	s.ClosePR()

	close(block)
	runUntilIdle(t, disp)

	if len(s.Files()) != 0 {
		t.Errorf("Files() = %+v, want empty after ClosePR", s.Files())
	}
}

// TestExpandGap_FetchErrorEmitsEventError covers a failed head-content
// fetch: it must be reported via EventError, and must leave the file's
// hunks untouched (nothing to expand from).
func TestExpandGap_FetchErrorEmitsEventError(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	wantErr := errors.New("boom")
	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		return gh.FileContentResult{}, wantErr
	})

	var sawErr bool
	s.Subscribe(func(e Event) {
		if e.Kind == EventError {
			sawErr = true
		}
	})

	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("ExpandGap() error = %v, want nil (the fetch error is reported asynchronously)", err)
	}
	runUntilIdle(t, disp)

	if !sawErr {
		t.Error("no EventError observed after a failed FileContent fetch")
	}
	if got := len(s.Files()[0].Hunks); got != 3 {
		t.Errorf("Hunks after a failed expand = %d, want 3 (unchanged)", got)
	}
}

// TestExpandGap_UnknownPathReturnsSyncError covers ExpandGap called for a
// path with no loaded FileEntry.
func TestExpandGap_UnknownPathReturnsSyncError(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("known.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if err := s.ExpandGap("missing.go", 1); err == nil {
		t.Fatal("ExpandGap() for an unloaded path error = nil, want an error")
	}
}

// TestExpandGap_EmptySHAReturnsSyncError covers ExpandGap called for a file
// with no head blob (File.SHA == "", for example a rename with no content
// change): it must fail synchronously, without starting any network call.
func TestExpandGap_EmptySHAReturnsSyncError(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFile("no-sha.go", expandGapFixturePatch),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	if err := s.ExpandGap("no-sha.go", 2); err == nil {
		t.Fatal("ExpandGap() for a file with no SHA error = nil, want an error")
	}
	if len(gitHub.fileContentCallsSnapshot()) != 0 {
		t.Errorf("FileContent called %d times, want 0 (no SHA to fetch from)", len(gitHub.fileContentCallsSnapshot()))
	}
}

// TestExpandGap_StaleGenerationResultDoesNotClearNewGenerationsInFlightMark
// is the regression test for a bug where applyFileContentResult deleted
// expandInFlight[path] unconditionally, before checking gen: a stale
// generation's (G1) own late result then deleted the *new* generation's
// (G2) in-flight mark for the same path (maps are reference types, so
// there is only ever one "the map" by the time either result lands),
// letting a further Enter on G2's still-loading gap start a redundant
// third fetch. The fix checks gen first (resetFiles already replaced the
// whole map, so G1's late result has nothing of its own left to clean up).
func TestExpandGap_StaleGenerationResultDoesNotClearNewGenerationsInFlightMark(t *testing.T) {
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	ref := filesTestRef()
	s := newFilesTestStore(t, gitHub, disp, ref, "head1")

	gitHub.setFilesFunc(func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
		return gh.FilesResult{Files: []model.ChangedFile{
			changedFileWithSHA("sample.go", expandGapFixturePatch, "sha-abc"),
		}}, nil
	})
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	var mu sync.Mutex
	calls := 0
	// started fires (once per call, buffered so the fake never blocks
	// sending to it) the instant each call begins - after the fake has
	// already recorded it in fileContentCalls but before this closure does
	// anything else - so the test can wait deterministically for a call to
	// have actually started, instead of racing the goroutine that runs it.
	started := make(chan int, 3)
	block1 := make(chan struct{})
	block2 := make(chan struct{})
	gitHub.setFileContentFunc(func(context.Context, model.RepoRef, string) (gh.FileContentResult, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		started <- n
		switch n {
		case 1:
			<-block1
		case 2:
			<-block2
			// n >= 3 (only reachable under the bug this test guards
			// against) returns immediately, unblocked, so a buggy third
			// fetch's result reaches applyFileContentResult and gets
			// counted instead of hanging the test forever.
		}
		return gh.FileContentResult{Lines: expandGapFixtureHeadLines()}, nil
	})

	// Fetch #1 starts under generation G1, then blocks.
	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("first ExpandGap() error = %v", err)
	}
	if got := <-started; got != 1 {
		t.Fatalf("first call started = %d, want 1", got)
	}

	// A force-push moves HeadOID: LoadFiles(false) starts a new generation
	// G2, superseding G1 (whose fetch #1 stays blocked, uncancelled by the
	// context switch, matching how a real in-flight network call would
	// keep running past ctx cancellation until it itself observes it).
	s.currentPR.HeadOID = "head2"
	s.LoadFiles(false)
	runUntilIdle(t, disp)

	// Fetch #2 starts under G2 for the same path, then also blocks.
	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("second ExpandGap() error = %v", err)
	}
	if got := <-started; got != 2 {
		t.Fatalf("second call started = %d, want 2", got)
	}

	// Resolve the stale G1 fetch late: its result must be discarded without
	// disturbing G2's own still-in-flight mark for the same path.
	close(block1)
	runUntilIdle(t, disp)

	// A further Enter while G2's fetch #2 is still in flight must still be
	// deduped - not a third fetch. Under the bug this guards against, a
	// third fetch would start and complete immediately (see the fake
	// above); runUntilIdle gives it every chance to do so before the final
	// count assertion.
	if err := s.ExpandGap("sample.go", 2); err != nil {
		t.Fatalf("third ExpandGap() error = %v", err)
	}
	runUntilIdle(t, disp)
	if len(gitHub.fileContentCallsSnapshot()) != 2 {
		t.Errorf("FileContent calls = %d, want still 2 (the stale G1 result must not have cleared G2's in-flight mark)",
			len(gitHub.fileContentCallsSnapshot()))
	}

	close(block2)
	runUntilIdle(t, disp)
}
