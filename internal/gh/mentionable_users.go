package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var mentionableUsersQuery = sync.OnceValue(func() string { return mustLoadQuery("mentionable_users.graphql") })

// mentionableUserNode mirrors one node of a "mentionableUsers { nodes {...}
// }" selection.
type mentionableUserNode struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// mentionableUsersResponse is the decoded shape of
// queries/mentionable_users.graphql.
type mentionableUsersResponse struct {
	Repository *struct {
		MentionableUsers *struct {
			Nodes []mentionableUserNode `json:"nodes"`
		} `json:"mentionableUsers"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// MentionableUsers returns repo's mentionable users (GraphQL's
// repository.mentionableUsers connection), used as the store's `@`-mention
// autocomplete candidate source and (M5) the edit-PR form's reviewers
// picker, which needs each user's own node ID (model.User.ID) to add a
// brand-new individual reviewer via RequestReviewers. query filters by
// login/name prefix on GitHub's side; an empty query is sent as an
// explicit null variable (via stringVariable, the same convention
// review.go's AddThreadReply uses for its optional pendingReviewID),
// matching an unfiltered request rather than a literal empty-string
// search. first bounds the page size; gprt does not paginate this
// connection further (see the store's own doc comments for why one page
// is an accepted limitation).
func (c *Client) MentionableUsers(
	ctx context.Context, repo model.RepoRef, query string, first int,
) ([]model.User, model.RateLimit, error) {
	variables := map[string]any{
		"owner": repo.Owner, "name": repo.Name,
		"query": stringVariable(query), "first": first,
	}
	var resp mentionableUsersResponse
	if err := c.gql.DoWithContext(ctx, mentionableUsersQuery(), variables, &resp); err != nil {
		return nil, model.RateLimit{}, classify(err)
	}
	if resp.Repository == nil || resp.Repository.MentionableUsers == nil {
		return nil, resp.RateLimit.toModel(), &Error{Kind: KindUnknown, Message: "mentionableUsers: no repository returned"}
	}
	nodes := resp.Repository.MentionableUsers.Nodes
	users := make([]model.User, 0, len(nodes))
	for _, n := range nodes {
		users = append(users, model.User{ID: n.ID, Login: n.Login, Name: n.Name})
	}
	return users, resp.RateLimit.toModel(), nil
}
