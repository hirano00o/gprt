package keys

// descriptions holds the one-line help text for each action, shown next to
// its ID in the "?" overlay. An action can be bound in several contexts
// (list.open opens a PR from the list and a file from the tree; thread.reply
// is reachable from a thread and from "c" on one), so each text stays
// context-free instead of naming a pane. Keep each entry within 40
// characters so a help row still fits an 80-column terminal;
// TestDescriptionCoversEveryAction enforces both that and completeness.
var descriptions = map[Action]string{
	ActionListDown:             "Move down",
	ActionListUp:               "Move up",
	ActionListTop:              "Move to the top",
	ActionListBottom:           "Move to the bottom",
	ActionListHalfDown:         "Move down half a page",
	ActionListHalfUp:           "Move up half a page",
	ActionListPageDown:         "Move down a page",
	ActionListPageUp:           "Move up a page",
	ActionListFilter:           "Filter the PR list",
	ActionListOpen:             "Open the selected item",
	ActionListNewPR:            "Create a pull request",
	ActionGlobalFocusLeft:      "Focus the previous pane",
	ActionGlobalFocusRight:     "Focus the next pane",
	ActionGlobalFocusDown:      "Switch focus to/from the composer",
	ActionGlobalFocusUp:        "Switch focus to/from the composer",
	ActionGlobalToggleList:     "Show or hide the PR list",
	ActionGlobalReload:         "Reload, ignoring the cache",
	ActionGlobalOpenBrowser:    "Open in the browser",
	ActionGlobalHelp:           "Show this help",
	ActionGlobalQuit:           "Quit",
	ActionGlobalCommand:        "Open the command line",
	ActionDetailTabNext:        "Switch to the next tab",
	ActionDetailTabPrev:        "Switch to the previous tab",
	ActionFilesToggleTree:      "Show or hide the file tree",
	ActionDiffVisual:           "Select lines for a range comment",
	ActionDiffComment:          "Add a comment",
	ActionDiffCommentFile:      "Add a file-level comment",
	ActionDiffFold:             "Fold the thread under the cursor",
	ActionDiffUnfoldAll:        "Unfold all threads",
	ActionDiffFoldAll:          "Fold all threads",
	ActionDiffScrollLeft:       "Scroll left",
	ActionDiffScrollRight:      "Scroll right",
	ActionDiffNextThread:       "Jump to the next thread",
	ActionDiffPrevThread:       "Jump to the previous thread",
	ActionDiffNextFile:         "Jump to the next file",
	ActionDiffPrevFile:         "Jump to the previous file",
	ActionDiffSearch:           "Search the diff",
	ActionDiffSearchNext:       "Go to the next search match",
	ActionDiffSearchPrev:       "Go to the previous search match",
	ActionDiffExpand:           "Expand the collapsed lines",
	ActionThreadReply:          "Reply to the thread",
	ActionThreadToggleResolved: "Resolve or unresolve thread",
	ActionCommentEdit:          "Edit the comment or PR body",
	ActionCommentDelete:        "Delete your comment",
	ActionCommentReact:         "Add or remove a reaction",
	ActionPRPending:            "List pending review comments",
	ActionPRThreads:            "List review threads",
	ActionPRSubmit:             "Submit the review",
	ActionPREdit:               "Edit the PR's title, labels, body",
}

// Description returns the short help text for a, or "" for an action
// without one, so callers can fall back to showing just the ID.
func (a Action) Description() string {
	return descriptions[a]
}
