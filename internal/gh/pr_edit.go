package gh

import (
	"context"
	"sync"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

var (
	updatePullRequestQuery  = sync.OnceValue(func() string { return mustLoadQuery("update_pull_request.graphql") })
	requestReviewersQuery   = sync.OnceValue(func() string { return mustLoadQuery("request_reviewers.graphql") })
	markReadyForReviewQuery = sync.OnceValue(func() string { return mustLoadQuery("mark_ready_for_review.graphql") })
	convertToDraftQuery     = sync.OnceValue(func() string { return mustLoadQuery("convert_to_draft.graphql") })
)

// UpdatePullRequestInput carries updatePullRequest's optional fields. A nil
// pointer means "leave this field unchanged" and is sent as an explicit
// GraphQL null variable (see stringPtrVariable/labelIDsVariable), matching
// the optional-variable convention used throughout internal/gh (see
// review.go's intVariable/stringVariable). LabelIDs replaces the pull
// request's entire label set when non-nil: a non-nil, empty slice clears
// every label, distinct from nil (leave labels unchanged).
type UpdatePullRequestInput struct {
	Title       *string
	Body        *string
	BaseRefName *string
	LabelIDs    *[]string
}

// stringPtrVariable returns nil for a nil pointer (an omitted optional
// field, sent as GraphQL null) or the pointee otherwise.
func stringPtrVariable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// labelIDsVariable returns nil for a nil pointer (leave labels unchanged)
// or ids otherwise — including a non-nil, empty slice, which clears every
// label (a real, distinct request from "don't touch labels").
func labelIDsVariable(ids *[]string) any {
	if ids == nil {
		return nil
	}
	return *ids
}

// updatePullRequestNode mirrors update_pull_request.graphql's
// "pullRequest" selection.
type updatePullRequestNode struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	BaseRefName string `json:"baseRefName"`
	Labels      struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// updatePullRequestResponse is the decoded shape of
// queries/update_pull_request.graphql.
type updatePullRequestResponse struct {
	UpdatePullRequest *struct {
		PullRequest *updatePullRequestNode `json:"pullRequest"`
	} `json:"updatePullRequest"`
}

// UpdatePullRequest edits an existing pull request's title, body, base
// branch, and/or labels via GraphQL's updatePullRequest mutation. Fields
// left nil in in are sent as explicit null, meaning "leave unchanged" (see
// UpdatePullRequestInput's doc comment). The returned model.PullRequest
// carries only the fields the mutation payload selects (Title, Body,
// BaseRefName, Labels, UpdatedAt); every other field is left at its zero
// value — the store's apply merges these onto the fuller pull request it
// already holds rather than replacing it outright.
func (c *Client) UpdatePullRequest(
	ctx context.Context, id string, in UpdatePullRequestInput,
) (model.PullRequest, error) {
	variables := map[string]any{
		"id":          id,
		"title":       stringPtrVariable(in.Title),
		"body":        stringPtrVariable(in.Body),
		"baseRefName": stringPtrVariable(in.BaseRefName),
		"labelIds":    labelIDsVariable(in.LabelIDs),
	}
	var resp updatePullRequestResponse
	if err := c.gql.DoWithContext(ctx, updatePullRequestQuery(), variables, &resp); err != nil {
		return model.PullRequest{}, classify(err)
	}
	if resp.UpdatePullRequest == nil || resp.UpdatePullRequest.PullRequest == nil {
		return model.PullRequest{}, &Error{Kind: KindUnknown, Message: "updatePullRequest: no pull request returned"}
	}

	node := resp.UpdatePullRequest.PullRequest
	labels := make([]model.Label, 0, len(node.Labels.Nodes))
	for _, l := range node.Labels.Nodes {
		labels = append(labels, model.Label{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	pr := model.PullRequest{
		Title:       node.Title,
		Body:        normalizeNewlines(node.Body),
		BaseRefName: node.BaseRefName,
		Labels:      labels,
		UpdatedAt:   node.UpdatedAt,
	}
	return pr, nil
}

// requestReviewersResponse is the decoded shape of
// queries/request_reviewers.graphql. Its "reviewRequests" selection
// matches pull_request.graphql's own field-for-field, so it decodes
// directly into reviewRequestNode and reuses mapReviewer.
type requestReviewersResponse struct {
	RequestReviews *struct {
		PullRequest *struct {
			ReviewRequests struct {
				Nodes []reviewRequestNode `json:"nodes"`
			} `json:"reviewRequests"`
		} `json:"pullRequest"`
	} `json:"requestReviews"`
}

// nonNilIDs returns a non-nil, empty slice for a nil ids, or ids
// unchanged otherwise. GitHub's requestReviews mutation treats a null
// userIds/teamIds as "leave this kind of reviewer unspecified" (no
// change) but an empty array as "clear every reviewer of that kind" (see
// https://github.com/cli/cli/issues/7721) — a real, distinct request from
// "don't touch this kind" — so a nil Go slice (indistinguishable at the
// call site from an explicitly empty one) must never round-trip to JSON
// null, unlike labelIDsVariable's *[]string, whose nil pointer genuinely
// does mean "unspecified".
func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// RequestReviewers replaces (union: false) or adds to (union: true) id's
// requested reviewers via GraphQL's requestReviews mutation, returning the
// pull request's full, refreshed set of review requests.
func (c *Client) RequestReviewers(
	ctx context.Context, id string, userIDs, teamIDs []string, union bool,
) ([]model.Reviewer, error) {
	variables := map[string]any{"id": id, "userIds": nonNilIDs(userIDs), "teamIds": nonNilIDs(teamIDs), "union": union}
	var resp requestReviewersResponse
	if err := c.gql.DoWithContext(ctx, requestReviewersQuery(), variables, &resp); err != nil {
		return nil, classify(err)
	}
	if resp.RequestReviews == nil || resp.RequestReviews.PullRequest == nil {
		return nil, &Error{Kind: KindUnknown, Message: "requestReviews: no pull request returned"}
	}

	nodes := resp.RequestReviews.PullRequest.ReviewRequests.Nodes
	reviewers := make([]model.Reviewer, 0, len(nodes))
	for _, n := range nodes {
		reviewers = append(reviewers, mapReviewer(n))
	}
	return reviewers, nil
}

// markReadyForReviewResponse is the decoded shape of
// queries/mark_ready_for_review.graphql.
type markReadyForReviewResponse struct {
	MarkPullRequestReadyForReview *struct {
		PullRequest *struct {
			IsDraft bool `json:"isDraft"`
		} `json:"pullRequest"`
	} `json:"markPullRequestReadyForReview"`
}

// MarkReadyForReview takes id out of draft via GraphQL's
// markPullRequestReadyForReview mutation, returning the pull request's
// post-toggle IsDraft (always false on success, but read back from the
// server rather than assumed).
func (c *Client) MarkReadyForReview(ctx context.Context, id string) (bool, error) {
	variables := map[string]any{"id": id}
	var resp markReadyForReviewResponse
	if err := c.gql.DoWithContext(ctx, markReadyForReviewQuery(), variables, &resp); err != nil {
		return false, classify(err)
	}
	if resp.MarkPullRequestReadyForReview == nil || resp.MarkPullRequestReadyForReview.PullRequest == nil {
		return false, &Error{Kind: KindUnknown, Message: "markPullRequestReadyForReview: no pull request returned"}
	}
	return resp.MarkPullRequestReadyForReview.PullRequest.IsDraft, nil
}

// convertToDraftResponse is the decoded shape of
// queries/convert_to_draft.graphql.
type convertToDraftResponse struct {
	ConvertPullRequestToDraft *struct {
		PullRequest *struct {
			IsDraft bool `json:"isDraft"`
		} `json:"pullRequest"`
	} `json:"convertPullRequestToDraft"`
}

// ConvertToDraft marks id as a draft via GraphQL's
// convertPullRequestToDraft mutation, returning the pull request's
// post-toggle IsDraft (always true on success, but read back from the
// server rather than assumed).
func (c *Client) ConvertToDraft(ctx context.Context, id string) (bool, error) {
	variables := map[string]any{"id": id}
	var resp convertToDraftResponse
	if err := c.gql.DoWithContext(ctx, convertToDraftQuery(), variables, &resp); err != nil {
		return false, classify(err)
	}
	if resp.ConvertPullRequestToDraft == nil || resp.ConvertPullRequestToDraft.PullRequest == nil {
		return false, &Error{Kind: KindUnknown, Message: "convertPullRequestToDraft: no pull request returned"}
	}
	return resp.ConvertPullRequestToDraft.PullRequest.IsDraft, nil
}
