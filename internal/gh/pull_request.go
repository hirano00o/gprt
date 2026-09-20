package gh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/hirano00o/gprt/internal/model"
)

var pullRequestQuery = sync.OnceValue(func() string {
	return mustLoadQuery("pull_request.graphql")
})

// maxDetailExtraPages bounds how many additional pages PullRequest fetches
// for each of its three independently paginated connections (review
// threads, timeline items, status-check contexts) beyond the first page
// already included in the initial response. A pull request pathological
// enough to need more than that is truncated rather than fetched without
// bound; the truncation is reported in DetailResult.Warnings.
const maxDetailExtraPages = 5

// pageInfoFragment mirrors the "pageInfo { hasNextPage endCursor }"
// selection shared by every paginated connection in pull_request.graphql.
type pageInfoFragment struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// reactionGroupFragment mirrors one node of a "reactionGroups" selection.
type reactionGroupFragment struct {
	Content          string `json:"content"`
	ViewerHasReacted bool   `json:"viewerHasReacted"`
	Reactors         struct {
		TotalCount int `json:"totalCount"`
	} `json:"reactors"`
}

// toReactionGroups maps a decoded "reactionGroups" selection into model
// form. Count comes from the group's reactor count, not a field named
// "count": the query selects "reactors { totalCount }" rather than a
// deprecated "count"-shaped field, since reactors can be users, bots,
// mannequins, or organizations.
func toReactionGroups(fragments []reactionGroupFragment) []model.ReactionGroup {
	groups := make([]model.ReactionGroup, 0, len(fragments))
	for _, f := range fragments {
		groups = append(groups, model.ReactionGroup{
			Content:          model.ReactionContent(f.Content),
			Count:            f.Reactors.TotalCount,
			ViewerHasReacted: f.ViewerHasReacted,
		})
	}
	return groups
}

// pullRequestDetailResponse is the decoded shape of queries/pull_request.graphql.
type pullRequestDetailResponse struct {
	Repository *struct {
		PullRequest *pullRequestDetailNode `json:"pullRequest"`
	} `json:"repository"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// pullRequestDetailNode is the "pullRequest" field of pullRequestDetailResponse.
type pullRequestDetailNode struct {
	ID               string `json:"id"`
	Number           int    `json:"number"`
	Title            string `json:"title"`
	Body             string `json:"body"`
	State            string `json:"state"`
	IsDraft          bool   `json:"isDraft"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
	ReviewDecision   string `json:"reviewDecision"`
	Author           struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		ID            string `json:"id"`
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	BaseRefName     string                  `json:"baseRefName"`
	HeadRefName     string                  `json:"headRefName"`
	HeadRefOid      string                  `json:"headRefOid"`
	Additions       int                     `json:"additions"`
	Deletions       int                     `json:"deletions"`
	ChangedFiles    int                     `json:"changedFiles"`
	CreatedAt       time.Time               `json:"createdAt"`
	UpdatedAt       time.Time               `json:"updatedAt"`
	URL             string                  `json:"url"`
	ViewerCanUpdate bool                    `json:"viewerCanUpdate"`
	ViewerDidAuthor bool                    `json:"viewerDidAuthor"`
	ViewerCanClose  bool                    `json:"viewerCanClose"`
	ViewerCanReopen bool                    `json:"viewerCanReopen"`
	ViewerCanReact  bool                    `json:"viewerCanReact"`
	ReactionGroups  []reactionGroupFragment `json:"reactionGroups"`
	Labels          struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []reviewRequestNode `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []pullRequestReviewNode `json:"nodes"`
	} `json:"latestReviews"`
	Commits struct {
		Nodes []detailCommitNode `json:"nodes"`
	} `json:"commits"`
	ReviewThreads reviewThreadConnection `json:"reviewThreads"`
	TimelineItems timelineConnection     `json:"timelineItems"`
	Reviews       struct {
		Nodes []pendingReviewNode `json:"nodes"`
	} `json:"reviews"`
}

// pullRequestReviewNode mirrors one node of pull_request.graphql's
// "latestReviews" connection. Unlike search's latestReviewNode, this
// includes Body and URL, which the detail query selects but search does
// not.
type pullRequestReviewNode struct {
	ID     string `json:"id"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State          string                  `json:"state"`
	Body           string                  `json:"body"`
	SubmittedAt    time.Time               `json:"submittedAt"`
	URL            string                  `json:"url"`
	ReactionGroups []reactionGroupFragment `json:"reactionGroups"`
}

// pendingReviewNode mirrors one node of the "reviews(states: [PENDING])"
// connection, used to discover the viewer's own pending review: GitHub has
// no dedicated field for it, so it must be found by author+state filter.
type pendingReviewNode struct {
	ID    string `json:"id"`
	Body  string `json:"body"`
	State string `json:"state"`
}

// statusCheckRollupFragment mirrors a "statusCheckRollup" selection.
type statusCheckRollupFragment struct {
	State    string `json:"state"`
	Contexts struct {
		PageInfo pageInfoFragment   `json:"pageInfo"`
		Nodes    []checkContextNode `json:"nodes"`
	} `json:"contexts"`
}

// detailCommitNode mirrors one node of the "commits(last: 1)" connection in
// pull_request.graphql, used only to read the head commit's status-check
// rollup and its (possibly paginated) contexts.
type detailCommitNode struct {
	Commit struct {
		OID               string                     `json:"oid"`
		StatusCheckRollup *statusCheckRollupFragment `json:"statusCheckRollup"`
	} `json:"commit"`
}

// checkContextNode mirrors one node of a "statusCheckRollup.contexts"
// selection: the union of the "... on CheckRun" and "... on StatusContext"
// fragments pull_request.graphql selects, discriminated by TypeName. Each
// concrete type's JSON object only ever carries the keys its own fragment
// selected, so a field specific to the other branch simply decodes to its
// zero value rather than colliding (for example a CheckRun node has no
// "context" key at all, leaving checkContextNode.Context "").
type checkContextNode struct {
	TypeName string `json:"__typename"`

	// CheckRun
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	CheckSuite struct {
		WorkflowRun *struct {
			Workflow struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`

	// StatusContext
	Context     string `json:"context"`
	State       string `json:"state"`
	TargetURL   string `json:"targetUrl"`
	Description string `json:"description"`

	// Shared: both CheckRun and StatusContext select
	// isRequired(pullRequestNumber: $number).
	IsRequired bool `json:"isRequired"`
}

// reviewThreadConnection mirrors the "reviewThreads" field.
type reviewThreadConnection struct {
	PageInfo pageInfoFragment   `json:"pageInfo"`
	Nodes    []reviewThreadNode `json:"nodes"`
}

// reviewThreadNode mirrors one node of the "reviewThreads" connection. Line
// and StartLine decode as plain ints (not pointers): GraphQL sends JSON
// null for a file-level thread's omitted line numbers, and encoding/json
// leaves a non-pointer int field at its zero value on a null, which is
// exactly what a file-subject thread should read as (SubjectType already
// distinguishes FILE from LINE, so there is no ambiguity with a real line
// 0, which cannot occur).
type reviewThreadNode struct {
	ID                 string `json:"id"`
	IsResolved         bool   `json:"isResolved"`
	IsOutdated         bool   `json:"isOutdated"`
	Path               string `json:"path"`
	Line               int    `json:"line"`
	StartLine          int    `json:"startLine"`
	DiffSide           string `json:"diffSide"`
	StartDiffSide      string `json:"startDiffSide"`
	SubjectType        string `json:"subjectType"`
	ViewerCanReply     bool   `json:"viewerCanReply"`
	ViewerCanResolve   bool   `json:"viewerCanResolve"`
	ViewerCanUnresolve bool   `json:"viewerCanUnresolve"`
	Comments           struct {
		Nodes []reviewCommentNode `json:"nodes"`
	} `json:"comments"`
}

// reviewCommentNode mirrors one node of a review thread's "comments"
// connection.
type reviewCommentNode struct {
	ID     string `json:"id"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body              string                  `json:"body"`
	CreatedAt         time.Time               `json:"createdAt"`
	State             string                  `json:"state"`
	URL               string                  `json:"url"`
	ReactionGroups    []reactionGroupFragment `json:"reactionGroups"`
	ViewerCanUpdate   bool                    `json:"viewerCanUpdate"`
	ViewerCanDelete   bool                    `json:"viewerCanDelete"`
	PullRequestReview *struct {
		ID string `json:"id"`
	} `json:"pullRequestReview"`
}

// timelineConnection mirrors the "timelineItems" field.
type timelineConnection struct {
	PageInfo pageInfoFragment   `json:"pageInfo"`
	Nodes    []timelineItemNode `json:"nodes"`
}

// timelineItemNode mirrors one node of the "timelineItems" connection: the
// union of every "... on <Type>" fragment pull_request.graphql selects,
// discriminated by TypeName. As with checkContextNode, each concrete
// type's JSON object only ever carries the keys its own fragment selected,
// so fields specific to other branches simply decode to their zero value.
type timelineItemNode struct {
	TypeName string `json:"__typename"`

	// IssueComment, PullRequestReview
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

	// PullRequestReview
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt"`

	// PullRequestCommit
	Commit *struct {
		OID             string `json:"oid"`
		MessageHeadline string `json:"messageHeadline"`
		Author          struct {
			User *struct {
				Login string `json:"login"`
			} `json:"user"`
			Name string `json:"name"`
		} `json:"author"`
		CommittedDate time.Time `json:"committedDate"`
	} `json:"commit"`

	// Every event branch
	Actor struct {
		Login string `json:"login"`
	} `json:"actor"`

	// ReviewRequestedEvent, ReviewRequestRemovedEvent. Reuses
	// requestedReviewerFragment from map.go: the event's union selects
	// only User and Team fragments (see pull_request.graphql), which is
	// a strict subset of what that fragment already decodes.
	RequestedReviewer *requestedReviewerFragment `json:"requestedReviewer"`

	// HeadRefForcePushedEvent
	AfterCommit *struct {
		AbbreviatedOid string `json:"abbreviatedOid"`
	} `json:"afterCommit"`

	// RenamedTitleEvent
	PreviousTitle string `json:"previousTitle"`
	CurrentTitle  string `json:"currentTitle"`

	// LabeledEvent, UnlabeledEvent
	Label *struct {
		Name string `json:"name"`
	} `json:"label"`

	// BaseRefChangedEvent
	PreviousRefName string `json:"previousRefName"`
	CurrentRefName  string `json:"currentRefName"`
}

// DetailResult is the full detail of one pull request, as returned by
// Client.PullRequest.
type DetailResult struct {
	PR model.PullRequest
	// Warnings holds human-readable notes about data that could not be
	// fully resolved: a node-scoped GraphQL error whose affected node was
	// still kept, and/or a note that one of the paginated connections
	// (review threads, timeline items, status-check contexts) was
	// truncated at maxDetailExtraPages. An empty slice means everything
	// decoded and paginated cleanly.
	Warnings  []string
	RateLimit model.RateLimit
}

// PullRequest fetches the full detail of one pull request: metadata,
// checks, review threads, and conversation timeline. viewerLogin is the
// authenticated user's login, used both to discover their own pending
// review (reviews(author: viewerLogin, states: [PENDING]) — GitHub has no
// dedicated field for "my pending review") and to attribute it in the
// returned model.
//
// Review threads, timeline items, and status-check contexts are each
// paginated independently. PullRequest follows every "hasNextPage" by
// re-issuing the same query with that connection's cursor advanced (the
// non-paginated fields are simply re-read each time — kept simple rather
// than clever), capping each connection at maxDetailExtraPages additional
// pages and noting any truncation in DetailResult.Warnings rather than
// fetching without bound. A connection that has already stopped
// (exhausted or capped) has its returned page discarded on every later
// round: its cursor variable is deliberately left unadvanced once capped,
// so re-merging its result would duplicate already-held items.
func (c *Client) PullRequest(ctx context.Context, ref model.PRRef, viewerLogin string) (DetailResult, error) {
	variables := map[string]any{
		"owner":          ref.Repo.Owner,
		"name":           ref.Repo.Name,
		"number":         ref.Number,
		"viewer":         viewerLogin,
		"checksCursor":   nil,
		"threadsCursor":  nil,
		"timelineCursor": nil,
	}

	var node *pullRequestDetailNode
	var rl model.RateLimit
	var warnings []string

	threadsDone, timelineDone, checksDone := false, false, false
	threadsExtra, timelineExtra, checksExtra := 0, 0, 0

	for {
		threadsActive := !threadsDone
		timelineActive := !timelineDone
		checksActive := !checksDone

		var resp pullRequestDetailResponse
		fetchErr := c.gql.DoWithContext(ctx, pullRequestQuery(), variables, &resp)

		var gqlErr *api.GraphQLError
		hadErr := fetchErr != nil
		if hadErr {
			if !errors.As(fetchErr, &gqlErr) || !allDetailScoped(gqlErr.Errors) {
				return DetailResult{}, classify(fetchErr)
			}
			warnings = append(warnings, warningMessages(gqlErr.Errors)...)
		}

		if resp.Repository == nil || resp.Repository.PullRequest == nil {
			if hadErr {
				// pullRequest was nulled out by a propagated error (a
				// non-null child resolver failing somewhere under it,
				// or the pull request genuinely not resolving) rather
				// than decoding as null with a clean response: classify
				// the real error instead of fabricating NotFound, which
				// would otherwise misreport, say, a timeout or a
				// permission error on one nested field as "this pull
				// request does not exist". classify already maps a
				// NOT_FOUND-typed item to KindNotFound, so a genuine
				// not-found is still reported correctly.
				return DetailResult{}, classify(fetchErr)
			}
			return DetailResult{}, &Error{Kind: KindNotFound, Message: "pull request not found"}
		}
		rl = resp.RateLimit.toModel()
		page := resp.Repository.PullRequest

		switch node {
		case nil:
			node = page
		default:
			if threadsActive {
				node.ReviewThreads.Nodes = append(node.ReviewThreads.Nodes, page.ReviewThreads.Nodes...)
				node.ReviewThreads.PageInfo = page.ReviewThreads.PageInfo
			}
			if timelineActive {
				node.TimelineItems.Nodes = append(node.TimelineItems.Nodes, page.TimelineItems.Nodes...)
				node.TimelineItems.PageInfo = page.TimelineItems.PageInfo
			}
			if checksActive {
				mergeChecksPage(node, page)
			}
		}

		more := false

		if threadsActive && node.ReviewThreads.PageInfo.HasNextPage {
			if threadsExtra >= maxDetailExtraPages {
				threadsDone = true
				warnings = append(warnings, "review threads truncated after reaching the pagination limit")
			} else {
				threadsExtra++
				variables["threadsCursor"] = node.ReviewThreads.PageInfo.EndCursor
				more = true
			}
		} else if threadsActive {
			threadsDone = true
		}

		if timelineActive && node.TimelineItems.PageInfo.HasNextPage {
			if timelineExtra >= maxDetailExtraPages {
				timelineDone = true
				warnings = append(warnings, "timeline truncated after reaching the pagination limit")
			} else {
				timelineExtra++
				variables["timelineCursor"] = node.TimelineItems.PageInfo.EndCursor
				more = true
			}
		} else if timelineActive {
			timelineDone = true
		}

		if rollup := detailChecksRollup(node); checksActive && rollup != nil && rollup.Contexts.PageInfo.HasNextPage {
			if checksExtra >= maxDetailExtraPages {
				checksDone = true
				warnings = append(warnings, "checks truncated after reaching the pagination limit")
			} else {
				checksExtra++
				variables["checksCursor"] = rollup.Contexts.PageInfo.EndCursor
				more = true
			}
		} else if checksActive {
			checksDone = true
		}

		if !more {
			break
		}
	}

	if threads, skipped := filterEmptyReviewThreads(node.ReviewThreads.Nodes); skipped > 0 {
		node.ReviewThreads.Nodes = threads
		warnings = append(warnings, fmt.Sprintf("%d review thread(s) omitted (could not be resolved)", skipped))
	}
	if items, skipped := filterEmptyTimelineItems(node.TimelineItems.Nodes); skipped > 0 {
		node.TimelineItems.Nodes = items
		warnings = append(warnings, fmt.Sprintf("%d timeline item(s) omitted (could not be resolved)", skipped))
	}
	if rollup := detailChecksRollup(node); rollup != nil {
		if contexts, skipped := filterEmptyCheckContexts(rollup.Contexts.Nodes); skipped > 0 {
			rollup.Contexts.Nodes = contexts
			warnings = append(warnings, fmt.Sprintf("%d check(s) omitted (could not be resolved)", skipped))
		}
	}

	return DetailResult{
		PR: mapPullRequestDetail(c.host, *node, viewerLogin),
		// Deduplicated (order-preserving): the same node-scoped GraphQL
		// error is re-reported by every subsequent pagination round (the
		// non-paginated fields, including the one it is scoped to, are
		// simply re-read each call — see the doc comment above), so
		// without this a single standing problem would appear as one
		// warning per round instead of once.
		Warnings:  dedupWarnings(warnings),
		RateLimit: rl,
	}, nil
}

// dedupWarnings returns warnings with duplicate entries removed, keeping
// the first occurrence of each (order-preserving), mirroring
// internal/store's appendNewWarnings convention for the same problem.
func dedupWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return warnings
	}
	seen := make(map[string]struct{}, len(warnings))
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// filterEmptyReviewThreads drops review thread nodes nulled out by
// GraphQL error propagation (a null array element decodes as a
// reviewThreadNode with every field, including ID, at its zero value —
// the same phenomenon SearchPullRequests guards against for search result
// nodes), returning the filtered slice and how many were dropped.
func filterEmptyReviewThreads(nodes []reviewThreadNode) ([]reviewThreadNode, int) {
	kept := make([]reviewThreadNode, 0, len(nodes))
	skipped := 0
	for _, n := range nodes {
		if n.ID == "" {
			skipped++
			continue
		}
		kept = append(kept, n)
	}
	return kept, skipped
}

// filterEmptyCheckContexts drops status-check context nodes nulled out by
// GraphQL error propagation (a null array element decodes as a
// checkContextNode with every field, including TypeName, at its zero
// value), returning the filtered slice and how many were dropped.
func filterEmptyCheckContexts(nodes []checkContextNode) ([]checkContextNode, int) {
	kept := make([]checkContextNode, 0, len(nodes))
	skipped := 0
	for _, n := range nodes {
		if n.TypeName == "" {
			skipped++
			continue
		}
		kept = append(kept, n)
	}
	return kept, skipped
}

// filterEmptyTimelineItems drops timeline item nodes nulled out by GraphQL
// error propagation (a null array element decodes as a timelineItemNode
// with every field, including TypeName, at its zero value), returning the
// filtered slice and how many were dropped. TypeName is a reliable phantom
// indicator here (unlike ID, which several legitimate branches — every
// event type — never select at all, so it is always "" for them too):
// filtering here means mapTimelineItem's own ok-based skip for an
// unrecognised TypeName is only ever reached defensively, for a union
// member the query truly has no fragment for, not for this expected case.
func filterEmptyTimelineItems(nodes []timelineItemNode) ([]timelineItemNode, int) {
	kept := make([]timelineItemNode, 0, len(nodes))
	skipped := 0
	for _, n := range nodes {
		if n.TypeName == "" {
			skipped++
			continue
		}
		kept = append(kept, n)
	}
	return kept, skipped
}

// detailChecksRollup returns node's head commit status-check rollup, or nil
// when there is no commit or its rollup has never been reported.
func detailChecksRollup(node *pullRequestDetailNode) *statusCheckRollupFragment {
	if len(node.Commits.Nodes) == 0 {
		return nil
	}
	return node.Commits.Nodes[0].Commit.StatusCheckRollup
}

// mergeChecksPage appends a later page's status-check contexts (and adopts
// its pageInfo) onto node's, when both node and page have a rollup to
// merge.
func mergeChecksPage(node, page *pullRequestDetailNode) {
	rollup := detailChecksRollup(node)
	pageRollup := detailChecksRollup(page)
	if rollup == nil || pageRollup == nil {
		return
	}
	rollup.Contexts.Nodes = append(rollup.Contexts.Nodes, pageRollup.Contexts.Nodes...)
	rollup.Contexts.PageInfo = pageRollup.Contexts.PageInfo
}

// allDetailScoped reports whether every item is scoped under
// ["repository","pullRequest", ...] — the root of pull_request.graphql's
// selection. A deeper failure (for example one reviewer inside
// reviewThreads) is recoverable the same way search's node-scoped errors
// are (see SearchPullRequests), but a failure scoped anywhere outside that
// root (for example the repository lookup itself) is not.
func allDetailScoped(items []api.GraphQLErrorItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !isDetailScopedPath(item.Path) {
			return false
		}
	}
	return true
}

// isDetailScopedPath reports whether path is rooted at
// ["repository","pullRequest", ...].
func isDetailScopedPath(path []any) bool {
	if len(path) < 2 {
		return false
	}
	first, ok := path[0].(string)
	if !ok || first != "repository" {
		return false
	}
	second, ok := path[1].(string)
	return ok && second == "pullRequest"
}
