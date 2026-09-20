package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func testPRRef() model.PRRef {
	return model.PRRef{
		Repo:   model.RepoRef{Host: "example.com", Owner: "acme", Name: "widgets"},
		Number: 42,
	}
}

// findTimelineEvent returns the Event item with the given Type from items,
// failing the test if none is found.
func findTimelineEvent(t *testing.T, items []model.TimelineItem, typeName string) *model.Event {
	t.Helper()
	for _, item := range items {
		if item.Kind == model.TimelineKindEvent && item.Event.Type == typeName {
			return item.Event
		}
	}
	t.Fatalf("no timeline event with Type = %q found in %d items", typeName, len(items))
	return nil
}

func TestClient_PullRequest_FullMappingWithPagination(t *testing.T) {
	page1, err := os.ReadFile("testdata/pull_request_page1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	page2, err := os.ReadFile("testdata/pull_request_page2.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var calls int
	var gotVariables []map[string]any
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotVariables = append(gotVariables, body.Variables)
		calls++

		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write(page1)
		} else {
			_, _ = w.Write(page2)
		}
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "octocat")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}

	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one page each for threads/timeline/checks)", calls)
	}
	if gotVariables[0]["threadsCursor"] != nil || gotVariables[0]["timelineCursor"] != nil || gotVariables[0]["checksCursor"] != nil {
		t.Errorf("first call variables = %+v, want every cursor nil", gotVariables[0])
	}
	if gotVariables[1]["threadsCursor"] != "threads-c1" {
		t.Errorf("second call threadsCursor = %v, want %q", gotVariables[1]["threadsCursor"], "threads-c1")
	}
	if gotVariables[1]["timelineCursor"] != "timeline-c1" {
		t.Errorf("second call timelineCursor = %v, want %q", gotVariables[1]["timelineCursor"], "timeline-c1")
	}
	if gotVariables[1]["checksCursor"] != "checks-c1" {
		t.Errorf("second call checksCursor = %v, want %q", gotVariables[1]["checksCursor"], "checks-c1")
	}

	pr := res.PR
	if pr.ID != "PR_kwDOAbCdef" || pr.Title != "Add PR detail query" || pr.Body != "This PR adds the detail query." {
		t.Errorf("PR core fields = %+v", pr)
	}
	if pr.Ref != testPRRef() {
		t.Errorf("Ref = %+v, want %+v", pr.Ref, testPRRef())
	}
	if pr.RepositoryID != "R_kgDOAbCdef" || pr.HeadOID != "abc123def456" {
		t.Errorf("RepositoryID/HeadOID = %q/%q", pr.RepositoryID, pr.HeadOID)
	}
	if pr.State != model.PRStateOpen || pr.IsDraft || pr.Mergeable != model.MergeableStateMergeable ||
		pr.MergeStateStatus != model.MergeStateStatusClean || pr.ReviewDecision != model.ReviewDecisionReviewRequired {
		t.Errorf("state fields = %+v", pr)
	}
	if !pr.ViewerCanUpdate || !pr.ViewerDidAuthor || !pr.ViewerCanClose || pr.ViewerCanReopen || !pr.ViewerCanReact {
		t.Errorf("viewer flags = %+v", pr)
	}

	if len(pr.ReactionGroups) != 2 || pr.ReactionGroups[0].Content != model.ReactionThumbsUp || pr.ReactionGroups[0].Count != 3 || !pr.ReactionGroups[0].ViewerHasReacted {
		t.Errorf("ReactionGroups = %+v", pr.ReactionGroups)
	}

	if len(pr.Labels) != 2 || pr.Labels[0].Name != "bug" || pr.Labels[1].Name != "enhancement" {
		t.Errorf("Labels = %+v", pr.Labels)
	}

	if len(pr.ReviewRequests) != 2 {
		t.Fatalf("ReviewRequests = %+v, want 2 entries", pr.ReviewRequests)
	}
	if pr.ReviewRequests[0].Kind != model.ReviewerKindUser || pr.ReviewRequests[0].Login != "reviewer1" || !pr.ReviewRequests[0].AsCodeOwner {
		t.Errorf("ReviewRequests[0] = %+v", pr.ReviewRequests[0])
	}
	if pr.ReviewRequests[1].Kind != model.ReviewerKindTeam || pr.ReviewRequests[1].Login != "core-team" {
		t.Errorf("ReviewRequests[1] = %+v", pr.ReviewRequests[1])
	}

	if len(pr.LatestReviews) != 1 {
		t.Fatalf("LatestReviews = %+v, want 1 entry", pr.LatestReviews)
	}
	if lr := pr.LatestReviews[0]; lr.Body != "Please address the comments." || lr.URL == "" || lr.State != model.ReviewStateChangesRequested {
		t.Errorf("LatestReviews[0] = %+v", lr)
	}

	// Checks: 2 contexts from page 1 + 1 from page 2, merged in order.
	if len(pr.Checks) != 3 {
		t.Fatalf("Checks = %+v, want 3 entries", pr.Checks)
	}
	if c0 := pr.Checks[0]; c0.Name != "build" || c0.Status != model.CheckStatusCompleted || c0.Conclusion != model.CheckConclusionSuccess || c0.Workflow != "CI" || !c0.IsRequired {
		t.Errorf("Checks[0] (CheckRun) = %+v", c0)
	}
	if c1 := pr.Checks[1]; c1.Name != "ci/legacy" || c1.Status != model.CheckStatusCompleted || c1.Conclusion != model.CheckConclusionFailure || c1.IsRequired {
		t.Errorf("Checks[1] (StatusContext) = %+v", c1)
	}
	if c2 := pr.Checks[2]; c2.Name != "deploy" || c2.Status != model.CheckStatusInProgress || c2.Workflow != "" {
		t.Errorf("Checks[2] (page 2 CheckRun, null workflowRun) = %+v", c2)
	}
	if pr.RollupState != model.StatusStateFailure {
		t.Errorf("RollupState = %q, want %q", pr.RollupState, model.StatusStateFailure)
	}

	// Review threads: RT_1 (page 1, LINE, 2 comments) + RT_2 (page 2, FILE, 1 comment).
	if len(pr.ReviewThreads) != 2 {
		t.Fatalf("ReviewThreads = %+v, want 2 entries", pr.ReviewThreads)
	}
	rt0 := pr.ReviewThreads[0]
	if rt0.ID != "RT_1" || rt0.SubjectType != model.ThreadSubjectLine || rt0.Line != 10 || len(rt0.Comments) != 2 {
		t.Errorf("ReviewThreads[0] = %+v", rt0)
	}
	if rt0.Comments[0].State != model.ReviewCommentStateSubmitted || rt0.Comments[0].ReviewID != "PRR_1" {
		t.Errorf("ReviewThreads[0].Comments[0] = %+v", rt0.Comments[0])
	}
	if rt0.Comments[1].State != model.ReviewCommentStatePending || !rt0.Comments[1].ViewerCanUpdate {
		t.Errorf("ReviewThreads[0].Comments[1] = %+v", rt0.Comments[1])
	}
	rt1 := pr.ReviewThreads[1]
	if rt1.ID != "RT_2" || rt1.SubjectType != model.ThreadSubjectFile || !rt1.IsResolved || !rt1.IsOutdated {
		t.Errorf("ReviewThreads[1] = %+v", rt1)
	}

	// Timeline: 16 items from page 1 + 1 from page 2.
	if len(pr.Timeline) != 17 {
		t.Fatalf("Timeline = %d items, want 17", len(pr.Timeline))
	}

	var issueComments, reviews, commits int
	for _, item := range pr.Timeline {
		switch item.Kind {
		case model.TimelineKindIssueComment:
			issueComments++
		case model.TimelineKindReview:
			reviews++
		case model.TimelineKindCommit:
			commits++
		}
	}
	if issueComments != 2 || reviews != 1 || commits != 2 {
		t.Errorf("timeline kind counts: issueComments=%d reviews=%d commits=%d, want 2/1/2", issueComments, reviews, commits)
	}

	for _, item := range pr.Timeline {
		if item.Kind != model.TimelineKindCommit {
			continue
		}
		switch item.Commit.OID {
		case "c1":
			if item.Commit.Author.Login != "octocat" {
				t.Errorf("commit c1 author = %+v, want login octocat", item.Commit.Author)
			}
		case "c2":
			if item.Commit.Author.Login != "" || item.Commit.Author.Name != "External Contributor" {
				t.Errorf("commit c2 author = %+v, want empty login, name External Contributor", item.Commit.Author)
			}
		}
	}

	if e := findTimelineEvent(t, pr.Timeline, "RenamedTitleEvent"); e.Detail != `renamed from "Old title" to "Add PR detail query"` {
		t.Errorf("RenamedTitleEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "LabeledEvent"); e.Detail != "added the bug label" {
		t.Errorf("LabeledEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "UnlabeledEvent"); e.Detail != "removed the wontfix label" {
		t.Errorf("UnlabeledEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "ReviewRequestedEvent"); e.Detail != "requested a review from reviewer1" {
		t.Errorf("ReviewRequestedEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "ReviewRequestRemovedEvent"); e.Detail != "removed the review request for core-team" {
		t.Errorf("ReviewRequestRemovedEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "HeadRefForcePushedEvent"); e.Detail != "force-pushed to abc1234" {
		t.Errorf("HeadRefForcePushedEvent.Detail = %q", e.Detail)
	}
	if e := findTimelineEvent(t, pr.Timeline, "BaseRefChangedEvent"); e.Detail != "changed the base branch from develop to main" {
		t.Errorf("BaseRefChangedEvent.Detail = %q", e.Detail)
	}
	// Events the query selects only actor/createdAt for have no detail text.
	if e := findTimelineEvent(t, pr.Timeline, "MergedEvent"); e.Detail != "" || e.Actor.Login != "octocat" {
		t.Errorf("MergedEvent = %+v", e)
	}

	if pr.PendingReview == nil {
		t.Fatal("PendingReview = nil, want the viewer's pending review")
	}
	if pr.PendingReview.ID != "PRV_1" || pr.PendingReview.Author.Login != "octocat" || pr.PendingReview.State != model.ReviewStatePending {
		t.Errorf("PendingReview = %+v", pr.PendingReview)
	}

	if res.RateLimit.Remaining != 4899 || !res.RateLimit.Known {
		t.Errorf("RateLimit = %+v, want the last page's rate limit", res.RateLimit)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", res.Warnings)
	}
}

func TestClient_PullRequest_EmptyRollupAndNoPendingReview(t *testing.T) {
	fixture, err := os.ReadFile("testdata/pull_request_minimal.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var calls int
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (nothing has a next page)", calls)
	}

	pr := res.PR
	if pr.Checks != nil {
		t.Errorf("Checks = %+v, want nil for a commit with no status-check rollup", pr.Checks)
	}
	if pr.RollupState != "" {
		t.Errorf("RollupState = %q, want empty", pr.RollupState)
	}
	if pr.PendingReview != nil {
		t.Errorf("PendingReview = %+v, want nil", pr.PendingReview)
	}
	if len(pr.Labels) != 0 || len(pr.ReviewRequests) != 0 || len(pr.LatestReviews) != 0 ||
		len(pr.ReviewThreads) != 0 || len(pr.Timeline) != 0 {
		t.Errorf("expected every connection empty, got PR = %+v", pr)
	}
}

func TestClient_PullRequest_NodeScopedErrorKeptWithWarning(t *testing.T) {
	fixture, err := os.ReadFile("testdata/pull_request_partial_error.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	if err != nil {
		t.Fatalf("PullRequest() error = %v, want nil (node-scoped errors must not fail the call)", err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "You do not have permission to view this team" {
		t.Errorf("Warnings = %v, want the GraphQL error message", res.Warnings)
	}
	if len(res.PR.ReviewRequests) != 1 || res.PR.ReviewRequests[0].Kind != model.ReviewerKindOther {
		t.Errorf("ReviewRequests = %+v, want the null reviewer degraded to ReviewerKindOther", res.PR.ReviewRequests)
	}
}

func TestClient_PullRequest_NotFound(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"repository":{"pullRequest":null},"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},` +
			`"errors":[{"message":"Could not resolve to a PullRequest.","path":["repository","pullRequest"],"type":"NOT_FOUND"}]}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	_, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("PullRequest() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindNotFound {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindNotFound)
	}
}

func TestClient_PullRequest_TopLevelErrorStillFails(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"repository":null,"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},` +
			`"errors":[{"message":"SAML enforcement blocks this repository","path":["repository"],"type":"FORBIDDEN"}]}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	_, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("PullRequest() error = %v (%T), want *gh.Error (a failure outside repository/pullRequest must still fail)", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindAuth)
	}
}

func TestClient_PullRequest_NullPullRequestFromNestedErrorClassifiesRealError(t *testing.T) {
	// pullRequest itself is null, but the error causing it is scoped
	// deep under repository.pullRequest (a non-null child resolver, for
	// example timelineItems, failing propagates the null up to the
	// nearest nullable ancestor). This must classify as the real error
	// (here FORBIDDEN -> KindAuth), not the fabricated
	// "pull request not found" NotFound placeholder.
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"repository":{"pullRequest":null},"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},` +
			`"errors":[{"message":"timed out fetching timeline items","path":["repository","pullRequest","timelineItems"],"type":"FORBIDDEN"}]}`
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	_, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("PullRequest() error = %v (%T), want *gh.Error", err, err)
	}
	if ghErr.Kind != KindAuth {
		t.Errorf("Kind = %v, want %v (the real propagated error, not a fabricated NotFound)", ghErr.Kind, KindAuth)
	}
	if ghErr.Message == "pull request not found" {
		t.Error("Message must not be the fabricated NotFound placeholder when a real error caused the null")
	}
}

func TestClient_PullRequest_NullPropagatedNodesSkippedAcrossConnections(t *testing.T) {
	fixture, err := os.ReadFile("testdata/pull_request_null_propagation.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if len(res.PR.ReviewThreads) != 1 || res.PR.ReviewThreads[0].ID != "RT_1" {
		t.Fatalf("ReviewThreads = %+v, want exactly the one resolvable thread", res.PR.ReviewThreads)
	}
	if len(res.PR.Checks) != 1 || res.PR.Checks[0].Name != "build" {
		t.Fatalf("Checks = %+v, want exactly the one resolvable check", res.PR.Checks)
	}
	if len(res.PR.Timeline) != 1 || res.PR.Timeline[0].Kind != model.TimelineKindIssueComment {
		t.Fatalf("Timeline = %+v, want exactly the one resolvable item", res.PR.Timeline)
	}
	wantWarnings := map[string]bool{
		"1 review thread(s) omitted (could not be resolved)": false,
		"1 check(s) omitted (could not be resolved)":         false,
		"1 timeline item(s) omitted (could not be resolved)": false,
	}
	for _, w := range res.Warnings {
		if _, ok := wantWarnings[w]; ok {
			wantWarnings[w] = true
		}
	}
	for msg, seen := range wantWarnings {
		if !seen {
			t.Errorf("Warnings = %v, missing %q", res.Warnings, msg)
		}
	}
}

func TestClient_PullRequest_WarningsDeduplicatedAcrossPages(t *testing.T) {
	// The same node-scoped error (on reviewRequests, a non-paginated
	// field re-read every round) is present in both pages; reviewThreads
	// needs a second page to finish. Warnings must report it once, not
	// once per round.
	call := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "application/json")
		hasNext := "false"
		if call == 1 {
			hasNext = "true"
		}
		body := fmt.Sprintf(`{"data":{"repository":{"pullRequest":{
			"id":"PR_dup","number":1,"title":"t","body":"","state":"OPEN","isDraft":false,
			"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"",
			"author":{"login":"a"},"repository":{"id":"R_dup","nameWithOwner":"acme/widgets"},
			"baseRefName":"main","headRefName":"feature","headRefOid":"abc",
			"additions":0,"deletions":0,"changedFiles":0,
			"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","url":"https://example.com/pr/1",
			"viewerCanUpdate":false,"viewerDidAuthor":false,"viewerCanClose":false,"viewerCanReopen":false,"viewerCanReact":false,
			"reactionGroups":[],"labels":{"nodes":[]},"reviewRequests":{"nodes":[{"asCodeOwner":false,"requestedReviewer":null}]},
			"latestReviews":{"nodes":[]},
			"commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":null}}]},
			"reviewThreads":{"pageInfo":{"hasNextPage":%s,"endCursor":"tc"},"nodes":[{"id":"RT_%d","isResolved":false,"isOutdated":false,"path":"a.go","line":1,"startLine":0,"diffSide":"RIGHT","startDiffSide":"RIGHT","subjectType":"LINE","viewerCanReply":true,"viewerCanResolve":true,"viewerCanUnresolve":false,"comments":{"nodes":[]}}]},
			"timelineItems":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},
			"reviews":{"nodes":[]}
		}},"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}},
		"errors":[{"message":"You do not have permission to view this team","path":["repository","pullRequest","reviewRequests","nodes",0,"requestedReviewer"],"type":"FORBIDDEN"}]}`, hasNext, call)
		_, _ = w.Write([]byte(body))
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "someone")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if call != 2 {
		t.Fatalf("calls = %d, want 2", call)
	}

	count := 0
	for _, w := range res.Warnings {
		if w == "You do not have permission to view this team" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Warnings = %v, want the persistent error reported exactly once, got %d times", res.Warnings, count)
	}
}

// pullRequestPageResponse builds a minimal, valid pull_request.graphql
// response body around one custom reviewThreads page, used by the
// pagination-cap test below to fabricate an arbitrarily long sequence of
// pages without a fixture file per page.
func pullRequestPageResponse(threadID string, hasNextPage bool, endCursor string) string {
	next := "false"
	if hasNextPage {
		next = "true"
	}
	return fmt.Sprintf(`{"data":{"repository":{"pullRequest":{
		"id":"PR_cap","number":42,"title":"t","body":"","state":"OPEN","isDraft":false,
		"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"",
		"author":{"login":"octocat"},"repository":{"id":"R_cap","nameWithOwner":"acme/widgets"},
		"baseRefName":"main","headRefName":"feature-x","headRefOid":"abc",
		"additions":0,"deletions":0,"changedFiles":0,
		"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","url":"https://example.com/pr/42",
		"viewerCanUpdate":false,"viewerDidAuthor":false,"viewerCanClose":false,"viewerCanReopen":false,"viewerCanReact":false,
		"reactionGroups":[],"labels":{"nodes":[]},"reviewRequests":{"nodes":[]},"latestReviews":{"nodes":[]},
		"commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":null}}]},
		"reviewThreads":{"pageInfo":{"hasNextPage":%s,"endCursor":%q},"nodes":[
			{"id":%q,"isResolved":false,"isOutdated":false,"path":"a.go","line":1,"startLine":0,
			 "diffSide":"RIGHT","startDiffSide":"RIGHT","subjectType":"LINE",
			 "viewerCanReply":true,"viewerCanResolve":true,"viewerCanUnresolve":false,
			 "comments":{"nodes":[]}}
		]},
		"timelineItems":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},
		"reviews":{"nodes":[]}
	}},"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}}}`, next, endCursor, threadID)
}

func TestClient_PullRequest_PaginationCapWarning(t *testing.T) {
	calls := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// Every page still reports hasNextPage: true, so review threads
		// alone would paginate forever without maxDetailExtraPages.
		_, _ = w.Write([]byte(pullRequestPageResponse(fmt.Sprintf("RT_%d", calls), true, fmt.Sprintf("c%d", calls))))
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "octocat")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}

	// One initial page plus maxDetailExtraPages additional pages.
	wantCalls := 1 + maxDetailExtraPages
	if calls != wantCalls {
		t.Fatalf("calls = %d, want %d (must stop at the pagination cap)", calls, wantCalls)
	}
	if len(res.PR.ReviewThreads) != wantCalls {
		t.Errorf("ReviewThreads = %d entries, want %d", len(res.PR.ReviewThreads), wantCalls)
	}

	var found bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "review threads truncated") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want a review-threads truncation warning", res.Warnings)
	}
}

// asymmetricPageResponse builds a pull_request.graphql response whose
// review threads connection still has more pages (one node per call,
// named by threadID) while its timeline connection always reports the
// same single, already-seen node: this simulates what a real GraphQL
// server does when queried again with an unadvanced (frozen) cursor,
// exercising the "already finished connections must not be re-merged"
// path in Client.PullRequest that a fixture where every connection
// finishes on the same page would never reach.
func asymmetricPageResponse(threadID string, threadsHasNext bool) string {
	next := "false"
	if threadsHasNext {
		next = "true"
	}
	return fmt.Sprintf(`{"data":{"repository":{"pullRequest":{
		"id":"PR_asym","number":42,"title":"t","body":"","state":"OPEN","isDraft":false,
		"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"",
		"author":{"login":"octocat"},"repository":{"id":"R_asym","nameWithOwner":"acme/widgets"},
		"baseRefName":"main","headRefName":"feature-x","headRefOid":"abc",
		"additions":0,"deletions":0,"changedFiles":0,
		"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","url":"https://example.com/pr/42",
		"viewerCanUpdate":false,"viewerDidAuthor":false,"viewerCanClose":false,"viewerCanReopen":false,"viewerCanReact":false,
		"reactionGroups":[],"labels":{"nodes":[]},"reviewRequests":{"nodes":[]},"latestReviews":{"nodes":[]},
		"commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":null}}]},
		"reviewThreads":{"pageInfo":{"hasNextPage":%s,"endCursor":"tc"},"nodes":[
			{"id":%q,"isResolved":false,"isOutdated":false,"path":"a.go","line":1,"startLine":0,
			 "diffSide":"RIGHT","startDiffSide":"RIGHT","subjectType":"LINE",
			 "viewerCanReply":true,"viewerCanResolve":true,"viewerCanUnresolve":false,
			 "comments":{"nodes":[]}}
		]},
		"timelineItems":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
			{"__typename":"IssueComment","id":"IC_1","author":{"login":"octocat"},"body":"only comment",
			 "createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","url":"https://example.com/ic/1",
			 "reactionGroups":[],"viewerCanUpdate":false,"viewerCanDelete":false}
		]},
		"reviews":{"nodes":[]}
	}},"rateLimit":{"remaining":10,"resetAt":"2026-09-11T00:00:00Z"}}}`, next, threadID)
}

func TestClient_PullRequest_AsymmetricPaginationDoesNotDuplicateFinishedConnections(t *testing.T) {
	calls := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// Timeline finishes on call 1 (hasNextPage: false); reviewThreads
		// keeps going through call 3. On calls 2 and 3, a real server
		// queried again with timelineCursor still nil (since it never
		// advances once a connection is finished) would return IC_1
		// again — Client.PullRequest must discard that repeat rather
		// than appending a second copy.
		_, _ = w.Write([]byte(asymmetricPageResponse(fmt.Sprintf("RT_%d", calls), calls < 3)))
	})
	defer srv.Close()

	res, err := c.PullRequest(context.Background(), testPRRef(), "octocat")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (reviewThreads needed 3 pages)", calls)
	}
	if len(res.PR.ReviewThreads) != 3 {
		t.Errorf("ReviewThreads = %d entries, want 3 (RT_1, RT_2, RT_3)", len(res.PR.ReviewThreads))
	}
	if len(res.PR.Timeline) != 1 {
		t.Errorf("Timeline = %d entries, want exactly 1 (IC_1 must not be merged again on calls 2/3)", len(res.PR.Timeline))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none (well within the pagination cap)", res.Warnings)
	}
}

func TestPullRequestQuery_NoEnterpriseTeamFragment(t *testing.T) {
	// GHES's schema has no EnterpriseTeam type; an inline fragment on it
	// would fail query validation there entirely (see the comment in
	// pull_request.graphql). The word may appear in a comment explaining
	// the omission, but never as "on EnterpriseTeam" (an inline fragment).
	if strings.Contains(pullRequestQuery(), "on EnterpriseTeam") {
		t.Error("pull_request.graphql must not contain an inline fragment on EnterpriseTeam (breaks GHES)")
	}
}
