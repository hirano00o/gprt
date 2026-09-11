package ui

import (
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// refreshList rebuilds the ListView's rows from the Store's current state.
// Called after every Store event that could change what Rows returns.
func (a *App) refreshList() {
	a.checkSectionWarnings()
	a.listView.SetRows(a.buildRows())
}

// checkSectionWarnings toasts the first line of a section's warnings the
// first time they appear (see warnedSections's doc comment for why "first
// time" instead of every refresh). The section's header row shows a
// "⚠ N" marker for as long as the warnings stand (see headerListRow); this
// only handles the one-time toast on top of that.
func (a *App) checkSectionWarnings() {
	for _, s := range a.deps.Store.SectionStates() {
		if len(s.Warnings) == 0 {
			delete(a.warnedSections, s.Section.Name)
			continue
		}
		if a.warnedSections[s.Section.Name] {
			continue
		}
		if a.warnedSections == nil {
			a.warnedSections = map[string]bool{}
		}
		a.warnedSections[s.Section.Name] = true
		a.showToast(s.Section.Name+": "+s.Warnings[0], theme.Warning)
	}
}

// previewDebounce is how long cursor movement waits before previewing a
// row into the detail column, so scrolling quickly through the list does
// not re-render on every intermediate row.
const previewDebounce = 300 * time.Millisecond

// onRowChanged is ListView's changed callback: it fires on every cursor
// movement (and once per SetRows, whenever a selectable row exists).
func (a *App) onRowChanged(row widget.ListRow) {
	a.triggerLoadMoreIfNeeded(row.ID)

	if a.previewTimer != nil {
		a.previewTimer.Stop()
	}
	id := row.ID
	a.previewTimer = time.AfterFunc(previewDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.showPreview(id)
		})
	})
}

// onRowSelected is ListView's selected callback, invoked by Select() in
// response to the list.open action (Enter / l): it previews immediately,
// with no debounce, and moves focus to the detail column.
func (a *App) onRowSelected(row widget.ListRow) {
	if a.previewTimer != nil {
		a.previewTimer.Stop()
		a.previewTimer = nil
	}
	a.showPreview(row.ID)
	a.focusDetail()
}

// showPreview sets the PR tab's content to the row identified by id, if it
// is still the row under the cursor and present in the current rowIndex.
// Both checks matter: the list may have changed between the debounce
// firing and now, and time.Timer.Stop cannot recall a callback that has
// already queued its QueueUpdateDraw, so a stale debounce for an earlier
// row can run after Enter previewed the current one and must not win.
func (a *App) showPreview(id string) {
	if id != a.listView.CurrentID() {
		return
	}
	state, ok := a.rowIndex[id]
	if !ok {
		return
	}
	a.currentPR = state
	a.renderPRTab()
}

// triggerLoadMoreIfNeeded starts fetching the next page of a section once
// the cursor reaches its last loaded row, if the section has one. Store's
// own in-flight guard makes this safe to call repeatedly for the same row.
func (a *App) triggerLoadMoreIfNeeded(id string) {
	state, ok := a.rowIndex[id]
	if !ok || !state.lastInSection {
		return
	}
	states := a.deps.Store.SectionStates()
	if state.sectionIndex < 0 || state.sectionIndex >= len(states) {
		return
	}
	if states[state.sectionIndex].HasNext {
		a.deps.Store.LoadMore(state.sectionIndex)
	}
}

// renderPRTab renders the "PR" tab's placeholder content: the currently
// previewed pull request's title, repository, author, state, and URL. The
// full description/checks/conversation view arrives in M1b.
func (a *App) renderPRTab() {
	if a.currentPR == nil {
		a.prView.SetText("Select a pull request to preview it here.")
		return
	}
	pr := a.currentPR.item.PR
	a.prView.SetText(fmt.Sprintf(
		"#%d %s\n\nRepository: %s\nAuthor:     %s\nState:      %s\nURL:        %s\n\nThe full detail view (description, checks, conversation) arrives in M1b.",
		pr.Ref.Number, pr.Title, pr.Ref.Repo.NameWithOwner(), pr.Author.Login, string(pr.State), pr.URL,
	))
}

// focusList moves focus to the PR list, re-expanding it first if it was
// collapsed (global.focus_left, "Ctrl-w h") — never focusing a zero-width
// pane.
func (a *App) focusList() {
	a.expandListColumn()
	a.app.SetFocus(a.listView)
}

// focusDetail moves focus to the detail column's currently visible tab.
func (a *App) focusDetail() {
	a.app.SetFocus(a.detailPages)
}

// isDetailFocused reports whether either detail tab currently has focus.
func (a *App) isDetailFocused() bool {
	switch a.app.GetFocus() {
	case a.prView, a.filesView:
		return true
	default:
		return false
	}
}

// toggleListColumn shows or hides the PR list column (global.toggle_list,
// "Ctrl-w o").
func (a *App) toggleListColumn() {
	if a.listExpanded {
		a.collapseListColumn()
	} else {
		a.expandListColumn()
	}
}

// collapseListColumn hides the PR list column. If the list has focus, it
// moves focus to the detail column first, since a hidden (zero-width)
// column cannot hold focus.
func (a *App) collapseListColumn() {
	if !a.listExpanded {
		return
	}
	if a.app.GetFocus() == a.listView {
		a.focusDetail()
	}
	a.row.ResizeItem(a.listColumn, 0, 0)
	a.listExpanded = false
}

// expandListColumn shows the PR list column if it is currently collapsed.
func (a *App) expandListColumn() {
	if a.listExpanded {
		return
	}
	a.row.ResizeItem(a.listColumn, 0, 1)
	a.listExpanded = true
}

// switchTab switches the detail column to the page named name (its index
// among the tab bar's labels), keeping focus on the detail column if it
// already had it.
func (a *App) switchTab(name string, index int) {
	a.currentTab = name
	a.detailPages.SwitchToPage(name)
	a.tabBar.SetActive(index)
	if a.isDetailFocused() {
		a.focusDetail()
	}
}

// nextTab and prevTab both toggle between gprt's two detail tabs: with
// exactly two tabs, "next" and "previous" are the same operation.
func (a *App) nextTab() {
	if a.currentTab == "pr" {
		a.switchTab("files", 1)
	} else {
		a.switchTab("pr", 0)
	}
}

func (a *App) prevTab() {
	a.nextTab()
}

// openFilter shows the filter input below the list and focuses it.
func (a *App) openFilter() {
	a.savedFocus = a.app.GetFocus()
	a.listFlex.ResizeItem(a.filterInput, 1, 0)
	a.app.SetFocus(a.filterInput)
}

// closeFilter hides the filter input and restores focus to whatever had it
// before openFilter. keep is false for Esc (clear the filter text, which
// also clears the Store's filter via the input's changed callback) and
// true for Enter (leave the filter as typed).
func (a *App) closeFilter(keep bool) {
	if !keep {
		a.filterInput.SetText("")
	}
	a.listFlex.ResizeItem(a.filterInput, 0, 0)
	a.restoreFocus()
}

// openCommandLine shows the ":" command input in the status bar row.
func (a *App) openCommandLine() {
	a.savedFocus = a.app.GetFocus()
	a.cmdLine.SetText("")
	a.cmdHistIdx = len(a.cmdHistory)
	a.bottomPages.SwitchToPage("command")
	a.app.SetFocus(a.cmdLine)
}

// closeCommandLine hides the command input and restores focus.
func (a *App) closeCommandLine() {
	a.bottomPages.SwitchToPage("status")
	a.restoreFocus()
}

// restoreFocus returns focus to whatever primitive last had it before a
// transient overlay (filter, command line, help, messages) took it, or to
// the list if none was recorded.
func (a *App) restoreFocus() {
	if a.savedFocus != nil {
		a.app.SetFocus(a.savedFocus)
		a.savedFocus = nil
		return
	}
	a.focusList()
}

// quit stops the application; Run returns as soon as tview's event loop
// notices.
func (a *App) quit() {
	a.app.Stop()
}

// reload discards cached data and re-fetches the list from the network
// (global.reload, "R"). The refreshed rows arrive via the EventListChanged
// subscription, not synchronously.
func (a *App) reload() {
	a.deps.Store.Reload()
}

// openCurrentInBrowser opens the current pull request's URL: the list
// cursor's row when the list has focus (so browsing does not wait for the
// preview debounce), otherwise whatever is currently shown in the detail
// column.
func (a *App) openCurrentInBrowser() {
	var url string
	switch {
	case a.app.GetFocus() == a.listView:
		if state, ok := a.rowIndex[a.listView.CurrentID()]; ok {
			url = state.item.PR.URL
		}
	case a.currentPR != nil:
		url = a.currentPR.item.PR.URL
	}
	if url == "" {
		return
	}
	if err := a.deps.Browser.Open(url); err != nil {
		a.showErrorToast("open browser: " + err.Error())
	}
}
