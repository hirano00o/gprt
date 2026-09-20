// Package store's on-demand branch/team search (SearchBranches,
// SearchTeams) backs a query-filtered picker (the base/head branch picker,
// the review-request team picker): unlike repository metadata or
// mentionable users, a search result here is never cached (each keystroke
// is its own, generally distinct query, so a disk/memory cache would
// mostly just accumulate one-shot entries with no reuse value) and is not
// per-repository/per-organization keyed in the Store — a single,
// generation-tokened slot per kind is enough, since only one such picker
// is open, and being typed into, at a time. A later call's generation
// token supersedes an earlier one still in flight, so a stale answer for
// an abandoned keystroke's query is dropped rather than applied over a
// newer one. Delivery is callback-only (fn, invoked on the UI goroutine
// from inside the Dispatch callback that resolves the search): there is
// exactly one consumer of a given search — the form it backs — so a
// second, event-based delivery path (an earlier revision of this file had
// one) would only have meant every result travelling twice for no reader
// of the second copy.
package store

import (
	"fmt"

	"github.com/hirano00o/gprt/internal/model"
)

// branchSearchFirst/teamSearchFirst bound each search's page size; gprt
// does not paginate either connection further (a query-filtered,
// as-you-type picker has no use for a "load more" of its own).
const (
	branchSearchFirst = 25
	teamSearchFirst   = 25
)

// SearchBranches starts a branch-name search for repo, matching query
// against GitHub's own refs(query:) filter, and returns a token
// identifying this call. fn (optional) is invoked on the UI goroutine,
// from inside the Dispatch callback that resolves the search — but only
// if this call is still the most recent one: a later SearchBranches call
// for the same or a different repo, made before this one resolved,
// supersedes it, and fn never fires for the superseded call at all (see
// the package doc for why staleness is tracked this way rather than by
// caching). A failed search still calls fn (with a nil slice and the
// error) and reports the failure via the usual `EventError`/log path,
// exactly like every other fetch in this package.
func (s *Store) SearchBranches(repo model.RepoRef, query string, fn func([]model.Branch, error)) int {
	s.branchSearchGen++
	gen := s.branchSearchGen
	ctx := s.baseCtx

	go func() {
		var branches []model.Branch
		var rl model.RateLimit
		var err error
		// A single deferred closure recovers a panic into err and always
		// dispatches the same apply path, matching every other fetch in
		// this package.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: search_branches: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "search_branches", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyBranchSearchResult(gen, branches, rl, err, cancelled, fn)
			})
		}()
		branches, rl, err = s.deps.GitHub.Branches(ctx, repo, query, branchSearchFirst)
	}()

	return gen
}

// applyBranchSearchResult applies (or reports the failure of) one
// SearchBranches call's result. It runs only on the UI goroutine, from a
// Dispatch callback. A result whose token no longer matches the most
// recent SearchBranches call (gen != s.branchSearchGen), or our own
// cancellation (app shutdown), is dropped entirely: no fn call, no
// EventError — the caller has already moved on.
func (s *Store) applyBranchSearchResult(
	gen int, branches []model.Branch, rl model.RateLimit, err error, cancelled bool, fn func([]model.Branch, error),
) {
	if gen != s.branchSearchGen || cancelled {
		return
	}
	if err != nil {
		s.deps.Logger.Error("search branches failed", "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		if fn != nil {
			fn(nil, err)
		}
		return
	}
	s.setRateLimit(rl)
	if fn != nil {
		fn(branches, nil)
	}
}

// SearchTeams starts a team search for org, matching query against
// GitHub's own organization.teams(query:) filter, mirroring
// SearchBranches in every other respect (including its behaviour for a
// user-owned org login — see gh.Client.Teams's own doc comment: it
// resolves to an empty, non-error result, applied and reported like any
// other successful search).
func (s *Store) SearchTeams(org, query string, fn func([]model.Team, error)) int {
	s.teamSearchGen++
	gen := s.teamSearchGen
	ctx := s.baseCtx

	go func() {
		var teams []model.Team
		var rl model.RateLimit
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: search_teams: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "search_teams", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() {
				s.applyTeamSearchResult(gen, teams, rl, err, cancelled, fn)
			})
		}()
		teams, rl, err = s.deps.GitHub.Teams(ctx, org, query, teamSearchFirst)
	}()

	return gen
}

// applyTeamSearchResult mirrors applyBranchSearchResult for SearchTeams.
func (s *Store) applyTeamSearchResult(
	gen int, teams []model.Team, rl model.RateLimit, err error, cancelled bool, fn func([]model.Team, error),
) {
	if gen != s.teamSearchGen || cancelled {
		return
	}
	if err != nil {
		s.deps.Logger.Error("search teams failed", "err", err)
		s.emit(Event{Kind: EventError, Err: err})
		if fn != nil {
			fn(nil, err)
		}
		return
	}
	s.setRateLimit(rl)
	if fn != nil {
		fn(teams, nil)
	}
}
