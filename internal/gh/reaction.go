package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var (
	addReactionQuery    = sync.OnceValue(func() string { return mustLoadQuery("add_reaction.graphql") })
	removeReactionQuery = sync.OnceValue(func() string { return mustLoadQuery("remove_reaction.graphql") })
)

// addReactionResponse is the decoded shape of queries/add_reaction.graphql.
// The payload's own top-level "reactionGroups" field is selected directly
// (rather than nesting through "subject { reactionGroups }", which
// AddReactionPayload also exposes via the Reactable interface): both
// resolve to the identical [ReactionGroup!] list (confirmed against the
// live schema's AddReactionPayload/RemoveReactionPayload), and the
// top-level field needs one fewer level of selection.
type addReactionResponse struct {
	AddReaction *struct {
		ReactionGroups []reactionGroupFragment `json:"reactionGroups"`
	} `json:"addReaction"`
}

// AddReaction adds content to subjectID (a pull request, issue comment,
// review comment, or review's node ID) via GraphQL's addReaction mutation,
// returning the subject's full, refreshed set of reaction groups (reused
// via reactionGroupFragment/toReactionGroups so the shape matches
// pull_request.graphql's own reactionGroups selection exactly).
func (c *Client) AddReaction(
	ctx context.Context, subjectID string, content model.ReactionContent,
) ([]model.ReactionGroup, error) {
	variables := map[string]any{"subjectId": subjectID, "content": string(content)}
	var resp addReactionResponse
	if err := c.gql.DoWithContext(ctx, addReactionQuery(), variables, &resp); err != nil {
		return nil, classify(err)
	}
	if resp.AddReaction == nil {
		return nil, &Error{Kind: KindUnknown, Message: "addReaction: no payload returned"}
	}
	return toReactionGroups(resp.AddReaction.ReactionGroups), nil
}

// removeReactionResponse is the decoded shape of
// queries/remove_reaction.graphql; see addReactionResponse's doc comment
// for why "reactionGroups" is selected directly on the payload.
type removeReactionResponse struct {
	RemoveReaction *struct {
		ReactionGroups []reactionGroupFragment `json:"reactionGroups"`
	} `json:"removeReaction"`
}

// RemoveReaction removes content from subjectID via GraphQL's
// removeReaction mutation, mirroring AddReaction.
func (c *Client) RemoveReaction(
	ctx context.Context, subjectID string, content model.ReactionContent,
) ([]model.ReactionGroup, error) {
	variables := map[string]any{"subjectId": subjectID, "content": string(content)}
	var resp removeReactionResponse
	if err := c.gql.DoWithContext(ctx, removeReactionQuery(), variables, &resp); err != nil {
		return nil, classify(err)
	}
	if resp.RemoveReaction == nil {
		return nil, &Error{Kind: KindUnknown, Message: "removeReaction: no payload returned"}
	}
	return toReactionGroups(resp.RemoveReaction.ReactionGroups), nil
}
