// Package store's CompareBranches backs the create-PR form's diff preview:
// the same base/head branch pair the form's own Head/Base fields hold, but
// resolved to actual changed files (via gh.Client.CompareFiles, REST's
// "compare two commits" endpoint) rather than branch names. Like
// SearchBranches/SearchTeams (search.go), a result here is never cached: a
// preview for an as-you-type form has no reuse value across distinct
// base/head pairs the way a repeatedly-viewed pull request's files do, and
// every previous head/base pair is abandoned the moment either field
// changes again. A single, non-repository-keyed generation-tokened slot
// (compareGen) is enough, mirroring branchSearchGen/teamSearchGen: only one
// create-PR form is open, and being edited, at a time.
package store

import (
	"fmt"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// CompareBranches starts a fetch of the changed files between base and head
// in repo, and returns a token identifying this call. fn (optional) is
// invoked on the UI goroutine, from inside the Dispatch callback that
// resolves the fetch — but only if this call is still the most recent one:
// a later CompareBranches call, made before this one resolved, supersedes
// it, and fn never fires for the superseded call at all (see the package
// doc for why staleness is tracked this way rather than by caching). A
// failed fetch still calls fn (with a zero gh.CompareResult and the error)
// and reports the failure via the usual EventError/log path, exactly like
// every other fetch in this package.
func (s *Store) CompareBranches(repo model.RepoRef, base, head string, fn func(gh.CompareResult, error)) int {
	s.compareGen++
	gen := s.compareGen
	ctx := s.baseCtx

	go func() {
		var res gh.CompareResult
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, matching every other fetch in
		// this package.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: compare_branches: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "compare_branches", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyCompareResult(gen, res, err, cancelled, fn)
			})
		}()
		res, err = s.deps.GitHub.CompareFiles(ctx, repo, base, head)
	}()

	return gen
}

// applyCompareResult applies (or reports the failure of) one
// CompareBranches call's result. It runs only on the UI goroutine, from a
// Dispatch callback. A result whose token no longer matches the most
// recent CompareBranches call (gen != s.compareGen), or our own
// cancellation (app shutdown), is dropped entirely: no fn call, no
// EventError — the caller has already moved on.
func (s *Store) applyCompareResult(
	gen int, res gh.CompareResult, err error, cancelled bool, fn func(gh.CompareResult, error),
) {
	if gen != s.compareGen || cancelled {
		return
	}
	if err != nil {
		s.deps.Logger.Error("compare branches failed", "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		if fn != nil {
			fn(gh.CompareResult{}, err)
		}
		return
	}
	s.setRateLimit(res.RateLimit)
	if fn != nil {
		fn(res, nil)
	}
}
