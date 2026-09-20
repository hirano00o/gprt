package gh

import (
	"fmt"
	"strings"

	"github.com/hirano00o/gprt/internal/model"
)

// normalizeNewlines replaces every "\r\n" in s with a bare "\n". GitHub
// passes a pull request's body, comment/review bodies, and commit message
// headlines through verbatim, so a Windows-authored one can carry CRLF line
// endings; gprt's own rendering (word wrap, line-by-line Markdown-lite
// parsing in internal/ui) only ever splits on "\n", which would otherwise
// leave a stray "\r" as a literal, visible control character at the end of
// every such line.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// mapPullRequestDetail maps a decoded pull_request.graphql result into
// model.PullRequest. host becomes the ref's host, mirroring
// mapPullRequest's convention in map.go: the query itself carries only
// "owner/name" for the repository. viewerLogin attributes the viewer's own
// pending review (see mapPendingReview), which the query does not select
// an author for since it is always the caller's own account.
func mapPullRequestDetail(host string, node pullRequestDetailNode, viewerLogin string) model.PullRequest {
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
			ID:             r.ID,
			Author:         model.User{Login: r.Author.Login},
			State:          model.ReviewState(r.State),
			Body:           normalizeNewlines(r.Body),
			SubmittedAt:    r.SubmittedAt,
			ReactionGroups: toReactionGroups(r.ReactionGroups),
			URL:            r.URL,
		})
	}

	checks, rollupState := mapChecks(node.Commits.Nodes)

	threads := make([]model.ReviewThread, 0, len(node.ReviewThreads.Nodes))
	for _, t := range node.ReviewThreads.Nodes {
		threads = append(threads, mapReviewThread(t))
	}

	timeline := make([]model.TimelineItem, 0, len(node.TimelineItems.Nodes))
	for _, ti := range node.TimelineItems.Nodes {
		if item, ok := mapTimelineItem(ti); ok {
			timeline = append(timeline, item)
		}
	}

	return model.PullRequest{
		ID:               node.ID,
		Ref:              ref,
		RepositoryID:     node.Repository.ID,
		Title:            node.Title,
		Body:             normalizeNewlines(node.Body),
		Author:           model.User{Login: node.Author.Login},
		State:            model.PRState(node.State),
		IsDraft:          node.IsDraft,
		ReviewDecision:   model.ReviewDecision(node.ReviewDecision),
		Mergeable:        model.MergeableState(node.Mergeable),
		MergeStateStatus: model.MergeStateStatus(node.MergeStateStatus),
		BaseRefName:      node.BaseRefName,
		HeadRefName:      node.HeadRefName,
		HeadOID:          node.HeadRefOid,
		Additions:        node.Additions,
		Deletions:        node.Deletions,
		ChangedFiles:     node.ChangedFiles,
		Labels:           labels,
		ReviewRequests:   reviewers,
		LatestReviews:    reviews,
		Checks:           checks,
		RollupState:      rollupState,
		Timeline:         timeline,
		ReviewThreads:    threads,
		PendingReview:    mapPendingReview(node.Reviews.Nodes, viewerLogin),
		ReactionGroups:   toReactionGroups(node.ReactionGroups),
		ViewerCanUpdate:  node.ViewerCanUpdate,
		ViewerDidAuthor:  node.ViewerDidAuthor,
		ViewerCanClose:   node.ViewerCanClose,
		ViewerCanReopen:  node.ViewerCanReopen,
		ViewerCanReact:   node.ViewerCanReact,
		CreatedAt:        node.CreatedAt,
		UpdatedAt:        node.UpdatedAt,
		URL:              node.URL,
	}
}

// mapChecks maps the head commit's status-check rollup (read from a
// "commits(last: 1)" selection) into a Check list plus the rollup's own
// overall state. Both are empty/"" when there is no commit or no rollup
// has ever been reported for it.
func mapChecks(nodes []detailCommitNode) ([]model.Check, model.StatusState) {
	if len(nodes) == 0 {
		return nil, ""
	}
	rollup := nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return nil, ""
	}
	checks := make([]model.Check, 0, len(rollup.Contexts.Nodes))
	for _, c := range rollup.Contexts.Nodes {
		checks = append(checks, mapCheck(c))
	}
	return checks, model.StatusState(rollup.State)
}

// mapCheck maps one statusCheckRollup.contexts node into a model.Check,
// branching on TypeName ("CheckRun" or "StatusContext" — see
// checkContextNode's doc comment).
func mapCheck(c checkContextNode) model.Check {
	switch c.TypeName {
	case "CheckRun":
		var workflow string
		if c.CheckSuite.WorkflowRun != nil {
			workflow = c.CheckSuite.WorkflowRun.Workflow.Name
		}
		return model.Check{
			Name:       c.Name,
			Status:     model.CheckStatus(c.Status),
			Conclusion: model.CheckConclusion(c.Conclusion),
			URL:        c.DetailsURL,
			Workflow:   workflow,
			IsRequired: c.IsRequired,
		}
	case "StatusContext":
		status, conclusion := mapLegacyStatusState(c.State)
		return model.Check{
			Name:       c.Context,
			Status:     status,
			Conclusion: conclusion,
			URL:        c.TargetURL,
			IsRequired: c.IsRequired,
		}
	default:
		// Should not occur: the query's union selection has fragments
		// for only CheckRun and StatusContext. Reported as an
		// otherwise-unnamed required/optional check rather than
		// dropped, so a future schema addition to the union degrades
		// visibly instead of silently vanishing from the tally.
		return model.Check{IsRequired: c.IsRequired}
	}
}

// mapLegacyStatusState maps a StatusContext's legacy StatusState
// (ERROR|EXPECTED|FAILURE|PENDING|SUCCESS — the older commit-status API)
// to the (Status, Conclusion) pair Summarize expects. EXPECTED is that
// API's own equivalent of "pending", which is exactly why model.Check
// carries CheckStatusExpected: using CheckStatusPending instead would
// conflate it with the modern API's distinct PENDING status for no
// benefit. ERROR is a completed check, using CheckConclusionError, which
// (like CheckStatusExpected) exists specifically for this legacy case: the
// modern CheckConclusionState enum has no "error" value. PENDING and any
// unrecognised value are treated as CheckStatusPending, matching
// Summarize's "not completed" pending rule.
func mapLegacyStatusState(state string) (model.CheckStatus, model.CheckConclusion) {
	switch model.StatusState(state) {
	case model.StatusStateSuccess:
		return model.CheckStatusCompleted, model.CheckConclusionSuccess
	case model.StatusStateFailure:
		return model.CheckStatusCompleted, model.CheckConclusionFailure
	case model.StatusStateError:
		return model.CheckStatusCompleted, model.CheckConclusionError
	case model.StatusStateExpected:
		return model.CheckStatusExpected, ""
	default: // model.StatusStatePending, or an unrecognised value.
		return model.CheckStatusPending, ""
	}
}

// mapReviewThread maps one reviewThreads node into model.ReviewThread.
func mapReviewThread(t reviewThreadNode) model.ReviewThread {
	comments := make([]model.ReviewComment, 0, len(t.Comments.Nodes))
	for _, c := range t.Comments.Nodes {
		comments = append(comments, mapReviewComment(c))
	}
	return model.ReviewThread{
		ID:                 t.ID,
		Path:               t.Path,
		Line:               t.Line,
		StartLine:          t.StartLine,
		Side:               model.DiffSide(t.DiffSide),
		StartSide:          model.DiffSide(t.StartDiffSide),
		SubjectType:        model.ThreadSubject(t.SubjectType),
		IsResolved:         t.IsResolved,
		IsOutdated:         t.IsOutdated,
		ViewerCanReply:     t.ViewerCanReply,
		ViewerCanResolve:   t.ViewerCanResolve,
		ViewerCanUnresolve: t.ViewerCanUnresolve,
		Comments:           comments,
	}
}

// mapReviewComment maps one review thread comment node into
// model.ReviewComment. ReviewID is left empty for the (unexpected) case
// where pullRequestReview decodes as null.
func mapReviewComment(c reviewCommentNode) model.ReviewComment {
	var reviewID string
	if c.PullRequestReview != nil {
		reviewID = c.PullRequestReview.ID
	}
	return model.ReviewComment{
		ID:              c.ID,
		ReviewID:        reviewID,
		Author:          model.User{Login: c.Author.Login},
		Body:            normalizeNewlines(c.Body),
		CreatedAt:       c.CreatedAt,
		State:           model.ReviewCommentState(c.State),
		ReactionGroups:  toReactionGroups(c.ReactionGroups),
		ViewerCanUpdate: c.ViewerCanUpdate,
		ViewerCanDelete: c.ViewerCanDelete,
		URL:             c.URL,
	}
}

// mapPendingReview maps the "reviews(states: [PENDING])" connection (at
// most one node — see pendingReviewNode's doc comment) into the viewer's
// pending review, or nil when none exists. Author is not selected by the
// query (it is always the caller's own account, passed in as
// viewerLogin), unlike every other Review in this package.
func mapPendingReview(nodes []pendingReviewNode, viewerLogin string) *model.Review {
	if len(nodes) == 0 {
		return nil
	}
	n := nodes[0]
	return &model.Review{
		ID:     n.ID,
		Author: model.User{Login: viewerLogin},
		State:  model.ReviewState(n.State),
		Body:   normalizeNewlines(n.Body),
	}
}

// mapTimelineItem maps one timelineItems node into a model.TimelineItem,
// branching on TypeName. ok is false for a union member the query does not
// select a fragment for (should not occur, since itemTypes: [...] in
// pull_request.graphql restricts the connection to exactly the branches
// handled below): the caller skips it rather than emitting a zero-value
// item.
func mapTimelineItem(n timelineItemNode) (model.TimelineItem, bool) {
	switch n.TypeName {
	case "IssueComment":
		return model.TimelineItem{
			Kind: model.TimelineKindIssueComment,
			IssueComment: &model.IssueComment{
				ID:              n.ID,
				Author:          model.User{Login: n.Author.Login},
				Body:            normalizeNewlines(n.Body),
				CreatedAt:       n.CreatedAt,
				UpdatedAt:       n.UpdatedAt,
				ReactionGroups:  toReactionGroups(n.ReactionGroups),
				ViewerCanUpdate: n.ViewerCanUpdate,
				ViewerCanDelete: n.ViewerCanDelete,
				URL:             n.URL,
			},
		}, true
	case "PullRequestReview":
		return model.TimelineItem{
			Kind: model.TimelineKindReview,
			Review: &model.Review{
				ID:             n.ID,
				Author:         model.User{Login: n.Author.Login},
				State:          model.ReviewState(n.State),
				Body:           normalizeNewlines(n.Body),
				SubmittedAt:    n.SubmittedAt,
				ReactionGroups: toReactionGroups(n.ReactionGroups),
				URL:            n.URL,
			},
		}, true
	case "PullRequestCommit":
		return mapTimelineCommit(n)
	case "ReadyForReviewEvent":
		return timelineEvent(n, "ReadyForReviewEvent", ""), true
	case "ConvertToDraftEvent":
		return timelineEvent(n, "ConvertToDraftEvent", ""), true
	case "MergedEvent":
		return timelineEvent(n, "MergedEvent", ""), true
	case "ClosedEvent":
		return timelineEvent(n, "ClosedEvent", ""), true
	case "ReopenedEvent":
		return timelineEvent(n, "ReopenedEvent", ""), true
	case "ReviewRequestedEvent":
		detail := "requested a review from " + detailReviewerName(n.RequestedReviewer)
		return timelineEvent(n, "ReviewRequestedEvent", detail), true
	case "ReviewRequestRemovedEvent":
		detail := "removed the review request for " + detailReviewerName(n.RequestedReviewer)
		return timelineEvent(n, "ReviewRequestRemovedEvent", detail), true
	case "HeadRefForcePushedEvent":
		var oid string
		if n.AfterCommit != nil {
			oid = n.AfterCommit.AbbreviatedOid
		}
		return timelineEvent(n, "HeadRefForcePushedEvent", "force-pushed to "+oid), true
	case "RenamedTitleEvent":
		detail := fmt.Sprintf("renamed from %q to %q", n.PreviousTitle, n.CurrentTitle)
		return timelineEvent(n, "RenamedTitleEvent", detail), true
	case "LabeledEvent":
		return timelineEvent(n, "LabeledEvent", "added the "+labelName(n.Label)+" label"), true
	case "UnlabeledEvent":
		return timelineEvent(n, "UnlabeledEvent", "removed the "+labelName(n.Label)+" label"), true
	case "BaseRefChangedEvent":
		detail := fmt.Sprintf("changed the base branch from %s to %s", n.PreviousRefName, n.CurrentRefName)
		return timelineEvent(n, "BaseRefChangedEvent", detail), true
	default:
		return model.TimelineItem{}, false
	}
}

// mapTimelineCommit maps a PullRequestCommit timeline node into a
// model.Commit item. ok is false when Commit itself decoded as null
// (should not occur; guards against a nil dereference if it ever does).
func mapTimelineCommit(n timelineItemNode) (model.TimelineItem, bool) {
	if n.Commit == nil {
		return model.TimelineItem{}, false
	}
	author := model.User{Name: n.Commit.Author.Name}
	if n.Commit.Author.User != nil {
		author.Login = n.Commit.Author.User.Login
	}
	return model.TimelineItem{
		Kind: model.TimelineKindCommit,
		Commit: &model.Commit{
			OID:         n.Commit.OID,
			Message:     normalizeNewlines(n.Commit.MessageHeadline),
			Author:      author,
			CommittedAt: n.Commit.CommittedDate,
		},
	}, true
}

// timelineEvent builds a model.TimelineItem wrapping a model.Event. Type
// is the GraphQL __typename verbatim (for example "ReviewRequestedEvent"):
// an unambiguous, already-stable identifier, rather than inventing a
// second naming scheme for the same set of event kinds.
func timelineEvent(n timelineItemNode, typeName, detail string) model.TimelineItem {
	return model.TimelineItem{
		Kind: model.TimelineKindEvent,
		Event: &model.Event{
			Type:   typeName,
			Actor:  model.User{Login: n.Actor.Login},
			At:     n.CreatedAt,
			Detail: detail,
		},
	}
}

// labelName reads a possibly-null label fragment's name, returning "" for
// null (a label deleted after the event was recorded).
func labelName(l *struct {
	Name string `json:"name"`
},
) string {
	if l == nil {
		return ""
	}
	return l.Name
}

// detailReviewerName renders a ReviewRequestedEvent/ReviewRequestRemovedEvent's
// requestedReviewer (User or Team only — see pull_request.graphql, which
// unlike reviewRequests' own union selects no Bot/Mannequin fragment for
// these two event types) as a display name, or "(removed)" for a null
// union (a deleted account or team) or an unselected type.
func detailReviewerName(r *requestedReviewerFragment) string {
	if r == nil {
		return "(removed)"
	}
	switch r.TypeName {
	case "User":
		return r.Login
	case "Team":
		return r.Slug
	default:
		return "(removed)"
	}
}
