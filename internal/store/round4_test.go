package store

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// Item 32: a Refresh that starts while a previous refresh's page-chain is
// still running must not permanently shrink the section's depth. Without
// carrying refreshTargetPages over, the new generation would compute its
// own chain target from the mid-chain loadedPages value (1, since the
// interrupted chain never got past page 1), losing the previous depth (4)
// forever.
func TestApplyFetchResult_RefreshDuringChainPreservesTargetDepth(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	gitHub.setSearchFunc(func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "p2"}, nil
		case "p2":
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: true, EndCursor: "p3"}, nil
		case "p3":
			return gh.SearchResult{Items: []model.PullRequest{pr("C", time.Now())}, HasNextPage: true, EndCursor: "p4"}, nil
		case "p4":
			return gh.SearchResult{Items: []model.PullRequest{pr("D", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	})

	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)

	if s.sections[0].loadedPages != 4 {
		t.Fatalf("loadedPages after 3 LoadMores = %d, want 4", s.sections[0].loadedPages)
	}

	// First refresh: page 1 applies immediately, then the chain's page-2
	// fetch blocks so the next refresh can interrupt it mid-chain.
	page2Release := make(chan struct{})
	gitHub.setSearchFunc(func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "r2"}, nil
		case "r2":
			<-page2Release
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: true, EndCursor: "r3"}, nil
		default:
			return gh.SearchResult{}, nil
		}
	})
	s.Refresh()
	runUntilIdle(t, disp)

	if got := s.sections[0].loadedPages; got != 1 {
		t.Fatalf("mid-chain loadedPages = %d, want 1 (only page 1 has applied so far)", got)
	}
	if got := s.sections[0].refreshTargetPages; got != 4 {
		t.Fatalf("refreshTargetPages = %d, want 4", got)
	}

	// Refresh again while the first refresh's chain is still blocked.
	gitHub.setSearchFunc(func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "s2"}, nil
		case "s2":
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: true, EndCursor: "s3"}, nil
		case "s3":
			return gh.SearchResult{Items: []model.PullRequest{pr("C", time.Now())}, HasNextPage: true, EndCursor: "s4"}, nil
		case "s4":
			return gh.SearchResult{Items: []model.PullRequest{pr("D", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	})
	s.Refresh()
	runUntilIdle(t, disp)

	close(page2Release) // let the first, now-superseded chain fetch return; it must be dropped
	runUntilIdle(t, disp)

	if got := s.sections[0].loadedPages; got != 4 {
		t.Errorf("loadedPages after the second refresh completes = %d, want 4 (restored, not shrunk to 1)", got)
	}
	var ids []string
	for _, it := range s.sections[0].items {
		ids = append(ids, it.ID)
	}
	if len(ids) != 4 {
		t.Errorf("items = %v, want 4 entries", ids)
	}
}

// Item 32: discarding sections after a login mismatch must also reset
// loadedPages/refreshTargetPages, so the new (correct) account's chain
// target is not inherited from the wrong account's pagination depth.
func TestDiscardAllSectionItems_ResetsPageDepth(t *testing.T) {
	cfg := config.Default()
	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	s.sections[0].loadedPages = 4
	s.sections[0].refreshTargetPages = 3

	s.discardAllSectionItems()

	if s.sections[0].loadedPages != 0 {
		t.Errorf("loadedPages = %d, want 0", s.sections[0].loadedPages)
	}
	if s.sections[0].refreshTargetPages != 0 {
		t.Errorf("refreshTargetPages = %d, want 0", s.sections[0].refreshTargetPages)
	}
}

// Item 33: a clean chained page beyond page 1 must not erase page 1's
// warnings from SectionStates.
func TestSectionStates_WarningsSurviveCleanChainedPage(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	// Build a 2-page section (loadedPages=2) so a subsequent refresh
	// chains to page 2.
	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "p2"}, nil
		case "p2":
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}
	s.LoadList(false)
	runUntilIdle(t, disp)
	s.LoadMore(0)
	runUntilIdle(t, disp)

	// Refresh: page 1 comes back with a warning; the chained page 2 is
	// clean (no warnings of its own).
	gitHub.searchFunc = func(_ context.Context, _, cursor string) (gh.SearchResult, error) {
		switch cursor {
		case "":
			return gh.SearchResult{
				Items: []model.PullRequest{pr("A", time.Now())}, HasNextPage: true, EndCursor: "p2b",
				Warnings: []string{"1 search result(s) omitted (could not be resolved)"},
			}, nil
		case "p2b":
			return gh.SearchResult{Items: []model.PullRequest{pr("B", time.Now())}, HasNextPage: false}, nil
		default:
			return gh.SearchResult{}, nil
		}
	}
	s.Refresh()
	runUntilIdle(t, disp)

	states := s.SectionStates()
	if len(states[0].Warnings) != 1 {
		t.Errorf("SectionStates()[0].Warnings = %v, want page 1's warning to survive the clean chained page 2", states[0].Warnings)
	}
}

// Item 34: recoverGoroutine's panic error must participate in
// recomputeLastErr like every other error source, rather than being
// assigned to lastErr directly: a later, unrelated section success must
// not silently erase it until the next generation starts.
func TestRecoverGoroutine_PanicErrorSurvivesUntilNextGeneration(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	logger := slog.New(slog.DiscardHandler)
	s.deps.Logger = logger

	// Force a panic directly through the shared recoverGoroutine path,
	// simulating what StartAutoRefresh's ticker goroutine would do.
	func() {
		defer s.recoverGoroutine("test")
		panic(errors.New("boom"))
	}()
	runUntilIdle(t, disp)

	if s.LastError() == nil {
		t.Fatalf("expected LastError() to be set after a recovered panic")
	}

	// A subsequent, unrelated section success must not erase the panic
	// error before the next generation starts.
	gitHub.searchFunc = func(context.Context, string, string) (gh.SearchResult, error) {
		return gh.SearchResult{}, nil
	}
	s.applyFetchResult(s.generation, 0, "", gh.SearchResult{}, nil, false)

	if s.LastError() == nil {
		t.Error("LastError() was cleared by an unrelated section success before the next generation started")
	}

	// Once a new generation starts, the panic is considered handled.
	s.LoadList(false)
	runUntilIdle(t, disp)

	if s.LastError() != nil {
		t.Errorf("LastError() = %v, want nil after a new generation starts and its sections succeed", s.LastError())
	}
}
