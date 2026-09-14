package gh

import (
	"context"
	"errors"
	"sync"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/hirano00o/gprt/internal/model"
)

var teamsQuery = sync.OnceValue(func() string { return mustLoadQuery("teams.graphql") })

// teamNode mirrors one node of an "organization.teams" selection.
type teamNode struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// teamsResponse is the decoded shape of queries/teams.graphql.
type teamsResponse struct {
	Organization *struct {
		Teams *struct {
			Nodes []teamNode `json:"nodes"`
		} `json:"teams"`
	} `json:"organization"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// Teams returns org's teams (GraphQL's organization.teams connection),
// usable as review-request targets alongside an individual user. query
// filters by slug/name on GitHub's side; an empty query is sent as an
// explicit null variable (the same convention MentionableUsers/Branches
// use). first bounds the page size; gprt does not paginate this connection
// further.
//
// org is not always an actual organization: a user-owned repository has
// none, and GitHub reports that as "organization" resolving to null
// accompanied by a NOT_FOUND-typed GraphQL error scoped to the
// "organization" path — expected, not a failure, so it returns an empty
// list with no error instead of propagating it (mirroring how PullRequest
// treats a node-scoped error as data quality, not a hard failure). Any
// other GraphQL/HTTP error still fails the call via classify.
func (c *Client) Teams(ctx context.Context, org, query string, first int) ([]model.Team, model.RateLimit, error) {
	variables := map[string]any{"login": org, "query": stringVariable(query), "first": first}
	var resp teamsResponse
	err := c.gql.DoWithContext(ctx, teamsQuery(), variables, &resp)
	if err != nil {
		var gqlErr *api.GraphQLError
		if errors.As(err, &gqlErr) && organizationNotFound(gqlErr) {
			return nil, resp.RateLimit.toModel(), nil
		}
		return nil, model.RateLimit{}, classify(err)
	}
	if resp.Organization == nil || resp.Organization.Teams == nil {
		return nil, resp.RateLimit.toModel(), nil
	}

	nodes := resp.Organization.Teams.Nodes
	teams := make([]model.Team, 0, len(nodes))
	for _, n := range nodes {
		teams = append(teams, model.Team{ID: n.ID, Slug: n.Slug, Name: n.Name})
	}
	return teams, resp.RateLimit.toModel(), nil
}

// organizationNotFound reports whether gqlErr contains a NOT_FOUND item
// scoped to the top-level "organization" field — GitHub's shape for "this
// login is a user account, not an organization" — as opposed to some other
// error (a permission failure, a genuine server error) that happens to
// also leave "organization" null.
func organizationNotFound(gqlErr *api.GraphQLError) bool {
	for _, item := range gqlErr.Errors {
		if item.Type == "NOT_FOUND" && len(item.Path) > 0 && item.Path[0] == "organization" {
			return true
		}
	}
	return false
}
