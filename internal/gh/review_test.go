package gh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

// decodeRequest is a small helper shared by every test below: it captures
// the GraphQL request's query text and variables map as the httptest
// handler runs, for assertions after the call returns.
func decodeRequest(t *testing.T, r *http.Request) (query string, vars map[string]any) {
	t.Helper()
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body.Query, body.Variables
}

func TestClient_CreatePendingReview_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReview": {
				"pullRequestReview": {"id": "PRR_1", "state": "PENDING", "body": ""}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	review, err := c.CreatePendingReview(context.Background(), "PR_1")
	if err != nil {
		t.Fatalf("CreatePendingReview() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation CreatePendingReview", "addPullRequestReview", "pullRequestId") {
		t.Errorf("request query = %q, want the CreatePendingReview mutation document", gotQuery)
	}
	if gotVars["id"] != "PR_1" {
		t.Errorf("request variables = %+v, want id=PR_1", gotVars)
	}
	if review.ID != "PRR_1" || review.State != model.ReviewStatePending {
		t.Errorf("CreatePendingReview() review = %+v, want ID=PRR_1 State=PENDING", review)
	}
}

func TestClient_CreatePendingReview_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "You already have a pending review"}`))
	})
	defer srv.Close()

	_, err := c.CreatePendingReview(context.Background(), "PR_1")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("CreatePendingReview() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindValidation {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindValidation)
	}
	if ghErr.Message != "You already have a pending review" {
		t.Errorf("Message = %q, want the GitHub validation message kept verbatim", ghErr.Message)
	}
}

func TestClient_AddReviewNow_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReview": {
				"pullRequestReview": {"id": "PRR_2", "state": "COMMENTED", "body": ""}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	threads := []DraftThread{
		{Path: "a.go", Line: 12, Side: model.DiffSideRight, Body: "nit"},
	}
	review, err := c.AddReviewNow(context.Background(), "PR_1", threads, "")
	if err != nil {
		t.Fatalf("AddReviewNow() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation AddReviewNow", "addPullRequestReview", "threads") {
		t.Errorf("request query = %q, want the AddReviewNow mutation document", gotQuery)
	}
	if gotVars["event"] != "COMMENT" {
		t.Errorf("request variables event = %v, want COMMENT", gotVars["event"])
	}
	gotThreads, ok := gotVars["threads"].([]any)
	if !ok || len(gotThreads) != 1 {
		t.Fatalf("request variables threads = %+v, want one thread", gotVars["threads"])
	}
	thread, ok := gotThreads[0].(map[string]any)
	if !ok || thread["path"] != "a.go" || thread["side"] != "RIGHT" || thread["body"] != "nit" {
		t.Errorf("thread input = %+v, want path=a.go side=RIGHT body=nit", thread)
	}
	if _, hasStartLine := thread["startLine"]; hasStartLine {
		t.Errorf("thread input = %+v, want no startLine for a single-line comment", thread)
	}
	if review.ID != "PRR_2" {
		t.Errorf("AddReviewNow() review.ID = %q, want PRR_2", review.ID)
	}
}

func TestClient_AddReviewNow_RangeThreadIncludesStartLine(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReview": {"pullRequestReview": {"id": "PRR_3", "state": "COMMENTED", "body": ""}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	threads := []DraftThread{
		{Path: "a.go", Line: 15, Side: model.DiffSideRight, StartLine: 12, StartSide: model.DiffSideRight, Body: "range"},
	}
	if _, err := c.AddReviewNow(context.Background(), "PR_1", threads, ""); err != nil {
		t.Fatalf("AddReviewNow() error = %v", err)
	}

	gotThreads, _ := gotVars["threads"].([]any)
	thread, _ := gotThreads[0].(map[string]any)
	if thread["startLine"] != float64(12) || thread["startSide"] != "RIGHT" {
		t.Errorf("thread input = %+v, want startLine=12 startSide=RIGHT", thread)
	}
}

func TestClient_AddReviewNowWithEvent_Success(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReview": {"pullRequestReview": {"id": "PRR_4", "state": "APPROVED", "body": "lgtm"}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	review, err := c.AddReviewNowWithEvent(context.Background(), "PR_1", model.ReviewEventApprove, "lgtm")
	if err != nil {
		t.Fatalf("AddReviewNowWithEvent() error = %v", err)
	}
	if gotVars["event"] != "APPROVE" || gotVars["body"] != "lgtm" {
		t.Errorf("request variables = %+v, want event=APPROVE body=lgtm", gotVars)
	}
	if gotVars["threads"] != nil {
		t.Errorf("request variables threads = %v, want nil (no threads for a submit-style review)", gotVars["threads"])
	}
	if review.State != model.ReviewStateApproved {
		t.Errorf("AddReviewNowWithEvent() review.State = %v, want APPROVED", review.State)
	}
}

func TestClient_AddReviewThread_LineSubject(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReviewThread": {
				"thread": {
					"id": "RT_1",
					"isResolved": false,
					"isOutdated": false,
					"path": "a.go",
					"line": 12,
					"startLine": 0,
					"diffSide": "RIGHT",
					"startDiffSide": "",
					"subjectType": "LINE",
					"viewerCanReply": true,
					"viewerCanResolve": true,
					"viewerCanUnresolve": false,
					"comments": {"nodes": [{
						"id": "RC_1",
						"author": {"login": "octocat"},
						"body": "hi",
						"createdAt": "2026-09-12T00:00:00Z",
						"state": "PENDING",
						"url": "https://example.com/RC_1",
						"reactionGroups": [],
						"viewerCanUpdate": true,
						"viewerCanDelete": true,
						"pullRequestReview": {"id": "PRR_1"}
					}]}
				}
			}
		}
	}`)

	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	thread, err := c.AddReviewThread(context.Background(), ThreadInput{
		PullRequestReviewID: "PRR_1",
		Path:                "a.go",
		Line:                12,
		Side:                model.DiffSideRight,
		SubjectType:         model.ThreadSubjectLine,
		Body:                "hi",
	})
	if err != nil {
		t.Fatalf("AddReviewThread() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation AddReviewThread", "addPullRequestReviewThread", "pullRequestReviewId") {
		t.Errorf("request query = %q, want the AddReviewThread mutation document", gotQuery)
	}
	if gotVars["reviewId"] != "PRR_1" || gotVars["path"] != "a.go" || gotVars["line"] != float64(12) ||
		gotVars["side"] != "RIGHT" || gotVars["subjectType"] != "LINE" || gotVars["body"] != "hi" {
		t.Errorf("request variables = %+v, want reviewId=PRR_1 path=a.go line=12 side=RIGHT subjectType=LINE body=hi", gotVars)
	}
	if gotVars["startLine"] != nil {
		t.Errorf("request variables startLine = %v, want nil for a single-line thread", gotVars["startLine"])
	}

	want := model.ReviewThread{
		ID: "RT_1", Path: "a.go", Line: 12, Side: model.DiffSideRight,
		SubjectType: model.ThreadSubjectLine, ViewerCanReply: true, ViewerCanResolve: true,
		Comments: []model.ReviewComment{{
			ID: "RC_1", ReviewID: "PRR_1", Author: model.User{Login: "octocat"}, Body: "hi",
			State: model.ReviewCommentStatePending, ViewerCanUpdate: true, ViewerCanDelete: true,
			URL: "https://example.com/RC_1",
		}},
	}
	if thread.ID != want.ID || thread.Path != want.Path || thread.Line != want.Line || thread.Side != want.Side ||
		thread.SubjectType != want.SubjectType || len(thread.Comments) != 1 ||
		thread.Comments[0].ID != want.Comments[0].ID || thread.Comments[0].ReviewID != want.Comments[0].ReviewID {
		t.Errorf("AddReviewThread() thread = %+v, want %+v", thread, want)
	}
}

func TestClient_AddReviewThread_FileSubjectOmitsLine(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReviewThread": {
				"thread": {
					"id": "RT_2", "isResolved": false, "isOutdated": false, "path": "a.go",
					"line": 0, "startLine": 0, "diffSide": "", "startDiffSide": "", "subjectType": "FILE",
					"viewerCanReply": true, "viewerCanResolve": true, "viewerCanUnresolve": false,
					"comments": {"nodes": []}
				}
			}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	thread, err := c.AddReviewThread(context.Background(), ThreadInput{
		PullRequestReviewID: "PRR_1", Path: "a.go", SubjectType: model.ThreadSubjectFile, Body: "file comment",
	})
	if err != nil {
		t.Fatalf("AddReviewThread() error = %v", err)
	}
	if gotVars["line"] != nil || gotVars["side"] != nil || gotVars["startLine"] != nil || gotVars["startSide"] != nil {
		t.Errorf("request variables = %+v, want line/side/startLine/startSide all nil for a FILE subject", gotVars)
	}
	if thread.SubjectType != model.ThreadSubjectFile {
		t.Errorf("AddReviewThread() thread.SubjectType = %v, want FILE", thread.SubjectType)
	}
}

func TestClient_AddThreadReply_WithPendingReview(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReviewThreadReply": {
				"comment": {
					"id": "RC_2", "author": {"login": "octocat"}, "body": "reply",
					"createdAt": "2026-09-12T00:00:00Z", "state": "PENDING",
					"url": "https://example.com/RC_2", "reactionGroups": [],
					"viewerCanUpdate": true, "viewerCanDelete": true,
					"pullRequestReview": {"id": "PRR_1"}
				}
			}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	comment, err := c.AddThreadReply(context.Background(), "RT_1", "reply", "PRR_1")
	if err != nil {
		t.Fatalf("AddThreadReply() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation AddThreadReply", "addPullRequestReviewThreadReply", "pullRequestReviewThreadId") {
		t.Errorf("request query = %q, want the AddThreadReply mutation document", gotQuery)
	}
	if gotVars["threadId"] != "RT_1" || gotVars["body"] != "reply" || gotVars["reviewId"] != "PRR_1" {
		t.Errorf("request variables = %+v, want threadId=RT_1 body=reply reviewId=PRR_1", gotVars)
	}
	if comment.ID != "RC_2" || comment.ReviewID != "PRR_1" {
		t.Errorf("AddThreadReply() comment = %+v, want ID=RC_2 ReviewID=PRR_1", comment)
	}
}

func TestClient_AddThreadReply_NoPendingReviewOmitsReviewID(t *testing.T) {
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"addPullRequestReviewThreadReply": {
				"comment": {
					"id": "RC_3", "author": {"login": "octocat"}, "body": "reply",
					"createdAt": "2026-09-12T00:00:00Z", "state": "SUBMITTED",
					"url": "https://example.com/RC_3", "reactionGroups": [],
					"viewerCanUpdate": true, "viewerCanDelete": true, "pullRequestReview": {"id": "PRR_2"}
				}
			}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	if _, err := c.AddThreadReply(context.Background(), "RT_1", "reply", ""); err != nil {
		t.Fatalf("AddThreadReply() error = %v", err)
	}
	if gotVars["reviewId"] != nil {
		t.Errorf("request variables reviewId = %v, want nil when no pending review exists", gotVars["reviewId"])
	}
}

func TestClient_SubmitReview_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"submitPullRequestReview": {"pullRequestReview": {"id": "PRR_1", "state": "APPROVED", "body": "lgtm"}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	review, err := c.SubmitReview(context.Background(), "PRR_1", model.ReviewEventApprove, "lgtm")
	if err != nil {
		t.Fatalf("SubmitReview() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation SubmitReview", "submitPullRequestReview", "pullRequestReviewId") {
		t.Errorf("request query = %q, want the SubmitReview mutation document", gotQuery)
	}
	if gotVars["reviewId"] != "PRR_1" || gotVars["event"] != "APPROVE" || gotVars["body"] != "lgtm" {
		t.Errorf("request variables = %+v, want reviewId=PRR_1 event=APPROVE body=lgtm", gotVars)
	}
	if review.State != model.ReviewStateApproved {
		t.Errorf("SubmitReview() review.State = %v, want APPROVED", review.State)
	}
}

func TestClient_SubmitReview_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "Can not approve your own pull request"}`))
	})
	defer srv.Close()

	_, err := c.SubmitReview(context.Background(), "PRR_1", model.ReviewEventApprove, "")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Fatalf("SubmitReview() error = %v, want a KindValidation *gh.Error", err)
	}
}

// TestClient_SubmitReview_UnprocessableGraphQLError exercises the other
// shape GitHub uses for a validation failure besides an HTTP 422: an HTTP
// 200 response whose GraphQL "errors" array carries an item with
// type: "UNPROCESSABLE" (as opposed to the "message"-only 422 body
// TestClient_SubmitReview_ValidationError covers).
func TestClient_SubmitReview_UnprocessableGraphQLError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data": null,
			"errors": [{"type": "UNPROCESSABLE", "message": "COMMENT requires a body or comments"}]
		}`))
	})
	defer srv.Close()

	_, err := c.SubmitReview(context.Background(), "PRR_1", model.ReviewEventComment, "")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("SubmitReview() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindValidation {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindValidation)
	}
	if ghErr.Message != "COMMENT requires a body or comments" {
		t.Errorf("Message = %q, want GitHub's message kept verbatim", ghErr.Message)
	}
}

func TestClient_DeletePendingReview_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"deletePullRequestReview": {"pullRequestReview": {"id": "PRR_1"}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	err := c.DeletePendingReview(context.Background(), "PRR_1")
	if err != nil {
		t.Fatalf("DeletePendingReview() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation DeletePendingReview", "deletePullRequestReview", "pullRequestReviewId") {
		t.Errorf("request query = %q, want the DeletePendingReview mutation document", gotQuery)
	}
	if gotVars["reviewId"] != "PRR_1" {
		t.Errorf("request variables = %+v, want reviewId=PRR_1", gotVars)
	}
}

func TestClient_DeletePendingReview_NotFoundError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	err := c.DeletePendingReview(context.Background(), "PRR_missing")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindNotFound {
		t.Fatalf("DeletePendingReview() error = %v, want a KindNotFound *gh.Error", err)
	}
}

func TestClient_UpdateReviewComment_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"updatePullRequestReviewComment": {
				"pullRequestReviewComment": {
					"id": "RC_1", "author": {"login": "octocat"}, "body": "edited",
					"createdAt": "2026-09-12T00:00:00Z", "state": "SUBMITTED",
					"url": "https://example.com/RC_1", "reactionGroups": [],
					"viewerCanUpdate": true, "viewerCanDelete": true, "pullRequestReview": {"id": "PRR_1"}
				}
			}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	comment, err := c.UpdateReviewComment(context.Background(), "RC_1", "edited")
	if err != nil {
		t.Fatalf("UpdateReviewComment() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation UpdateReviewComment", "updatePullRequestReviewComment", "pullRequestReviewCommentId") {
		t.Errorf("request query = %q, want the UpdateReviewComment mutation document", gotQuery)
	}
	if gotVars["id"] != "RC_1" || gotVars["body"] != "edited" {
		t.Errorf("request variables = %+v, want id=RC_1 body=edited", gotVars)
	}
	if comment.Body != "edited" {
		t.Errorf("UpdateReviewComment() comment.Body = %q, want %q", comment.Body, "edited")
	}
}

func TestClient_UpdateReviewComment_AuthError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	_, err := c.UpdateReviewComment(context.Background(), "RC_1", "edited")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindAuth {
		t.Fatalf("UpdateReviewComment() error = %v, want a KindAuth *gh.Error", err)
	}
}

func TestClient_DeleteReviewComment_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"deletePullRequestReviewComment": {"clientMutationId": null}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	err := c.DeleteReviewComment(context.Background(), "RC_1")
	if err != nil {
		t.Fatalf("DeleteReviewComment() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation DeleteReviewComment", "deletePullRequestReviewComment") {
		t.Errorf("request query = %q, want the DeleteReviewComment mutation document", gotQuery)
	}
	if gotVars["id"] != "RC_1" {
		t.Errorf("request variables = %+v, want id=RC_1", gotVars)
	}
}

func TestClient_DeleteReviewComment_NotFoundError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	err := c.DeleteReviewComment(context.Background(), "RC_missing")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindNotFound {
		t.Fatalf("DeleteReviewComment() error = %v, want a KindNotFound *gh.Error", err)
	}
}

func TestClient_ResolveThread_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"resolveReviewThread": {"thread": {
				"id": "RT_1", "isResolved": true,
				"viewerCanResolve": false, "viewerCanUnresolve": true
			}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	thread, err := c.ResolveThread(context.Background(), "RT_1")
	if err != nil {
		t.Fatalf("ResolveThread() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation ResolveThread", "resolveReviewThread", "threadId") {
		t.Errorf("request query = %q, want the ResolveThread mutation document", gotQuery)
	}
	if gotVars["threadId"] != "RT_1" {
		t.Errorf("request variables = %+v, want threadId=RT_1", gotVars)
	}
	if thread.ID != "RT_1" || !thread.IsResolved {
		t.Errorf("ResolveThread() thread = %+v, want ID=RT_1 IsResolved=true", thread)
	}
	if thread.ViewerCanResolve || !thread.ViewerCanUnresolve {
		t.Errorf("ResolveThread() thread = %+v, want ViewerCanResolve=false ViewerCanUnresolve=true", thread)
	}
}

func TestClient_ResolveThread_ValidationError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "line must be part of the diff"}`))
	})
	defer srv.Close()

	_, err := c.ResolveThread(context.Background(), "RT_1")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindValidation {
		t.Fatalf("ResolveThread() error = %v, want a KindValidation *gh.Error", err)
	}
	if ghErr.Message != "line must be part of the diff" {
		t.Errorf("Message = %q, want GitHub's message kept verbatim", ghErr.Message)
	}
}

func TestClient_UnresolveThread_Success(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fixture := []byte(`{
		"data": {
			"unresolveReviewThread": {"thread": {
				"id": "RT_1", "isResolved": false,
				"viewerCanResolve": true, "viewerCanUnresolve": false
			}}
		}
	}`)
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotVars = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	thread, err := c.UnresolveThread(context.Background(), "RT_1")
	if err != nil {
		t.Fatalf("UnresolveThread() error = %v", err)
	}
	if !containsAll(gotQuery, "mutation UnresolveThread", "unresolveReviewThread", "threadId") {
		t.Errorf("request query = %q, want the UnresolveThread mutation document", gotQuery)
	}
	if gotVars["threadId"] != "RT_1" {
		t.Errorf("request variables = %+v, want threadId=RT_1", gotVars)
	}
	if thread.ID != "RT_1" || thread.IsResolved {
		t.Errorf("UnresolveThread() thread = %+v, want ID=RT_1 IsResolved=false", thread)
	}
	if !thread.ViewerCanResolve || thread.ViewerCanUnresolve {
		t.Errorf("UnresolveThread() thread = %+v, want ViewerCanResolve=true ViewerCanUnresolve=false", thread)
	}
}

func TestClient_UnresolveThread_NotFoundError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	_, err := c.UnresolveThread(context.Background(), "RT_missing")
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != KindNotFound {
		t.Fatalf("UnresolveThread() error = %v, want a KindNotFound *gh.Error", err)
	}
}
