package gh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/hirano00o/gprt/internal/model"
)

var searchQuery = sync.OnceValue(func() string {
	return mustLoadQuery("search.graphql")
})

// searchResponse is the decoded shape of queries/search.graphql.
type searchResponse struct {
	Search struct {
		IssueCount int `json:"issueCount"`
		PageInfo   struct {
			EndCursor   string `json:"endCursor"`
			HasNextPage bool   `json:"hasNextPage"`
		} `json:"pageInfo"`
		Nodes []searchPullRequestNode `json:"nodes"`
	} `json:"search"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// SearchResult is one page of a pull request search.
type SearchResult struct {
	Items       []model.PullRequest
	EndCursor   string
	HasNextPage bool
	TotalCount  int
	RateLimit   model.RateLimit
	// Warnings holds human-readable notes about data that could not be
	// fully resolved on this page: a node-scoped GraphQL error (for
	// example INSUFFICIENT_SCOPES resolving a team reviewer, or FORBIDDEN
	// on a SAML-enforced org member) whose affected node was still kept,
	// and/or a count of nodes dropped because they were not a
	// PullRequest or had no ID (see mapPullRequest's callers). An empty
	// slice means the page decoded cleanly.
	Warnings []string
}

// SearchPullRequests runs a GitHub search for pull requests matching query,
// returning one page of up to 50 results starting after cursor (the empty
// string requests the first page).
//
// A GraphQL response can carry both data and errors at once: go-gh decodes
// "data" into resp regardless, then returns a non-nil *api.GraphQLError
// whenever "errors" is non-empty. Discarding resp in that case would throw
// away an otherwise-usable page every time a single node has a
// permission-scoped error (for example a PAT without read:org trying to
// resolve a team reviewer's name, or a SAML-enforced org member GitHub
// won't reveal to the viewer) — a problem that recurs on every refresh
// since the underlying cause does not go away. So a GraphQLError is only
// treated as fatal when at least one of its items is NOT scoped to a
// specific search result node (its Path does not start with
// "search","nodes",<index>): anything broader (the whole search field
// failing, for example) still fails the call as before.
func (c *Client) SearchPullRequests(ctx context.Context, query, cursor string) (SearchResult, error) {
	variables := map[string]any{"q": query, "cursor": cursorVariable(cursor)}

	var resp searchResponse
	fetchErr := c.gql.DoWithContext(ctx, searchQuery(), variables, &resp)

	var warnings []string
	var gqlErr *api.GraphQLError
	if fetchErr != nil {
		if !errors.As(fetchErr, &gqlErr) || !allNodeScoped(gqlErr.Errors) {
			return SearchResult{}, classify(fetchErr)
		}
		warnings = warningMessages(gqlErr.Errors)
	}

	items := make([]model.PullRequest, 0, len(resp.Search.Nodes))
	skipped := 0
	for _, node := range resp.Search.Nodes {
		// A node that is not a PullRequest (or was nulled out by
		// GraphQL error propagation) decodes with every field at its
		// zero value, ID included, since the query's "... on
		// PullRequest" fragment then matches nothing: without this
		// check it would surface as a phantom "#0" row.
		if node.ID == "" {
			skipped++
			continue
		}
		items = append(items, mapPullRequest(c.host, node))
	}

	if len(resp.Search.Nodes) > 0 && len(items) == 0 && gqlErr != nil {
		// Every node on a non-empty page failed to resolve, and a
		// node-scoped GraphQL error was present: this is a systemic
		// failure (for example every reviewer lookup on the page hitting
		// the same permission error), not "the page legitimately has
		// zero results" — surface it as an error rather than a
		// deceptively successful empty page that would otherwise hide
		// the underlying cause from the user.
		return SearchResult{}, classify(fetchErr)
	}

	if skipped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d search result(s) omitted (could not be resolved)", skipped))
	}

	return SearchResult{
		Items:       items,
		EndCursor:   resp.Search.PageInfo.EndCursor,
		HasNextPage: resp.Search.PageInfo.HasNextPage,
		TotalCount:  resp.Search.IssueCount,
		RateLimit:   resp.RateLimit.toModel(),
		Warnings:    warnings,
	}, nil
}

// allNodeScoped reports whether every item is scoped to a specific search
// result node (Path starts with "search", "nodes", <index>). An empty
// slice is not considered node-scoped: a GraphQLError with zero items
// should not occur, and treating it as recoverable would be a silent
// no-op that hides whatever caused DoWithContext to return an error at
// all.
func allNodeScoped(items []api.GraphQLErrorItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !isNodeScopedPath(item.Path) {
			return false
		}
	}
	return true
}

// isNodeScopedPath reports whether path is rooted at a specific search
// result node: ["search", "nodes", <index>, ...]. GraphQL error paths
// always alternate field name and list index for list traversals, so once
// the first two segments match, the third is guaranteed to be an index by
// construction of the query shape.
func isNodeScopedPath(path []any) bool {
	if len(path) < 3 {
		return false
	}
	first, ok := path[0].(string)
	if !ok || first != "search" {
		return false
	}
	second, ok := path[1].(string)
	return ok && second == "nodes"
}

// warningMessages extracts each error item's message, in order.
func warningMessages(items []api.GraphQLErrorItem) []string {
	warnings := make([]string, 0, len(items))
	for _, item := range items {
		warnings = append(warnings, item.Message)
	}
	return warnings
}

// cursorVariable returns nil for an empty cursor so the "$cursor: String"
// variable is sent as GraphQL null (requesting the first page) rather than
// an empty string, which search's "after" argument does not accept as
// equivalent to omitting it.
func cursorVariable(cursor string) any {
	if cursor == "" {
		return nil
	}
	return cursor
}

// sectionQualifier returns the search qualifier(s) that scope a built-in
// section, or custom verbatim for model.SectionKindCustom.
func sectionQualifier(kind model.SectionKind, custom string) string {
	switch kind {
	case model.SectionKindDirectReview:
		return "user-review-requested:@me"
	case model.SectionKindTeamReview:
		return "review-requested:@me -user-review-requested:@me"
	case model.SectionKindMine:
		return "author:@me -review-requested:@me"
	case model.SectionKindInvolved:
		return "involves:@me -author:@me -review-requested:@me"
	case model.SectionKindCustom:
		return custom
	default:
		return ""
	}
}

// stateTerm returns the search qualifier for a list state, or "" for "all"
// (no qualifier restricts by state) or an unrecognised value. "closed"
// excludes merged PRs (is:closed is:unmerged) so that open/closed/merged
// are mutually exclusive: without is:unmerged, "closed" would also match
// every merged PR (GitHub's state:closed covers both), making "closed"
// and "merged" overlapping views of the same data instead of a partition.
func stateTerm(state string) string {
	switch state {
	case "open":
		return "state:open"
	case "closed":
		return "is:closed is:unmerged"
	case "merged":
		return "is:merged"
	default:
		return ""
	}
}

// hasStateQualifier reports whether query already restricts by state, so
// BuildSearchQuery does not layer a conflicting state term of its own on
// top of a user-supplied custom section query. Negated forms (-state:,
// -is:open/closed/merged) count too: a query that already excludes a
// state is just as much "already restricting by state" as one that
// includes it, and layering gprt's own state:open on top of, say,
// -state:closed would needlessly narrow the user's query rather than
// leaving it alone as documented.
func hasStateQualifier(query string) bool {
	for _, tok := range strings.Fields(query) {
		tok = strings.TrimPrefix(tok, "-")
		if strings.HasPrefix(tok, "state:") {
			return true
		}
		switch tok {
		case "is:merged", "is:open", "is:closed":
			return true
		}
	}
	return false
}

// BuildSearchQuery composes a GitHub search query for one list section:
// gprt's fixed base qualifiers, then a state qualifier (skipped for
// "all", and skipped entirely for a Custom section whose own query already
// restricts by state), then the section's own qualifier (custom, verbatim,
// for a Custom section).
func BuildSearchQuery(kind model.SectionKind, custom string, state string) string {
	parts := []string{"type:pr archived:false sort:updated-desc"}

	skipState := kind == model.SectionKindCustom && hasStateQualifier(custom)
	if !skipState {
		if term := stateTerm(state); term != "" {
			parts = append(parts, term)
		}
	}

	if qualifier := sectionQualifier(kind, custom); qualifier != "" {
		parts = append(parts, qualifier)
	}

	return strings.Join(parts, " ")
}
