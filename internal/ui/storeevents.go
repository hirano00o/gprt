package ui

import (
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// subscribeStore wires every internal/store event this milestone reacts to.
// Subscribe's callback runs synchronously on the UI goroutine (see
// store.Store's package doc), so every handler here may call Store methods
// and touch widgets directly.
func (a *App) subscribeStore() {
	a.deps.Store.Subscribe(func(ev store.Event) {
		switch ev.Kind {
		case store.EventListChanged:
			a.refreshList()
		case store.EventLoadingChanged:
			a.onLoadingChanged()
			// A section's loading/stale flags flip synchronously when a
			// fetch starts (before its result arrives and fires
			// EventListChanged): refresh now too, so "R" shows the
			// stale dimming and the loading marker immediately instead
			// of only once the new data lands.
			a.refreshList()
		case store.EventViewerLoaded, store.EventRateLimitChanged:
			a.renderStatusBar(0)
		case store.EventPRChanged:
			// Before anything else: a composer belongs to the pull
			// request it was opened for (composerTarget.ref), never to
			// "whichever one happens to be open" — see
			// closeComposerIfWrongPR's own doc comment.
			a.closeComposerIfWrongPR()
			a.renderPRTab()
			a.onPRChangedForFiles()
			// The status bar's "✎ N" draft count is scoped to whichever
			// pull request is currently open.
			a.renderStatusBar(a.spinnerFrame)
			if a.overlay == "pending" {
				a.rebuildPendingList()
			}
			if a.overlay == "reaction" {
				a.rebuildReactionPicker()
			}
		case store.EventPRLoadingChanged:
			a.renderStatusBar(0)
			// A failed fetch changes DetailState().Err/Loading without
			// touching CurrentRef/CurrentPR, so it never fires
			// EventPRChanged: without this, the PR tab would stay on
			// "Loading pull request..." forever instead of showing the
			// error (see detail.go's emptyPRBlock).
			a.renderPRTab()
		case store.EventFilesChanged:
			a.rebuildFileTree()
			a.refreshCurrentFile()
			a.checkFilesWarnings()
		case store.EventFileHighlighted:
			// FilesState().Highlighting (the status bar's "highlighting N"
			// segment) decrements on every hunk highlight job's
			// completion, for any file, not just the one currently open —
			// re-render unconditionally, or it can freeze at whatever
			// count happened to be current the last time some other event
			// triggered a render.
			a.renderStatusBar(a.spinnerFrame)
			if ev.Path == a.currentFilePath {
				a.refreshCurrentFile()
			}
		case store.EventFilesLoadingChanged:
			a.renderStatusBar(0)
		case store.EventMutationChanged:
			a.onMutationChanged()
			if a.overlay == "pending" {
				a.rebuildPendingList()
			}
		case store.EventNotice:
			// A notice for a send still tracked in pendingSend (a
			// SendSingle silently coerced to "add to review") arrives
			// synchronously, from the same mutation apply, strictly
			// before the EventMutationChanged that follows it — see
			// review.go's own run closures, which emit EventNotice then
			// EventPRChanged before returning to finishMutation, which
			// emits EventMutationChanged last. Stash it there instead of
			// toasting immediately, so onMutationChanged's own success
			// toast can show the coercion notice instead of a misleading
			// "comment posted" (composer.go's own doc comment on this).
			// Any other notice (there is no other source of one today,
			// but a future one need not go through the composer) toasts
			// directly.
			if a.pendingSend != nil {
				a.pendingSend.notice = ev.Message
			} else {
				a.showToast(ev.Message, theme.Info)
			}
		case store.EventError:
			if ev.Err != nil {
				a.showToast(ev.Err.Error(), theme.Error)
			}
			// LastError (shown as a persistent marker, not just this
			// toast) and, for a section-scoped error, that section's
			// header row both need to reflect the new error state.
			a.renderStatusBar(0)
			a.refreshList()
		}
	})
}
