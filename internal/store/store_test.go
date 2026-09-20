package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// fakeDispatcher is a channel-driven fake event loop for tests. Unlike a
// fake that calls f() synchronously (forbidden: it would hide deadlocks a
// real app.QueueUpdateDraw could hit), goroutines started by the Store
// enqueue onto ch and the test drains it explicitly via runUntilIdle,
// mirroring how the real UI goroutine only ever runs dispatched callbacks
// one at a time.
type fakeDispatcher struct {
	ch chan func()
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{ch: make(chan func(), 256)}
}

func (d *fakeDispatcher) dispatch(f func()) {
	d.ch <- f
}

// runUntilIdle drains d, running every dispatched function on the calling
// (test) goroutine, until no further function arrives within idleWindow.
func runUntilIdle(t *testing.T, d *fakeDispatcher) {
	t.Helper()
	const idleWindow = 100 * time.Millisecond
	timer := time.NewTimer(idleWindow)
	defer timer.Stop()
	for {
		select {
		case f := <-d.ch:
			if !timer.Stop() {
				<-timer.C
			}
			f()
			timer.Reset(idleWindow)
		case <-timer.C:
			return
		}
	}
}

// searchCall records one SearchPullRequests invocation for assertions.
type searchCall struct {
	query  string
	cursor string
}

// detailCall records one PullRequest invocation for assertions.
type detailCall struct {
	ref         model.PRRef
	viewerLogin string
}

// filesCall records one ChangedFiles invocation for assertions.
type filesCall struct {
	ref  model.PRRef
	page int
	etag string
}

// fakeGitHub is a test double for the GitHub interface. viewerFunc,
// searchFunc, detailFunc, and filesFunc default to returning zero values
// with no error; tests override any of them to control timing and results.
type fakeGitHub struct {
	mu sync.Mutex

	viewerFunc        func(ctx context.Context) (model.User, model.RateLimit, error)
	searchFunc        func(ctx context.Context, query, cursor string) (gh.SearchResult, error)
	detailFunc        func(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error)
	filesFunc         func(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error)
	addCommentFunc    func(ctx context.Context, subjectID, body string) (model.IssueComment, model.RateLimit, error)
	updateCommentFunc func(ctx context.Context, id, body string) (model.IssueComment, model.RateLimit, error)
	deleteCommentFunc func(ctx context.Context, id string) (model.RateLimit, error)

	createPendingReviewFunc   func(ctx context.Context, prID string) (model.Review, model.RateLimit, error)
	addReviewNowFunc          func(ctx context.Context, prID string, threads []gh.DraftThread, body string) (model.Review, model.RateLimit, error)
	addReviewNowWithEventFunc func(ctx context.Context, prID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error)
	addReviewThreadFunc       func(ctx context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error)
	addThreadReplyFunc        func(ctx context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error)
	submitReviewFunc          func(ctx context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error)
	deletePendingReviewFunc   func(ctx context.Context, reviewID string) (model.RateLimit, error)
	updateReviewCommentFunc   func(ctx context.Context, id, body string) (model.ReviewComment, model.RateLimit, error)
	deleteReviewCommentFunc   func(ctx context.Context, id string) (model.RateLimit, error)
	resolveThreadFunc         func(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error)
	unresolveThreadFunc       func(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error)

	calls              []searchCall
	detailCalls        []detailCall
	filesCalls         []filesCall
	addCommentCalls    []addCommentCall
	updateCommentCalls []updateCommentCall
	deleteCommentCalls []deleteCommentCall

	createPendingReviewCalls   []createPendingReviewCall
	addReviewNowCalls          []addReviewNowCall
	addReviewNowWithEventCalls []addReviewNowWithEventCall
	addReviewThreadCalls       []addReviewThreadCall
	addThreadReplyCalls        []addThreadReplyCall
	submitReviewCalls          []submitReviewCall
	deletePendingReviewCalls   []deletePendingReviewCall
	updateReviewCommentCalls   []updateReviewCommentCall
	deleteReviewCommentCalls   []deleteReviewCommentCall
	resolveThreadCalls         []resolveThreadCall
	unresolveThreadCalls       []unresolveThreadCall
}

// createPendingReviewCall records one CreatePendingReview invocation.
type createPendingReviewCall struct{ prID string }

// addReviewNowCall records one AddReviewNow invocation.
type addReviewNowCall struct {
	prID    string
	threads []gh.DraftThread
	body    string
}

// addReviewNowWithEventCall records one AddReviewNowWithEvent invocation.
type addReviewNowWithEventCall struct {
	prID  string
	event model.ReviewEvent
	body  string
}

// addReviewThreadCall records one AddReviewThread invocation.
type addReviewThreadCall struct{ in gh.ThreadInput }

// addThreadReplyCall records one AddThreadReply invocation.
type addThreadReplyCall struct {
	threadID, body, pendingReviewID string
}

// submitReviewCall records one SubmitReview invocation.
type submitReviewCall struct {
	reviewID string
	event    model.ReviewEvent
	body     string
}

// deletePendingReviewCall records one DeletePendingReview invocation.
type deletePendingReviewCall struct{ reviewID string }

// updateReviewCommentCall records one UpdateReviewComment invocation.
type updateReviewCommentCall struct{ id, body string }

// deleteReviewCommentCall records one DeleteReviewComment invocation.
type deleteReviewCommentCall struct{ id string }

// resolveThreadCall records one ResolveThread invocation.
type resolveThreadCall struct{ threadID string }

// unresolveThreadCall records one UnresolveThread invocation.
type unresolveThreadCall struct{ threadID string }

// addCommentCall records one AddIssueComment invocation for assertions.
type addCommentCall struct {
	subjectID string
	body      string
}

// updateCommentCall records one UpdateIssueComment invocation for
// assertions.
type updateCommentCall struct {
	id   string
	body string
}

// deleteCommentCall records one DeleteIssueComment invocation for
// assertions.
type deleteCommentCall struct {
	id string
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		viewerFunc: func(context.Context) (model.User, model.RateLimit, error) {
			return model.User{}, model.RateLimit{}, nil
		},
		searchFunc: func(context.Context, string, string) (gh.SearchResult, error) {
			return gh.SearchResult{}, nil
		},
		detailFunc: func(context.Context, model.PRRef, string) (gh.DetailResult, error) {
			return gh.DetailResult{}, nil
		},
		filesFunc: func(context.Context, model.PRRef, int, string) (gh.FilesResult, error) {
			return gh.FilesResult{}, nil
		},
		addCommentFunc: func(context.Context, string, string) (model.IssueComment, model.RateLimit, error) {
			return model.IssueComment{}, model.RateLimit{}, nil
		},
		updateCommentFunc: func(context.Context, string, string) (model.IssueComment, model.RateLimit, error) {
			return model.IssueComment{}, model.RateLimit{}, nil
		},
		deleteCommentFunc: func(context.Context, string) (model.RateLimit, error) {
			return model.RateLimit{}, nil
		},
		createPendingReviewFunc: func(context.Context, string) (model.Review, model.RateLimit, error) {
			return model.Review{}, model.RateLimit{}, nil
		},
		addReviewNowFunc: func(context.Context, string, []gh.DraftThread, string) (model.Review, model.RateLimit, error) {
			return model.Review{}, model.RateLimit{}, nil
		},
		addReviewNowWithEventFunc: func(context.Context, string, model.ReviewEvent, string) (model.Review, model.RateLimit, error) {
			return model.Review{}, model.RateLimit{}, nil
		},
		addReviewThreadFunc: func(context.Context, gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
			return model.ReviewThread{}, model.RateLimit{}, nil
		},
		addThreadReplyFunc: func(context.Context, string, string, string) (model.ReviewComment, model.RateLimit, error) {
			return model.ReviewComment{}, model.RateLimit{}, nil
		},
		submitReviewFunc: func(context.Context, string, model.ReviewEvent, string) (model.Review, model.RateLimit, error) {
			return model.Review{}, model.RateLimit{}, nil
		},
		deletePendingReviewFunc: func(context.Context, string) (model.RateLimit, error) {
			return model.RateLimit{}, nil
		},
		updateReviewCommentFunc: func(context.Context, string, string) (model.ReviewComment, model.RateLimit, error) {
			return model.ReviewComment{}, model.RateLimit{}, nil
		},
		deleteReviewCommentFunc: func(context.Context, string) (model.RateLimit, error) {
			return model.RateLimit{}, nil
		},
		resolveThreadFunc: func(context.Context, string) (model.ReviewThread, model.RateLimit, error) {
			return model.ReviewThread{}, model.RateLimit{}, nil
		},
		unresolveThreadFunc: func(context.Context, string) (model.ReviewThread, model.RateLimit, error) {
			return model.ReviewThread{}, model.RateLimit{}, nil
		},
	}
}

func (f *fakeGitHub) Viewer(ctx context.Context) (model.User, model.RateLimit, error) {
	// The func value is copied out while holding the lock, then called
	// after releasing it: fn can itself block for an arbitrary time (some
	// tests deliberately do this to control fetch ordering), and holding
	// the lock across that call would deadlock a test goroutine trying to
	// reassign viewerFunc/searchFunc/detailFunc in the meantime.
	f.mu.Lock()
	fn := f.viewerFunc
	f.mu.Unlock()
	return fn(ctx)
}

func (f *fakeGitHub) SearchPullRequests(ctx context.Context, query, cursor string) (gh.SearchResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, searchCall{query: query, cursor: cursor})
	fn := f.searchFunc
	f.mu.Unlock()
	return fn(ctx, query, cursor)
}

func (f *fakeGitHub) PullRequest(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error) {
	f.mu.Lock()
	f.detailCalls = append(f.detailCalls, detailCall{ref: ref, viewerLogin: viewerLogin})
	fn := f.detailFunc
	f.mu.Unlock()
	return fn(ctx, ref, viewerLogin)
}

func (f *fakeGitHub) ChangedFiles(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error) {
	f.mu.Lock()
	f.filesCalls = append(f.filesCalls, filesCall{ref: ref, page: page, etag: etag})
	fn := f.filesFunc
	f.mu.Unlock()
	return fn(ctx, ref, page, etag)
}

func (f *fakeGitHub) AddIssueComment(ctx context.Context, subjectID, body string) (model.IssueComment, model.RateLimit, error) {
	f.mu.Lock()
	f.addCommentCalls = append(f.addCommentCalls, addCommentCall{subjectID: subjectID, body: body})
	fn := f.addCommentFunc
	f.mu.Unlock()
	return fn(ctx, subjectID, body)
}

func (f *fakeGitHub) UpdateIssueComment(ctx context.Context, id, body string) (model.IssueComment, model.RateLimit, error) {
	f.mu.Lock()
	f.updateCommentCalls = append(f.updateCommentCalls, updateCommentCall{id: id, body: body})
	fn := f.updateCommentFunc
	f.mu.Unlock()
	return fn(ctx, id, body)
}

func (f *fakeGitHub) DeleteIssueComment(ctx context.Context, id string) (model.RateLimit, error) {
	f.mu.Lock()
	f.deleteCommentCalls = append(f.deleteCommentCalls, deleteCommentCall{id: id})
	fn := f.deleteCommentFunc
	f.mu.Unlock()
	return fn(ctx, id)
}

func (f *fakeGitHub) CreatePendingReview(ctx context.Context, prID string) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	f.createPendingReviewCalls = append(f.createPendingReviewCalls, createPendingReviewCall{prID: prID})
	fn := f.createPendingReviewFunc
	f.mu.Unlock()
	return fn(ctx, prID)
}

func (f *fakeGitHub) AddReviewNow(
	ctx context.Context, prID string, threads []gh.DraftThread, body string,
) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	f.addReviewNowCalls = append(f.addReviewNowCalls, addReviewNowCall{prID: prID, threads: threads, body: body})
	fn := f.addReviewNowFunc
	f.mu.Unlock()
	return fn(ctx, prID, threads, body)
}

func (f *fakeGitHub) AddReviewNowWithEvent(
	ctx context.Context, prID string, event model.ReviewEvent, body string,
) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	f.addReviewNowWithEventCalls = append(f.addReviewNowWithEventCalls, addReviewNowWithEventCall{prID: prID, event: event, body: body})
	fn := f.addReviewNowWithEventFunc
	f.mu.Unlock()
	return fn(ctx, prID, event, body)
}

func (f *fakeGitHub) AddReviewThread(ctx context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	f.addReviewThreadCalls = append(f.addReviewThreadCalls, addReviewThreadCall{in: in})
	fn := f.addReviewThreadFunc
	f.mu.Unlock()
	return fn(ctx, in)
}

func (f *fakeGitHub) AddThreadReply(
	ctx context.Context, threadID, body, pendingReviewID string,
) (model.ReviewComment, model.RateLimit, error) {
	f.mu.Lock()
	f.addThreadReplyCalls = append(f.addThreadReplyCalls, addThreadReplyCall{
		threadID: threadID, body: body, pendingReviewID: pendingReviewID,
	})
	fn := f.addThreadReplyFunc
	f.mu.Unlock()
	return fn(ctx, threadID, body, pendingReviewID)
}

func (f *fakeGitHub) SubmitReview(
	ctx context.Context, reviewID string, event model.ReviewEvent, body string,
) (model.Review, model.RateLimit, error) {
	f.mu.Lock()
	f.submitReviewCalls = append(f.submitReviewCalls, submitReviewCall{reviewID: reviewID, event: event, body: body})
	fn := f.submitReviewFunc
	f.mu.Unlock()
	return fn(ctx, reviewID, event, body)
}

func (f *fakeGitHub) DeletePendingReview(ctx context.Context, reviewID string) (model.RateLimit, error) {
	f.mu.Lock()
	f.deletePendingReviewCalls = append(f.deletePendingReviewCalls, deletePendingReviewCall{reviewID: reviewID})
	fn := f.deletePendingReviewFunc
	f.mu.Unlock()
	return fn(ctx, reviewID)
}

func (f *fakeGitHub) UpdateReviewComment(ctx context.Context, id, body string) (model.ReviewComment, model.RateLimit, error) {
	f.mu.Lock()
	f.updateReviewCommentCalls = append(f.updateReviewCommentCalls, updateReviewCommentCall{id: id, body: body})
	fn := f.updateReviewCommentFunc
	f.mu.Unlock()
	return fn(ctx, id, body)
}

func (f *fakeGitHub) DeleteReviewComment(ctx context.Context, id string) (model.RateLimit, error) {
	f.mu.Lock()
	f.deleteReviewCommentCalls = append(f.deleteReviewCommentCalls, deleteReviewCommentCall{id: id})
	fn := f.deleteReviewCommentFunc
	f.mu.Unlock()
	return fn(ctx, id)
}

func (f *fakeGitHub) ResolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	f.resolveThreadCalls = append(f.resolveThreadCalls, resolveThreadCall{threadID: threadID})
	fn := f.resolveThreadFunc
	f.mu.Unlock()
	return fn(ctx, threadID)
}

func (f *fakeGitHub) UnresolveThread(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error) {
	f.mu.Lock()
	f.unresolveThreadCalls = append(f.unresolveThreadCalls, unresolveThreadCall{threadID: threadID})
	fn := f.unresolveThreadFunc
	f.mu.Unlock()
	return fn(ctx, threadID)
}

// createPendingReviewCallsSnapshot returns a copy of every
// CreatePendingReview call recorded so far, for assertions.
func (f *fakeGitHub) createPendingReviewCallsSnapshot() []createPendingReviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]createPendingReviewCall, len(f.createPendingReviewCalls))
	copy(out, f.createPendingReviewCalls)
	return out
}

// addReviewNowCallsSnapshot returns a copy of every AddReviewNow call
// recorded so far, for assertions.
func (f *fakeGitHub) addReviewNowCallsSnapshot() []addReviewNowCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addReviewNowCall, len(f.addReviewNowCalls))
	copy(out, f.addReviewNowCalls)
	return out
}

// addReviewNowWithEventCallsSnapshot returns a copy of every
// AddReviewNowWithEvent call recorded so far, for assertions.
func (f *fakeGitHub) addReviewNowWithEventCallsSnapshot() []addReviewNowWithEventCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addReviewNowWithEventCall, len(f.addReviewNowWithEventCalls))
	copy(out, f.addReviewNowWithEventCalls)
	return out
}

// addReviewThreadCallsSnapshot returns a copy of every AddReviewThread call
// recorded so far, for assertions.
func (f *fakeGitHub) addReviewThreadCallsSnapshot() []addReviewThreadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addReviewThreadCall, len(f.addReviewThreadCalls))
	copy(out, f.addReviewThreadCalls)
	return out
}

// addThreadReplyCallsSnapshot returns a copy of every AddThreadReply call
// recorded so far, for assertions.
func (f *fakeGitHub) addThreadReplyCallsSnapshot() []addThreadReplyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addThreadReplyCall, len(f.addThreadReplyCalls))
	copy(out, f.addThreadReplyCalls)
	return out
}

// submitReviewCallsSnapshot returns a copy of every SubmitReview call
// recorded so far, for assertions.
func (f *fakeGitHub) submitReviewCallsSnapshot() []submitReviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]submitReviewCall, len(f.submitReviewCalls))
	copy(out, f.submitReviewCalls)
	return out
}

// deletePendingReviewCallsSnapshot returns a copy of every
// DeletePendingReview call recorded so far, for assertions.
func (f *fakeGitHub) deletePendingReviewCallsSnapshot() []deletePendingReviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deletePendingReviewCall, len(f.deletePendingReviewCalls))
	copy(out, f.deletePendingReviewCalls)
	return out
}

// updateReviewCommentCallsSnapshot returns a copy of every
// UpdateReviewComment call recorded so far, for assertions.
func (f *fakeGitHub) updateReviewCommentCallsSnapshot() []updateReviewCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]updateReviewCommentCall, len(f.updateReviewCommentCalls))
	copy(out, f.updateReviewCommentCalls)
	return out
}

// deleteReviewCommentCallsSnapshot returns a copy of every
// DeleteReviewComment call recorded so far, for assertions.
func (f *fakeGitHub) deleteReviewCommentCallsSnapshot() []deleteReviewCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deleteReviewCommentCall, len(f.deleteReviewCommentCalls))
	copy(out, f.deleteReviewCommentCalls)
	return out
}

// resolveThreadCallsSnapshot returns a copy of every ResolveThread call
// recorded so far, for assertions.
func (f *fakeGitHub) resolveThreadCallsSnapshot() []resolveThreadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]resolveThreadCall, len(f.resolveThreadCalls))
	copy(out, f.resolveThreadCalls)
	return out
}

// unresolveThreadCallsSnapshot returns a copy of every UnresolveThread call
// recorded so far, for assertions.
func (f *fakeGitHub) unresolveThreadCallsSnapshot() []unresolveThreadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]unresolveThreadCall, len(f.unresolveThreadCalls))
	copy(out, f.unresolveThreadCalls)
	return out
}

// setCreatePendingReviewFunc reassigns createPendingReviewFunc under the
// lock, for the same reason setSearchFunc does.
func (f *fakeGitHub) setCreatePendingReviewFunc(fn func(ctx context.Context, prID string) (model.Review, model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createPendingReviewFunc = fn
}

// setAddReviewNowFunc reassigns addReviewNowFunc under the lock, for the
// same reason setSearchFunc does.
func (f *fakeGitHub) setAddReviewNowFunc(
	fn func(ctx context.Context, prID string, threads []gh.DraftThread, body string) (model.Review, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewNowFunc = fn
}

// setAddReviewNowWithEventFunc reassigns addReviewNowWithEventFunc under the
// lock, for the same reason setSearchFunc does.
func (f *fakeGitHub) setAddReviewNowWithEventFunc(
	fn func(ctx context.Context, prID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewNowWithEventFunc = fn
}

// setAddReviewThreadFunc reassigns addReviewThreadFunc under the lock, for
// the same reason setSearchFunc does.
func (f *fakeGitHub) setAddReviewThreadFunc(
	fn func(ctx context.Context, in gh.ThreadInput) (model.ReviewThread, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReviewThreadFunc = fn
}

// setAddThreadReplyFunc reassigns addThreadReplyFunc under the lock, for
// the same reason setSearchFunc does.
func (f *fakeGitHub) setAddThreadReplyFunc(
	fn func(ctx context.Context, threadID, body, pendingReviewID string) (model.ReviewComment, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addThreadReplyFunc = fn
}

// setSubmitReviewFunc reassigns submitReviewFunc under the lock, for the
// same reason setSearchFunc does.
func (f *fakeGitHub) setSubmitReviewFunc(
	fn func(ctx context.Context, reviewID string, event model.ReviewEvent, body string) (model.Review, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitReviewFunc = fn
}

// setDeletePendingReviewFunc reassigns deletePendingReviewFunc under the
// lock, for the same reason setSearchFunc does.
func (f *fakeGitHub) setDeletePendingReviewFunc(fn func(ctx context.Context, reviewID string) (model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletePendingReviewFunc = fn
}

// setUpdateReviewCommentFunc reassigns updateReviewCommentFunc under the
// lock, for the same reason setSearchFunc does.
func (f *fakeGitHub) setUpdateReviewCommentFunc(
	fn func(ctx context.Context, id, body string) (model.ReviewComment, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateReviewCommentFunc = fn
}

// setDeleteReviewCommentFunc reassigns deleteReviewCommentFunc under the
// lock, for the same reason setSearchFunc does.
func (f *fakeGitHub) setDeleteReviewCommentFunc(fn func(ctx context.Context, id string) (model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteReviewCommentFunc = fn
}

// setResolveThreadFunc reassigns resolveThreadFunc under the lock, for the
// same reason setSearchFunc does.
func (f *fakeGitHub) setResolveThreadFunc(
	fn func(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveThreadFunc = fn
}

// setUnresolveThreadFunc reassigns unresolveThreadFunc under the lock, for
// the same reason setSearchFunc does.
func (f *fakeGitHub) setUnresolveThreadFunc(
	fn func(ctx context.Context, threadID string) (model.ReviewThread, model.RateLimit, error),
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unresolveThreadFunc = fn
}

func (f *fakeGitHub) searchCalls() []searchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]searchCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeGitHub) detailCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.detailCalls)
}

func (f *fakeGitHub) addCommentCallsSnapshot() []addCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]addCommentCall, len(f.addCommentCalls))
	copy(out, f.addCommentCalls)
	return out
}

func (f *fakeGitHub) updateCommentCallsSnapshot() []updateCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]updateCommentCall, len(f.updateCommentCalls))
	copy(out, f.updateCommentCalls)
	return out
}

func (f *fakeGitHub) deleteCommentCallsSnapshot() []deleteCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deleteCommentCall, len(f.deleteCommentCalls))
	copy(out, f.deleteCommentCalls)
	return out
}

// setAddCommentFunc reassigns addCommentFunc under the lock, for the same
// reason setSearchFunc does.
func (f *fakeGitHub) setAddCommentFunc(fn func(ctx context.Context, subjectID, body string) (model.IssueComment, model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCommentFunc = fn
}

// setUpdateCommentFunc reassigns updateCommentFunc under the lock, for the
// same reason setSearchFunc does.
func (f *fakeGitHub) setUpdateCommentFunc(fn func(ctx context.Context, id, body string) (model.IssueComment, model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCommentFunc = fn
}

// setDeleteCommentFunc reassigns deleteCommentFunc under the lock, for the
// same reason setSearchFunc does.
func (f *fakeGitHub) setDeleteCommentFunc(fn func(ctx context.Context, id string) (model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCommentFunc = fn
}

// setSearchFunc reassigns searchFunc under the lock. Most tests reassign
// the field directly (safe in practice: they fully drain via
// runUntilIdle, which gives any goroutine spawned as a side effect of the
// drained callbacks time to have already read the old value, before
// reassigning); this is for the rare test that reassigns searchFunc while
// a fetch goroutine spawned moments earlier (as a side effect of the very
// dispatch callback runUntilIdle just ran) may not have started executing
// yet, which a plain field write would race with the read in
// SearchPullRequests.
func (f *fakeGitHub) setSearchFunc(fn func(ctx context.Context, query, cursor string) (gh.SearchResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchFunc = fn
}

// setDetailFunc reassigns detailFunc under the lock, for the same reason
// setSearchFunc does.
func (f *fakeGitHub) setDetailFunc(fn func(ctx context.Context, ref model.PRRef, viewerLogin string) (gh.DetailResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detailFunc = fn
}

// setFilesFunc reassigns filesFunc under the lock, for the same reason
// setSearchFunc does.
func (f *fakeGitHub) setFilesFunc(fn func(ctx context.Context, ref model.PRRef, page int, etag string) (gh.FilesResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filesFunc = fn
}

// filesCallsSnapshot returns a copy of every ChangedFiles call recorded so
// far, for assertions.
func (f *fakeGitHub) filesCallsSnapshot() []filesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]filesCall, len(f.filesCalls))
	copy(out, f.filesCalls)
	return out
}

// newTestStore builds a Store wired to a fakeGitHub and fakeDispatcher over
// an in-memory-backed cache.Store rooted at t.TempDir(), along with a fixed
// Now so tests can assert exact timestamps.
func newTestStore(t *testing.T, cfg config.Config, gitHub *fakeGitHub, disp *fakeDispatcher) *Store {
	t.Helper()
	cacheStore, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New() error = %v", err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	return New(Deps{
		GitHub:   gitHub,
		Cache:    cacheStore,
		Dispatch: disp.dispatch,
		Config:   cfg,
		Host:     "example.com",
		Now:      func() time.Time { return now },
	})
}

func TestSections_BuiltinsThenCustom(t *testing.T) {
	cfg := config.Default()
	cfg.List.Sections = []config.Section{{Name: "Backend", Query: "org:acme label:backend"}}

	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	sections := s.Sections()

	wantKinds := []model.SectionKind{
		model.SectionKindDirectReview,
		model.SectionKindTeamReview,
		model.SectionKindMine,
		model.SectionKindInvolved,
		model.SectionKindCustom,
	}
	if len(sections) != len(wantKinds) {
		t.Fatalf("Sections() has %d entries, want %d", len(sections), len(wantKinds))
	}
	for i, want := range wantKinds {
		if sections[i].Kind != want {
			t.Errorf("Sections()[%d].Kind = %v, want %v", i, sections[i].Kind, want)
		}
	}
	last := sections[len(sections)-1]
	if last.Name != "Backend" {
		t.Errorf("custom section Name = %q, want %q", last.Name, "Backend")
	}
	if last.Query != gh.BuildSearchQuery(model.SectionKindCustom, "org:acme label:backend", "open") {
		t.Errorf("custom section Query = %q, want the built query", last.Query)
	}
}

func TestSections_QueriesUseConfiguredState(t *testing.T) {
	cfg := config.Default()
	cfg.List.State = "closed"

	s := newTestStore(t, cfg, newFakeGitHub(), newFakeDispatcher())
	sections := s.Sections()

	want := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "closed")
	if sections[0].Query != want {
		t.Errorf("Sections()[0].Query = %q, want %q", sections[0].Query, want)
	}
}

func TestSetFilter_StateTriggersReloadWithNewQuery(t *testing.T) {
	cfg := config.Default() // state: open

	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	var mu sync.Mutex
	var queriesSeen []string
	gitHub.searchFunc = func(_ context.Context, query, _ string) (gh.SearchResult, error) {
		mu.Lock()
		queriesSeen = append(queriesSeen, query)
		mu.Unlock()
		return gh.SearchResult{}, nil
	}

	s.SetFilter("state:closed")
	runUntilIdle(t, disp)

	wantQuery := gh.BuildSearchQuery(model.SectionKindDirectReview, "", "closed")
	if s.Sections()[0].Query != wantQuery {
		t.Errorf("Sections()[0].Query = %q, want %q", s.Sections()[0].Query, wantQuery)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawNewQuery bool
	for _, q := range queriesSeen {
		if q == wantQuery {
			sawNewQuery = true
		}
	}
	if !sawNewQuery {
		t.Errorf("SearchPullRequests was never called with the reloaded query %q; calls = %v", wantQuery, queriesSeen)
	}
	if s.Filter() != "state:closed" {
		t.Errorf("Filter() = %q, want %q", s.Filter(), "state:closed")
	}
}

func TestSetFilter_NonStateQualifierDoesNotReload(t *testing.T) {
	cfg := config.Default()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newTestStore(t, cfg, gitHub, disp)

	s.SetFilter("author:alice")
	runUntilIdle(t, disp)

	if len(gitHub.searchCalls()) != 0 {
		t.Errorf("SetFilter with no state: qualifier must not trigger a reload; calls = %v", gitHub.searchCalls())
	}
}
