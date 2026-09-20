// dialogs_test.go covers showConfirm's own shared behavior — a single
// regression test here protects every caller (delete-comment, discard-
// changes, :merge/:close/:reopen, Ctrl-C-while-mutating) at once, rather
// than duplicating the same assertion in each of their own test files.
package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestShowConfirmDefaultsFocusToCancelNotConfirmLabel is a regression test
// for a real bug the first M5 review round found: tview's own Form (which
// Modal wraps its two buttons in) defaults focusIndex to 0 — the *first*
// AddButtons argument, confirmLabel, the destructive/executing choice —
// so a bare Enter right after a confirm dialog opens, with no navigation
// at all, must never run onConfirm. Exercised here via the delete-comment
// confirm (any showConfirm caller would do), since showConfirm itself
// carries the fix (modal.SetFocus(1)) all of them share.
func TestShowConfirmDefaultsFocusToCancelNotConfirmLabel(t *testing.T) {
	app, _, fake, _ := newTestApp(t, nil)
	ref := fixtureRef(1)
	openDetailForComposer(t, app, fake, ownCommentPR(ref))
	focusCommentBlock(app)

	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })

	// A bare Enter, with no navigation, lands on "Cancel" (the default
	// focus): the dialog closes, exactly like actually choosing Cancel
	// would, but onConfirm must never run.
	sendSpecial(app.app, tcell.KeyEnter)
	waitFor(t, app.app, func() bool { return app.overlay == "" })
	if len(fake.DeleteCommentIDs()) != 0 {
		t.Fatal("a bare Enter landed on the default \"Cancel\" focus and must not have run onConfirm (DeleteComment)")
	}

	// Re-open it and confirm the counter-case: Tab then Enter reaches the
	// confirm button and does run onConfirm.
	sendRune(app.app, 'd')
	waitFor(t, app.app, func() bool { return app.overlay == "confirm" })
	confirmYes(app.app)
	waitFor(t, app.app, func() bool { return len(fake.DeleteCommentIDs()) == 1 })
}
