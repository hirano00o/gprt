// Package store's review operations (CommentOnLines/CommentOnFile/
// ReplyToThread/EditReviewComment/DeleteReviewComment/SetThreadResolved/
// DiscardPendingReview/SubmitReview) publish and manage a pull request's
// review threads and the viewer's own pending review, through the same
// single-flight mutation queue as mutations.go's issue-comment operations
// (see that file's package doc for the queue's contract). Every operation
// here follows the identical shape: enqueueMutation/enqueuePreparedMutation
// captures the values a mutation.run closure needs (never live Store
// fields - see mutations.go), runs its GitHub call(s) in a goroutine, and
// applies the result optimistically on the UI goroutine via the closure
// finishMutation invokes.
//
// "Ensure a pending review" (create one if none exists yet, then use its
// ID) and "which send mode currently applies" (SendSingle coerced to
// SendToReview once a pending review exists) are NOT decided at enqueue
// time. The mutation queue is single-flight only in the sense that at most
// one mutation's run ever talks to GitHub at once; enqueueing is not
// serialized against running, so a second CommentOnLines/CommentOnFile/
// ReplyToThread/SubmitReview/DiscardPendingReview call can be queued while
// an earlier one is still in flight (or merely queued ahead of it) — and
// by the time this one's turn comes, that earlier one may have created a
// pending review, submitted it, or discarded it. Deciding "single vs.
// review" and "which pending review ID" from a snapshot taken back at
// enqueue time would use stale state in exactly that situation. Instead,
// each of these five operations uses enqueuePreparedMutation: a prepare
// closure re-reads the relevant Store state (and re-validates the target
// pull request is still the one open) on the UI goroutine immediately
// before run's goroutine starts, writing what run needs into shared
// closure variables — see mutations.go's mutation.prepare and
// enqueuePreparedMutation doc comments for why this is race-free.
package store

import (
	"context"
	"errors"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// errThreadHasPendingComments is emitted (as an EventError) by
// SetThreadResolved when the target thread still has PENDING comments:
// GitHub disallows resolving a thread that is still part of an unpublished
// review, so surfacing this locally avoids a round trip only to be told
// the same thing by the API.
var errThreadHasPendingComments = errors.New("store: cannot resolve a thread with pending comments")

// errThreadNotResolvable is emitted (as an EventError) by
// SetThreadResolved(threadID, true) when the thread's own ViewerCanResolve
// flag (GitHub's authoritative, per-thread permission — also covers
// "already resolved" and thread-ownership restrictions, not just the
// pending-comments case errThreadHasPendingComments covers) says the
// viewer cannot resolve it right now. errThreadNotUnresolvable is its
// mirror for SetThreadResolved(threadID, false)/ViewerCanUnresolve: kept
// as two distinct sentinels, not one shared message, so the surfaced
// EventError names the direction the caller actually asked for.
var (
	errThreadNotResolvable   = errors.New("store: you cannot resolve this thread")
	errThreadNotUnresolvable = errors.New("store: you cannot unresolve this thread")
)

// errNoPendingReview is emitted by DiscardPendingReview when the current
// pull request has no pending review to discard.
var errNoPendingReview = errors.New("store: no pending review to discard")

// sendCoercedToReviewMessage is EventNotice's message when a SendSingle
// call was silently coerced to SendToReview: a pending review already
// existed, or was created by an earlier queued mutation, by the time this
// one actually ran (see SendSingle's doc comment).
const sendCoercedToReviewMessage = "Added to your pending review instead of posting a single comment"

// SendMode selects which of GitHub's two review-comment publishing paths a
// CommentOnLines/CommentOnFile/ReplyToThread call uses.
type SendMode int

// Known send modes.
const (
	// SendSingle publishes immediately as a standalone, already-submitted
	// review (GraphQL's addPullRequestReview with event: COMMENT). Only
	// meaningful when the viewer has no pending review yet: every send
	// method coerces SendSingle to SendToReview once one exists by the
	// time the mutation actually runs (see the package doc for why that
	// check happens then, not at enqueue time), matching GitHub's own UI,
	// which stops offering a single-comment button in that state. A
	// coerced send emits EventNotice so the UI can tell the user.
	SendSingle SendMode = iota
	// SendToReview accumulates the comment on the viewer's pending review,
	// creating one first if none exists yet.
	SendToReview
)

// PendingReview returns the current pull request's pending review, or nil
// when it has none or no pull request is open.
func (s *Store) PendingReview() *model.Review {
	if s.currentPR == nil {
		return nil
	}
	return s.currentPR.PendingReview
}

// HasPendingReview reports whether the current pull request has a pending
// review.
func (s *Store) HasPendingReview() bool {
	return s.pendingReviewID() != ""
}

// CanSendSingle reports whether a new comment can be published immediately
// as a standalone review (no pending review already exists to attach it
// to). The UI uses this to decide whether to offer "Add single comment" at
// all, per GitHub's own convention.
func (s *Store) CanSendSingle() bool {
	return !s.HasPendingReview()
}

// PendingComments returns every review comment across the current pull
// request's threads whose State is PENDING: the comments the viewer's own
// pending review (if any) has accumulated so far. It does not include the
// pending review's own top-level body text, which has no ReviewComment
// shape of its own (no ID, no per-comment State, no thread); a caller that
// also needs it reads PendingReview().Body separately.
func (s *Store) PendingComments() []model.ReviewComment {
	if s.currentPR == nil {
		return nil
	}
	var out []model.ReviewComment
	for _, t := range s.currentPR.ReviewThreads {
		for _, c := range t.Comments {
			if c.State == model.ReviewCommentStatePending {
				out = append(out, c)
			}
		}
	}
	return out
}

// pendingReviewID returns the current pull request's pending review ID, or
// "" when it has none or no pull request is open.
func (s *Store) pendingReviewID() string {
	if s.currentPR == nil || s.currentPR.PendingReview == nil {
		return ""
	}
	return s.currentPR.PendingReview.ID
}

// sendModeFor coerces mode to SendToReview whenever a pending review
// already exists (see SendSingle's doc comment), leaving it unchanged
// otherwise. Called only from a prepare closure (see the package doc), so
// it always reads state as of just before the mutation runs, not as of
// enqueue time.
func (s *Store) sendModeFor(mode SendMode) (effective SendMode, pendingID string) {
	pendingID = s.pendingReviewID()
	if pendingID != "" {
		return SendToReview, pendingID
	}
	return mode, pendingID
}

// ensurePendingReviewID returns pendingID unchanged when it is already
// non-empty, or creates a new pending review via GitHub.CreatePendingReview
// otherwise. created is non-nil only when a new review was actually
// created, so the caller's apply closure knows whether to add it to the
// store optimistically. Called only from inside a mutation's run closure
// (see the package doc): ctx and prID/pendingID are values already written
// by that mutation's own prepare closure, never live Store fields; s.deps
// is Store's immutable dependency set, safe to read from any goroutine.
func (s *Store) ensurePendingReviewID(
	ctx context.Context, prID, pendingID string,
) (reviewID string, created *model.Review, err error) {
	if pendingID != "" {
		return pendingID, nil, nil
	}
	rev, err := s.deps.GitHub.CreatePendingReview(ctx, prID)
	if err != nil {
		return "", nil, err
	}
	return rev.ID, &rev, nil
}

// draftThreadFromRange builds a gh.DraftThread for AddReviewNow from a
// diff.Range. A range whose StartLine is zero or equal to Line is a
// single-line comment: StartLine/StartSide are left zero so gh.DraftThread
// omits them from the GraphQL input entirely (see DraftThread.toInput),
// rather than sending a same-line "range" of length one.
func draftThreadFromRange(path string, r diff.Range, body string) gh.DraftThread {
	t := gh.DraftThread{Path: path, Line: r.Line, Side: r.Side, Body: body}
	if r.StartLine != 0 && r.StartLine != r.Line {
		t.StartLine = r.StartLine
		t.StartSide = r.StartSide
	}
	return t
}

// threadInputFromRange builds a gh.ThreadInput for AddReviewThread from a
// diff.Range, mirroring draftThreadFromRange's single-line-vs-range
// distinction.
func threadInputFromRange(reviewID, path string, r diff.Range, body string) gh.ThreadInput {
	in := gh.ThreadInput{
		PullRequestReviewID: reviewID, Path: path, Line: r.Line, Side: r.Side,
		SubjectType: model.ThreadSubjectLine, Body: body,
	}
	if r.StartLine != 0 && r.StartLine != r.Line {
		in.StartLine = r.StartLine
		in.StartSide = r.StartSide
	}
	return in
}

// markThreadCommentsSubmitted returns a copy of thread with every comment's
// State set to SUBMITTED. Used by CommentOnFile's SendSingle path: the
// review owning the thread AddReviewThread just returned was immediately
// submitted (SubmitReview, event: COMMENT) within the same mutation run,
// so its comment(s) are no longer PENDING — reflecting that locally avoids
// briefly showing a stale PENDING state before the refetch that always
// follows a mutation confirms the same thing from GitHub's own data.
func markThreadCommentsSubmitted(thread model.ReviewThread) model.ReviewThread {
	comments := make([]model.ReviewComment, len(thread.Comments))
	for i, c := range thread.Comments {
		c.State = model.ReviewCommentStateSubmitted
		comments[i] = c
	}
	thread.Comments = comments
	return thread
}

// CommentOnLines publishes a comment anchored to a single line or a
// single-sided line range (anchor; see diff.RangeAnchor for how the caller
// derives one) on path. mode selects single-comment vs add-to-review
// publishing; which one actually applies is decided immediately before the
// mutation runs, not at enqueue time (see the package doc), so a pending
// review created by an earlier queued mutation — or SendSingle's coercion
// to SendToReview once one exists — is always resolved from up-to-date
// state. Returns false (besides emitting an EventError) without enqueueing
// anything when no pull request is open or its detail has not resolved yet
// — see AddComment's doc comment for why a caller holding text on the
// strength of this call must check the return value.
func (s *Store) CommentOnLines(path string, anchor diff.Range, body string, mode SendMode) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var prID string
	var effectiveMode SendMode
	var pendingID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		prID = s.currentPR.ID
		effectiveMode, pendingID = s.sendModeFor(mode)
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		if effectiveMode == SendSingle {
			s.deps.Logger.Debug("comment_on_lines: single", "path", path)
			thread := draftThreadFromRange(path, anchor, body)
			_, err := s.deps.GitHub.AddReviewNow(ctx, prID, []gh.DraftThread{thread}, "")
			if err != nil {
				return nil, err
			}
			return func() {
				s.emit(Event{Kind: EventPRChanged})
			}, nil
		}

		s.deps.Logger.Debug("comment_on_lines: review", "path", path, "creates_pending", pendingID == "")
		reviewID, created, err := s.ensurePendingReviewID(ctx, prID, pendingID)
		if err != nil {
			return nil, err
		}
		thread, err := s.deps.GitHub.AddReviewThread(ctx, threadInputFromRange(reviewID, path, anchor, body))
		if err != nil {
			return nil, err
		}
		coerced := mode == SendSingle
		return func() {
			if coerced {
				s.emit(Event{Kind: EventNotice, Message: sendCoercedToReviewMessage})
			}
			if created != nil {
				s.applyCreatedPendingReview(*created)
			}
			s.appendReviewThread(thread)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutation("comment_on_lines", prepare, run)
	return true
}

// CommentOnFile publishes a whole-file comment on path. mode's actual
// path (single vs. add-to-review) is decided at run-start time, not
// enqueue time — see CommentOnLines' doc comment. For SendSingle (only
// reachable when no pending review exists by then), the review is
// created, given its one thread, and submitted in a single mutation run;
// a failure partway through is never left as silent local drift from
// GitHub's own state, since every finished mutation — successful or not —
// invalidates and refetches this pull request (see invalidateAndRefetch in
// mutations.go), so a pending review GitHub ends up holding after such a
// failure is reflected locally by that refetch, offered for discard via
// DiscardPendingReview.
func (s *Store) CommentOnFile(path, body string, mode SendMode) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var prID string
	var effectiveMode SendMode
	var pendingID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		prID = s.currentPR.ID
		effectiveMode, pendingID = s.sendModeFor(mode)
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		if effectiveMode == SendSingle {
			s.deps.Logger.Debug("comment_on_file: single", "path", path)
			rev, err := s.deps.GitHub.CreatePendingReview(ctx, prID)
			if err != nil {
				return nil, err
			}
			thread, err := s.deps.GitHub.AddReviewThread(ctx, gh.ThreadInput{
				PullRequestReviewID: rev.ID, Path: path, SubjectType: model.ThreadSubjectFile, Body: body,
			})
			if err != nil {
				return nil, err
			}
			_, err = s.deps.GitHub.SubmitReview(ctx, rev.ID, model.ReviewEventComment, "")
			if err != nil {
				return nil, err
			}
			return func() {
				s.appendReviewThread(markThreadCommentsSubmitted(thread))
				s.emit(Event{Kind: EventPRChanged})
			}, nil
		}

		s.deps.Logger.Debug("comment_on_file: review", "path", path, "creates_pending", pendingID == "")
		reviewID, created, err := s.ensurePendingReviewID(ctx, prID, pendingID)
		if err != nil {
			return nil, err
		}
		thread, err := s.deps.GitHub.AddReviewThread(ctx, gh.ThreadInput{
			PullRequestReviewID: reviewID, Path: path, SubjectType: model.ThreadSubjectFile, Body: body,
		})
		if err != nil {
			return nil, err
		}
		coerced := mode == SendSingle
		return func() {
			if coerced {
				s.emit(Event{Kind: EventNotice, Message: sendCoercedToReviewMessage})
			}
			if created != nil {
				s.applyCreatedPendingReview(*created)
			}
			s.appendReviewThread(thread)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutation("comment_on_file", prepare, run)
	return true
}

// ReplyToThread replies to an existing review thread. mode's actual path
// is decided at run-start time, not enqueue time — see CommentOnLines' doc
// comment.
func (s *Store) ReplyToThread(threadID, body string, mode SendMode) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var prID string
	var effectiveMode SendMode
	var pendingID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		prID = s.currentPR.ID
		effectiveMode, pendingID = s.sendModeFor(mode)
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		if effectiveMode == SendSingle {
			s.deps.Logger.Debug("reply_to_thread: single", "thread", threadID)
			comment, err := s.deps.GitHub.AddThreadReply(ctx, threadID, body, "")
			if err != nil {
				return nil, err
			}
			return func() {
				s.appendThreadReply(threadID, comment)
				s.emit(Event{Kind: EventPRChanged})
			}, nil
		}

		s.deps.Logger.Debug("reply_to_thread: review", "thread", threadID, "creates_pending", pendingID == "")
		reviewID, created, err := s.ensurePendingReviewID(ctx, prID, pendingID)
		if err != nil {
			return nil, err
		}
		comment, err := s.deps.GitHub.AddThreadReply(ctx, threadID, body, reviewID)
		if err != nil {
			return nil, err
		}
		coerced := mode == SendSingle
		return func() {
			if coerced {
				s.emit(Event{Kind: EventNotice, Message: sendCoercedToReviewMessage})
			}
			if created != nil {
				s.applyCreatedPendingReview(*created)
			}
			s.appendThreadReply(threadID, comment)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutation("reply_to_thread", prepare, run)
	return true
}

// EditReviewComment updates an existing review comment's body.
func (s *Store) EditReviewComment(id, body string) bool {
	if !s.mutationTarget() {
		return false
	}
	s.enqueueMutation("edit_review_comment", func(ctx context.Context) (func(), error) {
		comment, err := s.deps.GitHub.UpdateReviewComment(ctx, id, body)
		if err != nil {
			return nil, err
		}
		return func() {
			s.replaceReviewComment(comment)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// DeleteReviewComment deletes an existing review comment. If it was the
// last comment of a thread belonging to a pending review, the (now
// commentless) thread is dropped locally, but the pending review itself is
// left in place: the UI's pending-comments list ("p") is where discarding
// an empty pending review is offered (see DiscardPendingReview), not an
// automatic side effect of deleting its last comment.
func (s *Store) DeleteReviewComment(id string) bool {
	if !s.mutationTarget() {
		return false
	}
	s.enqueueMutation("delete_review_comment", func(ctx context.Context) (func(), error) {
		if err := s.deps.GitHub.DeleteReviewComment(ctx, id); err != nil {
			return nil, err
		}
		return func() {
			s.removeReviewComment(id)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// findReviewThread returns the review thread whose ID is threadID on the
// current pull request, or (zero value, false) when no pull request is
// open or none matches.
func (s *Store) findReviewThread(threadID string) (model.ReviewThread, bool) {
	if s.currentPR == nil {
		return model.ReviewThread{}, false
	}
	for _, t := range s.currentPR.ReviewThreads {
		if t.ID == threadID {
			return t, true
		}
	}
	return model.ReviewThread{}, false
}

// threadHasPendingComments reports whether thread has any comment whose
// State is PENDING.
func (s *Store) threadHasPendingComments(thread model.ReviewThread) bool {
	for _, c := range thread.Comments {
		if c.State == model.ReviewCommentStatePending {
			return true
		}
	}
	return false
}

// SetThreadResolved resolves or unresolves threadID. When the thread is
// found locally, refused (with an EventError, no mutation enqueued) if
// either: the viewer cannot perform that specific action right now
// (thread.ViewerCanResolve/ViewerCanUnresolve — GitHub's own authoritative
// per-thread permission, checked first since it also covers "already
// resolved"/"already unresolved" and thread-ownership restrictions, not
// just the case below), or the thread still has PENDING comments (GitHub
// does not allow resolving a thread that is still part of an unpublished
// review). A thread not found locally (should not normally happen) is not
// refused: the mutation is enqueued and left to GitHub's own validation.
func (s *Store) SetThreadResolved(threadID string, resolved bool) bool {
	if !s.mutationTarget() {
		return false
	}
	if thread, found := s.findReviewThread(threadID); found {
		switch {
		case resolved && !thread.ViewerCanResolve:
			s.emit(Event{Kind: EventError, Err: errThreadNotResolvable})
			return false
		case !resolved && !thread.ViewerCanUnresolve:
			s.emit(Event{Kind: EventError, Err: errThreadNotUnresolvable})
			return false
		case s.threadHasPendingComments(thread):
			s.emit(Event{Kind: EventError, Err: errThreadHasPendingComments})
			return false
		}
	}

	name := "resolve_thread"
	if !resolved {
		name = "unresolve_thread"
	}
	s.enqueueMutation(name, func(ctx context.Context) (func(), error) {
		var thread model.ReviewThread
		var err error
		if resolved {
			thread, err = s.deps.GitHub.ResolveThread(ctx, threadID)
		} else {
			thread, err = s.deps.GitHub.UnresolveThread(ctx, threadID)
		}
		if err != nil {
			return nil, err
		}
		return func() {
			s.applyThreadResolution(thread)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	})
	return true
}

// DiscardPendingReview deletes the current pull request's pending review.
// Returns false (besides emitting an EventError) when no pull request is
// open, its detail has not resolved yet, or it has no pending review to
// discard right now. prepare re-checks the latter immediately before the
// mutation runs too (see the package doc), in case an earlier queued
// mutation submitted or discarded it first.
func (s *Store) DiscardPendingReview() bool {
	if !s.mutationTarget() {
		return false
	}
	if s.pendingReviewID() == "" {
		s.emit(Event{Kind: EventError, Err: errNoPendingReview})
		return false
	}
	ref := *s.current

	var pendingID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		pendingID = s.pendingReviewID()
		if pendingID == "" {
			return errNoPendingReview
		}
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		if err := s.deps.GitHub.DeletePendingReview(ctx, pendingID); err != nil {
			return nil, err
		}
		return func() {
			s.applyDiscardPendingReview()
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	s.enqueuePreparedMutation("discard_pending_review", prepare, run)
	return true
}

// SubmitReview submits the current pull request's review with event and
// body: an existing pending review is submitted via GitHub.SubmitReview;
// with none, GitHub.AddReviewNowWithEvent both creates and submits a
// review in one call. Which of the two applies is decided at run-start
// time, not enqueue time — see CommentOnLines' doc comment — so a pending
// review an earlier queued mutation created is never missed.
func (s *Store) SubmitReview(event model.ReviewEvent, body string) bool {
	if !s.mutationTarget() {
		return false
	}
	ref := *s.current

	var prID string
	var pendingID string
	prepare := func() error {
		if s.current == nil || *s.current != ref || s.currentPR == nil {
			return errMutationTargetChanged
		}
		prID = s.currentPR.ID
		pendingID = s.pendingReviewID()
		return nil
	}

	run := func(ctx context.Context) (func(), error) {
		var review model.Review
		var err error
		if pendingID != "" {
			s.deps.Logger.Debug("submit_review: pending", "review_id", pendingID)
			review, err = s.deps.GitHub.SubmitReview(ctx, pendingID, event, body)
		} else {
			s.deps.Logger.Debug("submit_review: no pending review")
			review, err = s.deps.GitHub.AddReviewNowWithEvent(ctx, prID, event, body)
		}
		if err != nil {
			return nil, err
		}
		return func() {
			s.applySubmitReview(review)
			s.emit(Event{Kind: EventPRChanged})
		}, nil
	}

	// M5: also refreshes the list on success (enqueuePreparedMutationWithListRefresh,
	// not the plain enqueuePreparedMutation every other review operation in
	// this file uses) — a submitted review can flip ReviewDecision, which
	// the list itself displays; see mutations.go's mutation.refreshList doc
	// comment.
	s.enqueuePreparedMutationWithListRefresh("submit_review", prepare, run)
	return true
}

// applyCreatedPendingReview records review as the current pull request's
// newly created pending review, copy-on-write (see appendTimelineComment in
// mutations.go for the pattern every apply helper in this file follows). A
// no-op if no pull request is open (should not normally happen: only
// called from a mutation's apply, which finishMutation only invokes while
// the mutation's target ref is still current).
func (s *Store) applyCreatedPendingReview(review model.Review) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.PendingReview = &review
	s.currentPR = &pr
}

// appendReviewThread appends thread to the current pull request's
// ReviewThreads, copy-on-write.
func (s *Store) appendReviewThread(thread model.ReviewThread) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.ReviewThreads = append(append([]model.ReviewThread(nil), pr.ReviewThreads...), thread)
	s.currentPR = &pr
}

// appendThreadReply appends comment to the thread whose ID is threadID,
// copy-on-write. A no-op if no pull request is open or threadID is not
// found (should not normally happen: ReplyToThread always targets a
// thread the Store already holds).
func (s *Store) appendThreadReply(threadID string, comment model.ReviewComment) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	threads := append([]model.ReviewThread(nil), pr.ReviewThreads...)
	for i, t := range threads {
		if t.ID != threadID {
			continue
		}
		t.Comments = append(append([]model.ReviewComment(nil), t.Comments...), comment)
		threads[i] = t
		break
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}

// replaceReviewComment replaces the review comment whose ID matches
// updated.ID with updated, copy-on-write. A no-op if no pull request is
// open, or if no comment matches (should not normally happen: EditReviewComment
// always targets a comment the Store already holds).
func (s *Store) replaceReviewComment(updated model.ReviewComment) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	threads := make([]model.ReviewThread, len(pr.ReviewThreads))
	for i, t := range pr.ReviewThreads {
		comments := t.Comments
		for j, c := range comments {
			if c.ID != updated.ID {
				continue
			}
			comments = append([]model.ReviewComment(nil), comments...)
			comments[j] = updated
			break
		}
		t.Comments = comments
		threads[i] = t
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}

// removeReviewComment removes the review comment whose ID is id,
// copy-on-write. A thread left with no comments after the removal is
// dropped entirely (see CommentOnFile's/DeleteReviewComment's doc comments
// for why the pending review itself, if any, is left untouched here). A
// no-op if no pull request is open.
func (s *Store) removeReviewComment(id string) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	threads := make([]model.ReviewThread, 0, len(pr.ReviewThreads))
	for _, t := range pr.ReviewThreads {
		comments := make([]model.ReviewComment, 0, len(t.Comments))
		for _, c := range t.Comments {
			if c.ID == id {
				continue
			}
			comments = append(comments, c)
		}
		if len(comments) == 0 {
			continue
		}
		t.Comments = comments
		threads = append(threads, t)
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}

// applyThreadResolution overwrites the IsResolved/ViewerCanResolve/
// ViewerCanUnresolve fields of the thread whose ID is updated.ID with
// updated's own values (from ResolveThread/UnresolveThread's payload),
// copy-on-write. All three are taken from the server's response rather
// than flipped locally: GitHub toggles both viewer-can-* flags together
// with IsResolved (a resolved thread reports ViewerCanUnresolve, not
// ViewerCanResolve, and vice versa), and using the server's own values
// avoids SetThreadResolved refusing a second toggle against a stale flag
// this Store never updated, before a refetch confirms the first one. A
// no-op if no pull request is open or updated.ID is not found.
func (s *Store) applyThreadResolution(updated model.ReviewThread) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	threads := append([]model.ReviewThread(nil), pr.ReviewThreads...)
	for i, t := range threads {
		if t.ID != updated.ID {
			continue
		}
		t.IsResolved = updated.IsResolved
		t.ViewerCanResolve = updated.ViewerCanResolve
		t.ViewerCanUnresolve = updated.ViewerCanUnresolve
		threads[i] = t
		break
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}

// applyDiscardPendingReview clears the current pull request's pending
// review and removes every PENDING comment (dropping any thread left
// commentless as a result), copy-on-write.
func (s *Store) applyDiscardPendingReview() {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.PendingReview = nil
	threads := make([]model.ReviewThread, 0, len(pr.ReviewThreads))
	for _, t := range pr.ReviewThreads {
		comments := make([]model.ReviewComment, 0, len(t.Comments))
		for _, c := range t.Comments {
			if c.State == model.ReviewCommentStatePending {
				continue
			}
			comments = append(comments, c)
		}
		if len(comments) == 0 {
			continue
		}
		t.Comments = comments
		threads = append(threads, t)
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}

// applySubmitReview clears the current pull request's pending review and
// marks every PENDING comment SUBMITTED (kept, not removed), copy-on-write.
// review's own richer fields (SubmittedAt, ReactionGroups, its place in
// LatestReviews) are left to the refetch finishMutation always triggers
// immediately after, rather than reconstructed here.
func (s *Store) applySubmitReview(_ model.Review) {
	if s.currentPR == nil {
		return
	}
	pr := *s.currentPR
	pr.PendingReview = nil
	threads := make([]model.ReviewThread, len(pr.ReviewThreads))
	for i, t := range pr.ReviewThreads {
		comments := make([]model.ReviewComment, len(t.Comments))
		for j, c := range t.Comments {
			if c.State == model.ReviewCommentStatePending {
				c.State = model.ReviewCommentStateSubmitted
			}
			comments[j] = c
		}
		t.Comments = comments
		threads[i] = t
	}
	pr.ReviewThreads = threads
	s.currentPR = &pr
}
