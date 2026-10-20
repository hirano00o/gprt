package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// outdatedHeadOID is the head the outdated-anchor tests' pull request sits at.
const outdatedHeadOID = "head000"

// outdatedThread builds an outdated, line-less RIGHT thread anchored at
// originalLine of path on commit orig: the shape GitHub reports once new
// commits change the commented code.
func outdatedThread(id, path string, originalLine int, orig string) model.ReviewThread {
	return model.ReviewThread{
		ID: id, Path: path, IsOutdated: true, OriginalLine: originalLine, OriginalCommit: orig,
		Side: model.DiffSideRight, SubjectType: model.ThreadSubjectLine,
	}
}

// newOutdatedTestStore is newFilesTestStore with threads attached to the
// current pull request.
func newOutdatedTestStore(
	t *testing.T, gitHub *fakeGitHub, disp *fakeDispatcher, threads ...model.ReviewThread,
) *Store {
	t.Helper()
	s := newFilesTestStore(t, gitHub, disp, filesTestRef(), outdatedHeadOID)
	s.currentPR.ReviewThreads = threads
	return s
}

// compareRecorder installs a CompareFiles fake on gitHub that answers every
// call with res (or err) and records each (base, head) pair it was asked.
type compareRecorder struct {
	mu    sync.Mutex
	bases []string
	heads []string
}

func (r *compareRecorder) install(gitHub *fakeGitHub, res gh.CompareResult, err error) {
	gitHub.setCompareFilesFunc(func(_ context.Context, _ model.RepoRef, base, head string) (gh.CompareResult, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.bases = append(r.bases, base)
		r.heads = append(r.heads, head)
		return res, err
	})
}

func (r *compareRecorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bases)
}

// aheadCompare is an "ahead" compare result in which a.go gained one line
// at the top (so old line N maps to N+1) and b.go was removed.
func aheadCompare() gh.CompareResult {
	return gh.CompareResult{
		Status: "ahead",
		Files: []model.ChangedFile{
			changedFile("a.go", "@@ -0,0 +1 @@\n+new first line"),
			{Path: "b.go", Status: model.FileStatusRemoved},
		},
	}
}

func TestLoadOutdatedAnchors_OneFetchPerDistinctCommitAndCached(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, aheadCompare(), nil)
	disp := newFakeDispatcher()
	s := newOutdatedTestStore(t, gitHub, disp,
		outdatedThread("T1", "a.go", 3, "orig1"),
		outdatedThread("T2", "c.go", 5, "orig1"),
		outdatedThread("T3", "a.go", 9, "orig2"),
	)
	events := collectEvents(s)

	s.LoadOutdatedAnchors()
	s.LoadOutdatedAnchors() // in flight: must not start a second fetch.
	runUntilIdle(t, disp)

	if got := rec.calls(); got != 2 {
		t.Fatalf("CompareFiles calls = %d, want 2 (one per distinct original commit)", got)
	}
	if n := countEventKind(*events, EventOutdatedAnchorsChanged); n != 2 {
		t.Errorf("EventOutdatedAnchorsChanged count = %d, want 2", n)
	}

	s.LoadOutdatedAnchors() // cached: no further fetch.
	runUntilIdle(t, disp)
	if got := rec.calls(); got != 2 {
		t.Errorf("CompareFiles calls after cache hit = %d, want still 2", got)
	}
	// The fetches run concurrently, so only the set of compares is fixed.
	got := map[string]bool{}
	for i := range rec.bases {
		got[rec.bases[i]+"..."+rec.heads[i]] = true
	}
	for _, orig := range []string{"orig1", "orig2"} {
		if want := orig + "..." + outdatedHeadOID; !got[want] {
			t.Errorf("compares = %v, missing %s", got, want)
		}
	}
}

func TestLoadOutdatedAnchors_SkipsIneligibleThreads(t *testing.T) {
	left := outdatedThread("left", "a.go", 3, "orig1")
	left.Side = model.DiffSideLeft
	current := outdatedThread("current", "a.go", 3, "orig1")
	current.IsOutdated = false
	hasLine := outdatedThread("hasLine", "a.go", 3, "orig1")
	hasLine.Line = 4
	file := outdatedThread("file", "a.go", 3, "orig1")
	file.SubjectType = model.ThreadSubjectFile
	noCommit := outdatedThread("noCommit", "a.go", 3, "")
	atHead := outdatedThread("atHead", "a.go", 3, outdatedHeadOID)
	noOriginalLine := outdatedThread("noOriginalLine", "a.go", 0, "orig1")

	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, aheadCompare(), nil)
	disp := newFakeDispatcher()
	s := newOutdatedTestStore(t, gitHub, disp, left, current, hasLine, file, noCommit, atHead, noOriginalLine)

	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)

	if got := rec.calls(); got != 0 {
		t.Errorf("CompareFiles calls = %d, want 0 for ineligible threads", got)
	}
}

func TestLoadOutdatedAnchors_NoCurrentPRIsNoop(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, aheadCompare(), nil)
	disp := newFakeDispatcher()
	s := newOutdatedTestStore(t, gitHub, disp, outdatedThread("T1", "a.go", 3, "orig1"))
	s.ClosePR()

	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)

	if got := rec.calls(); got != 0 {
		t.Errorf("CompareFiles calls = %d, want 0 without a current PR", got)
	}
}

func TestLoadOutdatedAnchors_CapsFetchesPerCall(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, aheadCompare(), nil)
	disp := newFakeDispatcher()
	var threads []model.ReviewThread
	total := outdatedFetchesPerCall + 2
	for i := range total {
		threads = append(threads, outdatedThread("T", "a.go", 1, "orig"+string(rune('A'+i))))
	}
	s := newOutdatedTestStore(t, gitHub, disp, threads...)

	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)
	if got := rec.calls(); got != outdatedFetchesPerCall {
		t.Fatalf("first call fetched %d commits, want the cap %d", got, outdatedFetchesPerCall)
	}

	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)
	if got := rec.calls(); got != total {
		t.Errorf("after second call fetched %d commits, want %d", got, total)
	}
}

func TestOutdatedAnchor_MapsAndClassifiesPaths(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	res := aheadCompare()
	res.Files = append(res.Files,
		model.ChangedFile{Path: "new.go", PreviousPath: "old.go", Status: model.FileStatusRenamed},
		model.ChangedFile{Path: "img.png", Status: model.FileStatusModified}, // no patch
	)
	rec.install(gitHub, res, nil)
	disp := newFakeDispatcher()
	mapped := outdatedThread("mapped", "a.go", 3, "orig1")
	removed := outdatedThread("removed", "b.go", 3, "orig1")
	renamedAway := outdatedThread("renamed", "old.go", 3, "orig1")
	noPatch := outdatedThread("nopatch", "img.png", 3, "orig1")
	unchanged := outdatedThread("unchanged", "c.go", 5, "orig1")
	s := newOutdatedTestStore(t, gitHub, disp, mapped, removed, renamedAway, noPatch, unchanged)

	if _, ok := s.OutdatedAnchor(mapped); ok {
		t.Error("OutdatedAnchor before any result: ok = true, want false")
	}
	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)

	tests := []struct {
		name     string
		thread   model.ReviewThread
		wantLine int
		wantOK   bool
	}{
		{"mapped through hunks", mapped, 4, true},
		{"removed file", removed, 0, false},
		{"renamed away", renamedAway, 0, false},
		{"file without patch", noPatch, 0, false},
		{"path absent from compare keeps its line", unchanged, 5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line, ok := s.OutdatedAnchor(tc.thread)
			if ok != tc.wantOK || line != tc.wantLine {
				t.Errorf("OutdatedAnchor() = (%d,%v), want (%d,%v)", line, ok, tc.wantLine, tc.wantOK)
			}
		})
	}
}

func TestOutdatedAnchor_NotRelocatedWhenUnreliable(t *testing.T) {
	tests := []struct {
		name string
		res  gh.CompareResult
		err  error
	}{
		{"diverged", gh.CompareResult{Status: "diverged"}, nil},
		{"behind", gh.CompareResult{Status: "behind"}, nil},
		{"truncated", gh.CompareResult{Status: "ahead", Truncated: true}, nil},
		{"failed", gh.CompareResult{}, errors.New("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gitHub := newFakeGitHub()
			var rec compareRecorder
			rec.install(gitHub, tc.res, tc.err)
			disp := newFakeDispatcher()
			th := outdatedThread("T1", "c.go", 5, "orig1")
			s := newOutdatedTestStore(t, gitHub, disp, th)
			events := collectEvents(s)

			s.LoadOutdatedAnchors()
			runUntilIdle(t, disp)

			if line, ok := s.OutdatedAnchor(th); ok {
				t.Errorf("OutdatedAnchor() = (%d,true), want not ok", line)
			}
			if n := countEventKind(*events, EventError); n != 0 {
				t.Errorf("EventError count = %d, want 0 (a failed relocation is silent)", n)
			}
			// A failed or unreliable result is cached too: no retry storm.
			s.LoadOutdatedAnchors()
			runUntilIdle(t, disp)
			if got := rec.calls(); got != 1 {
				t.Errorf("CompareFiles calls = %d, want 1", got)
			}
		})
	}
}

func TestOutdatedAnchor_IdenticalStatusRelocates(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, gh.CompareResult{Status: "identical"}, nil)
	disp := newFakeDispatcher()
	th := outdatedThread("T1", "c.go", 5, "orig1")
	s := newOutdatedTestStore(t, gitHub, disp, th)

	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)

	if line, ok := s.OutdatedAnchor(th); !ok || line != 5 {
		t.Errorf("OutdatedAnchor() = (%d,%v), want (5,true)", line, ok)
	}
}

func TestLoadOutdatedAnchors_StaleResultDroppedAfterPRSwitch(t *testing.T) {
	gitHub := newFakeGitHub()
	started := make(chan struct{})
	release := make(chan struct{})
	gitHub.setCompareFilesFunc(func(context.Context, model.RepoRef, string, string) (gh.CompareResult, error) {
		close(started)
		<-release
		return aheadCompare(), nil
	})
	disp := newFakeDispatcher()
	th := outdatedThread("T1", "a.go", 3, "orig1")
	s := newOutdatedTestStore(t, gitHub, disp, th)

	s.LoadOutdatedAnchors()
	<-started
	s.ClosePR()
	other := filesTestRef()
	other.Number = 8
	s.current = &other
	s.currentPR = &model.PullRequest{ID: "PR_2", Ref: other, HeadOID: outdatedHeadOID, ReviewThreads: []model.ReviewThread{th}}
	events := collectEvents(s)
	close(release)
	runUntilIdle(t, disp)

	if line, ok := s.OutdatedAnchor(th); ok {
		t.Errorf("OutdatedAnchor() = (%d,true), want the stale result dropped", line)
	}
	if n := countEventKind(*events, EventOutdatedAnchorsChanged); n != 0 {
		t.Errorf("EventOutdatedAnchorsChanged count = %d, want 0 for a dropped result", n)
	}
}

func TestOutdatedAnchors_ClearedOnClosePR(t *testing.T) {
	gitHub := newFakeGitHub()
	var rec compareRecorder
	rec.install(gitHub, aheadCompare(), nil)
	disp := newFakeDispatcher()
	th := outdatedThread("T1", "a.go", 3, "orig1")
	s := newOutdatedTestStore(t, gitHub, disp, th)
	s.LoadOutdatedAnchors()
	runUntilIdle(t, disp)

	s.ClosePR()
	if len(s.outdatedAnchors) != 0 {
		t.Errorf("outdatedAnchors = %d entries after ClosePR, want 0", len(s.outdatedAnchors))
	}
}
