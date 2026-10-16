// Package store's LoadOutdatedAnchors / OutdatedAnchor relocate outdated
// review threads. Once new commits change the commented code, GitHub marks
// a thread outdated and reports its line as null, keeping only the line it
// had on its original commit (ReviewThread.OriginalLine on
// ReviewThread.OriginalCommit). GitHub's own UI still shows such a thread
// at the corresponding place in the current file; to do the same, the diff
// between the original commit and the pull request's head is fetched (REST
// compare, via gh.Client.CompareFiles) and the original line is mapped
// through its hunks (diff.MapOldLine).
//
// This deliberately does not reuse CompareBranches' single generation-
// tokened slot (compare.go): that slot belongs to the create-PR form and
// supersedes older calls, whereas here several original commits are
// fetched concurrently and each result stays valid for as long as the same
// pull request stays open, so results are cached per (repo, original
// commit, head) and dropped only when the pull request changes.
package store

import (
	"fmt"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// outdatedFetchesPerCall caps how many compares one LoadOutdatedAnchors
// call starts: a long-lived pull request can have outdated threads on many
// distinct commits, and each compare is a REST request counted against the
// rate limit. Remaining commits are picked up by the next call (the UI
// calls it whenever it loads files, and again on every result).
const outdatedFetchesPerCall = 8

// outdatedKey identifies one compare: original commit orig against the
// pull request's head commit, in repo.
type outdatedKey struct {
	repo       model.RepoRef
	orig, head string
}

// outdatedResult is the parsed outcome of one compare.
type outdatedResult struct {
	// files holds the parsed hunks of every changed file with a patch.
	files map[string][]diff.Hunk
	// gone marks paths whose old-side content cannot be mapped: removed,
	// renamed away, or changed without a usable patch (binary or too large).
	gone map[string]bool
	// truncated: the compare hit the endpoint's file cap, so a path missing
	// from files may in fact have changed.
	truncated bool
	// failed: the request or parsing failed; nothing is relocated.
	failed bool
	// diverged: head is not a descendant of orig (force-push or rebase), so
	// the compare is not a pure old-to-new history and its patches say
	// nothing reliable about where the old line went.
	diverged bool
}

// LoadOutdatedAnchors starts the compares needed to relocate the current
// pull request's outdated threads, then emits EventOutdatedAnchorsChanged
// as each result arrives. It is idempotent (a compare already cached or in
// flight is not started again) and a no-op without a current pull request.
//
// Only threads GitHub left without a line (IsOutdated, Line == 0) on the
// RIGHT side of a LINE subject, with a known original line and commit other than
// the current head, are candidates: a LEFT thread refers to the base side,
// which the original commit's compare does not describe.
func (s *Store) LoadOutdatedAnchors() {
	if s.current == nil || s.currentPR == nil {
		return
	}
	ref, head := *s.current, s.currentPR.HeadOID
	started := 0
	for _, t := range s.currentPR.ReviewThreads {
		if !t.IsOutdated || t.Line != 0 || t.Side != model.DiffSideRight ||
			t.SubjectType != model.ThreadSubjectLine || t.OriginalLine <= 0 ||
			t.OriginalCommit == "" || t.OriginalCommit == head {
			continue
		}
		key := outdatedKey{repo: ref.Repo, orig: t.OriginalCommit, head: head}
		if _, ok := s.outdatedAnchors[key]; ok || s.outdatedInFlight[key] {
			continue
		}
		if started >= outdatedFetchesPerCall {
			return
		}
		started++
		s.startOutdatedFetch(ref, key)
	}
}

// startOutdatedFetch fetches and parses the compare for key in a goroutine
// and applies it on the UI goroutine. Parsing happens off the UI goroutine
// because a compare can carry up to 300 patches.
func (s *Store) startOutdatedFetch(ref model.PRRef, key outdatedKey) {
	if s.outdatedInFlight == nil {
		s.outdatedInFlight = map[outdatedKey]bool{}
	}
	s.outdatedInFlight[key] = true
	ctx := s.baseCtx

	go func() {
		res := outdatedResult{failed: true}
		defer func() {
			if r := recover(); r != nil {
				res = outdatedResult{failed: true}
				s.deps.Logger.Error("goroutine panic", "op", "outdated_anchors", "err", fmt.Errorf("panic: %v", r))
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() { s.applyOutdatedResult(ref, key, res, cancelled) })
		}()
		cmp, err := s.deps.GitHub.CompareFiles(ctx, key.repo, key.orig, key.head)
		if err != nil {
			s.deps.Logger.Warn("outdated thread compare failed", "orig", key.orig, "head", key.head, "err", err)
			return
		}
		res = parseOutdatedCompare(cmp)
	}()
}

// parseOutdatedCompare turns a compare result into an outdatedResult. A
// patch that fails to parse marks only its own path gone; the rest of the
// compare is still usable.
func parseOutdatedCompare(cmp gh.CompareResult) outdatedResult {
	res := outdatedResult{
		files:     map[string][]diff.Hunk{},
		gone:      map[string]bool{},
		truncated: cmp.Truncated,
		diverged:  cmp.Status != "ahead" && cmp.Status != "identical",
	}
	for _, f := range cmp.Files {
		if f.PreviousPath != "" && f.PreviousPath != f.Path {
			res.gone[f.PreviousPath] = true
		}
		if f.Status == model.FileStatusRemoved || !f.HasPatch {
			res.gone[f.Path] = true
			continue
		}
		hunks, err := diff.Parse(f.Patch)
		if err != nil {
			res.gone[f.Path] = true
			continue
		}
		res.files[f.Path] = hunks
	}
	return res
}

// applyOutdatedResult stores one compare's result and emits
// EventOutdatedAnchorsChanged. It runs only on the UI goroutine. The result
// is dropped when the pull request was switched or closed meanwhile, or
// when its head moved on (a refresh brought new commits, so key.head no
// longer describes what is displayed).
func (s *Store) applyOutdatedResult(ref model.PRRef, key outdatedKey, res outdatedResult, cancelled bool) {
	if cancelled || s.current == nil || *s.current != ref || s.currentPR == nil || s.currentPR.HeadOID != key.head {
		return
	}
	delete(s.outdatedInFlight, key)
	if s.outdatedAnchors == nil {
		s.outdatedAnchors = map[outdatedKey]outdatedResult{}
	}
	s.outdatedAnchors[key] = res
	s.emit(Event{Kind: EventOutdatedAnchorsChanged})
}

// resetOutdatedAnchors discards every cached and in-flight compare: they
// belong to the pull request that was just replaced, closed or reloaded.
func (s *Store) resetOutdatedAnchors() {
	s.outdatedAnchors = nil
	s.outdatedInFlight = nil
}

// OutdatedAnchor returns the current head's line corresponding to outdated
// thread t's original line, for a thread LoadOutdatedAnchors covers. ok is
// false when it cannot be placed: no result yet, a failed, diverged or
// truncated-and-unknown compare, or a file that was removed, renamed away
// or has no patch. A path the compare does not list is unchanged since the
// original commit, so the original line still applies.
func (s *Store) OutdatedAnchor(t model.ReviewThread) (line int, ok bool) {
	if s.current == nil || s.currentPR == nil || t.OriginalLine <= 0 {
		return 0, false
	}
	res, found := s.outdatedAnchors[outdatedKey{repo: s.current.Repo, orig: t.OriginalCommit, head: s.currentPR.HeadOID}]
	if !found || res.failed || res.diverged || res.gone[t.Path] {
		return 0, false
	}
	hunks, changed := res.files[t.Path]
	if changed {
		return diff.MapOldLine(hunks, t.OriginalLine)
	}
	if res.truncated {
		return 0, false
	}
	return t.OriginalLine, true
}
