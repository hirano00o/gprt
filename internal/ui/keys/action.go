package keys

// Action identifies a user-triggerable command. Action IDs are stable
// strings (not iota constants) because they are also the keys a user's
// config.yaml "keys:" map rebinds by; see docs/KEYBINDINGS.md, whose
// "Action ID" column this list reproduces exactly.
type Action string

// Every action ID gprt defines, grouped as in docs/KEYBINDINGS.md. Not
// every action is dispatched yet — the router wires up the subset each
// milestone implements — but the full set is declared here up front so a
// config's "keys:" map, and the "?" help screen, have one place to check an
// action ID against.
const (
	ActionListDown             Action = "list.down"
	ActionListUp               Action = "list.up"
	ActionListTop              Action = "list.top"
	ActionListBottom           Action = "list.bottom"
	ActionListHalfDown         Action = "list.half_down"
	ActionListHalfUp           Action = "list.half_up"
	ActionListPageDown         Action = "list.page_down"
	ActionListPageUp           Action = "list.page_up"
	ActionListFilter           Action = "list.filter"
	ActionListOpen             Action = "list.open"
	ActionListNewPR            Action = "list.new_pr"
	ActionGlobalFocusLeft      Action = "global.focus_left"
	ActionGlobalFocusRight     Action = "global.focus_right"
	ActionGlobalFocusDown      Action = "global.focus_down"
	ActionGlobalFocusUp        Action = "global.focus_up"
	ActionGlobalToggleList     Action = "global.toggle_list"
	ActionGlobalReload         Action = "global.reload"
	ActionGlobalOpenBrowser    Action = "global.open_browser"
	ActionGlobalHelp           Action = "global.help"
	ActionGlobalQuit           Action = "global.quit"
	ActionGlobalCommand        Action = "global.command"
	ActionDetailTabNext        Action = "detail.tab_next"
	ActionDetailTabPrev        Action = "detail.tab_prev"
	ActionFilesToggleTree      Action = "files.toggle_tree"
	ActionDiffVisual           Action = "diff.visual"
	ActionDiffComment          Action = "diff.comment"
	ActionDiffCommentFile      Action = "diff.comment_file"
	ActionDiffFold             Action = "diff.fold"
	ActionDiffUnfoldAll        Action = "diff.unfold_all"
	ActionDiffFoldAll          Action = "diff.fold_all"
	ActionDiffScrollLeft       Action = "diff.scroll_left"
	ActionDiffScrollRight      Action = "diff.scroll_right"
	ActionDiffNextThread       Action = "diff.next_thread"
	ActionDiffPrevThread       Action = "diff.prev_thread"
	ActionDiffNextFile         Action = "diff.next_file"
	ActionDiffPrevFile         Action = "diff.prev_file"
	ActionDiffSearch           Action = "diff.search"
	ActionDiffSearchNext       Action = "diff.search_next"
	ActionDiffSearchPrev       Action = "diff.search_prev"
	ActionDiffExpand           Action = "diff.expand"
	ActionThreadReply          Action = "thread.reply"
	ActionThreadToggleResolved Action = "thread.toggle_resolved"
	ActionCommentEdit          Action = "comment.edit"
	ActionCommentDelete        Action = "comment.delete"
	ActionCommentReact         Action = "comment.react"
	ActionPRPending            Action = "pr.pending"
	ActionPRThreads            Action = "pr.threads"
	ActionPRSubmit             Action = "pr.submit"
	ActionPREdit               Action = "pr.edit"
)

// AllActions returns every action ID gprt defines, in no particular order.
// Merge uses it to reject a config's "keys:" entry for an unknown action.
func AllActions() []Action {
	return []Action{
		ActionListDown, ActionListUp, ActionListTop, ActionListBottom,
		ActionListHalfDown, ActionListHalfUp, ActionListPageDown, ActionListPageUp,
		ActionListFilter, ActionListOpen, ActionListNewPR,
		ActionGlobalFocusLeft, ActionGlobalFocusRight, ActionGlobalFocusDown, ActionGlobalFocusUp,
		ActionGlobalToggleList, ActionGlobalReload, ActionGlobalOpenBrowser,
		ActionGlobalHelp, ActionGlobalQuit, ActionGlobalCommand,
		ActionDetailTabNext, ActionDetailTabPrev,
		ActionFilesToggleTree,
		ActionDiffVisual, ActionDiffComment, ActionDiffCommentFile,
		ActionDiffFold, ActionDiffUnfoldAll, ActionDiffFoldAll,
		ActionDiffScrollLeft, ActionDiffScrollRight,
		ActionDiffNextThread, ActionDiffPrevThread, ActionDiffNextFile, ActionDiffPrevFile,
		ActionDiffSearch, ActionDiffSearchNext, ActionDiffSearchPrev, ActionDiffExpand,
		ActionThreadReply, ActionThreadToggleResolved,
		ActionCommentEdit, ActionCommentDelete, ActionCommentReact,
		ActionPRPending, ActionPRThreads, ActionPRSubmit, ActionPREdit,
	}
}

// Context identifies which pane or mode a keypress is interpreted in. A
// Keymap binds sequences per context; Sequencer.Feed and Keymap.Lookup fall
// back from a specific context to ContextGlobal when the specific context
// has no binding at all for the pressed sequence.
type Context string

// Every context gprt's router distinguishes.
const (
	ContextGlobal   Context = "global"
	ContextList     Context = "list"
	ContextDetail   Context = "detail"
	ContextFiles    Context = "files"
	ContextDiff     Context = "diff"
	ContextThread   Context = "thread"
	ContextComment  Context = "comment"
	ContextPR       Context = "pr"
	ContextComposer Context = "composer"
)
