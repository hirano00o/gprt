package gh

import (
	"context"
	"strings"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var createPullRequestQuery = sync.OnceValue(func() string { return mustLoadQuery("create_pull_request.graphql") })

// CreatePullRequestInput carries createPullRequest's fields. Unlike
// UpdatePullRequestInput, every field here is a plain value, not a
// pointer: creating a pull request has no "leave unchanged" case for any
// of them.
type CreatePullRequestInput struct {
	RepositoryID string
	BaseRefName  string
	HeadRefName  string
	Title        string
	Body         string
	Draft        bool
}

// createPullRequestResponse is the decoded shape of
// queries/create_pull_request.graphql.
type createPullRequestResponse struct {
	CreatePullRequest *struct {
		PullRequest *struct {
			ID         string `json:"id"`
			Number     int    `json:"number"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
			URL string `json:"url"`
		} `json:"pullRequest"`
	} `json:"createPullRequest"`
}

// CreatePullRequest opens a new pull request via GraphQL's
// createPullRequest mutation. The returned model.PullRequest carries only
// ID, Ref (Number and a RepoRef built from host + the payload's
// "repository.nameWithOwner"), and URL; every other field is left at its
// zero value — the store's apply (see internal/store's CreatePullRequest)
// opens the new pull request by ref rather than trying to seed its full
// detail from this minimal payload.
func (c *Client) CreatePullRequest(ctx context.Context, in CreatePullRequestInput) (model.PullRequest, error) {
	variables := map[string]any{
		"repositoryId": in.RepositoryID,
		"baseRefName":  in.BaseRefName,
		"headRefName":  in.HeadRefName,
		"title":        in.Title,
		"body":         in.Body,
		"draft":        in.Draft,
	}
	var resp createPullRequestResponse
	if err := c.gql.DoWithContext(ctx, createPullRequestQuery(), variables, &resp); err != nil {
		return model.PullRequest{}, classify(err)
	}
	if resp.CreatePullRequest == nil || resp.CreatePullRequest.PullRequest == nil {
		return model.PullRequest{}, &Error{Kind: KindUnknown, Message: "createPullRequest: no pull request returned"}
	}

	node := resp.CreatePullRequest.PullRequest
	owner, name, _ := strings.Cut(node.Repository.NameWithOwner, "/")
	pr := model.PullRequest{
		ID: node.ID,
		Ref: model.PRRef{
			Repo:   model.RepoRef{Host: c.host, Owner: owner, Name: name},
			Number: node.Number,
		},
		URL: node.URL,
	}
	return pr, nil
}
