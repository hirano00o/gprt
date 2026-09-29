// composer.go wires internal/ui/editor's Editor primitive into gprt's
// detail column: opening/closing the composer pane, draft persistence
// (write-through save, restore on open, delete on send/discard), sending
// a comment through internal/store's mutation queue, and deriving mention
// candidates from the currently open pull request.
package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"github.com/sahilm/fuzzy"

	"github.com/hirano00o/gprt/internal/diff"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/editor"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// composerKind identifies what a composerTarget's text becomes once sent.
type composerKind int

// Known composer kinds.
const (
	// composerKindComment sends a new general (issue) comment.
	composerKindComment composerKind = iota
	// composerKindEdit updates an existing (issue) comment's body.
	composerKindEdit
	// composerKindLineComment sends a new review comment anchored to a
	// single diff line or a single-sided line range (target.path,
	// target.anchor).
	composerKindLineComment
	// composerKindFileComment sends a new whole-file review comment
	// (target.path).
	composerKindFileComment
	// composerKindReply sends a reply to an existing review thread
	// (target.threadID).
	composerKindReply
	// composerKindReviewEdit updates an existing review comment's body
	// (target.commentID).
	composerKindReviewEdit
	// composerKindReviewBody submits (or creates and submits) the current
	// pull request's review with target.reviewEvent (submitreview.go).
	composerKindReviewBody
	// composerKindPRBody updates the current pull request's own body
	// (prbody.go), sending Store.UpdatePullRequestMeta with only Body set.
	composerKindPRBody
	// composerKindNewPRBody edits the create-PR form's own body
	// (createform.go), for a pull request that does not exist yet. Unlike
	// every other kind, Send never calls the store at all: it hands the
	// text back to the form (App.createFormBody) and the composer closes
	// keeping its draft, deleted only once the pull request is actually
	// created. It is also the only kind not bound to Store.CurrentRef() —
	// composerTarget.ref carries a repository-scoped sentinel ref (Number
	// 0, see drafts.KindNewPR's own doc comment), not a real pull request
	// — so closeComposerIfWrongPR must never close it on an unrelated PR
	// switch, and sendComposer must never refuse it merely because some
	// other (or no) pull request happens to be current.
	composerKindNewPRBody
)

// composerTarget identifies what the currently open composer is editing.
type composerTarget struct {
	kind composerKind
	// ref is the pull request this composer belongs to. sendComposer
	// refuses to send once Store.CurrentRef() no longer equals it (a
	// different pull request became current), and the App itself closes
	// the composer proactively the moment that happens (see
	// closeComposerIfWrongPR) — together, a Send can never reach the
	// store for a pull request other than the one the composer was
	// actually opened for.
	ref model.PRRef
	// commentID is the comment being edited, for composerKindEdit and
	// composerKindReviewEdit.
	commentID string
	// path is the file being commented on, for composerKindLineComment and
	// composerKindFileComment.
	path string
	// anchor is the line/range being commented on, for
	// composerKindLineComment only.
	anchor diff.Range
	// threadID is the thread being replied to, for composerKindReply only.
	threadID string
	// reviewEvent is the review event being submitted, for
	// composerKindReviewBody only (submitreview.go).
	reviewEvent model.ReviewEvent
	// title is the composer's title line ("Comment on #12",
	// "Edit comment by @alice", …).
	title string
	// draftKey is where this target's draft is saved/restored.
	draftKey drafts.Key
}

// pendingSend is the text and target of a composer's most recent Send,
// kept until the resulting mutation finishes — see the App.pendingSend
// field's own doc comment for why exactly one can ever be outstanding.
type pendingSend struct {
	text   string
	target composerTarget
	// notice is a store.EventNotice message (for example a SendSingle send
	// silently coerced to "add to review") that arrived for this send —
	// see storeevents.go's own EventNotice handler and onMutationChanged's
	// use of it below. Empty when no notice fired for this send.
	notice string
}

// openGeneralCommentComposer opens a composer for a new general PR
// comment (diff.comment, "c", while the PR tab has focus).
func (a *App) openGeneralCommentComposer() {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		a.showToast("no pull request open", theme.Warning)
		return
	}
	a.openComposer(composerTarget{
		kind:     composerKindComment,
		ref:      ref,
		title:    fmt.Sprintf("Comment on #%d", ref.Number),
		draftKey: drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: "issue"},
	}, "")
}

// openLineOrRangeComposer opens a composer for a new review comment
// anchored to a single diff line or a single-sided line range on path
// (diff.comment, "c", while the diff has focus and the cursor is not on a
// thread). r.StartLine == 0 or == r.Line renders the single-line title
// form; otherwise the range form.
func (a *App) openLineOrRangeComposer(path string, r diff.Range) {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return
	}
	var title string
	if r.StartLine != 0 && r.StartLine != r.Line {
		title = fmt.Sprintf("Comment on %s:%d-%d (%s)", path, r.StartLine, r.Line, r.Side)
	} else {
		title = fmt.Sprintf("Comment on %s:%d (%s)", path, r.Line, r.Side)
	}
	a.openComposer(composerTarget{
		kind:     composerKindLineComment,
		ref:      ref,
		path:     path,
		anchor:   r,
		title:    title,
		draftKey: drafts.Key{PR: ref.Key(), Kind: drafts.KindComment, Anchor: formatLineAnchor(path, r.Side, r.StartLine, r.Line)},
	}, "")
}

// openFileCommentComposer opens a composer for a new whole-file review
// comment on path (diff.comment_file, "C").
func (a *App) openFileCommentComposer(path string) {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return
	}
	a.openComposer(composerTarget{
		kind:     composerKindFileComment,
		ref:      ref,
		path:     path,
		title:    "Comment on file " + path,
		draftKey: drafts.Key{PR: ref.Key(), Kind: drafts.KindFile, Anchor: "file:" + path},
	}, "")
}

// openReplyComposer opens a composer replying to threadID (thread.reply,
// "r", or "c" on an existing thread); authorLogin names the thread's first
// comment's author, for the title only.
func (a *App) openReplyComposer(threadID, authorLogin string) {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return
	}
	a.openComposer(composerTarget{
		kind:     composerKindReply,
		ref:      ref,
		threadID: threadID,
		title:    "Reply to @" + authorLogin,
		draftKey: drafts.Key{PR: ref.Key(), Kind: drafts.KindReply, Anchor: threadID},
	}, "")
}

// openReviewCommentEditComposer opens an edit composer for an existing
// review comment (comment.edit, "e", with the diff's cursor on a thread
// row, or "Enter" on a pending item in the "p" list), prefilled with its
// live body.
func (a *App) openReviewCommentEditComposer(c model.ReviewComment) {
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return
	}
	a.openComposer(composerTarget{
		kind:      composerKindReviewEdit,
		ref:       ref,
		commentID: c.ID,
		title:     "Edit review comment",
		draftKey:  drafts.Key{PR: ref.Key(), Kind: drafts.KindEdit, Anchor: c.ID},
	}, c.Body)
}

// editCurrentComment opens an edit composer for the issue comment block
// under the PR tab's cursor (comment.edit, "e"), or toasts why it cannot:
// no comment is under the cursor, or the viewer does not own it. On the
// description block (the pull request's own body), it opens the PR-body
// composer instead (prbody.go) — see openPRBodyEditComposer's own doc
// comment for its ViewerCanUpdate check.
func (a *App) editCurrentComment() {
	if a.app.GetFocus() == a.prView && a.prView.CurrentID() == "description" {
		a.openPRBodyEditComposer()
		return
	}
	c, ok := a.currentIssueComment()
	if !ok {
		return
	}
	if !c.ViewerCanUpdate {
		a.showToast("you can only edit your own comments", theme.Warning)
		return
	}
	ref, _ := a.deps.Store.CurrentRef()
	a.openComposer(composerTarget{
		kind:      composerKindEdit,
		ref:       ref,
		commentID: c.ID,
		title:     "Edit comment by @" + c.Author.Login,
		draftKey:  drafts.Key{PR: ref.Key(), Kind: drafts.KindEdit, Anchor: c.ID},
	}, c.Body)
}

// deleteCurrentComment confirms, then deletes, the issue comment block
// under the PR tab's cursor (comment.delete, "d"), or toasts why it
// cannot: no comment is under the cursor, or the viewer does not own it.
// On the description block, "d" is a no-op toast — a pull request's own
// body cannot be deleted, only edited (see editCurrentComment).
func (a *App) deleteCurrentComment() {
	if a.app.GetFocus() == a.prView && a.prView.CurrentID() == "description" {
		a.showToast("cannot delete the pull request body; edit it with \"e\" instead", theme.Warning)
		return
	}
	c, ok := a.currentIssueComment()
	if !ok {
		return
	}
	if !c.ViewerCanDelete {
		a.showToast("you can only delete your own comments", theme.Warning)
		return
	}
	id := c.ID
	a.showConfirm("Delete this comment?", "Delete", func() {
		a.deps.Store.DeleteComment(id)
	})
}

// currentIssueComment returns the model.IssueComment the PR tab's cursor
// is currently on, if any: block IDs for a timeline entry are
// "timeline:<index>" (see detailtimeline.go's conversationBlocks), so this
// parses that back into a Timeline index rather than widget.Block needing
// a comment-specific field of its own.
func (a *App) currentIssueComment() (*model.IssueComment, bool) {
	if a.app.GetFocus() != a.prView {
		return nil, false
	}
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return nil, false
	}
	var idx int
	if n, err := fmt.Sscanf(a.prView.CurrentID(), "timeline:%d", &idx); n != 1 || err != nil {
		return nil, false
	}
	if idx < 0 || idx >= len(pr.Timeline) {
		return nil, false
	}
	item := pr.Timeline[idx]
	if item.Kind != model.TimelineKindIssueComment || item.IssueComment == nil {
		return nil, false
	}
	return item.IssueComment, true
}

// openComposer opens target's composer pane, saving (not discarding) any
// currently open composer's own draft first if one is already open for a
// different target — switching targets never asks, per docs/DESIGN.md.
// The buffer is seeded from an existing draft for target, if one exists,
// or from prefill otherwise (the comment's live body, for an edit
// composer; "" for a new general comment).
func (a *App) openComposer(target composerTarget, prefill string) {
	if a.composerFlex != nil {
		if a.composerTarget != nil && *a.composerTarget == target {
			a.app.SetFocus(a.composerEditor)
			return
		}
		a.closeComposer(true)
	}

	a.composerReturnFocus = a.app.GetFocus()
	a.composerDraftSaveErrShown = false
	a.composerDirty = false
	a.composerCtrlWPending = false
	t := target
	a.composerTarget = &t

	a.composerTitle = tview.NewTextView().SetText(target.title)
	a.composerEditor = editor.New(editor.Options{
		Candidates:     a.mentionCandidates,
		Suspend:        a.suspendForExternalEditor,
		ExternalEditor: a.deps.Config.Editor,
		OnChange:       a.onComposerChange,
		OnAction:       a.onComposerAction,
		OnError:        a.onComposerError,
	})

	text := prefill
	if a.deps.Drafts != nil {
		if d, ok, err := a.deps.Drafts.Load(target.draftKey); err != nil {
			// A load failure (as opposed to "no draft exists", which
			// Load reports as ok == false, not an error) means a draft
			// may exist on disk but could not be restored — a toast, not
			// just a debug-log Warn, since the user would otherwise have
			// no way to know their previous text might still be sitting
			// there unrecovered.
			a.showErrorToast("draft load failed: " + err.Error())
		} else if ok {
			text = d.Text
		}
	}
	if text != "" {
		a.composerEditor.SetText(text)
	}
	// Baseline for onComposerChange's own flip detection (see its doc
	// comment): whether a draft already exists for target right now,
	// exactly matching what Drafts.Save(target.draftKey, text)'s own
	// empty-deletes-the-draft rule would compute for this same text.
	a.composerDraftGutterMarked = text != ""

	a.composerFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	a.composerFlex.AddItem(a.composerTitle, 1, 0, false)
	a.composerFlex.AddItem(a.composerEditor, 0, 1, true)
	if target.kind == composerKindNewPRBody {
		// The create-PR form's own body slot hosts this composer instead of
		// detailColumn (composerHost below) — swap out its read-only body
		// view in favour of the composer's own Flex, proportionally sized
		// (composerHeight() is detailColumn-based, meaningless here).
		a.createFormBodySlot.RemoveItem(a.createFormBodyView)
		a.createFormBodySlot.AddItem(a.composerFlex, 0, 1, true)
	} else {
		a.detailColumn.AddItem(a.composerFlex, a.composerHeight(), 0, true)
	}

	a.app.SetFocus(a.composerEditor)
}

// composerHost returns the container that hosts the composer's own Flex
// for kind: a.detailColumn for every kind except composerKindNewPRBody,
// whose own host is the create-PR form's own body slot instead (see
// composerKindNewPRBody's own doc comment for why that composer lives
// inside the create form's dialog rather than the detail column like every
// other one).
func (a *App) composerHost(kind composerKind) *tview.Flex {
	if kind == composerKindNewPRBody {
		return a.createFormBodySlot
	}
	return a.detailColumn
}

// closeComposerIfWrongPR closes the open composer, saving its draft
// (under its own original target key — the composer belonging to the
// pull request it was opened for, not whichever one is current by the
// time this runs), the moment the currently open pull request no longer
// matches it. Called from EventPRChanged, so a composer for pull request
// A can never still be open, let alone sent, once pull request B becomes
// current — see composerTarget.ref's own doc comment. A no-op for
// composerKindNewPRBody (the create-PR form's own body composer): it is
// not bound to Store.CurrentRef() at all — the create-PR form itself must
// stay open across an unrelated pull request switch, per its own design —
// so an EventPRChanged here must never close it.
//
// The composer's own send-mode choice menu (dialogs.go's showChoiceMenu,
// a.overlay == "choice") stays open across this — it is a separate
// overlay from the composer pane itself, and closeComposer alone does not
// touch it — so it is cancelled here too (without invoking its stored
// callback): otherwise a menu opened for this composer's target would
// linger on screen after the composer underneath it is gone, and its own
// "Add single/to review" choice would (harmlessly, since
// performReviewSend re-checks the ref itself — see its own doc comment,
// but confusingly) still be offered against a target that no longer
// applies.
func (a *App) closeComposerIfWrongPR() {
	if a.composerTarget == nil || a.composerTarget.kind == composerKindNewPRBody {
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.composerTarget.ref {
		// a.choiceMenu != nil, not a.overlay == "choice": a Ctrl-C-while-
		// mutating confirm can be stacked on top of the choice menu
		// (a.overlay == "confirm") when this runs, and closeChoiceMenu's
		// own guard is field-based for exactly that reason — see its doc
		// comment.
		if a.choiceMenu != nil {
			a.closeChoiceMenu(false, -1)
		}
		a.closeComposer(true)
	}
}

// composerHeight computes the composer pane's fixed height: about 40% of
// the detail column's current inner height, never less than 6 lines.
func (a *App) composerHeight() int {
	_, _, _, h := a.detailColumn.GetInnerRect()
	height := h * 4 / 10
	if height < 6 {
		height = 6
	}
	return height
}

// closeComposer removes the composer pane, saving its draft (keepDraft)
// or deleting it (send, or ":q!"), and restores focus to whichever pane
// had it before the composer opened — or, for composerKindNewPRBody, to
// the create-PR form itself (see the restoreCreateFormBodySlot branch
// below).
func (a *App) closeComposer(keepDraft bool) {
	if a.composerFlex == nil {
		return
	}
	target := *a.composerTarget
	text := a.composerEditor.Text()

	a.composerCtrlWPending = false
	a.composerHost(target.kind).RemoveItem(a.composerFlex)
	a.composerFlex = nil
	a.composerTitle = nil
	a.composerEditor = nil
	a.composerTarget = nil

	if a.deps.Drafts != nil {
		switch {
		case keepDraft && a.composerDirty:
			// Only when the user actually changed something this
			// session (see composerDirty's own doc comment) — merely
			// opening a composer (which may itself seed the buffer from
			// a prefill or an existing draft) and closing it again with
			// ":q" must not, by itself, create a draft. An existing
			// draft the user left untouched simply is not re-saved here,
			// which is a no-op, not a loss: it is still on disk from
			// before.
			if err := a.deps.Drafts.Save(target.draftKey, text); err != nil {
				a.showErrorToast("draft save failed: " + err.Error())
			}
		case !keepDraft:
			if err := a.deps.Drafts.Delete(target.draftKey); err != nil {
				// A failed delete after a send (":q!"'s own explicit
				// discard makes this less urgent, but sendComposer also
				// calls closeComposer(false)) would otherwise let a
				// stale draft resurrect text that was already
				// successfully posted/edited — a toast, not just a
				// debug-log Warn.
				a.showErrorToast("draft delete failed: " + err.Error())
			}
		}
		a.refreshDraftGutterIfNeeded(target)
	}

	if target.kind == composerKindNewPRBody {
		// The create-PR form's own body slot hosted this composer's own
		// Flex in place of its usual read-only body view (openComposer's
		// own host-swap above) — swap back and give the form focus
		// directly, ignoring composerReturnFocus (openCreatePRBodyComposer
		// clears it for exactly this reason).
		a.composerReturnFocus = nil
		a.restoreCreateFormBodySlot()
	} else if a.overlay == "confirm" {
		// Closed underneath a confirm (closeComposerIfWrongPR on a PR
		// switch): moving focus now would take it off the still-visible
		// Modal. composerReturnFocus is left for showConfirm, which
		// refocuses it once the dialog closes.
	} else if a.composerReturnFocus != nil {
		a.app.SetFocus(a.composerReturnFocus)
		a.composerReturnFocus = nil
	} else {
		a.focusDetail()
	}
	a.renderStatusBar(a.spinnerFrame)
}

// onComposerChange write-throughs the composer's buffer to its draft on
// every edit; a save failure toasts once per composer session (further
// failures — for example a filesystem that stays read-only — would
// otherwise re-toast on every keystroke).
func (a *App) onComposerChange(text string) {
	// Set unconditionally, before the Drafts-disabled early return below:
	// "dirty" tracks whether the user made a real edit this session,
	// independent of whether persisting it is even possible.
	a.composerDirty = true
	if a.deps.Drafts == nil || a.composerTarget == nil {
		return
	}
	if err := a.deps.Drafts.Save(a.composerTarget.draftKey, text); err != nil {
		if !a.composerDraftSaveErrShown {
			a.composerDraftSaveErrShown = true
			a.showErrorToast("draft save failed: " + err.Error())
		}
		return
	}
	// refreshDraftGutterIfNeeded's own path (files.go) lists and decodes
	// every draft for the whole pull request, then rebuilds the diff's
	// entire row set — real work worth doing once a saved draft actually
	// starts or stops existing on this line, but not once per keystroke
	// while composing it (Drafts.Save's own empty-text-deletes rule means
	// "has a draft" is exactly text != ""): only call it when that
	// boolean actually flips.
	if nowMarked := text != ""; nowMarked != a.composerDraftGutterMarked {
		a.composerDraftGutterMarked = nowMarked
		a.refreshDraftGutterIfNeeded(*a.composerTarget)
	}
	a.renderStatusBar(a.spinnerFrame)
}

// onComposerAction reacts to the editor's own send/close/discard
// requests (":w"/Ctrl-s, ":q", ":q!"); ModeChanged needs no extra work
// here since Editor already renders its own mode line.
//
// For composerKindNewPRBody, ":q" (CloseRequested) also hands the buffer's
// current text back to App.createFormBody before closing — unlike every
// other composer kind, whose own draft is the only record of unsent text,
// this one is round-tripped into a sibling form field, and only Send
// (sendNewPRBodyComposer) did that until this was found missing in review:
// reopening ":q"'s own kept draft made it merely *look* saved, while
// Create would still submit whatever createFormBody held before. ":q!"
// (DiscardRequested) is unaffected — discarding still means discarding.
func (a *App) onComposerAction(act editor.Action) {
	switch act.Kind {
	case editor.SendRequested:
		a.sendComposer()
	case editor.CloseRequested:
		if a.composerTarget != nil && a.composerTarget.kind == composerKindNewPRBody {
			a.createFormBody = a.composerEditor.Text()
		}
		a.closeComposer(true)
	case editor.DiscardRequested:
		a.closeComposer(false)
	}
}

// sendComposer implements Send (":w"/Ctrl-s): refused with a toast while
// a mutation is already in flight, or if the buffer is blank; otherwise it
// calls the matching Store method and only closes the composer (deleting
// its draft) and starts tracking App.pendingSend if that call actually
// enqueued a mutation — Store.AddComment/EditComment return false without
// enqueueing anything when the pull request is not (yet) loaded enough to
// attach a comment to (only an EventError, which the store's own
// subscription already toasts, happens in that case). Closing
// unconditionally here would both lose the typed text (the draft is
// deleted on close) and leave pendingSend set with no mutation ever
// coming to resolve it. See onMutationChanged in storeevents.go for what
// happens once a truly enqueued mutation finishes.
func (a *App) sendComposer() {
	if a.composerTarget == nil {
		return
	}
	if a.composerTarget.kind == composerKindNewPRBody {
		// Never touches the store or Store.CurrentRef() at all — see
		// composerKindNewPRBody's own doc comment.
		a.sendNewPRBodyComposer(*a.composerTarget, a.composerEditor.Text())
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.composerTarget.ref {
		// Defensive: closeComposerIfWrongPR (called from EventPRChanged)
		// should already have closed the composer the moment the current
		// pull request diverged from its own target, so this is normally
		// unreachable — but a Send must never depend on that alone for
		// correctness, since posting to whichever pull request happens
		// to be open would be a real, if rare, correctness bug.
		a.showToast("cannot send: a different pull request is now open", theme.Warning)
		return
	}
	if a.deps.Store.Mutating() {
		a.showToast("a mutation is already in progress; try again shortly", theme.Warning)
		return
	}
	text := a.composerEditor.Text()
	target := *a.composerTarget
	// A review-body send has its own body/pending-comments validation
	// (sendReviewBodyComposer): APPROVE never requires a body, so the
	// generic "cannot send an empty comment" rule below must not apply to
	// this target kind at all. A PR-body edit is exempt for a different
	// reason: an empty description is a legitimate, intentional state (the
	// user clearing it out), not "nothing to send" — composerKindPRBody's
	// own sendComposer case below sends target's (possibly empty) text as
	// Body: &text either way, found missing in the second M5 review round.
	if target.kind != composerKindReviewBody && target.kind != composerKindPRBody && strings.TrimSpace(text) == "" {
		a.showToast("cannot send an empty comment", theme.Warning)
		return
	}

	switch target.kind {
	case composerKindEdit:
		a.finishSend(target, text, a.deps.Store.EditComment(target.commentID, text))
	case composerKindReviewEdit:
		a.finishSend(target, text, a.deps.Store.EditReviewComment(target.commentID, text))
	case composerKindLineComment, composerKindFileComment, composerKindReply:
		a.sendReviewComposer(target, text)
	case composerKindReviewBody:
		a.sendReviewBodyComposer(target, text)
	case composerKindPRBody:
		a.finishSend(target, text, a.deps.Store.UpdatePullRequestMeta(gh.UpdatePullRequestInput{Body: &text}))
	default:
		a.finishSend(target, text, a.deps.Store.AddComment(text))
	}
}

// sendNewPRBodyComposer implements Send for the create-PR form's own body
// composer (composerKindNewPRBody): unlike every other composer target, it
// never calls the store — it hands text back to the form (App.createFormBody)
// and closes the composer keeping its draft (deleted only once the pull
// request is actually created — see createform.go's onPullRequestCreated).
// An empty body is a legitimate, intentional value (the user clearing a
// prefilled template), so — unlike the generic case in sendComposer — this
// is never refused for being blank.
func (a *App) sendNewPRBodyComposer(target composerTarget, text string) {
	a.createFormBody = text
	a.closeComposer(true)
}

// finishSend closes the composer (deleting its draft) and starts tracking
// App.pendingSend only when enqueued is true — see sendComposer's own doc
// comment for why a false return (nothing was actually enqueued) must
// leave the composer open with its text intact instead.
func (a *App) finishSend(target composerTarget, text string, enqueued bool) {
	if !enqueued {
		return
	}
	a.pendingSend = &pendingSend{text: text, target: target}
	a.closeComposer(false)
}

// sendReviewComposer implements Send for a review comment/reply target
// (composerKindLineComment/composerKindFileComment/composerKindReply):
// mirroring GitHub's own two buttons, it offers a choice between "Add
// single comment" and "Add to review" whenever Store.CanSendSingle() is
// true (no pending review exists yet); cancelling the menu (Esc/q) leaves
// the composer open with text intact, exactly as a refused send does. Once
// a pending review already exists, every new comment must attach to it —
// GitHub's own UI hides the single-comment button in that state too — so
// no menu is shown at all.
func (a *App) sendReviewComposer(target composerTarget, text string) {
	if !a.deps.Store.CanSendSingle() {
		a.finishSend(target, text, a.performReviewSend(target, text, store.SendToReview))
		return
	}
	a.showChoiceMenu("Send comment", []string{"Add single comment", "Add to review"}, func(i int) {
		mode := store.SendSingle
		if i == 1 {
			mode = store.SendToReview
		}
		a.finishSend(target, text, a.performReviewSend(target, text, mode))
	})
}

// performReviewSend calls the Store method matching target.kind with mode.
// It re-checks target.ref against Store.CurrentRef() itself — the same
// guard sendComposer applies before ever showing the send-mode menu — since
// this runs from that menu's own onChoose callback, which can fire well
// after the menu was shown (the user is looking at a modal list; nothing
// stops EventPRChanged from landing in the meantime, for example a
// background refresh or the list's own preview debounce switching the
// current pull request): closeComposerIfWrongPR already cancels the menu
// the instant that happens, but a Send must never depend on that reaching
// the menu before the user's own Enter keypress does, for the same reason
// sendComposer's own equality check does not depend on
// closeComposerIfWrongPR alone.
func (a *App) performReviewSend(target composerTarget, text string, mode store.SendMode) bool {
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != target.ref {
		a.showToast("cannot send: a different pull request is now open", theme.Warning)
		return false
	}
	switch target.kind {
	case composerKindLineComment:
		return a.deps.Store.CommentOnLines(target.path, target.anchor, text, mode)
	case composerKindFileComment:
		return a.deps.Store.CommentOnFile(target.path, text, mode)
	case composerKindReply:
		return a.deps.Store.ReplyToThread(target.threadID, text, mode)
	default:
		return false
	}
}

// onMutationChanged reacts to store.EventMutationChanged for a send
// still tracked in pendingSend: on success it toasts confirmation; on
// failure it re-saves the draft (so the typed text is not lost) and
// toasts the reason. Every other mutation (an edit/delete not started
// from a composer, or one that finished while pendingSend was already
// nil) is ignored. It also recomputes the status bar's draft count,
// since a re-saved draft on failure changes it.
//
// Success/failure is read from Store.MutationError(), never
// Store.LastError(): LastError() also surfaces other, lower-priority
// standing errors (the current pull request's files, or a list section)
// whenever nothing higher-priority masks them, so a perfectly successful
// send happening while one of those already stood would otherwise be
// reported as "not sent" and needlessly re-save the (already-deleted)
// draft.
func (a *App) onMutationChanged() {
	a.renderStatusBar(a.spinnerFrame)
	if a.deps.Store.Mutating() || a.pendingSend == nil {
		return
	}
	ps := a.pendingSend
	a.pendingSend = nil

	if err := a.deps.Store.MutationError(); err != nil {
		switch {
		case a.composerTarget != nil && *a.composerTarget == ps.target:
			// The user has already reopened a composer for this exact
			// target (before this failure was even known) and may have
			// started typing something new into it: writing ps.text
			// straight to the draft file, underneath the live editor,
			// would have the *next* write-through save silently
			// overwrite it right back with the failed text, discarding
			// whatever the user has typed since. Merge into the live
			// buffer instead — through SetTextFromOutside, so `u`
			// returns to just the reopened composer's own content — and
			// let the normal write-through path persist the result.
			current := a.composerEditor.Text()
			merged := ps.text
			if strings.TrimSpace(current) != "" {
				merged = ps.text + "\n\n" + current
			}
			a.composerEditor.Vim().SetTextFromOutside(merged)
		case a.deps.Drafts != nil:
			if saveErr := a.deps.Drafts.Save(ps.target.draftKey, ps.text); saveErr != nil {
				a.deps.Logger.Error("draft re-save after failed send failed", "err", saveErr)
			}
		}
		// The re-save above (or the merge-into-live-editor branch, whose
		// own write-through already ran) may have just resurrected a
		// draft the gutter marker had already stopped showing (send
		// closed the composer and deleted it immediately — see
		// sendComposer's own doc comment); a no-op when target's path is
		// not the currently open file, or its kind is not a line/range/
		// file comment at all.
		a.refreshDraftGutterIfNeeded(ps.target)
		a.showToast("not sent; draft kept ("+err.Error()+")", theme.Error)
		a.renderStatusBar(a.spinnerFrame)
		return
	}

	// ps.notice (store.EventNotice, stashed by storeevents.go's own
	// handler) means the send did not do quite what the user asked —
	// today, only a SendSingle silently coerced to "add to review"
	// because a pending review already existed by run time — so it takes
	// priority over the generic success toast, which would otherwise
	// claim a single comment was posted when it was not.
	if ps.notice != "" {
		a.showToast(ps.notice, theme.Info)
		return
	}
	if ps.target.kind == composerKindReviewBody {
		// The list's own review-decision/state columns are refreshed by
		// Store itself (finishMutation's mutation.refreshList, since M5 —
		// see docs/DESIGN.md's Store section): SubmitReview's mutation
		// already calls Store.Refresh() on success, so calling it again
		// here would start a second, redundant list generation on top of
		// the store's own for no benefit.
		a.showToast("review submitted", theme.Success)
		return
	}
	verb := "posted"
	switch ps.target.kind {
	case composerKindEdit, composerKindReviewEdit, composerKindPRBody:
		verb = "updated"
	}
	a.showToast("comment "+verb, theme.Success)
}

// suspendForExternalEditor bridges editor.Options.Suspend to
// tview.Application.Suspend, syncing the screen afterward — v0.42.0's
// Suspend does not do this itself, which would otherwise leave the
// terminal showing stale/corrupted content after the external editor
// exits (see docs/DESIGN.md).
func (a *App) suspendForExternalEditor(f func()) bool {
	if !a.app.Suspend(f) {
		return false
	}
	a.app.Sync()
	return true
}

// onComposerError reports an editor.Options.OnError callback (":e"
// failing for any reason) as an error toast, in addition to the same
// message the editor's own status line already shows.
func (a *App) onComposerError(message string) {
	a.showErrorToast(message)
}

// mentionCandidates derives "@" completion candidates from the currently
// open pull request — its author, requested reviewers, and every
// comment/review author already in its timeline, followed by the
// repository's own mentionable users (Store.MentionableUsers, M4 data
// slice; nil until that repository's fetch resolves, which range handles
// like any other empty slice) — deduplicated by login (a repository user
// already listed as a PR participant is not repeated). A non-empty prefix
// fuzzy-matches (github.com/sahilm/fuzzy) against "login name" for each
// candidate, so a query can hit either half and a scattered subsequence
// (not just a strict prefix) still matches; fuzzy.FindFrom's own scoring
// already ranks an exact-prefix match above a scattered one and folds case
// itself (see candidateSource), so no separate lower-casing is needed
// here. An empty prefix returns every candidate as-is, in the
// participant-priority order add() built above.
func (a *App) mentionCandidates(prefix string) []editor.Candidate {
	if a.composerTarget != nil && a.composerTarget.kind == composerKindNewPRBody {
		// No "current pull request" participants exist yet at all — see
		// composerKindNewPRBody's own doc comment — so candidates come
		// solely from the chosen repository's own mentionable users.
		return a.newPRMentionCandidates(prefix)
	}
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return nil
	}

	// seen maps a login already added to its index in all, so a later add()
	// for the same login (for example the repository's own mentionable
	// users, which — unlike model.Reviewer — always carries a Name) can
	// backfill an empty Name left by an earlier one (a bare
	// pr.ReviewRequests entry has no Name field at all) instead of losing
	// it: without this, a requested reviewer who has not yet commented
	// would show with no display name in the mention popup even though the
	// repository's own data has one.
	seen := make(map[string]int)
	var all []editor.Candidate
	add := func(login, name string) {
		if login == "" {
			return
		}
		if idx, ok := seen[login]; ok {
			if all[idx].Name == "" && name != "" {
				all[idx].Name = name
			}
			return
		}
		seen[login] = len(all)
		all = append(all, editor.Candidate{Login: login, Name: name})
	}

	add(pr.Author.Login, pr.Author.Name)
	for _, r := range pr.ReviewRequests {
		if r.Kind == model.ReviewerKindUser {
			add(r.Login, "")
		}
	}
	for _, rv := range pr.LatestReviews {
		add(rv.Author.Login, rv.Author.Name)
	}
	for _, item := range pr.Timeline {
		switch item.Kind {
		case model.TimelineKindIssueComment:
			if item.IssueComment != nil {
				add(item.IssueComment.Author.Login, item.IssueComment.Author.Name)
			}
		case model.TimelineKindReview:
			if item.Review != nil {
				add(item.Review.Author.Login, item.Review.Author.Name)
			}
		}
	}
	for _, u := range a.deps.Store.MentionableUsers() {
		add(u.Login, u.Name)
	}

	return fuzzyFilterCandidates(prefix, all)
}

// newPRMentionCandidates is mentionCandidates' counterpart for the
// create-PR form's own body composer (composerKindNewPRBody): candidates
// are solely the chosen repository's own mentionable users (Store.
// MentionableUsersOf, App.createFormRepo), since there is no pull
// request's own participants to draw from yet.
func (a *App) newPRMentionCandidates(prefix string) []editor.Candidate {
	users := a.deps.Store.MentionableUsersOf(a.createFormRepo)
	all := make([]editor.Candidate, len(users))
	for i, u := range users {
		all[i] = editor.Candidate{Login: u.Login, Name: u.Name}
	}
	return fuzzyFilterCandidates(prefix, all)
}

// fuzzyFilterCandidates is mentionCandidates'/newPRMentionCandidates'
// shared tail: an empty prefix returns every candidate as-is, in the
// caller's own priority order; otherwise it fuzzy-matches
// (github.com/sahilm/fuzzy) against "login name" for each candidate (see
// candidateSource), so a query can hit either half and a scattered
// subsequence — not just a strict prefix — still matches, ranked by the
// library's own scoring (which also folds case itself, so no separate
// lower-casing is needed here).
func fuzzyFilterCandidates(prefix string, all []editor.Candidate) []editor.Candidate {
	if prefix == "" {
		return all
	}
	matches := fuzzy.FindFrom(prefix, candidateSource(all))
	filtered := make([]editor.Candidate, len(matches))
	for i, m := range matches {
		filtered[i] = all[m.Index]
	}
	return filtered
}

// candidateSource adapts a []editor.Candidate to fuzzy.Source for
// mentionCandidates: each candidate's match string is its login alone, or
// "login name" when it has one, so a query can fuzzy-match either half —
// for example a display name with no hint of it in the login itself.
type candidateSource []editor.Candidate

func (c candidateSource) String(i int) string {
	if c[i].Name == "" {
		return c[i].Login
	}
	return c[i].Login + " " + c[i].Name
}

func (c candidateSource) Len() int { return len(c) }

// draftCount returns how many drafts exist for the currently open pull
// request, or 0 when none is open or drafts are disabled — shown by the
// status bar as "✎ N".
func (a *App) draftCount() int {
	if a.deps.Drafts == nil {
		return 0
	}
	ref, ok := a.deps.Store.CurrentRef()
	if !ok {
		return 0
	}
	n, err := a.deps.Drafts.Count(ref.Key())
	if err != nil {
		a.deps.Logger.Warn("draft count failed", "err", err)
	}
	return n
}
