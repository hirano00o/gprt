package gh

import (
	"context"
	"embed"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

//go:embed queries/*.graphql
var queriesFS embed.FS

// mustLoadQuery reads and returns the embedded GraphQL document at
// "queries/<name>", panicking if it is missing: that can only happen if the
// embed directive and a method's file name have drifted apart, which is a
// programming error caught at first use in tests, never a runtime
// condition a caller could recover from.
func mustLoadQuery(name string) string {
	data, err := queriesFS.ReadFile("queries/" + name)
	if err != nil {
		panic("gh: missing embedded query " + name + ": " + err.Error())
	}
	return string(data)
}

var viewerQuery = sync.OnceValue(func() string {
	return mustLoadQuery("viewer.graphql")
})

// viewerResponse is the decoded shape of queries/viewer.graphql.
type viewerResponse struct {
	Viewer struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	} `json:"viewer"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// Viewer returns the authenticated user and the current GraphQL rate limit.
func (c *Client) Viewer(ctx context.Context) (model.User, model.RateLimit, error) {
	var resp viewerResponse
	if err := c.gql.DoWithContext(ctx, viewerQuery(), nil, &resp); err != nil {
		return model.User{}, model.RateLimit{}, classify(err)
	}

	user := model.User{Login: resp.Viewer.Login, Name: resp.Viewer.Name}
	return user, resp.RateLimit.toModel(), nil
}
