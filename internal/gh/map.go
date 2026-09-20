package gh

import (
	"strings"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

// rateLimitFragment mirrors the "rateLimit { remaining resetAt }" selection
// every embedded query includes, so every client method can report the
// caller's remaining GraphQL budget alongside its result. It is decoded as
// a pointer (see the response structs' RateLimit field) because the
// GraphQL field itself is nullable: some GHES instances report no rate
// limit at all when rate limiting is turned off, in which case the whole
// "rateLimit" JSON value is null rather than an object with zero fields.
type rateLimitFragment struct {
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

// toModel maps f to a model.RateLimit. A nil f (the field was null) maps
// to the zero value with Known left false, distinguishing "the server
// reported zero remaining requests" from "the server did not report a
// rate limit at all".
func (f *rateLimitFragment) toModel() model.RateLimit {
	if f == nil {
		return model.RateLimit{}
	}
	return model.RateLimit{Remaining: f.Remaining, ResetAt: f.ResetAt, Known: true}
}

// labelNode mirrors one node of a pull request's "labels" connection. ID
// is selected only by pull_request.graphql (not search.graphql), so a
// label mapped from a search result decodes with ID left empty — see
// model.Label's own doc comment for why only the detail query needs it.
type labelNode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// requestedReviewerFragment mirrors the "requestedReviewer" union on a
// review request: __typename discriminates which of the other fields is
// meaningful. Login carries the login for User, Bot, and Mannequin; Slug
// carries the slug for Team; ID carries the node ID for User and Team only
// (selected only by pull_request.graphql's reviewRequests connection, not
// search.graphql or the timeline events' own requestedReviewer selections,
// none of which need it — see model.Reviewer's own doc comment for why
// only that one connection does). EnterpriseTeam is deliberately not given
// a matching inline fragment in the query (see queries/search.graphql), so
// it always decodes with Slug (and Login, ID) empty even though __typename
// still correctly reports "EnterpriseTeam". A removed (null) reviewer
// decodes with an empty TypeName and every other field zero too.
type requestedReviewerFragment struct {
	TypeName string `json:"__typename"`
	ID       string `json:"id"`
	Login    string `json:"login"`
	Slug     string `json:"slug"`
}

// reviewRequestNode mirrors one node of a pull request's "reviewRequests"
// connection.
type reviewRequestNode struct {
	AsCodeOwner       bool                      `json:"asCodeOwner"`
	RequestedReviewer requestedReviewerFragment `json:"requestedReviewer"`
}

// latestReviewNode mirrors one node of a pull request's "latestReviews"
// connection.
type latestReviewNode struct {
	ID     string `json:"id"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// commitNode mirrors one node of a pull request's "commits(last: 1)"
// connection, used only to read the head commit's status-check rollup.
type commitNode struct {
	Commit struct {
		StatusCheckRollup *struct {
			State string `json:"state"`
		} `json:"statusCheckRollup"`
	} `json:"commit"`
}

// searchPullRequestNode is the "... on PullRequest" fragment of a search
// result node, decoded from GraphQL JSON before mapPullRequest maps it into
// model.PullRequest.
type searchPullRequestNode struct {
	ID             string `json:"id"`
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		ID            string `json:"id"`
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	BaseRefName     string    `json:"baseRefName"`
	HeadRefName     string    `json:"headRefName"`
	HeadRefOid      string    `json:"headRefOid"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	URL             string    `json:"url"`
	Additions       int       `json:"additions"`
	Deletions       int       `json:"deletions"`
	ChangedFiles    int       `json:"changedFiles"`
	ViewerDidAuthor bool      `json:"viewerDidAuthor"`
	Labels          struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []reviewRequestNode `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []latestReviewNode `json:"nodes"`
	} `json:"latestReviews"`
	Commits struct {
		Nodes []commitNode `json:"nodes"`
	} `json:"commits"`
}

// mapPullRequest maps a decoded search result node into model.PullRequest.
// host becomes the RepoRef's host, since the search result itself carries
// only "owner/name". Checks stays empty: it is populated only by the (not
// yet implemented) PR detail query; RollupState carries the head commit's
// rollup so the list icon has something to show before that detail query
// exists.
func mapPullRequest(host string, node searchPullRequestNode) model.PullRequest {
	owner, name, _ := strings.Cut(node.Repository.NameWithOwner, "/")
	ref := model.PRRef{
		Repo:   model.RepoRef{Host: host, Owner: owner, Name: name},
		Number: node.Number,
	}

	labels := make([]model.Label, 0, len(node.Labels.Nodes))
	for _, l := range node.Labels.Nodes {
		labels = append(labels, model.Label{ID: l.ID, Name: l.Name, Color: l.Color})
	}

	reviewers := make([]model.Reviewer, 0, len(node.ReviewRequests.Nodes))
	for _, rr := range node.ReviewRequests.Nodes {
		reviewers = append(reviewers, mapReviewer(rr))
	}

	reviews := make([]model.Review, 0, len(node.LatestReviews.Nodes))
	for _, r := range node.LatestReviews.Nodes {
		reviews = append(reviews, model.Review{
			ID:          r.ID,
			Author:      model.User{Login: r.Author.Login},
			State:       model.ReviewState(r.State),
			SubmittedAt: r.SubmittedAt,
		})
	}

	return model.PullRequest{
		ID:              node.ID,
		Ref:             ref,
		RepositoryID:    node.Repository.ID,
		Title:           node.Title,
		Author:          model.User{Login: node.Author.Login},
		State:           model.PRState(node.State),
		IsDraft:         node.IsDraft,
		ReviewDecision:  model.ReviewDecision(node.ReviewDecision),
		BaseRefName:     node.BaseRefName,
		HeadRefName:     node.HeadRefName,
		HeadOID:         node.HeadRefOid,
		Additions:       node.Additions,
		Deletions:       node.Deletions,
		ChangedFiles:    node.ChangedFiles,
		Labels:          labels,
		ReviewRequests:  reviewers,
		LatestReviews:   reviews,
		RollupState:     mapRollupState(node.Commits.Nodes),
		ViewerDidAuthor: node.ViewerDidAuthor,
		CreatedAt:       node.CreatedAt,
		UpdatedAt:       node.UpdatedAt,
		URL:             node.URL,
	}
}

// mapReviewer maps one reviewRequests node's union field into a
// model.Reviewer. Only User and Team map to ReviewerKindUser/Team; Bot and
// Mannequin still keep a display name (Login) but map to ReviewerKindOther,
// same as EnterpriseTeam (which has no name available — see its case
// below) and a removed (null) reviewer, rather than being dropped
// entirely — a review request that silently vanished from the list would
// be a worse failure mode than an unstyled row.
func mapReviewer(rr reviewRequestNode) model.Reviewer {
	switch rr.RequestedReviewer.TypeName {
	case "User":
		return model.Reviewer{
			ID:          rr.RequestedReviewer.ID,
			Login:       rr.RequestedReviewer.Login,
			Kind:        model.ReviewerKindUser,
			AsCodeOwner: rr.AsCodeOwner,
		}
	case "Team":
		return model.Reviewer{
			ID:          rr.RequestedReviewer.ID,
			Login:       rr.RequestedReviewer.Slug,
			Kind:        model.ReviewerKindTeam,
			AsCodeOwner: rr.AsCodeOwner,
		}
	case "Bot", "Mannequin":
		return model.Reviewer{
			Login:       rr.RequestedReviewer.Login,
			Kind:        model.ReviewerKindOther,
			AsCodeOwner: rr.AsCodeOwner,
		}
	case "EnterpriseTeam":
		// No name is available: unlike Team, the query does not select
		// an inline fragment's fields for EnterpriseTeam (see
		// queries/search.graphql for why), so there is no slug to read.
		// __typename alone still identifies it, kept as its own case
		// rather than falling through to default for that reason.
		return model.Reviewer{Kind: model.ReviewerKindOther, AsCodeOwner: rr.AsCodeOwner}
	default:
		return model.Reviewer{Kind: model.ReviewerKindOther, AsCodeOwner: rr.AsCodeOwner}
	}
}

// mapRollupState reads the head commit's status-check rollup from a
// "commits(last: 1)" selection. It returns "" when there is no commit (should
// not happen) or no rollup has ever been reported for it.
func mapRollupState(nodes []commitNode) model.StatusState {
	if len(nodes) == 0 {
		return ""
	}
	rollup := nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return ""
	}
	return model.StatusState(rollup.State)
}
