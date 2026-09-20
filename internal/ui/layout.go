package ui

import (
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/ui/widget"
)

// build constructs the whole widget tree and wires the callbacks that do
// not depend on Run having started (nothing here touches the network).
func (a *App) build() {
	a.buildListColumn()
	a.buildDetailColumn()
	a.buildStatusBar()

	a.row = tview.NewFlex().SetDirection(tview.FlexColumn)
	a.row.AddItem(a.listColumn, 0, 1, true)
	a.row.AddItem(a.detailColumn, 0, 2, false)
	a.listExpanded = true

	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	mainFlex.AddItem(a.row, 0, 1, true)
	mainFlex.AddItem(a.bottomPages, 1, 0, false)

	a.root = tview.NewPages()
	a.root.AddPage("main", mainFlex, true, true)

	a.app.SetInputCapture(a.handleKey)
	a.app.SetFocus(a.listView)

	a.renderPRTab()
	a.refreshList()
	a.renderStatusBar(0)
}

func (a *App) buildListColumn() {
	a.listView = widget.NewListView()
	a.listView.SetBorder(true).SetTitle(" Pull requests ")
	a.listView.SetChangedFunc(a.onRowChanged)
	a.listView.SetSelectedFunc(a.onRowSelected)

	a.filterInput = tview.NewInputField().SetLabel("/")
	a.filterInput.SetChangedFunc(func(text string) {
		a.deps.Store.SetFilter(text)
		a.refreshList()
	})

	a.listFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	a.listFlex.AddItem(a.listView, 0, 1, true)
	a.listFlex.AddItem(a.filterInput, 0, 0, false) // hidden until "/" opens it
	a.listColumn = a.listFlex
}

func (a *App) buildDetailColumn() {
	a.tabBar = newTabBarView([]string{"PR", "Files"})
	a.tabBar.SetActive(0)

	a.prView = widget.NewDetailView()
	a.filesView = tview.NewTextView().SetWrap(true)
	a.filesView.SetText("Files view arrives in M2.")

	a.detailPages = tview.NewPages()
	a.detailPages.AddPage("pr", a.prView, true, true)
	a.detailPages.AddPage("files", a.filesView, true, false)
	a.currentTab = "pr"

	a.detailColumn = tview.NewFlex().SetDirection(tview.FlexRow)
	a.detailColumn.AddItem(a.tabBar, 1, 0, false)
	a.detailColumn.AddItem(a.detailPages, 0, 1, true)
}

func (a *App) buildStatusBar() {
	a.statusBar = newStatusBarView()

	a.cmdLine = tview.NewInputField().SetLabel(":")

	a.bottomPages = tview.NewPages()
	a.bottomPages.AddPage("status", a.statusBar, true, true)
	a.bottomPages.AddPage("command", a.cmdLine, true, false)
}
