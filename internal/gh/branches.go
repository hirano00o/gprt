package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var branchesQuery = sync.OnceValue(func() string { return mustLoadQuery("branches.graphql") })

// branchNode mirrors one node of a "refs(refPrefix: \"refs/heads/\") { nodes
// {...} }" selection. target is decoded as a pointer since GitObject is an
// interface field that could in principle be null; in practice a real
// branch ref always has a target.
type branchNode struct {
	Name   string `json:"name"`
	Target *struct {
		OID string `json:"oid"`
	} `json:"target"`
}

// branchesResponse is the decoded shape of queries/branches.graphql.
type branchesResponse struct {
	Repository *struct {
		Refs *struct {
			Nodes []branchNode `json:"nodes"`
		} `json:"refs"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// Branches returns repo's branches (GraphQL's repository.refs connection,
// refPrefix: "refs/heads/"), used by the base/head branch picker. query
// filters by name prefix on GitHub's side; an empty query is sent as an
// explicit null variable (via stringVariable, the same convention
// MentionableUsers uses), matching an unfiltered request rather than a
// literal empty-string search. first bounds the page size; gprt does not
// paginate this connection further.
func (c *Client) Branches(
	ctx context.Context, repo model.RepoRef, query string, first int,
) ([]model.Branch, model.RateLimit, error) {
	variables := map[string]any{
		"owner": repo.Owner, "name": repo.Name,
		"query": stringVariable(query), "first": first,
	}
	var resp branchesResponse
	if err := c.gql.DoWithContext(ctx, branchesQuery(), variables, &resp); err != nil {
		return nil, model.RateLimit{}, classify(err)
	}
	if resp.Repository == nil || resp.Repository.Refs == nil {
		return nil, resp.RateLimit.toModel(), &Error{Kind: KindUnknown, Message: "branches: no repository returned"}
	}

	nodes := resp.Repository.Refs.Nodes
	branches := make([]model.Branch, 0, len(nodes))
	for _, n := range nodes {
		b := model.Branch{Name: n.Name}
		if n.Target != nil {
			b.HeadOID = n.Target.OID
		}
		branches = append(branches, b)
	}
	return branches, resp.RateLimit.toModel(), nil
}
