package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var repositoryQuery = sync.OnceValue(func() string { return mustLoadQuery("repository.graphql") })

// repositoryInfoResponse is the decoded shape of queries/repository.graphql.
type repositoryInfoResponse struct {
	Repository *struct {
		ID               string `json:"id"`
		DefaultBranchRef *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		MergeCommitAllowed  bool   `json:"mergeCommitAllowed"`
		SquashMergeAllowed  bool   `json:"squashMergeAllowed"`
		RebaseMergeAllowed  bool   `json:"rebaseMergeAllowed"`
		DeleteBranchOnMerge bool   `json:"deleteBranchOnMerge"`
		ViewerPermission    string `json:"viewerPermission"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// Repository returns repo's metadata (allowed merge strategies, default
// branch, and the viewer's own permission level), used to drive the
// edit/merge/create-PR forms (M5). A null "repository" with no GraphQL
// error at all (as opposed to one caused by an error, which classify
// reports instead) is *gh.Error{Kind: KindNotFound}, mirroring
// PullRequest's own null-vs-error distinction.
func (c *Client) Repository(ctx context.Context, repo model.RepoRef) (model.RepositoryInfo, model.RateLimit, error) {
	variables := map[string]any{"owner": repo.Owner, "name": repo.Name}
	var resp repositoryInfoResponse
	if err := c.gql.DoWithContext(ctx, repositoryQuery(), variables, &resp); err != nil {
		return model.RepositoryInfo{}, model.RateLimit{}, classify(err)
	}
	if resp.Repository == nil {
		return model.RepositoryInfo{}, resp.RateLimit.toModel(),
			&Error{Kind: KindNotFound, Message: "repository: not found"}
	}

	info := model.RepositoryInfo{
		ID:                  resp.Repository.ID,
		Ref:                 repo,
		MergeCommitAllowed:  resp.Repository.MergeCommitAllowed,
		SquashMergeAllowed:  resp.Repository.SquashMergeAllowed,
		RebaseMergeAllowed:  resp.Repository.RebaseMergeAllowed,
		DeleteBranchOnMerge: resp.Repository.DeleteBranchOnMerge,
		ViewerPermission:    resp.Repository.ViewerPermission,
	}
	if resp.Repository.DefaultBranchRef != nil {
		info.DefaultBranch = resp.Repository.DefaultBranchRef.Name
	}
	return info, resp.RateLimit.toModel(), nil
}
