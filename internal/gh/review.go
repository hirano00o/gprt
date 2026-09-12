package gh

import (
	"context"
	"sync"

	"github.com/hirano00o/gprt/internal/model"
)

var (
	createPendingReviewQuery = sync.OnceValue(func() string { return mustLoadQuery("create_pending_review.graphql") })
	addReviewNowQuery        = sync.OnceValue(func() string { return mustLoadQuery("add_review_now.graphql") })
	addReviewThreadQuery     = sync.OnceValue(func() string { return mustLoadQuery("add_review_thread.graphql") })
	addThreadReplyQuery      = sync.OnceValue(func() string { return mustLoadQuery("add_thread_reply.graphql") })
	submitReviewQuery        = sync.OnceValue(func() string { return mustLoadQuery("submit_review.graphql") })
	deletePendingReviewQuery = sync.OnceValue(func() string { return mustLoadQuery("delete_pending_review.graphql") })
	updateReviewCommentQuery = sync.OnceValue(func() string { return mustLoadQuery("update_review_comment.graphql") })
	deleteReviewCommentQuery = sync.OnceValue(func() string { return mustLoadQuery("delete_review_comment.graphql") })
	resolveThreadQuery       = sync.OnceValue(func() string { return mustLoadQuery("resolve_thread.graphql") })
	unresolveThreadQuery     = sync.OnceValue(func() string { return mustLoadQuery("unresolve_thread.graphql") })
)

// intVariable returns nil for a zero n, so an optional "Int" GraphQL
// variable is sent as null (matching a genuinely absent line number - a
// file-level thread's omitted line/startLine, or a single-line thread's
// omitted startLine) rather than the number 0, which can never be a real
// line number. Mirrors cursorVariable in search.go.
func intVariable(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// sideVariable returns nil for an empty side, for the same reason
// intVariable returns nil for a zero line: a file-level thread selects no
// side at all.
func sideVariable(side model.DiffSide) any {
	if side == "" {
		return nil
	}
	return string(side)
}

// stringVariable returns nil for an empty s, so an optional "ID" GraphQL
// variable (a reply's pullRequestReviewId, sent only when replying while a
// pending review exists) is sent as null rather than an empty string,
// which GitHub would reject as an invalid ID.
func stringVariable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// reviewNode mirrors the minimal "pullRequestReview { id state body }"
// selection shared by create_pending_review.graphql, add_review_now.graphql,
// and submit_review.graphql. It deliberately omits Author, SubmittedAt,
// ReactionGroups, and URL: a review fresh out of one of these mutations has
// no meaningful value for most of them yet (SubmittedAt is zero for a
// pending review; Author is always the viewer's own account, which none of
// these mutations' inputs carry a login for), and every mutation is
// followed by a full detail refetch (see store's finishMutation) that fills
// them in from pull_request.graphql's own, richer selection.
type reviewNode struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Body  string `json:"body"`
}

// toModel maps a decoded reviewNode into a minimal model.Review (see the
// type's own doc comment for which fields are deliberately left zero).
func (n reviewNode) toModel() model.Review {
	return model.Review{ID: n.ID, State: model.ReviewState(n.State), Body: normalizeNewlines(n.Body)}
}

// addPullRequestReviewResponse is the decoded shape shared by
// create_pending_review.graphql and add_review_now.graphql: both invoke
// GraphQL's addPullRequestReview mutation and select the identical
// "pullRequestReview { id state body }" payload, differing only in which
// input fields (event, body, threads) their variables populate.
type addPullRequestReviewResponse struct {
	AddPullRequestReview *struct {
		PullRequestReview *reviewNode `json:"pullRequestReview"`
	} `json:"addPullRequestReview"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// runAddPullRequestReview executes query (either create_pending_review's or
// add_review_now's document) with variables and decodes the shared
// addPullRequestReview payload shape, used by CreatePendingReview,
// AddReviewNow, and AddReviewNowWithEvent.
func (c *Client) runAddPullRequestReview(
	ctx context.Context, query string, variables map[string]any,
) (model.Review, model.RateLimit, error) {
	var resp addPullRequestReviewResponse
	if err := c.gql.DoWithContext(ctx, query, variables, &resp); err != nil {
		return model.Review{}, model.RateLimit{}, classify(err)
	}
	if resp.AddPullRequestReview == nil || resp.AddPullRequestReview.PullRequestReview == nil {
		return model.Review{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "addPullRequestReview: no review returned"}
	}
	return resp.AddPullRequestReview.PullRequestReview.toModel(), resp.RateLimit.toModel(), nil
}

// CreatePendingReview starts the viewer's pending review on prID (GraphQL's
// addPullRequestReview with no event, which GitHub always creates as
// PENDING). GitHub allows at most one pending review per user per pull
// request; calling this while one already exists is a caller error (the
// store's EnsurePendingReview-style callers check PendingReview() first).
func (c *Client) CreatePendingReview(ctx context.Context, prID string) (model.Review, model.RateLimit, error) {
	variables := map[string]any{"id": prID}
	return c.runAddPullRequestReview(ctx, createPendingReviewQuery(), variables)
}

// DraftThread is one line, range, or (StartLine == 0) single-line comment
// bundled into a single AddReviewNow call, mapping onto GraphQL's own
// DraftPullRequestReviewThread input object.
type DraftThread struct {
	Path string
	Line int
	Side model.DiffSide
	// StartLine is the first line of a multi-line range comment. Zero means
	// a single-line comment: StartLine/StartSide are then omitted from the
	// GraphQL input entirely (see toInput), matching a real line number
	// never being zero.
	StartLine int
	StartSide model.DiffSide
	Body      string
}

// toInput maps t into the map[string]any GraphQL expects for one
// DraftPullRequestReviewThread list element.
func (t DraftThread) toInput() map[string]any {
	in := map[string]any{"path": t.Path, "line": t.Line, "side": string(t.Side), "body": t.Body}
	if t.StartLine != 0 {
		in["startLine"] = t.StartLine
		in["startSide"] = string(t.StartSide)
	}
	return in
}

// AddReviewNow publishes threads as a new review on prID immediately
// (GraphQL's addPullRequestReview with event: COMMENT and a threads list),
// used for a single line/range comment when no pending review exists. body
// is the review's own top-level body (gprt's callers always pass "" here:
// the review has no separate body text of its own for a single comment,
// only the thread(s) it carries).
func (c *Client) AddReviewNow(
	ctx context.Context, prID string, threads []DraftThread, body string,
) (model.Review, model.RateLimit, error) {
	threadInputs := make([]map[string]any, 0, len(threads))
	for _, t := range threads {
		threadInputs = append(threadInputs, t.toInput())
	}
	variables := map[string]any{"id": prID, "event": string(model.ReviewEventComment), "body": body, "threads": threadInputs}
	return c.runAddPullRequestReview(ctx, addReviewNowQuery(), variables)
}

// AddReviewNowWithEvent publishes a review on prID immediately with no
// threads (GraphQL's addPullRequestReview with an explicit event and body,
// threads omitted), used by the store's SubmitReview when no pending review
// exists yet: GitHub's addPullRequestReview mutation both creates and
// submits a review in one call whenever event is non-empty, so there is no
// separate "create, then submit" round trip for this path (unlike
// SubmitReview, which finishes an already-created pending review via
// submitPullRequestReview).
func (c *Client) AddReviewNowWithEvent(
	ctx context.Context, prID string, event model.ReviewEvent, body string,
) (model.Review, model.RateLimit, error) {
	variables := map[string]any{"id": prID, "event": string(event), "body": body, "threads": nil}
	return c.runAddPullRequestReview(ctx, addReviewNowQuery(), variables)
}

// ThreadInput specifies a new review thread anchored to a line, range, or
// whole file, added to an existing (pending) review via
// addPullRequestReviewThread. Line/StartLine/Side/StartSide are left zero
// for a FILE-subject thread (see toVariables): GraphQL's own input accepts
// null for all four in that case.
type ThreadInput struct {
	PullRequestReviewID string
	Path                string
	Line                int
	Side                model.DiffSide
	StartLine           int
	StartSide           model.DiffSide
	SubjectType         model.ThreadSubject
	Body                string
}

// toVariables maps in into the GraphQL variables map for
// add_review_thread.graphql.
func (in ThreadInput) toVariables() map[string]any {
	return map[string]any{
		"reviewId":    in.PullRequestReviewID,
		"path":        in.Path,
		"subjectType": string(in.SubjectType),
		"body":        in.Body,
		"line":        intVariable(in.Line),
		"side":        sideVariable(in.Side),
		"startLine":   intVariable(in.StartLine),
		"startSide":   sideVariable(in.StartSide),
	}
}

// addReviewThreadResponse is the decoded shape of
// queries/add_review_thread.graphql. Its "thread" selection matches
// pull_request.graphql's own reviewThreads node field-for-field (see
// reviewThreadNode's doc comment in pull_request.go), so the payload
// decodes directly into that same type and reuses mapReviewThread.
type addReviewThreadResponse struct {
	AddPullRequestReviewThread *struct {
		Thread *reviewThreadNode `json:"thread"`
	} `json:"addPullRequestReviewThread"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// AddReviewThread adds a new thread (line, range, or whole-file) to an
// existing pending review.
func (c *Client) AddReviewThread(ctx context.Context, in ThreadInput) (model.ReviewThread, model.RateLimit, error) {
	var resp addReviewThreadResponse
	if err := c.gql.DoWithContext(ctx, addReviewThreadQuery(), in.toVariables(), &resp); err != nil {
		return model.ReviewThread{}, model.RateLimit{}, classify(err)
	}
	if resp.AddPullRequestReviewThread == nil || resp.AddPullRequestReviewThread.Thread == nil {
		return model.ReviewThread{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "addPullRequestReviewThread: no thread returned"}
	}
	return mapReviewThread(*resp.AddPullRequestReviewThread.Thread), resp.RateLimit.toModel(), nil
}

// addThreadReplyResponse is the decoded shape of
// queries/add_thread_reply.graphql. Its "comment" selection matches a
// review thread's own "comments" node field-for-field (see
// reviewCommentNode's doc comment in pull_request.go), so it decodes
// directly into that same type and reuses mapReviewComment.
type addThreadReplyResponse struct {
	AddPullRequestReviewThreadReply *struct {
		Comment *reviewCommentNode `json:"comment"`
	} `json:"addPullRequestReviewThreadReply"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// AddThreadReply replies to an existing review thread. pendingReviewID is
// the viewer's pending review's ID when one exists (the reply attaches to
// it, matching GitHub's own UI), or "" to reply immediately with no pending
// review.
func (c *Client) AddThreadReply(
	ctx context.Context, threadID, body, pendingReviewID string,
) (model.ReviewComment, model.RateLimit, error) {
	variables := map[string]any{"threadId": threadID, "body": body, "reviewId": stringVariable(pendingReviewID)}
	var resp addThreadReplyResponse
	if err := c.gql.DoWithContext(ctx, addThreadReplyQuery(), variables, &resp); err != nil {
		return model.ReviewComment{}, model.RateLimit{}, classify(err)
	}
	if resp.AddPullRequestReviewThreadReply == nil || resp.AddPullRequestReviewThreadReply.Comment == nil {
		return model.ReviewComment{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "addPullRequestReviewThreadReply: no comment returned"}
	}
	return mapReviewComment(*resp.AddPullRequestReviewThreadReply.Comment), resp.RateLimit.toModel(), nil
}

// submitReviewResponse is the decoded shape of queries/submit_review.graphql.
type submitReviewResponse struct {
	SubmitPullRequestReview *struct {
		PullRequestReview *reviewNode `json:"pullRequestReview"`
	} `json:"submitPullRequestReview"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// SubmitReview submits an existing pending review (identified by reviewID)
// with the given event (APPROVE, REQUEST_CHANGES, or COMMENT) and body.
func (c *Client) SubmitReview(
	ctx context.Context, reviewID string, event model.ReviewEvent, body string,
) (model.Review, model.RateLimit, error) {
	variables := map[string]any{"reviewId": reviewID, "event": string(event), "body": body}
	var resp submitReviewResponse
	if err := c.gql.DoWithContext(ctx, submitReviewQuery(), variables, &resp); err != nil {
		return model.Review{}, model.RateLimit{}, classify(err)
	}
	if resp.SubmitPullRequestReview == nil || resp.SubmitPullRequestReview.PullRequestReview == nil {
		return model.Review{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "submitPullRequestReview: no review returned"}
	}
	return resp.SubmitPullRequestReview.PullRequestReview.toModel(), resp.RateLimit.toModel(), nil
}

// deletePendingReviewResponse is the decoded shape of
// queries/delete_pending_review.graphql. The payload carries nothing gprt
// reads besides rateLimit, mirroring deleteIssueCommentResponse.
type deletePendingReviewResponse struct {
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// DeletePendingReview discards the viewer's pending review (GraphQL's
// deletePullRequestReview), used by the "discard review" action.
func (c *Client) DeletePendingReview(ctx context.Context, reviewID string) (model.RateLimit, error) {
	variables := map[string]any{"reviewId": reviewID}
	var resp deletePendingReviewResponse
	if err := c.gql.DoWithContext(ctx, deletePendingReviewQuery(), variables, &resp); err != nil {
		return model.RateLimit{}, classify(err)
	}
	return resp.RateLimit.toModel(), nil
}

// updateReviewCommentResponse is the decoded shape of
// queries/update_review_comment.graphql; see addReviewThreadResponse's doc
// comment for why its "pullRequestReviewComment" selection decodes directly
// into reviewCommentNode.
type updateReviewCommentResponse struct {
	UpdatePullRequestReviewComment *struct {
		PullRequestReviewComment *reviewCommentNode `json:"pullRequestReviewComment"`
	} `json:"updatePullRequestReviewComment"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// UpdateReviewComment edits an existing review comment's body.
func (c *Client) UpdateReviewComment(ctx context.Context, id, body string) (model.ReviewComment, model.RateLimit, error) {
	variables := map[string]any{"id": id, "body": body}
	var resp updateReviewCommentResponse
	if err := c.gql.DoWithContext(ctx, updateReviewCommentQuery(), variables, &resp); err != nil {
		return model.ReviewComment{}, model.RateLimit{}, classify(err)
	}
	if resp.UpdatePullRequestReviewComment == nil || resp.UpdatePullRequestReviewComment.PullRequestReviewComment == nil {
		return model.ReviewComment{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "updatePullRequestReviewComment: no comment returned"}
	}
	return mapReviewComment(*resp.UpdatePullRequestReviewComment.PullRequestReviewComment), resp.RateLimit.toModel(), nil
}

// deleteReviewCommentResponse is the decoded shape of
// queries/delete_review_comment.graphql, mirroring
// deletePendingReviewResponse.
type deleteReviewCommentResponse struct {
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// DeleteReviewComment deletes an existing review comment.
func (c *Client) DeleteReviewComment(ctx context.Context, id string) (model.RateLimit, error) {
	variables := map[string]any{"id": id}
	var resp deleteReviewCommentResponse
	if err := c.gql.DoWithContext(ctx, deleteReviewCommentQuery(), variables, &resp); err != nil {
		return model.RateLimit{}, classify(err)
	}
	return resp.RateLimit.toModel(), nil
}

// threadResolutionNode mirrors the "thread { id isResolved viewerCanResolve
// viewerCanUnresolve }" selection shared by resolve_thread.graphql and
// unresolve_thread.graphql: deliberately smaller than reviewThreadNode's
// full field set (see AddReviewThread), but includes both viewer-can-*
// flags alongside IsResolved — GitHub flips both when a thread's resolved
// state changes (a resolved thread reports viewerCanUnresolve, not
// viewerCanResolve, and vice versa), so the store must overwrite all three
// together from this payload rather than only IsResolved: toggling a
// thread twice in a row, before a refetch confirms the first toggle, would
// otherwise be refused locally against a stale viewerCanUnresolve/
// viewerCanResolve value the store never updated.
type threadResolutionNode struct {
	ID                 string `json:"id"`
	IsResolved         bool   `json:"isResolved"`
	ViewerCanResolve   bool   `json:"viewerCanResolve"`
	ViewerCanUnresolve bool   `json:"viewerCanUnresolve"`
}

// toModel maps a decoded threadResolutionNode into a minimal
// model.ReviewThread carrying only ID, IsResolved, ViewerCanResolve, and
// ViewerCanUnresolve; every other field is left zero (the caller replaces
// just those fields on the thread it already holds - see store's
// SetThreadResolved).
func (n threadResolutionNode) toModel() model.ReviewThread {
	return model.ReviewThread{
		ID: n.ID, IsResolved: n.IsResolved,
		ViewerCanResolve: n.ViewerCanResolve, ViewerCanUnresolve: n.ViewerCanUnresolve,
	}
}

// resolveThreadResponse is the decoded shape of queries/resolve_thread.graphql.
type resolveThreadResponse struct {
	ResolveReviewThread *struct {
		Thread *threadResolutionNode `json:"thread"`
	} `json:"resolveReviewThread"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// ResolveThread marks threadID resolved.
func (c *Client) ResolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	variables := map[string]any{"threadId": threadID}
	var resp resolveThreadResponse
	if err := c.gql.DoWithContext(ctx, resolveThreadQuery(), variables, &resp); err != nil {
		return model.ReviewThread{}, model.RateLimit{}, classify(err)
	}
	if resp.ResolveReviewThread == nil || resp.ResolveReviewThread.Thread == nil {
		return model.ReviewThread{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "resolveReviewThread: no thread returned"}
	}
	return resp.ResolveReviewThread.Thread.toModel(), resp.RateLimit.toModel(), nil
}

// unresolveThreadResponse is the decoded shape of
// queries/unresolve_thread.graphql.
type unresolveThreadResponse struct {
	UnresolveReviewThread *struct {
		Thread *threadResolutionNode `json:"thread"`
	} `json:"unresolveReviewThread"`
	RateLimit *rateLimitFragment `json:"rateLimit"`
}

// UnresolveThread marks threadID unresolved.
func (c *Client) UnresolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	variables := map[string]any{"threadId": threadID}
	var resp unresolveThreadResponse
	if err := c.gql.DoWithContext(ctx, unresolveThreadQuery(), variables, &resp); err != nil {
		return model.ReviewThread{}, model.RateLimit{}, classify(err)
	}
	if resp.UnresolveReviewThread == nil || resp.UnresolveReviewThread.Thread == nil {
		return model.ReviewThread{}, resp.RateLimit.toModel(),
			&Error{Kind: KindUnknown, Message: "unresolveReviewThread: no thread returned"}
	}
	return resp.UnresolveReviewThread.Thread.toModel(), resp.RateLimit.toModel(), nil
}
