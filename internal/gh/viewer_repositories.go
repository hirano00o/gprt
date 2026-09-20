package gh

import (
	"context"
	"strings"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var viewerRepositoriesQuery = sync.OnceValue(func() string { return mustLoadQuery("viewer_repositories.graphql") })

// viewerRepositoryNode mirrors one node of the "viewer.repositories"
// connection.
type viewerRepositoryNode struct {
	ID               string `json:"id"`
	NameWithOwner    string `json:"nameWithOwner"`
	IsArchived       bool   `json:"isArchived"`
	DefaultBranchRef *struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

// viewerRepositoriesResponse is the decoded shape of
// queries/viewer_repositories.graphql.
type viewerRepositoriesResponse struct {
	Viewer struct {
		Repositories struct {
			Nodes []viewerRepositoryNode `json:"nodes"`
		} `json:"repositories"`
	} `json:"viewer"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// ViewerRepositories returns up to first of the viewer's own repositories
// (GraphQL's viewer.repositories, affiliations OWNER/COLLABORATOR/
// ORGANIZATION_MEMBER, ordered by most recently pushed to), the create-
// pull-request repository picker's candidate list — filtered locally by
// the UI as the user types. host becomes each result's RepoRef.Host, since
// the query itself only returns "owner/name".
func (c *Client) ViewerRepositories(ctx context.Context, first int) ([]model.RepositorySummary, model.RateLimit, error) {
	variables := map[string]any{"first": first}
	var resp viewerRepositoriesResponse
	if err := c.gql.DoWithContext(ctx, viewerRepositoriesQuery(), variables, &resp); err != nil {
		return nil, model.RateLimit{}, classify(err)
	}

	nodes := resp.Viewer.Repositories.Nodes
	repos := make([]model.RepositorySummary, 0, len(nodes))
	for _, n := range nodes {
		owner, name, _ := strings.Cut(n.NameWithOwner, "/")
		summary := model.RepositorySummary{
			Ref:        model.RepoRef{Host: c.host, Owner: owner, Name: name},
			ID:         n.ID,
			IsArchived: n.IsArchived,
		}
		if n.DefaultBranchRef != nil {
			summary.DefaultBranch = n.DefaultBranchRef.Name
		}
		repos = append(repos, summary)
	}
	return repos, resp.RateLimit.toModel(), nil
}
