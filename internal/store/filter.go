package store

import (
	"fmt"
	"strings"

	"github.com/hirano00o/gprt/internal/model"
)

// parsedFilter is the parsed form of a list filter string: recognised
// "qualifier:value" tokens plus the remaining free-text tokens. It has no
// dependency on Store, so it is unit-tested directly against
// model.PullRequest values.
type parsedFilter struct {
	repo     string
	author   string
	reviewer string
	label    string
	state    string // "" means no state override was requested
	text     []string
}

// parseFilter splits raw on whitespace, recognising the "repo:", "author:",
// "reviewer:", "label:", and "state:" qualifiers (case-sensitive prefixes;
// the value is taken verbatim). Every other token is kept as free text.
func parseFilter(raw string) parsedFilter {
	var f parsedFilter
	for _, tok := range strings.Fields(raw) {
		switch {
		case strings.HasPrefix(tok, "repo:"):
			f.repo = strings.TrimPrefix(tok, "repo:")
		case strings.HasPrefix(tok, "author:"):
			f.author = strings.TrimPrefix(tok, "author:")
		case strings.HasPrefix(tok, "reviewer:"):
			f.reviewer = strings.TrimPrefix(tok, "reviewer:")
		case strings.HasPrefix(tok, "label:"):
			f.label = strings.TrimPrefix(tok, "label:")
		case strings.HasPrefix(tok, "state:"):
			f.state = strings.TrimPrefix(tok, "state:")
		default:
			f.text = append(f.text, tok)
		}
	}
	return f
}

// matches reports whether pr (shown under sec) satisfies every qualifier
// and every free-text token. state is not checked here: a state change is
// handled by the Store as a re-search (see SetFilter), not a client-side
// predicate, so by the time matches runs, every loaded item already
// reflects the desired state.
func (f parsedFilter) matches(pr model.PullRequest, _ model.Section) bool {
	if f.repo != "" && !containsFold(pr.Ref.Repo.NameWithOwner(), f.repo) {
		return false
	}
	if f.author != "" && !strings.EqualFold(pr.Author.Login, f.author) {
		return false
	}
	if f.reviewer != "" && !hasReviewer(pr.ReviewRequests, f.reviewer) {
		return false
	}
	if f.label != "" && !hasLabel(pr.Labels, f.label) {
		return false
	}
	for _, tok := range f.text {
		if !matchesFreeText(pr, tok) {
			return false
		}
	}
	return true
}

func hasReviewer(reviewers []model.Reviewer, want string) bool {
	for _, r := range reviewers {
		if strings.EqualFold(r.Login, want) {
			return true
		}
	}
	return false
}

func hasLabel(labels []model.Label, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(l.Name, want) {
			return true
		}
	}
	return false
}

// matchesFreeText reports whether token matches pr's title, "#number",
// "owner/name", or author login, case-insensitively.
func matchesFreeText(pr model.PullRequest, token string) bool {
	return containsFold(pr.Title, token) ||
		containsFold(fmt.Sprintf("#%d", pr.Ref.Number), token) ||
		containsFold(pr.Ref.Repo.NameWithOwner(), token) ||
		containsFold(pr.Author.Login, token)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// SetFilter updates the current filter text.
//
// A "state:" qualifier switches the search state; omitting one (an empty
// filter, or one with only other qualifiers) reverts the state to
// Config.List.State rather than leaving it at whatever a previous
// SetFilter call last set it to, so "state:closed" followed later by
// "author:alice" shows open PRs again, not closed ones filtered by
// author.
//
// A state value that is a strict prefix of exactly one or more valid
// states ("c", "clo", "close") is treated as incomplete, not wrong: the
// user is presumably still typing toward "closed", so this is silent
// (Rows/ListChanged only, no EventError) and does not change the state.
// A value that is not a prefix of any valid state (state:bogus) is a real
// mistake: it is reported via EventError, once, the first time it is
// seen — repeating SetFilter with the exact same invalid value does not
// re-emit the error, so a user who has not touched the state: part of the
// filter since is not shown the same complaint on every keystroke
// elsewhere in the filter string. It sets no lastErr: an invalid filter is
// a user-input problem, not a fetch failure. Either way, the raw text is
// still stored, so Filter() returns exactly what was typed and any other
// qualifier in the same string still takes effect.
//
// When the resolved state does change, every section's search query is
// rebuilt against it, every section is marked stale (so Rows immediately
// shows the old data as stale rather than looking like it silently
// stopped updating until the new fetch lands), and a full Reload is
// triggered. Otherwise the filter is simply applied to the already-loaded
// items via a ListChanged event.
func (s *Store) SetFilter(text string) {
	s.filterText = text
	s.filter = parseFilter(text)

	wantState := s.deps.Config.List.State
	if s.filter.state != "" {
		switch {
		case isValidState(s.filter.state):
			wantState = s.filter.state
			s.lastInvalidStateFilter = ""
		case isPrefixOfValidState(s.filter.state):
			s.lastInvalidStateFilter = ""
			s.emit(Event{Kind: EventListChanged})
			return
		default:
			if s.filter.state != s.lastInvalidStateFilter {
				s.lastInvalidStateFilter = s.filter.state
				s.emit(Event{Kind: EventError, Err: fmt.Errorf("store: unknown state %q", s.filter.state)})
			}
			s.emit(Event{Kind: EventListChanged})
			return
		}
	} else {
		s.lastInvalidStateFilter = ""
	}

	if wantState != s.state {
		s.state = wantState
		s.rebuildSectionQueries()
		s.markSectionsStale()
		s.emit(Event{Kind: EventListChanged})
		s.Reload()
		return
	}

	s.emit(Event{Kind: EventListChanged})
}

// markSectionsStale marks every section's items as not yet confirmed by
// the fetch a state change is about to trigger.
func (s *Store) markSectionsStale() {
	for _, sec := range s.sections {
		sec.stale = true
	}
}

// Filter returns the current raw filter text.
func (s *Store) Filter() string {
	return s.filterText
}

func isValidState(state string) bool {
	switch state {
	case "open", "closed", "merged", "all":
		return true
	default:
		return false
	}
}

// isPrefixOfValidState reports whether prefix is a strict prefix of a
// valid state value (an exact match is handled by isValidState before this
// is ever consulted, and "" is never passed in — see SetFilter).
func isPrefixOfValidState(prefix string) bool {
	for _, state := range [...]string{"open", "closed", "merged", "all"} {
		if strings.HasPrefix(state, prefix) {
			return true
		}
	}
	return false
}

func (s *Store) rebuildSectionQueries() {
	for _, sec := range s.sections {
		sec.query = buildSectionQuery(sec, s.state)
	}
}
