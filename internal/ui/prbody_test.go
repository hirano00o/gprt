// prbody_test.go covers PR body editing: "e" on the PR tab's description
// block opens a composer (composerKindPRBody) prefilled with the current
// body, sending Store.UpdatePullRequestMeta with only Body set; "d" on the
// description block is a no-op toast, never a delete.
package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/drafts"
)

func TestEditPRBodyOpensComposerPrefilledWithBody(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.Body = "the current body"
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'e')

	waitFor(t, app.app, func() bool { return composerOpen(app) })
	if got, want := query(app.app, func() string { return app.composerTitle.GetText(false) }), "Edit PR body"; got != want {
		t.Fatalf("composer title = %q, want %q", got, want)
	}
	if got, want := query(app.app, func() string { return composerText(app) }), "the current body"; got != want {
		t.Fatalf("composer text = %q, want the pull request's own body %q", got, want)
	}
	wantKey := drafts.Key{PR: ref.Key(), Kind: drafts.KindPRBody, Anchor: "body"}
	if got := query(app.app, func() drafts.Key { return app.composerTarget.draftKey }); got != wantKey {
		t.Fatalf("composer draft key = %+v, want %+v", got, wantKey)
	}
}

func TestEditPRBodyRefusedWhenViewerCannotUpdate(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.ViewerCanUpdate = false
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'e')

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot edit") })
	if composerOpen(app) {
		t.Fatal("ViewerCanUpdate false: the PR body composer must not open")
	}
}

func TestEditPRBodySendCallsUpdatePullRequestMetaWithBodyOnly(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.Body = "the current body"
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "the current body" })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	call := fake.UpdatePullRequestCalls()[0]
	if call.in.Body == nil || *call.in.Body != "the current body" {
		t.Fatalf("UpdatePullRequest Body = %v, want %q", call.in.Body, "the current body")
	}
	if call.in.Title != nil || call.in.BaseRefName != nil || call.in.LabelIDs != nil {
		t.Fatalf("UpdatePullRequest = %+v, want only Body set", call.in)
	}
	waitFor(t, app.app, func() bool { return app.statusBar.toast == "comment updated" })
}

// TestEditPRBodyCanBeEmptied is a regression test for the second M5 review
// round: sendComposer's own generic "cannot send an empty comment" guard
// originally applied to composerKindPRBody too, making it impossible to
// clear a pull request's description — an empty body is a legitimate,
// intentional state, unlike an empty general comment.
func TestEditPRBodyCanBeEmptied(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	pr := editablePR(ref)
	pr.Body = "the current body"
	openDetailForComposer(t, app, fake, pr)
	focusPRBlock(app, "description")

	sendRune(app.app, 'e')
	waitFor(t, app.app, func() bool { return composerOpen(app) })
	waitFor(t, app.app, func() bool { return composerText(app) == "the current body" })
	act(app.app, func() { app.composerEditor.SetText("") })

	sendRune(app.app, ':')
	sendRune(app.app, 'w')
	sendSpecial(app.app, tcell.KeyEnter)

	waitFor(t, app.app, func() bool { return len(fake.UpdatePullRequestCalls()) == 1 })
	call := fake.UpdatePullRequestCalls()[0]
	if call.in.Body == nil || *call.in.Body != "" {
		t.Fatalf("UpdatePullRequest Body = %v, want a non-nil, empty string", call.in.Body)
	}
}

func TestDeletePRBodyIsANoOpToast(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, editablePR(ref))
	focusPRBlock(app, "description")

	sendRune(app.app, 'd')

	waitFor(t, app.app, func() bool { return containsSubstring(app.statusBar.toast, "cannot delete") })
	if app.overlay == "confirm" {
		t.Fatal("d on the PR body must never open a delete confirmation")
	}
	if len(fake.UpdatePullRequestCalls()) != 0 {
		t.Fatal("d on the PR body must never call the store")
	}
}
