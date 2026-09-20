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

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/editor"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// composerKind identifies what a composerTarget's text becomes once sent.
type composerKind int

// Known composer kinds.
const (
	// composerKindComment sends a new general (issue) comment.
	composerKindComment composerKind = iota
	// composerKindEdit updates an existing comment's body.
	composerKindEdit
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
	// commentID is the comment being edited, for composerKindEdit only.
	commentID string
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

// editCurrentComment opens an edit composer for the issue comment block
// under the PR tab's cursor (comment.edit, "e"), or toasts why it cannot:
// no comment is under the cursor, or the viewer does not own it.
func (a *App) editCurrentComment() {
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
func (a *App) deleteCurrentComment() {
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

	a.composerFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	a.composerFlex.AddItem(a.composerTitle, 1, 0, false)
	a.composerFlex.AddItem(a.composerEditor, 0, 1, true)
	a.detailColumn.AddItem(a.composerFlex, a.composerHeight(), 0, true)

	a.app.SetFocus(a.composerEditor)
}

// closeComposerIfWrongPR closes the open composer, saving its draft
// (under its own original target key — the composer belonging to the
// pull request it was opened for, not whichever one is current by the
// time this runs), the moment the currently open pull request no longer
// matches it. Called from EventPRChanged, so a composer for pull request
// A can never still be open, let alone sent, once pull request B becomes
// current — see composerTarget.ref's own doc comment.
func (a *App) closeComposerIfWrongPR() {
	if a.composerTarget == nil {
		return
	}
	if ref, ok := a.deps.Store.CurrentRef(); !ok || ref != a.composerTarget.ref {
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
// had it before the composer opened.
func (a *App) closeComposer(keepDraft bool) {
	if a.composerFlex == nil {
		return
	}
	target := *a.composerTarget
	text := a.composerEditor.Text()

	a.composerCtrlWPending = false
	a.detailColumn.RemoveItem(a.composerFlex)
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
	}

	if a.composerReturnFocus != nil {
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
	a.renderStatusBar(a.spinnerFrame)
}

// onComposerAction reacts to the editor's own send/close/discard
// requests (":w"/Ctrl-s, ":q", ":q!"); ModeChanged needs no extra work
// here since Editor already renders its own mode line.
func (a *App) onComposerAction(act editor.Action) {
	switch act.Kind {
	case editor.SendRequested:
		a.sendComposer()
	case editor.CloseRequested:
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
	if strings.TrimSpace(text) == "" {
		a.showToast("cannot send an empty comment", theme.Warning)
		return
	}

	target := *a.composerTarget
	var enqueued bool
	switch target.kind {
	case composerKindEdit:
		enqueued = a.deps.Store.EditComment(target.commentID, text)
	default:
		enqueued = a.deps.Store.AddComment(text)
	}
	if !enqueued {
		return
	}

	a.pendingSend = &pendingSend{text: text, target: target}
	a.closeComposer(false)
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
		a.showToast("not sent; draft kept ("+err.Error()+")", theme.Error)
		a.renderStatusBar(a.spinnerFrame)
		return
	}

	verb := "posted"
	if ps.target.kind == composerKindEdit {
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
// comment/review author already in its timeline — deduplicated by login
// and filtered by a case-insensitive prefix match. M4 replaces this with
// the repository's real mentionableUsers query.
func (a *App) mentionCandidates(prefix string) []editor.Candidate {
	pr := a.deps.Store.CurrentPR()
	if pr == nil {
		return nil
	}

	seen := make(map[string]bool)
	var all []editor.Candidate
	add := func(login, name string) {
		if login == "" || seen[login] {
			return
		}
		seen[login] = true
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

	if prefix == "" {
		return all
	}
	lower := strings.ToLower(prefix)
	filtered := make([]editor.Candidate, 0, len(all))
	for _, c := range all {
		if strings.HasPrefix(strings.ToLower(c.Login), lower) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

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
