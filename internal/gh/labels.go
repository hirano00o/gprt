package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var labelsQuery = sync.OnceValue(func() string { return mustLoadQuery("labels.graphql") })

// labelsPageSize is the page size requested per call; a repository can
// have more than 100 labels, so Labels paginates while hasNextPage holds.
const labelsPageSize = 100

// labelsResponse is the decoded shape of queries/labels.graphql.
type labelsResponse struct {
	Repository *struct {
		Labels *struct {
			Nodes    []labelNode      `json:"nodes"`
			PageInfo pageInfoFragment `json:"pageInfo"`
		} `json:"labels"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// Labels returns every one of repo's labels, paginating with after while
// hasNextPage holds (a repository can have more than 100 labels). The
// returned rate limit is the final page's; a failure on any page discards
// whatever was fetched so far and returns nil, matching MentionableUsers'
// fail-fast behaviour for a simple read.
func (c *Client) Labels(ctx context.Context, repo model.RepoRef) ([]model.Label, model.RateLimit, error) {
	var labels []model.Label
	var rl model.RateLimit
	after := ""

	for {
		variables := map[string]any{
			"owner": repo.Owner, "name": repo.Name,
			"first": labelsPageSize, "after": stringVariable(after),
		}
		var resp labelsResponse
		if err := c.gql.DoWithContext(ctx, labelsQuery(), variables, &resp); err != nil {
			return nil, model.RateLimit{}, classify(err)
		}
		if resp.Repository == nil || resp.Repository.Labels == nil {
			return nil, resp.RateLimit.toModel(), &Error{Kind: KindUnknown, Message: "labels: no repository returned"}
		}

		for _, n := range resp.Repository.Labels.Nodes {
			labels = append(labels, model.Label{ID: n.ID, Name: n.Name, Color: n.Color})
		}
		rl = resp.RateLimit.toModel()

		if !resp.Repository.Labels.PageInfo.HasNextPage {
			return labels, rl, nil
		}
		after = resp.Repository.Labels.PageInfo.EndCursor
	}
}
