package gh

import (
	"context"
	"sync"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

var (
	mergePullRequestQuery  = sync.OnceValue(func() string { return mustLoadQuery("merge_pull_request.graphql") })
	closePullRequestQuery  = sync.OnceValue(func() string { return mustLoadQuery("close_pull_request.graphql") })
	reopenPullRequestQuery = sync.OnceValue(func() string { return mustLoadQuery("reopen_pull_request.graphql") })
)

// mergePullRequestResponse is the decoded shape of
// queries/merge_pull_request.graphql.
type mergePullRequestResponse struct {
	MergePullRequest *struct {
		PullRequest *struct {
			State    string    `json:"state"`
			Merged   bool      `json:"merged"`
			MergedAt time.Time `json:"mergedAt"`
		} `json:"pullRequest"`
	} `json:"mergePullRequest"`
}

// MergePullRequest merges id via GraphQL's mergePullRequest mutation.
// commitHeadline/commitBody are sent as explicit null when nil (GitHub
// fills in its own default commit message in that case, matching the
// optional-variable convention used throughout internal/gh).
// expectedHeadOid guards against merging a commit the user never saw:
// GitHub rejects the mutation with a validation error when the pull
// request's actual head has since moved past it (the store's Merge
// captures this from the currently loaded pull request's own HeadOID
// immediately before the mutation runs — see internal/store/mutations.go's
// prepare pattern). The returned model.PullRequest carries only State,
// Merged, and MergedAt; every other field is left at its zero value.
func (c *Client) MergePullRequest(
	ctx context.Context, id string, method model.MergeMethod, commitHeadline, commitBody *string, expectedHeadOID string,
) (model.PullRequest, error) {
	variables := map[string]any{
		"id":              id,
		"mergeMethod":     string(method),
		"commitHeadline":  stringPtrVariable(commitHeadline),
		"commitBody":      stringPtrVariable(commitBody),
		"expectedHeadOid": expectedHeadOID,
	}
	var resp mergePullRequestResponse
	if err := c.gql.DoWithContext(ctx, mergePullRequestQuery(), variables, &resp); err != nil {
		return model.PullRequest{}, classify(err)
	}
	if resp.MergePullRequest == nil || resp.MergePullRequest.PullRequest == nil {
		return model.PullRequest{}, &Error{Kind: KindUnknown, Message: "mergePullRequest: no pull request returned"}
	}

	node := resp.MergePullRequest.PullRequest
	pr := model.PullRequest{
		State:    model.PRState(node.State),
		Merged:   node.Merged,
		MergedAt: node.MergedAt,
	}
	return pr, nil
}

// closePullRequestResponse is the decoded shape of
// queries/close_pull_request.graphql.
type closePullRequestResponse struct {
	ClosePullRequest *struct {
		PullRequest *struct {
			State string `json:"state"`
		} `json:"pullRequest"`
	} `json:"closePullRequest"`
}

// ClosePullRequest closes id via GraphQL's closePullRequest mutation,
// returning its post-close state (read back from the server rather than
// assumed CLOSED, matching every other mutation in this package).
func (c *Client) ClosePullRequest(ctx context.Context, id string) (model.PRState, error) {
	variables := map[string]any{"id": id}
	var resp closePullRequestResponse
	if err := c.gql.DoWithContext(ctx, closePullRequestQuery(), variables, &resp); err != nil {
		return "", classify(err)
	}
	if resp.ClosePullRequest == nil || resp.ClosePullRequest.PullRequest == nil {
		return "", &Error{Kind: KindUnknown, Message: "closePullRequest: no pull request returned"}
	}
	return model.PRState(resp.ClosePullRequest.PullRequest.State), nil
}

// reopenPullRequestResponse is the decoded shape of
// queries/reopen_pull_request.graphql.
type reopenPullRequestResponse struct {
	ReopenPullRequest *struct {
		PullRequest *struct {
			State string `json:"state"`
		} `json:"pullRequest"`
	} `json:"reopenPullRequest"`
}

// ReopenPullRequest reopens id via GraphQL's reopenPullRequest mutation,
// returning its post-reopen state.
func (c *Client) ReopenPullRequest(ctx context.Context, id string) (model.PRState, error) {
	variables := map[string]any{"id": id}
	var resp reopenPullRequestResponse
	if err := c.gql.DoWithContext(ctx, reopenPullRequestQuery(), variables, &resp); err != nil {
		return "", classify(err)
	}
	if resp.ReopenPullRequest == nil || resp.ReopenPullRequest.PullRequest == nil {
		return "", &Error{Kind: KindUnknown, Message: "reopenPullRequest: no pull request returned"}
	}
	return model.PRState(resp.ReopenPullRequest.PullRequest.State), nil
}
