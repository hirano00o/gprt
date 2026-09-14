package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/logging"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// fakeGitHub is a minimal test double for store.GitHub: Viewer returns a
// fixed user, SearchPullRequests returns whatever was registered for the
// exact query text (built with gh.BuildSearchQuery so it matches what
// Store actually sends), and an empty result for anything else (including
// every LoadMore page, since these tests never scroll past one page). A
// test that needs to observe an in-flight fetch (for example the loading
// marker a section's header row should show) can arm block via SetBlock so
// SearchPullRequests waits until the test closes it. PullRequest similarly
// returns whatever was registered via SetPRResult/SetPRError for the
// requested ref, gated by a separate prBlock so a test can observe the PR
// detail fetch's own loading/stale window independently of the list's.
type fakeGitHub struct {
	mu          sync.Mutex
	viewer      model.User
	results     map[string]gh.SearchResult
	errs        map[string]error
	block       chan struct{}
	searchCalls int
	prResult    map[string]gh.DetailResult
	prErr       map[string]error
	prBlock     chan struct{}
	prCalls     int
	filesPages  map[string][]gh.FilesResult
	filesErrs   map[string]map[int]error
	filesBlock  chan struct{}

	addCommentErr      error
	addCommentBodies   []string
	addCommentBlock    chan struct{}
	updateCommentErr   error
	updateCommentCalls []struct{ id, body string }
	deleteCommentErr   error
	deleteCommentIDs   []string

	createPendingReviewErr     error
	createPendingReviewCalls   int
	addReviewNowErr            error
	addReviewNowCalls          []addReviewNowCall
	addReviewNowWithEventErr   error
	addReviewNowWithEventCalls []addReviewNowWithEventCall
	addReviewThreadErr         error
	addReviewThreadCalls       []gh.ThreadInput
	addThreadReplyErr          error
	addThreadReplyCalls        []addThreadReplyCall
	submitReviewErr            error
	submitReviewCalls          []submitReviewCall
	deletePendingReviewErr     error
	deletePendingReviewCalls   []string
	updateReviewCommentErr     error
	updateReviewCommentCalls   []updateReviewCommentCall
	deleteReviewCommentErr     error
	deleteReviewCommentIDs     []string
	resolveThreadErr           error
	resolveThreadIDs           []string
	unresolveThreadErr         error
	unresolveThreadIDs         []string

	addReactionErr        error
	addReactionCalls      []reactionCall
	removeReactionErr     error
	removeReactionCalls   []reactionCall
	mentionableUsers      map[model.RepoRef][]model.User
	mentionableUsersErr   error
	mentionableUsersBlock chan struct{}
	mentionableUsersCalls []mentionableUsersCall

	repositoryInfo          map[model.RepoRef]model.RepositoryInfo
	repositoryErr           map[model.RepoRef]error
	repositoryBlock         chan struct{}
	repositoryCalls         int
	labels                  map[model.RepoRef][]model.Label
	labelsErr               map[model.RepoRef]error
	templates               map[model.RepoRef][]model.PullRequestTemplate
	branchesResults         map[string][]model.Branch
	branchesErr             error
	branchesCalls           []branchesCall
	branchesBlock           chan struct{}
	teamsResults            map[string][]model.Team
	teamsErr                error
	teamsCalls              []teamsCall
	updatePullRequestCalls  []updatePullRequestCall
	updatePullRequestErr    error
	requestReviewersCalls   []requestReviewersCall
	requestReviewersErr     error
	markReadyForReviewCalls []string
	markReadyForReviewErr   error
	convertToDraftCalls     []string
	convertToDraftErr       error
	mergePullRequestCalls   []mergePullRequestCall
	mergePullRequestErr     error
	closePullRequestIDs     []string
	closePullRequestErr     error
	reopenPullRequestIDs    []string
	reopenPullRequestErr    error

	viewerRepositories      []model.RepositorySummary
	viewerRepositoriesErr   error
	createPullRequestCalls  []createPullRequestCall
	createPullRequestErr    error
	createPullRequestResult model.PullRequest
}

type branchesCall struct {
	repo  model.RepoRef
	query string
	first int
}

type teamsCall struct {
	org, query string
	first      int
}

type updatePullRequestCall struct {
	id string
	in gh.UpdatePullRequestInput
}

type requestReviewersCall struct {
	id               string
	userIDs, teamIDs []string
	union            bool
}

type mergePullRequestCall struct {
	id              string
	method          model.MergeMethod
	headline, body  *string
	expectedHeadOID string
}

// The review-mutation recorder call shapes below are named types (rather
// than the inline anonymous structs the rest of fakeGitHub's recorders
// use) purely so their own accessor methods below have a return type to
// name.
type addReviewNowCall struct {
	prID    string
	threads []gh.DraftThread
	body    string
}

type addReviewNowWithEventCall struct {
	prID  string
	event model.ReviewEvent
	body  string
}

type addThreadReplyCall struct {
	threadID, body, pendingReviewID string
}

type submitReviewCall struct {
	reviewID string
	event    model.ReviewEvent
	body     string
}

type updateReviewCommentCall struct{ id, body string }

type reactionCall struct {
	subjectID string
	content   model.ReactionContent
}

type mentionableUsersCall struct {
	repo  model.RepoRef
	query string
	first int
}

type createPullRequestCall struct {
	in gh.CreatePullRequestInput
}

func (f *fakeGitHub) Viewer(context.Context) (model.User, model.RateLimit, error) {
	return f.viewer, model.RateLimit{}, nil
}

func (f *fakeGitHub) PullRequest(ctx context.Context, ref model.PRRef, _ string) (gh.DetailResult, error) {
	f.mu.Lock()
	f.prCalls++
	block := f.prBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return gh.DetailResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.prErr[ref.Key()]; ok {
		return gh.DetailResult{}, err
	}
	return f.prResult[ref.Key()], nil
}

// SetPRResult changes what PullRequest returns for ref, clearing any error
// previously armed for it via SetPRError.
func (f *fakeGitHub) SetPRResult(ref model.PRRef, res gh.DetailResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prResult == nil {
		f.prResult = map[string]gh.DetailResult{}
	}
	f.prResult[ref.Key()] = res
	delete(f.prErr, ref.Key())
}

// SetPRError makes PullRequest fail with err for ref.
func (f *fakeGitHub) SetPRError(ref model.PRRef, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prErr == nil {
		f.prErr = map[string]error{}
	}
	f.prErr[ref.Key()] = err
}

// SetPRBlock arms (or, passed nil, disarms) a gate every subsequent
// PullRequest call waits on before returning.
func (f *fakeGitHub) SetPRBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prBlock = ch
}

// ChangedFiles returns whatever page was registered for ref via
// SetFilesPages (1-based, matching the REST endpoint's own "page"
// parameter), or an empty result for a ref/page nothing was registered
// for.
func (f *fakeGitHub) ChangedFiles(ctx context.Context, ref model.PRRef, page int, _ string) (gh.FilesResult, error) {
	f.mu.Lock()
	block := f.filesBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return gh.FilesResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if errs, ok := f.filesErrs[ref.Key()]; ok {
		if err, ok := errs[page]; ok {
			return gh.FilesResult{}, err
		}
	}
	pages := f.filesPages[ref.Key()]
	if page < 1 || page > len(pages) {
		return gh.FilesResult{}, nil
	}
	return pages[page-1], nil
}

// SetAddCommentBlock arms (or, passed nil, disarms) a gate every
// subsequent AddIssueComment call waits on before returning — used to
// hold a mutation "in flight" long enough for a test to observe
// Store.Mutating() before it resolves.
func (f *fakeGitHub) SetAddCommentBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCommentBlock = ch
}

// AddIssueComment records body and returns addCommentErr if armed (via
// SetAddCommentError), otherwise a synthesized comment with that body.
func (f *fakeGitHub) AddIssueComment(ctx context.Context, _, body string) (model.IssueComment, model.RateLimit, error) {
	f.mu.Lock()
	block := f.addCommentBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return model.IssueComment{}, model.RateLimit{}, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCommentBodies = append(f.addCommentBodies, body)
	if f.addCommentErr != nil {
		return model.IssueComment{}, model.RateLimit{}, f.addCommentErr
	}
	return model.IssueComment{ID: "IC_new", Author: model.User{Login: "octocat"}, Body: body, ViewerCanUpdate: true, ViewerCanDelete: true}, model.RateLimit{}, nil
}

// SetAddCommentError makes AddIssueComment fail with err.
func (f *fakeGitHub) SetAddCommentError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCommentErr = err
}

// AddCommentBodies returns every body AddIssueComment has been called
// with so far, in call order.
func (f *fakeGitHub) AddCommentBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.addCommentBodies...)
}

// UpdateIssueComment records (id, body) and returns updateCommentErr if
// armed, otherwise a synthesized comment with that body.
func (f *fakeGitHub) UpdateIssueComment(_ context.Context, id, body string) (model.IssueComment, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCommentCalls = append(f.updateCommentCalls, struct{ id, body string }{id, body})
	if f.updateCommentErr != nil {
		return model.IssueComment{}, model.RateLimit{}, f.updateCommentErr
	}
	return model.IssueComment{ID: id, Author: model.User{Login: "octocat"}, Body: body, ViewerCanUpdate: true, ViewerCanDelete: true}, model.RateLimit{}, nil
}

// SetUpdateCommentError makes UpdateIssueComment fail with err.
func (f *fakeGitHub) SetUpdateCommentError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCommentErr = err
}

// UpdateCommentBodies returns every body UpdateIssueComment has been
// called with so far, in call order.
func (f *fakeGitHub) UpdateCommentBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.updateCommentCalls))
	for i, c := range f.updateCommentCalls {
		out[i] = c.body
	}
	return out
}

// DeleteIssueComment records id and returns deleteCommentErr if armed.
func (f *fakeGitHub) DeleteIssueComment(_ context.Context, id string) (model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCommentIDs = append(f.deleteCommentIDs, id)
	if f.deleteCommentErr != nil {
		return model.RateLimit{}, f.deleteCommentErr
	}
	return model.RateLimit{}, nil
}

// DeleteCommentIDs returns every ID DeleteIssueComment has been called
// with so far, in call order.
func (f *fakeGitHub) DeleteCommentIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteCommentIDs...)
}

// Review mutations (see internal/store/review.go): each records its call
// (mirroring AddIssueComment/UpdateIssueComment/DeleteIssueComment above)
// and returns a synthesized, minimally-plausible result unless the
// matching Set*Error was armed.
func (f *fakeGitHub) CreatePendingReview(context.Context, string) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createPendingReviewCalls++
	if f.createPendingReviewErr != nil {
		return model.Review{}, model.RateLimit{}, f.createPendingReviewErr
	}
	return model.Review{ID: "PVR_new", State: model.ReviewStatePending}, model.RateLimit{}, nil
}

func (f *fakeGitHub) AddReviewNow(_ context.Context, prID string, threads []gh.DraftThread, body string) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewNowCalls = append(f.addReviewNowCalls, addReviewNowCall{prID, threads, body})
	if f.addReviewNowErr != nil {
		return model.Review{}, model.RateLimit{}, f.addReviewNowErr
	}
	return model.Review{ID: "PVR_submitted", State: model.ReviewStateCommented}, model.RateLimit{}, nil
}

func (f *fakeGitHub) AddReviewNowWithEvent(_ context.Context, prID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewNowWithEventCalls = append(f.addReviewNowWithEventCalls, addReviewNowWithEventCall{prID, event, body})
	if f.addReviewNowWithEventErr != nil {
		return model.Review{}, model.RateLimit{}, f.addReviewNowWithEventErr
	}
	return model.Review{ID: "PVR_submitted"}, model.RateLimit{}, nil
}

// SetAddReviewNowWithEventError makes AddReviewNowWithEvent fail with err.
func (f *fakeGitHub) SetAddReviewNowWithEventError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewNowWithEventErr = err
}

// AddReviewNowWithEventCalls returns every AddReviewNowWithEvent call so
// far, in call order.
func (f *fakeGitHub) AddReviewNowWithEventCalls() []addReviewNowWithEventCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]addReviewNowWithEventCall(nil), f.addReviewNowWithEventCalls...)
}

func (f *fakeGitHub) AddReviewThread(_ context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewThreadCalls = append(f.addReviewThreadCalls, in)
	if f.addReviewThreadErr != nil {
		return model.ReviewThread{}, model.RateLimit{}, f.addReviewThreadErr
	}
	return model.ReviewThread{
		ID: "RT_new", Path: in.Path, Line: in.Line, Side: in.Side,
		StartLine: in.StartLine, StartSide: in.StartSide, SubjectType: in.SubjectType,
		ViewerCanResolve: true,
		Comments: []model.ReviewComment{{
			ID: "RC_new", Author: model.User{Login: "octocat"}, Body: in.Body,
			State: model.ReviewCommentStatePending, ViewerCanUpdate: true, ViewerCanDelete: true,
		}},
	}, model.RateLimit{}, nil
}

func (f *fakeGitHub) AddThreadReply(_ context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addThreadReplyCalls = append(f.addThreadReplyCalls, addThreadReplyCall{threadID, body, pendingReviewID})
	if f.addThreadReplyErr != nil {
		return model.ReviewComment{}, model.RateLimit{}, f.addThreadReplyErr
	}
	state := model.ReviewCommentStateSubmitted
	if pendingReviewID != "" {
		state = model.ReviewCommentStatePending
	}
	return model.ReviewComment{ID: "RC_reply", Author: model.User{Login: "octocat"}, Body: body, State: state, ViewerCanUpdate: true, ViewerCanDelete: true}, model.RateLimit{}, nil
}

func (f *fakeGitHub) SubmitReview(_ context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitReviewCalls = append(f.submitReviewCalls, submitReviewCall{reviewID, event, body})
	if f.submitReviewErr != nil {
		return model.Review{}, model.RateLimit{}, f.submitReviewErr
	}
	return model.Review{ID: reviewID}, model.RateLimit{}, nil
}

// SetSubmitReviewError makes SubmitReview fail with err.
func (f *fakeGitHub) SetSubmitReviewError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitReviewErr = err
}

// SubmitReviewCalls returns every SubmitReview call so far, in call order.
func (f *fakeGitHub) SubmitReviewCalls() []submitReviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]submitReviewCall(nil), f.submitReviewCalls...)
}

func (f *fakeGitHub) DeletePendingReview(_ context.Context, reviewID string) (model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletePendingReviewCalls = append(f.deletePendingReviewCalls, reviewID)
	return model.RateLimit{}, f.deletePendingReviewErr
}

func (f *fakeGitHub) UpdateReviewComment(_ context.Context, id, body string) (model.ReviewComment, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateReviewCommentCalls = append(f.updateReviewCommentCalls, updateReviewCommentCall{id, body})
	if f.updateReviewCommentErr != nil {
		return model.ReviewComment{}, model.RateLimit{}, f.updateReviewCommentErr
	}
	return model.ReviewComment{ID: id, Author: model.User{Login: "octocat"}, Body: body, ViewerCanUpdate: true, ViewerCanDelete: true}, model.RateLimit{}, nil
}

// UpdateReviewCommentBodies returns every body UpdateReviewComment has been
// called with so far, in call order.
func (f *fakeGitHub) UpdateReviewCommentBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.updateReviewCommentCalls))
	for i, c := range f.updateReviewCommentCalls {
		out[i] = c.body
	}
	return out
}

func (f *fakeGitHub) DeleteReviewComment(_ context.Context, id string) (model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteReviewCommentIDs = append(f.deleteReviewCommentIDs, id)
	return model.RateLimit{}, f.deleteReviewCommentErr
}

func (f *fakeGitHub) ResolveThread(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveThreadIDs = append(f.resolveThreadIDs, threadID)
	if f.resolveThreadErr != nil {
		return model.ReviewThread{}, model.RateLimit{}, f.resolveThreadErr
	}
	return model.ReviewThread{ID: threadID, IsResolved: true, ViewerCanUnresolve: true}, model.RateLimit{}, nil
}

func (f *fakeGitHub) UnresolveThread(_ context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unresolveThreadIDs = append(f.unresolveThreadIDs, threadID)
	if f.unresolveThreadErr != nil {
		return model.ReviewThread{}, model.RateLimit{}, f.unresolveThreadErr
	}
	return model.ReviewThread{ID: threadID, IsResolved: false, ViewerCanResolve: true}, model.RateLimit{}, nil
}

// AddReaction records subjectID/content and returns a synthesized,
// minimally-plausible result unless SetAddReactionError was armed: a
// single-group result for content with ViewerHasReacted true, matching
// what Store.applyReactionGroups needs to reflect a successful toggle.
func (f *fakeGitHub) AddReaction(_ context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReactionCalls = append(f.addReactionCalls, reactionCall{subjectID, content})
	if f.addReactionErr != nil {
		return nil, model.RateLimit{}, f.addReactionErr
	}
	return []model.ReactionGroup{{Content: content, Count: 1, ViewerHasReacted: true}}, model.RateLimit{}, nil
}

// RemoveReaction records subjectID/content and returns a synthesized,
// minimally-plausible result (the group removed entirely, mirroring
// GitHub's own behaviour once a reaction's count reaches zero) unless
// SetRemoveReactionError was armed.
func (f *fakeGitHub) RemoveReaction(_ context.Context, subjectID string, content model.ReactionContent) ([]model.ReactionGroup, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeReactionCalls = append(f.removeReactionCalls, reactionCall{subjectID, content})
	if f.removeReactionErr != nil {
		return nil, model.RateLimit{}, f.removeReactionErr
	}
	return nil, model.RateLimit{}, nil
}

// SetMentionableUsers registers the users MentionableUsers returns for
// repo (an unregistered repo returns nil, matching a repository with no
// mentionable-users fetch configured for this test).
func (f *fakeGitHub) SetMentionableUsers(repo model.RepoRef, users []model.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mentionableUsers == nil {
		f.mentionableUsers = map[model.RepoRef][]model.User{}
	}
	f.mentionableUsers[repo] = users
}

// SetMentionableUsersError makes every MentionableUsers call fail with err.
func (f *fakeGitHub) SetMentionableUsersError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mentionableUsersErr = err
}

// SetMentionableUsersBlock arms (or, passed nil, disarms) a gate every
// subsequent MentionableUsers call waits on before returning — used to
// hold the fetch "in flight" long enough for a test to observe a
// consumer's own loading state (for example the edit-reviewers overlay's
// "(loading users…)" row) before it resolves.
func (f *fakeGitHub) SetMentionableUsersBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mentionableUsersBlock = ch
}

func (f *fakeGitHub) MentionableUsers(ctx context.Context, repo model.RepoRef, query string, first int) ([]model.User, model.RateLimit, error) {
	f.mu.Lock()
	f.mentionableUsersCalls = append(f.mentionableUsersCalls, mentionableUsersCall{repo, query, first})
	block := f.mentionableUsersBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, model.RateLimit{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mentionableUsersErr != nil {
		return nil, model.RateLimit{}, f.mentionableUsersErr
	}
	return f.mentionableUsers[repo], model.RateLimit{}, nil
}

// AddReactionCalls returns every AddReaction call so far, in call order.
func (f *fakeGitHub) AddReactionCalls() []reactionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]reactionCall(nil), f.addReactionCalls...)
}

// RemoveReactionCalls returns every RemoveReaction call so far, in call
// order.
func (f *fakeGitHub) RemoveReactionCalls() []reactionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]reactionCall(nil), f.removeReactionCalls...)
}

// MentionableUsersCalls returns every MentionableUsers call so far, in
// call order.
func (f *fakeGitHub) MentionableUsersCalls() []mentionableUsersCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mentionableUsersCall(nil), f.mentionableUsersCalls...)
}

// CreatePendingReviewCalls returns how many times CreatePendingReview has
// been called so far.
func (f *fakeGitHub) CreatePendingReviewCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createPendingReviewCalls
}

// AddReviewNowCalls returns every AddReviewNow call so far, in call order.
func (f *fakeGitHub) AddReviewNowCalls() []addReviewNowCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]addReviewNowCall(nil), f.addReviewNowCalls...)
}

// AddReviewThreadCalls returns every AddReviewThread call's own input so
// far, in call order.
func (f *fakeGitHub) AddReviewThreadCalls() []gh.ThreadInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gh.ThreadInput(nil), f.addReviewThreadCalls...)
}

// AddThreadReplyCalls returns every AddThreadReply call so far, in call
// order.
func (f *fakeGitHub) AddThreadReplyCalls() []addThreadReplyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]addThreadReplyCall(nil), f.addThreadReplyCalls...)
}

// UpdateReviewCommentCalls returns every UpdateReviewComment call so far,
// in call order.
func (f *fakeGitHub) UpdateReviewCommentCalls() []updateReviewCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]updateReviewCommentCall(nil), f.updateReviewCommentCalls...)
}

// DeleteReviewCommentIDs returns every ID DeleteReviewComment has been
// called with so far, in call order.
func (f *fakeGitHub) DeleteReviewCommentIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteReviewCommentIDs...)
}

// DeletePendingReviewCalls returns every ID DeletePendingReview has been
// called with so far, in call order.
func (f *fakeGitHub) DeletePendingReviewCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletePendingReviewCalls...)
}

// ResolveThreadIDs returns every ID ResolveThread has been called with so
// far, in call order.
func (f *fakeGitHub) ResolveThreadIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resolveThreadIDs...)
}

// UnresolveThreadIDs returns every ID UnresolveThread has been called with
// so far, in call order.
func (f *fakeGitHub) UnresolveThreadIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.unresolveThreadIDs...)
}

// SetFilesPages registers the sequence of REST pages ChangedFiles returns
// for ref, in page order (pages[0] is page 1, and so on).
func (f *fakeGitHub) SetFilesPages(ref model.PRRef, pages []gh.FilesResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.filesPages == nil {
		f.filesPages = map[string][]gh.FilesResult{}
	}
	f.filesPages[ref.Key()] = pages
}

// SetFilesError makes ChangedFiles fail with err for ref's given page
// (1-based).
func (f *fakeGitHub) SetFilesError(ref model.PRRef, page int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.filesErrs == nil {
		f.filesErrs = map[string]map[int]error{}
	}
	if f.filesErrs[ref.Key()] == nil {
		f.filesErrs[ref.Key()] = map[int]error{}
	}
	f.filesErrs[ref.Key()][page] = err
}

// SetFilesBlock arms (or, passed nil, disarms) a gate every subsequent
// ChangedFiles call waits on before returning.
func (f *fakeGitHub) SetFilesBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filesBlock = ch
}

// PRCalls returns how many times PullRequest has been called so far.
func (f *fakeGitHub) PRCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prCalls
}

// SearchCalls returns how many times SearchPullRequests has been called so
// far (across every section/query), used to detect a fresh list refresh
// (Store.Refresh) without needing a query-scoped counter.
func (f *fakeGitHub) SearchCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searchCalls
}

func (f *fakeGitHub) SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error) {
	f.mu.Lock()
	f.searchCalls++
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return gh.SearchResult{}, ctx.Err()
		}
	}
	if cursor != "" {
		return gh.SearchResult{}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[query]; ok {
		return gh.SearchResult{}, err
	}
	return f.results[query], nil
}

// SetBlock arms (or, passed nil, disarms) a gate every subsequent
// SearchPullRequests call waits on before returning.
func (f *fakeGitHub) SetBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = ch
}

// SetResult changes what SearchPullRequests returns for query, clearing any
// error previously armed for it via SetError.
func (f *fakeGitHub) SetResult(query string, res gh.SearchResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		f.results = map[string]gh.SearchResult{}
	}
	f.results[query] = res
	delete(f.errs, query)
}

// SetError makes SearchPullRequests fail with err for query.
func (f *fakeGitHub) SetError(query string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[query] = err
}

// M5 repository metadata / branch / team / edit-merge-create/create reads
// and mutations (see internal/store's repository.go, viewer_repositories.go,
// search.go, pr_edit.go, pr_create.go).

// SetRepositoryInfo registers the RepositoryInfo Repository returns for
// repo, clearing any error previously armed for it via SetRepositoryError.
func (f *fakeGitHub) SetRepositoryInfo(repo model.RepoRef, info model.RepositoryInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.repositoryInfo == nil {
		f.repositoryInfo = map[model.RepoRef]model.RepositoryInfo{}
	}
	f.repositoryInfo[repo] = info
	delete(f.repositoryErr, repo)
}

// SetRepositoryError makes Repository fail with err for repo.
func (f *fakeGitHub) SetRepositoryError(repo model.RepoRef, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.repositoryErr == nil {
		f.repositoryErr = map[model.RepoRef]error{}
	}
	f.repositoryErr[repo] = err
}

// SetRepositoryBlock arms (or, passed nil, disarms) a gate every
// subsequent Repository call waits on before returning — used to hold a
// repository-metadata fetch "in flight" long enough for a test to observe
// the merge dialog's own loading placeholder before it resolves.
func (f *fakeGitHub) SetRepositoryBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repositoryBlock = ch
}

// RepositoryCalls returns how many times Repository has been called so
// far (across every repo), used to assert that a failed fetch is actually
// retried rather than merely leaving a "still loading" state forever.
func (f *fakeGitHub) RepositoryCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repositoryCalls
}

func (f *fakeGitHub) Repository(ctx context.Context, repo model.RepoRef) (model.RepositoryInfo, model.RateLimit, error) {
	f.mu.Lock()
	f.repositoryCalls++
	block := f.repositoryBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return model.RepositoryInfo{}, model.RateLimit{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.repositoryErr[repo]; ok {
		return model.RepositoryInfo{}, model.RateLimit{}, err
	}
	return f.repositoryInfo[repo], model.RateLimit{}, nil
}

// SetLabels registers the labels Labels returns for repo.
func (f *fakeGitHub) SetLabels(repo model.RepoRef, labels []model.Label) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labels == nil {
		f.labels = map[model.RepoRef][]model.Label{}
	}
	f.labels[repo] = labels
}

// SetLabelsError makes Labels fail with err for repo.
func (f *fakeGitHub) SetLabelsError(repo model.RepoRef, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labelsErr == nil {
		f.labelsErr = map[model.RepoRef]error{}
	}
	f.labelsErr[repo] = err
}

func (f *fakeGitHub) Labels(_ context.Context, repo model.RepoRef) ([]model.Label, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.labelsErr[repo]; ok {
		return nil, model.RateLimit{}, err
	}
	return f.labels[repo], model.RateLimit{}, nil
}

// SetTemplates registers the templates PullRequestTemplates returns for
// repo.
func (f *fakeGitHub) SetTemplates(repo model.RepoRef, templates []model.PullRequestTemplate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.templates == nil {
		f.templates = map[model.RepoRef][]model.PullRequestTemplate{}
	}
	f.templates[repo] = templates
}

func (f *fakeGitHub) PullRequestTemplates(_ context.Context, repo model.RepoRef) ([]model.PullRequestTemplate, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.templates[repo], model.RateLimit{}, nil
}

// SetViewerRepositories makes ViewerRepositories return repos.
func (f *fakeGitHub) SetViewerRepositories(repos []model.RepositorySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewerRepositories = repos
}

// SetViewerRepositoriesError makes every ViewerRepositories call fail with
// err.
func (f *fakeGitHub) SetViewerRepositoriesError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewerRepositoriesErr = err
}

func (f *fakeGitHub) ViewerRepositories(context.Context, int) ([]model.RepositorySummary, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.viewerRepositoriesErr != nil {
		return nil, model.RateLimit{}, f.viewerRepositoriesErr
	}
	return f.viewerRepositories, model.RateLimit{}, nil
}

// SetBranches registers the branches Branches returns for the given typed
// query (an empty query is the unfiltered case).
func (f *fakeGitHub) SetBranches(query string, branches []model.Branch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.branchesResults == nil {
		f.branchesResults = map[string][]model.Branch{}
	}
	f.branchesResults[query] = branches
}

// SetBranchesError makes every Branches call fail with err.
func (f *fakeGitHub) SetBranchesError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchesErr = err
}

// BranchesCalls returns every Branches call so far, in call order.
func (f *fakeGitHub) BranchesCalls() []branchesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]branchesCall(nil), f.branchesCalls...)
}

// SetBranchesBlock makes every subsequent Branches call wait until ch is
// closed (or its context is cancelled) before returning — used to control
// exactly when a stale SearchBranches call's own result becomes available,
// so a test can assert it is dropped rather than merely hoping the real
// goroutine scheduling happens to order two calls the way it needs.
func (f *fakeGitHub) SetBranchesBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchesBlock = ch
}

func (f *fakeGitHub) Branches(ctx context.Context, repo model.RepoRef, query string, first int) ([]model.Branch, model.RateLimit, error) {
	f.mu.Lock()
	block := f.branchesBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, model.RateLimit{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchesCalls = append(f.branchesCalls, branchesCall{repo: repo, query: query, first: first})
	if f.branchesErr != nil {
		return nil, model.RateLimit{}, f.branchesErr
	}
	return f.branchesResults[query], model.RateLimit{}, nil
}

// SetTeams registers the teams Teams returns for the given typed query (an
// empty query is the unfiltered case).
func (f *fakeGitHub) SetTeams(query string, teams []model.Team) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.teamsResults == nil {
		f.teamsResults = map[string][]model.Team{}
	}
	f.teamsResults[query] = teams
}

// SetTeamsError makes every Teams call fail with err.
func (f *fakeGitHub) SetTeamsError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamsErr = err
}

// TeamsCalls returns every Teams call so far, in call order.
func (f *fakeGitHub) TeamsCalls() []teamsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]teamsCall(nil), f.teamsCalls...)
}

func (f *fakeGitHub) Teams(_ context.Context, org, query string, first int) ([]model.Team, model.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamsCalls = append(f.teamsCalls, teamsCall{org: org, query: query, first: first})
	if f.teamsErr != nil {
		return nil, model.RateLimit{}, f.teamsErr
	}
	return f.teamsResults[query], model.RateLimit{}, nil
}

// SetUpdatePullRequestError makes every UpdatePullRequest call fail with
// err.
func (f *fakeGitHub) SetUpdatePullRequestError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updatePullRequestErr = err
}

// UpdatePullRequestCalls returns every UpdatePullRequest call so far, in
// call order.
func (f *fakeGitHub) UpdatePullRequestCalls() []updatePullRequestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]updatePullRequestCall(nil), f.updatePullRequestCalls...)
}

// UpdatePullRequest records the call and synthesizes a minimally-plausible
// result, echoing back whichever fields in were actually set (nil means
// "leave unchanged", mirroring UpdatePullRequestInput's own convention —
// see gh.UpdatePullRequestInput's doc comment) — good enough for a test to
// assert on Store's own optimistic apply without needing the fake to track
// each pull request's full, evolving state.
func (f *fakeGitHub) UpdatePullRequest(
	_ context.Context, id string, in gh.UpdatePullRequestInput,
) (model.PullRequest, model.RateLimit, error) {
	f.mu.Lock()
	f.updatePullRequestCalls = append(f.updatePullRequestCalls, updatePullRequestCall{id: id, in: in})
	err := f.updatePullRequestErr
	f.mu.Unlock()
	if err != nil {
		return model.PullRequest{}, model.RateLimit{}, err
	}
	result := model.PullRequest{ID: id, UpdatedAt: time.Now()}
	if in.Title != nil {
		result.Title = *in.Title
	}
	if in.Body != nil {
		result.Body = *in.Body
	}
	if in.BaseRefName != nil {
		result.BaseRefName = *in.BaseRefName
	}
	if in.LabelIDs != nil {
		for _, labelID := range *in.LabelIDs {
			result.Labels = append(result.Labels, model.Label{ID: labelID})
		}
	}
	return result, model.RateLimit{}, nil
}

// SetRequestReviewersError makes every RequestReviewers call fail with err.
func (f *fakeGitHub) SetRequestReviewersError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestReviewersErr = err
}

// RequestReviewersCalls returns every RequestReviewers call so far, in
// call order.
func (f *fakeGitHub) RequestReviewersCalls() []requestReviewersCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]requestReviewersCall(nil), f.requestReviewersCalls...)
}

func (f *fakeGitHub) RequestReviewers(
	_ context.Context, id string, userIDs, teamIDs []string, union bool,
) ([]model.Reviewer, model.RateLimit, error) {
	f.mu.Lock()
	f.requestReviewersCalls = append(
		f.requestReviewersCalls, requestReviewersCall{id: id, userIDs: userIDs, teamIDs: teamIDs, union: union},
	)
	err := f.requestReviewersErr
	f.mu.Unlock()
	if err != nil {
		return nil, model.RateLimit{}, err
	}
	reviewers := make([]model.Reviewer, 0, len(userIDs)+len(teamIDs))
	for _, uid := range userIDs {
		reviewers = append(reviewers, model.Reviewer{ID: uid, Kind: model.ReviewerKindUser})
	}
	for _, tid := range teamIDs {
		reviewers = append(reviewers, model.Reviewer{ID: tid, Kind: model.ReviewerKindTeam})
	}
	return reviewers, model.RateLimit{}, nil
}

// SetMarkReadyForReviewError makes every MarkReadyForReview call fail with
// err.
func (f *fakeGitHub) SetMarkReadyForReviewError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markReadyForReviewErr = err
}

// MarkReadyForReviewCalls returns every ID MarkReadyForReview has been
// called with so far, in call order.
func (f *fakeGitHub) MarkReadyForReviewCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.markReadyForReviewCalls...)
}

func (f *fakeGitHub) MarkReadyForReview(_ context.Context, id string) (bool, model.RateLimit, error) {
	f.mu.Lock()
	f.markReadyForReviewCalls = append(f.markReadyForReviewCalls, id)
	err := f.markReadyForReviewErr
	f.mu.Unlock()
	if err != nil {
		return false, model.RateLimit{}, err
	}
	return false, model.RateLimit{}, nil
}

// SetConvertToDraftError makes every ConvertToDraft call fail with err.
func (f *fakeGitHub) SetConvertToDraftError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.convertToDraftErr = err
}

// ConvertToDraftCalls returns every ID ConvertToDraft has been called with
// so far, in call order.
func (f *fakeGitHub) ConvertToDraftCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.convertToDraftCalls...)
}

func (f *fakeGitHub) ConvertToDraft(_ context.Context, id string) (bool, model.RateLimit, error) {
	f.mu.Lock()
	f.convertToDraftCalls = append(f.convertToDraftCalls, id)
	err := f.convertToDraftErr
	f.mu.Unlock()
	if err != nil {
		return false, model.RateLimit{}, err
	}
	return true, model.RateLimit{}, nil
}

// SetMergePullRequestError makes every MergePullRequest call fail with err.
func (f *fakeGitHub) SetMergePullRequestError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mergePullRequestErr = err
}

// MergePullRequestCalls returns every MergePullRequest call so far, in
// call order.
func (f *fakeGitHub) MergePullRequestCalls() []mergePullRequestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mergePullRequestCall(nil), f.mergePullRequestCalls...)
}

func (f *fakeGitHub) MergePullRequest(
	_ context.Context, id string, method model.MergeMethod, commitHeadline, commitBody *string, expectedHeadOID string,
) (model.PullRequest, model.RateLimit, error) {
	f.mu.Lock()
	f.mergePullRequestCalls = append(f.mergePullRequestCalls, mergePullRequestCall{
		id: id, method: method, headline: commitHeadline, body: commitBody, expectedHeadOID: expectedHeadOID,
	})
	err := f.mergePullRequestErr
	f.mu.Unlock()
	if err != nil {
		return model.PullRequest{}, model.RateLimit{}, err
	}
	return model.PullRequest{ID: id, State: model.PRStateMerged, Merged: true, MergedAt: time.Now()}, model.RateLimit{}, nil
}

// SetClosePullRequestError makes every ClosePullRequest call fail with err.
func (f *fakeGitHub) SetClosePullRequestError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closePullRequestErr = err
}

// ClosePullRequestIDs returns every ID ClosePullRequest has been called
// with so far, in call order.
func (f *fakeGitHub) ClosePullRequestIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closePullRequestIDs...)
}

func (f *fakeGitHub) ClosePullRequest(_ context.Context, id string) (model.PRState, model.RateLimit, error) {
	f.mu.Lock()
	f.closePullRequestIDs = append(f.closePullRequestIDs, id)
	err := f.closePullRequestErr
	f.mu.Unlock()
	if err != nil {
		return "", model.RateLimit{}, err
	}
	return model.PRStateClosed, model.RateLimit{}, nil
}

// SetReopenPullRequestError makes every ReopenPullRequest call fail with
// err.
func (f *fakeGitHub) SetReopenPullRequestError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reopenPullRequestErr = err
}

// ReopenPullRequestIDs returns every ID ReopenPullRequest has been called
// with so far, in call order.
func (f *fakeGitHub) ReopenPullRequestIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reopenPullRequestIDs...)
}

func (f *fakeGitHub) ReopenPullRequest(_ context.Context, id string) (model.PRState, model.RateLimit, error) {
	f.mu.Lock()
	f.reopenPullRequestIDs = append(f.reopenPullRequestIDs, id)
	err := f.reopenPullRequestErr
	f.mu.Unlock()
	if err != nil {
		return "", model.RateLimit{}, err
	}
	return model.PRStateOpen, model.RateLimit{}, nil
}

// SetCreatePullRequestResult makes CreatePullRequest succeed, returning pr.
func (f *fakeGitHub) SetCreatePullRequestResult(pr model.PullRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createPullRequestResult = pr
	f.createPullRequestErr = nil
}

// SetCreatePullRequestError makes every CreatePullRequest call fail with
// err.
func (f *fakeGitHub) SetCreatePullRequestError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createPullRequestErr = err
}

// CreatePullRequestCalls returns every CreatePullRequest call so far, in
// call order.
func (f *fakeGitHub) CreatePullRequestCalls() []createPullRequestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]createPullRequestCall(nil), f.createPullRequestCalls...)
}

func (f *fakeGitHub) CreatePullRequest(_ context.Context, in gh.CreatePullRequestInput) (model.PullRequest, model.RateLimit, error) {
	f.mu.Lock()
	f.createPullRequestCalls = append(f.createPullRequestCalls, createPullRequestCall{in: in})
	err := f.createPullRequestErr
	result := f.createPullRequestResult
	f.mu.Unlock()
	if err != nil {
		return model.PullRequest{}, model.RateLimit{}, err
	}
	return result, model.RateLimit{}, nil
}

func fixtureRef(number int) model.PRRef {
	return model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}, Number: number}
}

func fixturePR(number int, title string) model.PullRequest {
	ref := fixtureRef(number)
	return model.PullRequest{
		ID:        fmt.Sprintf("PR_fixture_%d", number),
		Ref:       ref,
		Title:     title,
		Author:    model.User{Login: "alice"},
		State:     model.PRStateOpen,
		UpdatedAt: time.Now(),
		URL:       fmt.Sprintf("https://github.com/%s/pull/%d", ref.Repo.NameWithOwner(), ref.Number),
	}
}

// fixtureDetailPR builds a rich pull request detail fixture for ref,
// covering every block the PR tab renders: a body with a heading and a
// fenced code block, two labels, two reviewers (one approved, one
// requested), three checks (success/failure/pending, the failing one with
// a details URL), a timeline with an issue comment (with a reaction), a
// review with a body, a commit, and a RenamedTitleEvent, and two review
// threads (one resolved).
func fixtureDetailPR(ref model.PRRef) model.PullRequest {
	now := time.Now()
	return model.PullRequest{
		ID:               fmt.Sprintf("PR_detail_%d", ref.Number),
		Ref:              ref,
		Title:            "Add widget support",
		Body:             "# Summary\n\nThis adds widgets.\n\n```\nfunc Widget() {}\n```\n",
		Author:           model.User{Login: "alice"},
		State:            model.PRStateOpen,
		MergeStateStatus: model.MergeStateStatusClean,
		BaseRefName:      "main",
		HeadRefName:      "alice/widgets",
		ReviewDecision:   model.ReviewDecisionReviewRequired,
		Labels:           []model.Label{{Name: "backend"}, {Name: "urgent"}},
		ReviewRequests:   []model.Reviewer{{Login: "bob", Kind: model.ReviewerKindUser}},
		LatestReviews:    []model.Review{{Author: model.User{Login: "carol"}, State: model.ReviewStateApproved}},
		Additions:        12,
		Deletions:        4,
		ChangedFiles:     2,
		Checks: []model.Check{
			{Name: "build", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionSuccess, Workflow: "CI"},
			{Name: "lint", Status: model.CheckStatusCompleted, Conclusion: model.CheckConclusionFailure, Workflow: "CI", URL: fmt.Sprintf("https://github.com/%s/runs/lint", ref.Repo.NameWithOwner())},
			{Name: "deploy", Status: model.CheckStatusInProgress, Workflow: "CD"},
		},
		Timeline: []model.TimelineItem{
			{
				Kind: model.TimelineKindIssueComment,
				IssueComment: &model.IssueComment{
					ID:             "IC_1",
					Author:         model.User{Login: "dave"},
					Body:           "Thanks for the PR!",
					CreatedAt:      now.Add(-30 * time.Minute),
					ReactionGroups: []model.ReactionGroup{{Content: model.ReactionThumbsUp, Count: 2}},
					URL:            fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-1", ref.Repo.NameWithOwner(), ref.Number),
				},
			},
			{
				Kind: model.TimelineKindReview,
				Review: &model.Review{
					ID:          "RV_1",
					Author:      model.User{Login: "carol"},
					State:       model.ReviewStateApproved,
					Body:        "Looks great.",
					SubmittedAt: now.Add(-20 * time.Minute),
					URL:         fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-1", ref.Repo.NameWithOwner(), ref.Number),
				},
			},
			{
				Kind: model.TimelineKindCommit,
				Commit: &model.Commit{
					OID:         "abcdef1234567890",
					Message:     "Add widget support",
					Author:      model.User{Login: "alice"},
					CommittedAt: now.Add(-1 * time.Hour),
				},
			},
			{
				Kind: model.TimelineKindEvent,
				Event: &model.Event{
					Type:  "RenamedTitleEvent",
					Actor: model.User{Login: "alice"},
					At:    now.Add(-40 * time.Minute),
					// Matching internal/gh/pull_request_map.go's actual
					// rendered format exactly (fmt.Sprintf("renamed from
					// %q to %q", ...)): it is a complete phrase, not a
					// fragment meant to follow a UI-side verb.
					Detail: `renamed from "Add widgets" to "Add widget support"`,
				},
			},
		},
		ReviewThreads: []model.ReviewThread{
			{ID: "t1", Path: "a.go", Line: 1, IsResolved: true},
			{ID: "t2", Path: "b.go", Line: 2, IsResolved: false},
		},
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-5 * time.Minute),
		URL:       fmt.Sprintf("https://github.com/%s/pull/%d", ref.Repo.NameWithOwner(), ref.Number),
	}
}

// newTestApp builds an App wired to a fake GitHub with two fixture pull
// requests in two different sections (direct review requests, and the
// viewer's own), running against a tcell.SimulationScreen. It blocks until
// both fixtures have loaded and registers a cleanup that stops the app and
// waits for Run to return.
func newTestApp(t *testing.T, overrides map[string]string) (*App, <-chan struct{}, *fakeGitHub, tcell.SimulationScreen) {
	t.Helper()

	km, err := keys.Merge(keys.Defaults(), overrides)
	if err != nil {
		t.Fatalf("keys.Merge: %v", err)
	}

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	mineQuery := gh.BuildSearchQuery(model.SectionKindMine, "", "open")
	fake := &fakeGitHub{
		viewer: model.User{Login: "octocat"},
		results: map[string]gh.SearchResult{
			directQuery: {Items: []model.PullRequest{fixturePR(1, "First PR")}},
			mineQuery:   {Items: []model.PullRequest{fixturePR(2, "Second PR")}},
		},
	}

	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}

	// A real logging.Setup (not just slog.DiscardHandler) so its Recorder
	// keeps error-and-above entries: tests can then check app.deps.Recent()
	// the same way the ":messages" overlay does, for anything the UI
	// itself logs (browser launcher failures, for instance).
	logger, closeLog, err := logging.Setup(logging.Options{Debug: false, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = closeLog() })
	recorder, _ := logger.Handler().(*logging.Recorder)

	cfg := config.Default()
	cfg.RefreshInterval = time.Hour // long enough to never tick during a test

	draftStore, err := drafts.New(t.TempDir())
	if err != nil {
		t.Fatalf("drafts.New: %v", err)
	}

	var app *App
	st := store.New(store.Deps{
		GitHub:   fake,
		Cache:    cacheStore,
		Dispatch: func(f func()) { app.Dispatch(f) },
		Logger:   logger,
		Config:   cfg,
		Host:     "github.com",
	})

	app = New(Deps{
		Store:  st,
		Config: cfg,
		Keymap: km,
		Icons:  theme.Unicode(),
		Browser: &browser.Opener{
			Env:      func(string) string { return "" },
			Fallback: func(string) error { return nil },
		},
		Logger:  logger,
		Recent:  recorder.Recent,
		Version: "test",
		Drafts:  draftStore,
	})

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(100, 30)
	app.app.SetScreen(screen)

	// done is closed, never sent-on, so both the test and this cleanup can
	// safely wait on it — closing broadcasts to every receiver, whereas a
	// single buffered send would only ever satisfy the first one to read
	// it, leaving the other blocked for the full timeout below.
	done := make(chan struct{})
	go func() {
		_ = app.Run()
		close(done)
	}()
	t.Cleanup(func() {
		app.app.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("app.Run() did not return after Stop()")
		}
	})

	// Both fixtures live in different sections (DirectReview, Mine), whose
	// fetches complete on independent goroutines: waiting for only one of
	// them would let this return as soon as whichever section happens to
	// finish first, leaving the cursor on an unpredictable row depending
	// on fetch-completion order rather than the sections' priority order.
	firstKey, secondKey := fixtureRef(1).Key(), fixtureRef(2).Key()
	waitFor(t, app.app, func() bool {
		return app.rowIndex[firstKey] != nil && app.rowIndex[secondKey] != nil
	})

	// The two sections' fetches complete on independent goroutines, so
	// whichever cursor position ListView picked while only one of them
	// had loaded (it keeps the cursor on whatever was selected, by ID, as
	// rows are added — the right behaviour for a real refresh, but
	// nondeterministic here) is not a stable starting point for tests.
	// Move to the top now that both fixtures are present: by then, row
	// order is fully determined by fixed section priority, never by
	// fetch-arrival order.
	act(app.app, func() { app.listView.MoveTop() })
	return app, done, fake, screen
}

// act runs f on the UI goroutine (via app.QueueUpdate) and waits for it to
// finish, without needing a return value.
func act(app *tview.Application, f func()) {
	app.QueueUpdate(f)
}

// query runs f on the UI goroutine (via app.QueueUpdate) and returns its
// result, so a test can read App- or widget-owned state without racing the
// event loop goroutine the way a direct, unsynchronized field read would.
// It forces one draw cycle first (via Application.ForceDraw, which is safe
// to call from within a queued update — see its doc comment): unlike
// widget.ListView, widget.DetailView only rebuilds its blocks inside Draw
// (so its width-dependent content matches whatever is actually on screen),
// and this test harness drives input through app.QueueUpdate directly
// rather than tview's own polling event loop, which is what normally
// triggers a draw after every real keypress (see Application.Run's
// EventLoop) — without this, a query reading app.prView.Rows() right after
// a state change could observe stale content from the last real draw.
func query[T any](app *tview.Application, f func() T) T {
	var v T
	app.QueueUpdate(func() {
		app.ForceDraw()
		v = f()
	})
	return v
}

// waitFor polls cond, evaluated on the UI goroutine via app.QueueUpdate,
// until it returns true or a two-second deadline passes. See query's doc
// comment for why it forces a draw cycle before every check.
func waitFor(t *testing.T, app *tview.Application, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := make(chan bool, 1)
		app.QueueUpdate(func() {
			app.ForceDraw()
			result <- cond()
		})
		if <-result {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition was never met before the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sendKey delivers ev exactly as tview's own event loop would: through
// app.GetInputCapture(), then (if the capture returned a non-nil event) to
// whatever primitive currently has focus. It runs on the UI goroutine via
// app.QueueUpdate and does not return until fully processed, so tests never
// race the event loop the way driving input through the SimulationScreen's
// own InjectKey/PollEvent path would (the update and event channels have no
// ordering guarantee relative to each other).
func sendKey(app *tview.Application, ev *tcell.EventKey) {
	app.QueueUpdate(func() {
		result := ev
		if capture := app.GetInputCapture(); capture != nil {
			result = capture(ev)
		}
		if result == nil {
			return
		}
		if focused := app.GetFocus(); focused != nil {
			if h := focused.InputHandler(); h != nil {
				h(result, func(p tview.Primitive) { app.SetFocus(p) })
			}
		}
	})
}

func sendRune(app *tview.Application, r rune) {
	sendKey(app, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

func sendSpecial(app *tview.Application, key tcell.Key) {
	sendKey(app, tcell.NewEventKey(key, 0, tcell.ModNone))
}

// confirmYes navigates a showConfirm-style *tview.Modal (or the
// pendingConfirm/merge/close/reopen dialogs, which share its shape) from
// its default-focused "Cancel" button to the confirm button and selects
// it. Tab wraps a two-button Modal's Form around from the last button
// (Cancel, index 1, the default focus — see showConfirm's own doc comment
// for why) back to the first (the confirm button, index 0), so a single
// Tab then Enter reaches it regardless of which of the two is currently
// focused.
func confirmYes(app *tview.Application) {
	sendSpecial(app, tcell.KeyTab)
	sendSpecial(app, tcell.KeyEnter)
}

func TestAppInitialRenderShowsSectionsAndRows(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	title := query(app.app, func() string {
		id := app.listView.CurrentID()
		if info, ok := app.rowIndex[id]; ok {
			return info.item.PR.Title
		}
		return ""
	})
	if title != "First PR" {
		t.Errorf("initial cursor row's title = %q, want the direct-review fixture (First PR)", title)
	}

	rows := query(app.app, app.buildRows)
	var sawHeader, sawFirst, sawSecond bool
	for _, r := range rows {
		if !r.Selectable {
			sawHeader = true
		}
		for _, line := range r.Lines {
			for _, span := range line {
				if span.Text != "" {
					switch {
					case containsSubstring(span.Text, "First PR"):
						sawFirst = true
					case containsSubstring(span.Text, "Second PR"):
						sawSecond = true
					}
				}
			}
		}
	}
	if !sawHeader {
		t.Error("no section header row was rendered")
	}
	if !sawFirst || !sawSecond {
		t.Errorf("expected both fixture PRs to render as rows (First PR seen=%v, Second PR seen=%v)", sawFirst, sawSecond)
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestAppJKMoveCursor(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	first := query(app.app, app.listView.CurrentID)
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })
	second := query(app.app, app.listView.CurrentID)
	if second == first {
		t.Fatalf("j did not move the cursor")
	}

	sendRune(app.app, 'k')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == first })
}

func TestAppStalePreviewCallbackIsIgnoredAfterSelectionMoves(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	fake.SetPRResult(fixtureRef(1), gh.DetailResult{PR: fixtureDetailPR(fixtureRef(1))})
	secondPR := fixtureDetailPR(fixtureRef(2))
	secondPR.Title = "Second PR detail"
	fake.SetPRResult(fixtureRef(2), gh.DetailResult{PR: secondPR})

	firstID := query(app.app, app.listView.CurrentID)
	act(app.app, func() { app.listView.MoveBy(1) })
	secondID := query(app.app, app.listView.CurrentID)
	if secondID == firstID {
		t.Fatal("MoveBy(1) did not move the cursor; the fixture needs at least two rows")
	}

	// A legitimate preview of the row the cursor is now on: showPreview now
	// calls Store.OpenPR, whose fetch resolves asynchronously.
	act(app.app, func() { app.showPreview(secondID) })
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.Title == "Second PR detail"
	})
	wantTitle := query(app.app, func() string {
		if pr := app.deps.Store.CurrentPR(); pr != nil {
			return pr.Title
		}
		return ""
	})

	// A stale debounce callback for the FIRST row fires late — as if its
	// timer.Stop() call had raced the timer already starting, which
	// Timer.Stop's own documentation says it cannot prevent — after the
	// cursor has already moved on. It must not override the still-current
	// preview.
	act(app.app, func() { app.showPreview(firstID) })

	gotTitle := query(app.app, func() string {
		if pr := app.deps.Store.CurrentPR(); pr != nil {
			return pr.Title
		}
		return ""
	})
	if gotTitle != wantTitle {
		t.Fatalf("a stale preview callback overrode the current selection: CurrentPR().Title = %q, want %q", gotTitle, wantTitle)
	}
}

// TestAppStalePreviewIfNotAlreadyOpenCallbackIsIgnoredAfterSelectionMoves is
// previewIfNotAlreadyOpen's sibling of the showPreview test above: the
// debounce path has its own "id != CurrentID()" stale-cursor guard (see
// previewIfNotAlreadyOpen's doc comment), exercised here directly rather
// than only ever through showPreview.
func TestAppStalePreviewIfNotAlreadyOpenCallbackIsIgnoredAfterSelectionMoves(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	fake.SetPRResult(fixtureRef(1), gh.DetailResult{PR: fixtureDetailPR(fixtureRef(1))})
	secondPR := fixtureDetailPR(fixtureRef(2))
	secondPR.Title = "Second PR detail"
	fake.SetPRResult(fixtureRef(2), gh.DetailResult{PR: secondPR})

	firstID := query(app.app, app.listView.CurrentID)
	act(app.app, func() { app.listView.MoveBy(1) })
	secondID := query(app.app, app.listView.CurrentID)
	if secondID == firstID {
		t.Fatal("MoveBy(1) did not move the cursor; the fixture needs at least two rows")
	}

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.Title == "Second PR detail"
	})

	// A stale debounce callback for the FIRST row (the one the cursor was
	// on before Enter moved focus and opened the second) fires late. It
	// must not override the still-current selection.
	act(app.app, func() { app.previewIfNotAlreadyOpen(firstID) })

	gotTitle := query(app.app, func() string {
		if pr := app.deps.Store.CurrentPR(); pr != nil {
			return pr.Title
		}
		return ""
	})
	if gotTitle != "Second PR detail" {
		t.Fatalf("a stale previewIfNotAlreadyOpen callback overrode the current selection: CurrentPR().Title = %q, want %q", gotTitle, "Second PR detail")
	}
}

func TestAppGgAndG(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)
	first := query(app.app, app.listView.CurrentID)

	sendRune(app.app, 'G')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })
	last := query(app.app, app.listView.CurrentID)

	sendRune(app.app, 'g')
	sendRune(app.app, 'g')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == first })

	sendRune(app.app, 'G')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == last })
}

func TestAppFilterShowsAndNarrowsRows(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.filterInput })

	for _, r := range "second" {
		sendRune(app.app, r)
	}
	waitFor(t, app.app, func() bool { return app.deps.Store.Filter() == "second" })

	waitFor(t, app.app, func() bool {
		rows := app.buildRows()
		for _, row := range rows {
			for _, line := range row.Lines {
				for _, span := range line {
					if containsSubstring(span.Text, "First PR") {
						return false
					}
				}
			}
		}
		return true
	})
}

func TestAppEscClearsFilter(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, '/')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.filterInput })
	sendRune(app.app, 'x')
	waitFor(t, app.app, func() bool { return app.deps.Store.Filter() == "x" })

	sendSpecial(app.app, tcell.KeyEsc)
	waitFor(t, app.app, func() bool {
		return app.deps.Store.Filter() == "" && app.app.GetFocus() == app.listView
	})
}

func TestAppLoadingChangedRefreshesListRowsImmediately(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	// Block every fetch this reload starts so the test can observe the
	// list's rows while a section is still loading, before any result
	// arrives (which would fire EventListChanged on its own regardless of
	// this fix).
	block := make(chan struct{})
	fake.SetBlock(block)
	defer close(block)

	act(app.app, func() { app.reload() })

	// app.listView.Rows() reflects only what a real SetRows call last
	// applied — unlike calling app.buildRows() directly, which would
	// reflect live Store state regardless of whether the event that is
	// supposed to trigger a refresh actually did.
	loadingMarker := theme.Unicode().Loading
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if containsSubstring(span.Text, loadingMarker) {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestAppCommandLineQuits(t *testing.T) {
	app, done, _, _ := newTestApp(t, nil)

	sendRune(app.app, ':')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.cmdLine })

	sendRune(app.app, 'q')
	sendSpecial(app.app, tcell.KeyEnter)

	select {
	case <-time.After(2 * time.Second):
		t.Fatal(":q did not stop the app")
	case <-done:
	}
}

func TestAppToastTimerRaceDoesNotClearANewerToast(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	var firstSeq int
	act(app.app, func() {
		app.showToast("first", theme.Error)
		firstSeq = app.toastSeq
		app.showToast("second", theme.Error)
		// Simulate the first toast's timer callback firing late, as if
		// its Stop() call had raced its own expiry (time.Timer.Stop
		// returning false does not guarantee the callback has not
		// already started) — it must not clear the newer toast.
		app.clearToastIfCurrent(firstSeq)
	})

	if got := query(app.app, func() string { return app.statusBar.toast }); got != "second" {
		t.Fatalf("a stale toast callback cleared the newer toast; status bar toast = %q, want %q", got, "second")
	}
}

func TestAppToastClearsAfterItsDuration(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	old := toastDuration
	toastDuration = 20 * time.Millisecond
	t.Cleanup(func() { toastDuration = old })

	act(app.app, func() { app.showToast("hello", theme.Error) })
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "" })
}

func TestAppHelpOpensAndCloses(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestAppHelpAlsoClosesOnQuestionMark(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "" })
}

func TestAppOverlayForwardsUnboundKeysToItsTextView(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })

	// "j" is not a close key: the overlay's scrollable TextView must
	// receive it (so j/k/gg/G scroll the help/messages text), which means
	// the router must return the original event instead of swallowing it.
	var result *tcell.EventKey
	act(app.app, func() {
		result = app.handleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	})
	if result == nil {
		t.Fatal("handleKey swallowed an unbound key while an overlay was open; its TextView cannot scroll")
	}
}

// TestAppDetailPaneForwardsGenuinelyUnboundKeys guards the router's general
// "let it through" fallback for the detail context: a key with no binding
// in ContextDetail or ContextGlobal (unlike j/k/gg/G/Ctrl-d/Ctrl-u, which
// M1b now binds to move the PR tab's DetailView cursor — see
// TestAppJKGMoveWithinDetail) must still reach whatever has focus
// unmolested, so a still-native TextView (the Files tab's placeholder,
// until M2) keeps scrolling on any key gprt does not bind itself.
func TestAppDetailPaneForwardsGenuinelyUnboundKeys(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	var result *tcell.EventKey
	act(app.app, func() {
		result = app.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone))
	})
	if result == nil {
		t.Fatal("handleKey swallowed a key bound in no context while a detail pane was focused")
	}
}

func TestAppDetailPaneBoundKeysStillDispatch(t *testing.T) {
	// A regression guard for the fix above: an actually-bound key in
	// ContextDetail (gt) must still be consumed by the router, not
	// forwarded to the TextView as if it were unbound.
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
}

func TestAppCtrlWMovesFocus(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.listView })
}

func TestAppCtrlWoCollapsesList(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)
	if !query(app.app, func() bool { return app.listExpanded }) {
		t.Fatal("list must start expanded")
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return !app.listExpanded })
}

func TestAppCtrlWhReExpandsACollapsedList(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	// Collapse the list and move focus to the detail column, matching
	// what Ctrl-w o itself already does when the list has focus.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return !app.listExpanded && app.isDetailFocused() })

	// Ctrl-w h ("focus the previous column") must never focus a
	// zero-width list column: it should re-expand the list first.
	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool {
		return app.listExpanded && app.app.GetFocus() == app.listView
	})
}

func TestAppGtSwitchesTab(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendRune(app.app, 'g')
	sendRune(app.app, 't')
	waitFor(t, app.app, func() bool { return app.currentTab == "files" })
}

func TestAppRemappedKeyFromConfig(t *testing.T) {
	app, _, _, _ := newTestApp(t, map[string]string{"list.down": "<C-n>"})
	first := query(app.app, app.listView.CurrentID)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlN, 0, tcell.ModCtrl))
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() != first })

	// The old default ("j") must no longer trigger the action.
	before := query(app.app, app.listView.CurrentID)
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.listView.CurrentID() == before })
}

func TestAppCtrlHAmbiguityBothSwitchTabs(t *testing.T) {
	t.Run("legacy terminal reports KeyBackspace", func(t *testing.T) {
		app, _, _, _ := newTestApp(t, nil)
		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
		sendRune(app.app, 'l')
		waitFor(t, app.app, func() bool { return app.isDetailFocused() })
		sendRune(app.app, 'g')
		sendRune(app.app, 't') // move off "pr" first so tab_prev is observable
		waitFor(t, app.app, func() bool { return app.currentTab == "files" })

		sendKey(app.app, tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))
		waitFor(t, app.app, func() bool { return app.currentTab == "pr" })
	})

	t.Run("CSI-u terminal reports KeyCtrlH", func(t *testing.T) {
		app, _, _, _ := newTestApp(t, nil)
		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
		sendRune(app.app, 'l')
		waitFor(t, app.app, func() bool { return app.isDetailFocused() })
		sendRune(app.app, 'g')
		sendRune(app.app, 't')
		waitFor(t, app.app, func() bool { return app.currentTab == "files" })

		sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlH, 0, tcell.ModCtrl))
		waitFor(t, app.app, func() bool { return app.currentTab == "pr" })
	})
}

func TestAppShowErrorFromABackgroundGoroutine(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.ShowError("browser: launcher exited with status 1")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ShowError did not return; it must be safe to call from any goroutine")
	}

	waitFor(t, app.app, func() bool {
		return app.statusBar.toast == "browser: launcher exited with status 1"
	})

	entries := query(app.app, func() []logging.Entry { return app.deps.Recent() })
	found := false
	for _, e := range entries {
		if containsSubstring(e.String(), "browser: launcher exited with status 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("ShowError's message did not reach :messages; entries = %+v", entries)
	}
}

func TestAppOpenBrowserFailureShowsAToast(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	act(app.app, func() {
		app.deps.Browser = &browser.Opener{
			Env: func(string) string { return "" },
			Fallback: func(string) error {
				return fmt.Errorf("no browser found")
			},
		}
	})

	sendRune(app.app, 'o')

	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "no browser found")
	})

	entries := query(app.app, func() []logging.Entry { return app.deps.Recent() })
	found := false
	for _, e := range entries {
		if containsSubstring(e.String(), "no browser found") {
			found = true
		}
	}
	if !found {
		t.Errorf("Browser.Open's error did not reach :messages; entries = %+v", entries)
	}
}

// TestAppOpenBrowserWithPRTabFocusedAndNoPROpenShowsAToast guards against
// "o" silently doing nothing on the PR tab before any pull request has
// ever been opened (the empty-state block has no URL, and CurrentPR() is
// nil): the user should be told there is nothing to open, not left
// guessing whether the key was even registered.
func TestAppOpenBrowserWithPRTabFocusedAndNoPROpenShowsAToast(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.prView })

	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "no pull request open")
	})
}

func TestAppPersistentErrorMarkerOnStatusBarAndHeaderRow(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetError(directQuery, errors.New("network unreachable"))

	act(app.app, func() { app.reload() })

	waitFor(t, app.app, func() bool { return app.deps.Store.LastError() != nil })

	// The status bar's right side must show a persistent error marker
	// while Store.LastError() stands, not just a five-second toast.
	waitFor(t, app.app, func() bool { return app.statusBar.errorMarker != "" })

	// DirectReview keeps its previous items despite the failed refresh
	// (see internal/store's own docs), so its header row still renders —
	// and must now carry an error marker.
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if span.Style == theme.Error {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestAppSectionWarningsShowHeaderMarkerAndToastOnce(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetResult(directQuery, gh.SearchResult{
		Items:    []model.PullRequest{fixturePR(1, "First PR")},
		Warnings: []string{"1 reviewer could not be resolved"},
	})

	act(app.app, func() { app.reload() })

	// Header row gets a theme.Warning span mentioning the warning count.
	waitFor(t, app.app, func() bool {
		for _, row := range app.listView.Rows() {
			if row.Selectable {
				continue
			}
			for _, line := range row.Lines {
				for _, span := range line {
					if span.Style == theme.Warning && containsSubstring(span.Text, "1") {
						return true
					}
				}
			}
		}
		return false
	})

	// The first time a section reports warnings, it toasts one line
	// naming the section and the first warning.
	waitFor(t, app.app, func() bool {
		return containsSubstring(app.statusBar.toast, "reviewer could not be resolved")
	})
}

func TestAppMessagesOverlayListsSectionWarnings(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)

	directQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open")
	fake.SetResult(directQuery, gh.SearchResult{
		Items:    []model.PullRequest{fixturePR(1, "First PR")},
		Warnings: []string{"1 reviewer could not be resolved"},
	})
	act(app.app, func() { app.reload() })
	waitFor(t, app.app, func() bool { return app.deps.Store.SectionStates()[0].Warnings != nil })

	sendRune(app.app, '?')
	waitFor(t, app.app, func() bool { return app.overlay == "help" })
	sendRune(app.app, 'q')
	waitFor(t, app.app, func() bool { return app.overlay == "" })

	sendRune(app.app, ':')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.cmdLine })
	for _, r := range "messages" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "messages" })

	if !app.root.HasPage("messages") {
		t.Fatal("messages overlay page not found")
	}
	text := query(app.app, func() string {
		return app.root.GetPage("messages").(*tview.TextView).GetText(true)
	})
	if !containsSubstring(text, "Warnings") {
		t.Errorf(":messages text = %q, want it to include a \"Warnings\" heading", text)
	}
	if !containsSubstring(text, "reviewer could not be resolved") {
		t.Errorf(":messages text = %q, want it to list the section's current warning", text)
	}
}

// TestAppMessagesOverlayListsDetailWarnings guards against the current
// pull request's own DetailState().Warnings (a paginated timeline/threads/
// checks connection truncated at its limit, for example) being invisible
// anywhere in the UI: the PR tab's header already shows them as "⚠" lines,
// but :messages should list them too, alongside section warnings, the same
// way it already does for the list.
func TestAppMessagesOverlayListsDetailWarnings(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	fake.SetPRResult(ref, gh.DetailResult{PR: pr, Warnings: []string{"5 review threads omitted"}})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return len(app.deps.Store.DetailState().Warnings) > 0 })

	sendRune(app.app, ':')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.cmdLine })
	for _, r := range "messages" {
		sendRune(app.app, r)
	}
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "messages" })

	text := query(app.app, func() string {
		return app.root.GetPage("messages").(*tview.TextView).GetText(true)
	})
	if !containsSubstring(text, "Warnings") {
		t.Errorf(":messages text = %q, want it to include a \"Warnings\" heading", text)
	}
	if !containsSubstring(text, "5 review threads omitted") {
		t.Errorf(":messages text = %q, want it to list the current pull request's detail warning", text)
	}
}

func TestAppStatusBarHintFollowsFocus(t *testing.T) {
	app, _, _, _ := newTestApp(t, nil)
	listHint := query(app.app, func() string { return app.statusBar.hint })
	if !strings.Contains(listHint, "filter") {
		t.Fatalf("initial hint should describe the list pane, got %q", listHint)
	}

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'l')
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	detailHint := query(app.app, func() string { return app.statusBar.hint })
	if detailHint == listHint || !strings.Contains(detailHint, "tab") {
		t.Fatalf("hint did not follow focus to the detail pane: got %q", detailHint)
	}
}

func TestReviewerSpansBoldTheViewer(t *testing.T) {
	pr := &model.PullRequest{
		ReviewRequests: []model.Reviewer{{Login: "alice", Kind: model.ReviewerKindUser}, {Login: "bob", Kind: model.ReviewerKindUser}},
	}
	bold := map[string]bool{}
	for _, s := range reviewerSpans(pr, "bob") {
		_, _, attrs := s.Style.Decompose()
		bold[s.Text] = attrs&tcell.AttrBold != 0
	}
	if !bold["bob"] || bold["alice"] {
		t.Fatalf("expected only the viewer's login in bold, got %v", bold)
	}
}

// prViewText flattens every span of every rendered row of the PR tab's
// DetailView into one string, for substring assertions that do not care
// about styling or exact line boundaries.
func prViewText(dv *widget.DetailView) string {
	var sb strings.Builder
	for _, row := range dv.Rows() {
		for _, line := range row.Lines {
			for _, s := range line {
				sb.WriteString(s.Text)
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// detailSelectableIndex returns the index, among rows' selectable rows
// only, of the row with the given ID — the form widget.ListView.SetCursor
// (and so widget.DetailView.SetCursor, promoted from it) expects — or -1 if
// no selectable row has that ID.
func detailSelectableIndex(rows []widget.ListRow, id string) int {
	idx := -1
	for _, r := range rows {
		if !r.Selectable {
			continue
		}
		idx++
		if r.ID == id {
			return idx
		}
	}
	return -1
}

func TestAppEnterOpensPRAndShowsDetail(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	waitFor(t, app.app, func() bool {
		return containsSubstring(prViewText(app.prView), "Add widget support")
	})
}

func TestAppFailedDetailFetchShowsErrorInsteadOfLoadingForever(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRError(ref, errors.New("boom"))

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.DetailState().Err != nil })

	waitFor(t, app.app, func() bool {
		return containsSubstring(prViewText(app.prView), "Error loading pull request: boom")
	})
	if containsSubstring(prViewText(app.prView), "Loading pull request") {
		t.Error("PR tab still shows the loading placeholder after the fetch failed")
	}
}

func TestAppShowsCachedThenLiveDetail(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	// First open: no cache yet, so it goes straight to the (unblocked)
	// network fetch, which also writes the on-disk cache entry.
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !app.deps.Store.DetailState().FetchedAt.IsZero() })

	// Re-open the same pull request with the network fetch blocked: OpenPR
	// applies the now-cached detail synchronously, marked stale, before the
	// (blocked) network fetch resolves. Re-opened explicitly via
	// Store.OpenPR, not another Enter keypress: focus is already on the
	// detail pane at this point, where "Enter" is unbound, so a second
	// keypress here would exercise nothing.
	block := make(chan struct{})
	fake.SetPRBlock(block)
	act(app.app, func() { app.deps.Store.OpenPR(ref) })

	waitFor(t, app.app, func() bool {
		return app.deps.Store.DetailState().Stale && containsSubstring(prViewText(app.prView), "cached")
	})

	close(block)
	waitFor(t, app.app, func() bool {
		return !app.deps.Store.DetailState().Stale && !containsSubstring(prViewText(app.prView), "cached")
	})
}

func TestAppChecksSummaryAndRowsRenderInDetail(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		text := prViewText(app.prView)
		return containsSubstring(text, "1 passed") && containsSubstring(text, "1 failed") && containsSubstring(text, "1 pending") &&
			containsSubstring(text, "build") && containsSubstring(text, "lint") && containsSubstring(text, "deploy")
	})
}

func TestAppConversationCommentAndEventRenderInDetail(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		text := prViewText(app.prView)
		return containsSubstring(text, "dave") &&
			containsSubstring(text, "commented") &&
			containsSubstring(text, "Thanks for the PR!") &&
			containsSubstring(text, `renamed from "Add widgets" to "Add widget support"`)
	})
}

func TestAppJKGMoveWithinDetail(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })
	waitFor(t, app.app, func() bool { return app.prView.CurrentID() != "" })

	first := query(app.app, app.prView.CurrentID)
	sendRune(app.app, 'j')
	waitFor(t, app.app, func() bool { return app.prView.CurrentID() != first })
	second := query(app.app, app.prView.CurrentID)

	sendRune(app.app, 'k')
	waitFor(t, app.app, func() bool { return app.prView.CurrentID() == first })

	sendRune(app.app, 'G')
	waitFor(t, app.app, func() bool {
		id := app.prView.CurrentID()
		return id != first && id != second
	})
	last := query(app.app, app.prView.CurrentID)
	if !strings.HasPrefix(last, "timeline:") {
		t.Errorf("CurrentID() after G = %q, want the last conversation block (a timeline: ID)", last)
	}
}

func TestAppOOpensCheckAndHeaderURLsInBrowser(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })
	waitFor(t, app.app, func() bool { return detailSelectableIndex(app.prView.Rows(), "check:1") >= 0 })

	var opened []string
	act(app.app, func() {
		app.deps.Browser = &browser.Opener{
			Env:      func(string) string { return "" },
			Fallback: func(rawURL string) error { opened = append(opened, rawURL); return nil },
		}
	})

	// opened is only ever mutated inside Fallback, which openCurrentInBrowser
	// calls on the UI goroutine (via sendRune below); waitFor's own
	// QueueUpdate round trip is what makes reading it directly afterwards
	// (rather than nesting another query/QueueUpdate call, which would
	// deadlock: see query's doc comment) race-free from the test goroutine.
	failingCheckIdx := query(app.app, func() int { return detailSelectableIndex(app.prView.Rows(), "check:1") })
	act(app.app, func() { app.prView.SetCursor(failingCheckIdx) })
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return len(opened) == 1 })
	if got := opened[0]; got != pr.Checks[1].URL {
		t.Errorf("o on the failing check row opened %q, want its details URL %q", got, pr.Checks[1].URL)
	}

	act(app.app, func() { app.prView.MoveTop() }) // the header is the first selectable block
	if id := query(app.app, app.prView.CurrentID); id != "header" {
		t.Fatalf("MoveTop() landed on %q, want the header block", id)
	}
	sendRune(app.app, 'o')
	waitFor(t, app.app, func() bool { return len(opened) == 2 })
	if got := opened[1]; got != pr.URL {
		t.Errorf("o on the header opened %q, want the PR URL %q", got, pr.URL)
	}
}

func TestAppNarrowingScreenRewrapsDescription(t *testing.T) {
	app, _, fake, screen := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := fixtureDetailPR(ref)
	pr.Body = strings.Repeat("word ", 40)
	fake.SetPRResult(ref, gh.DetailResult{PR: pr})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.deps.Store.CurrentPR() != nil })

	descLines := func() int {
		for _, row := range app.prView.Rows() {
			if row.ID == "description" {
				return len(row.Lines)
			}
		}
		return -1
	}
	wideLines := query(app.app, descLines)
	if wideLines <= 0 {
		t.Fatal("description block not found before resizing")
	}

	screen.SetSize(40, 30)
	waitFor(t, app.app, func() bool { return descLines() != wideLines })
}

func TestAppReloadTriggersASecondPullRequestFetch(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return fake.PRCalls() >= 1 })
	firstCalls := fake.PRCalls()

	sendRune(app.app, 'R')
	waitFor(t, app.app, func() bool { return fake.PRCalls() > firstCalls })
}

// TestAppRefreshListDoesNotReopenAnAlreadyOpenPR guards against a list
// rebuild (auto-refresh, a filter keystroke, a loading/error transition,
// ...) re-firing the cursor's changed callback for a row that did not
// actually move — widget.ListView.SetRows calls its changed callback
// unconditionally whenever a selectable row exists, not only when the
// cursor's row changed — which would otherwise restart the preview
// debounce and needlessly re-open (and briefly re-show as "(cached)") a
// pull request that is already open and unchanged.
func TestAppRefreshListDoesNotReopenAnAlreadyOpenPR(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return !app.deps.Store.DetailState().FetchedAt.IsZero() })
	callsAfterOpen := fake.PRCalls()

	for range 5 {
		act(app.app, func() { app.refreshList() })
	}
	// Give the debounce timer (300ms) a chance to fire if the bug were
	// still present, then synchronise with the UI goroutine once more so
	// any callback it queued has definitely run.
	time.Sleep(previewDebounce + 100*time.Millisecond)
	act(app.app, func() {})

	if got := fake.PRCalls(); got != callsAfterOpen {
		t.Errorf("PullRequest was called %d times after refreshList with no cursor change, want still %d (no re-open)", got, callsAfterOpen)
	}
	if app.deps.Store.DetailState().Stale {
		t.Error("DetailState().Stale = true after a redundant refresh; the already-open PR must not be re-shown as (cached)")
	}
}

// TestAppOpenPRDoesNotSelfSustainALoopWhileFetchIsSlow reproduces the full
// self-sustaining loop the redundant-re-open bug caused, not just its
// single-refreshList symptom (TestAppRefreshListDoesNotReopenAnAlreadyOpenPR
// above): OpenPR's own fetch starting fires EventLoadingChanged, whose
// handler calls refreshList, which re-fires the cursor's changed callback
// and restarts the preview debounce; with a fetch slower than the
// debounce, the old code's debounce would fire while the first fetch was
// still in flight and re-call OpenPR, starting a second fetch that itself
// triggers another EventLoadingChanged/refreshList/debounce cycle,
// indefinitely, for as long as the fetch keeps taking longer than the
// debounce. The "already open" guard (previewIfNotAlreadyOpen) breaks the
// cycle at its very first iteration, so exactly one PullRequest call ever
// happens no matter how long the fetch is blocked.
func TestAppOpenPRDoesNotSelfSustainALoopWhileFetchIsSlow(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	block := make(chan struct{})
	fake.SetPRBlock(block)
	t.Cleanup(func() { close(block) })

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return fake.PRCalls() >= 1 })

	// Let several debounce windows pass while the fetch is still blocked:
	// with the bug present, each one would have re-called OpenPR (a new
	// fetch, itself blocked, itself triggering the next cycle).
	time.Sleep(3*previewDebounce + 200*time.Millisecond)
	act(app.app, func() {}) // synchronise with the UI goroutine once more

	if got := fake.PRCalls(); got != 1 {
		t.Errorf("PullRequest was called %d times while its own fetch was still in flight, want exactly 1 (no self-sustaining re-open loop)", got)
	}
}

// TestAppSwitchingPRResetsDetailCursorToTop guards against the PR tab's
// cursor carrying over between pull requests: block IDs are positional
// (header, description, checks-summary, check:N, timeline:N), not scoped
// to a pull request, so widget.ListView's own by-ID cursor preservation
// cannot tell "the same PR, re-fetched" from "a completely different PR"
// on its own.
func TestAppSwitchingPRResetsDetailCursorToTop(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	refA, refB := fixtureRef(1), fixtureRef(2)
	fake.SetPRResult(refA, gh.DetailResult{PR: fixtureDetailPR(refA)})
	fake.SetPRResult(refB, gh.DetailResult{PR: fixtureDetailPR(refB)})

	// Open A (cursor starts on row A per newTestApp's MoveTop()), then B,
	// so A's detail gets cached for a later re-open.
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.Ref == refA
	})

	act(app.app, func() { app.listView.MoveBy(1) })
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.Ref == refB
	})

	// Move the PR tab's cursor away from the top for PR B.
	act(app.app, func() { app.prView.MoveBottom() })
	waitFor(t, app.app, func() bool { return app.prView.CurrentID() != "header" })

	// Re-open A: OpenPR applies its cache immediately.
	act(app.app, func() { app.deps.Store.OpenPR(refA) })
	waitFor(t, app.app, func() bool {
		pr := app.deps.Store.CurrentPR()
		return pr != nil && pr.Ref == refA
	})

	if id := query(app.app, app.prView.CurrentID); id != "header" {
		t.Errorf("CurrentID() after switching to a different (cached) PR = %q, want the top block (header)", id)
	}
}

func TestAppCtrlWhFromDetailReturnsToList(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	fake.SetPRResult(ref, gh.DetailResult{PR: fixtureDetailPR(ref)})

	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.isDetailFocused() })

	sendKey(app.app, tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	sendRune(app.app, 'h')
	waitFor(t, app.app, func() bool { return app.app.GetFocus() == app.listView })
}
