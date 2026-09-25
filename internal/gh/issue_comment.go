package gh

import (
	"context"
	"sync"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

var addCommentQuery = sync.OnceValue(func() string {
	return mustLoadQuery("add_comment.graphql")
})

var updateIssueCommentQuery = sync.OnceValue(func() string {
	return mustLoadQuery("update_issue_comment.graphql")
})

var deleteIssueCommentQuery = sync.OnceValue(func() string {
	return mustLoadQuery("delete_issue_comment.graphql")
})

// issueCommentNode mirrors the "IssueComment" field selection shared by
// add_comment.graphql's commentEdge.node and update_issue_comment.graphql's
// issueComment, matching pull_request.graphql's own IssueComment timeline
// fragment field-for-field.
type issueCommentNode struct {
	ID     string `json:"id"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body            string                  `json:"body"`
	CreatedAt       time.Time               `json:"createdAt"`
	UpdatedAt       time.Time               `json:"updatedAt"`
	URL             string                  `json:"url"`
	ReactionGroups  []reactionGroupFragment `json:"reactionGroups"`
	ViewerCanUpdate bool                    `json:"viewerCanUpdate"`
	ViewerCanDelete bool                    `json:"viewerCanDelete"`
}

// toModel maps a decoded issueCommentNode into model.IssueComment.
func (n issueCommentNode) toModel() model.IssueComment {
	return model.IssueComment{
		ID:              n.ID,
		Author:          model.User{Login: n.Author.Login},
		Body:            normalizeNewlines(n.Body),
		CreatedAt:       n.CreatedAt,
		UpdatedAt:       n.UpdatedAt,
		ReactionGroups:  toReactionGroups(n.ReactionGroups),
		ViewerCanUpdate: n.ViewerCanUpdate,
		ViewerCanDelete: n.ViewerCanDelete,
		URL:             n.URL,
	}
}

// addCommentResponse is the decoded shape of queries/add_comment.graphql.
type addCommentResponse struct {
	AddComment *struct {
		CommentEdge *struct {
			Node issueCommentNode `json:"node"`
		} `json:"commentEdge"`
	} `json:"addComment"`
}

// AddIssueComment adds a general (non-review) comment to subjectID, most
// commonly a pull request's own node ID for a PR-tab general comment
// (GraphQL's addComment mutation accepts any commentable subject, though
// gprt only ever calls it with a pull request ID today).
func (c *Client) AddIssueComment(ctx context.Context, subjectID, body string) (model.IssueComment, error) {
	variables := map[string]any{"id": subjectID, "body": body}
	var resp addCommentResponse
	if err := c.gql.DoWithContext(ctx, addCommentQuery(), variables, &resp); err != nil {
		return model.IssueComment{}, classify(err)
	}
	if resp.AddComment == nil || resp.AddComment.CommentEdge == nil {
		return model.IssueComment{}, &Error{Kind: KindUnknown, Message: "addComment: no comment returned"}
	}
	return resp.AddComment.CommentEdge.Node.toModel(), nil
}

// updateIssueCommentResponse is the decoded shape of
// queries/update_issue_comment.graphql.
type updateIssueCommentResponse struct {
	UpdateIssueComment *struct {
		IssueComment issueCommentNode `json:"issueComment"`
	} `json:"updateIssueComment"`
}

// UpdateIssueComment edits an existing issue comment's body.
func (c *Client) UpdateIssueComment(ctx context.Context, id, body string) (model.IssueComment, error) {
	variables := map[string]any{"id": id, "body": body}
	var resp updateIssueCommentResponse
	if err := c.gql.DoWithContext(ctx, updateIssueCommentQuery(), variables, &resp); err != nil {
		return model.IssueComment{}, classify(err)
	}
	if resp.UpdateIssueComment == nil {
		return model.IssueComment{}, &Error{Kind: KindUnknown, Message: "updateIssueComment: no comment returned"}
	}
	return resp.UpdateIssueComment.IssueComment.toModel(), nil
}

// DeleteIssueComment deletes an existing issue comment. The mutation's
// payload carries nothing gprt reads (only clientMutationId, which gprt
// never sets), so the response decodes into an empty struct purely to
// satisfy DoWithContext's signature.
func (c *Client) DeleteIssueComment(ctx context.Context, id string) error {
	variables := map[string]any{"id": id}
	var resp struct{}
	if err := c.gql.DoWithContext(ctx, deleteIssueCommentQuery(), variables, &resp); err != nil {
		return classify(err)
	}
	return nil
}
