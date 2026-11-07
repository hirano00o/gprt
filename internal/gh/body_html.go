package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var bodyHTMLQuery = sync.OnceValue(func() string { return mustLoadQuery("body_html.graphql") })

// bodyHTMLChunk is how many node IDs one request asks for, GraphQL's
// documented maximum for the nodes field.
const bodyHTMLChunk = 100

// bodyHTMLResponse is the decoded shape of queries/body_html.graphql. A
// node is null when its ID is unknown (deleted, or not visible), and an
// object of another type has no fields at all.
type bodyHTMLResponse struct {
	Nodes []*struct {
		ID       string `json:"id"`
		BodyHTML string `json:"bodyHTML"`
	} `json:"nodes"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// BodyHTML returns GitHub's rendered HTML for the body of each given node
// (a pull request, issue comment, review, or review comment), keyed by node
// ID; unknown IDs are simply absent. The HTML is the only place GitHub
// exposes an image URL that can be downloaded without credentials (see
// package inlineimg). IDs are requested in chunks of 100.
func (c *Client) BodyHTML(ctx context.Context, ids []string) (map[string]string, model.RateLimit, error) {
	out := make(map[string]string, len(ids))
	var rl model.RateLimit
	for start := 0; start < len(ids); start += bodyHTMLChunk {
		chunk := ids[start:min(start+bodyHTMLChunk, len(ids))]
		var resp bodyHTMLResponse
		if err := c.gql.DoWithContext(ctx, bodyHTMLQuery(), map[string]any{"ids": chunk}, &resp); err != nil {
			return nil, model.RateLimit{}, classify(err)
		}
		for _, n := range resp.Nodes {
			if n != nil && n.ID != "" {
				out[n.ID] = n.BodyHTML
			}
		}
		rl = resp.RateLimit.toModel()
	}
	return out, rl, nil
}
