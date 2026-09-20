package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var pullRequestTemplatesQuery = sync.OnceValue(func() string { return mustLoadQuery("pull_request_templates.graphql") })

// pullRequestTemplateNode mirrors one element of a "pullRequestTemplates"
// selection. Filename is nullable (GitHub's own root-level
// "PULL_REQUEST_TEMPLATE.md" template has no filename), so a Go zero value
// (empty string) is the correct decode for a null filename.
type pullRequestTemplateNode struct {
	Filename string `json:"filename"`
	Body     string `json:"body"`
}

// pullRequestTemplatesResponse is the decoded shape of
// queries/pull_request_templates.graphql.
type pullRequestTemplatesResponse struct {
	Repository *struct {
		PullRequestTemplates []pullRequestTemplateNode `json:"pullRequestTemplates"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// PullRequestTemplates returns repo's pull request templates (GraphQL's
// repository.pullRequestTemplates), offered when creating a new pull
// request. A repository with no templates returns an empty slice, not an
// error.
func (c *Client) PullRequestTemplates(
	ctx context.Context, repo model.RepoRef,
) ([]model.PullRequestTemplate, model.RateLimit, error) {
	variables := map[string]any{"owner": repo.Owner, "name": repo.Name}
	var resp pullRequestTemplatesResponse
	if err := c.gql.DoWithContext(ctx, pullRequestTemplatesQuery(), variables, &resp); err != nil {
		return nil, model.RateLimit{}, classify(err)
	}
	if resp.Repository == nil {
		return nil, resp.RateLimit.toModel(), &Error{Kind: KindUnknown, Message: "pullRequestTemplates: no repository returned"}
	}

	templates := make([]model.PullRequestTemplate, 0, len(resp.Repository.PullRequestTemplates))
	for _, n := range resp.Repository.PullRequestTemplates {
		templates = append(templates, model.PullRequestTemplate{Filename: n.Filename, Body: n.Body})
	}
	return templates, resp.RateLimit.toModel(), nil
}
