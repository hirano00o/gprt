package keys

// mustParse parses a vim-notation sequence known at compile time to be
// valid. It panics on failure, which can only happen if defaultTable below
// contains a typo — a programmer error caught immediately by
// TestDefaultsBuild, never a possible outcome of user input.
func mustParse(seq string) []Key {
	k, err := Parse(seq)
	if err != nil {
		panic("keys: invalid default sequence " + seq + ": " + err.Error())
	}
	return k
}

// defaultEntry is one row of docs/KEYBINDINGS.md's default bindings table:
// a sequence bound to an action in every listed context.
type defaultEntry struct {
	ctxs     []Context
	sequence string
	action   Action
}

// defaultTable reproduces docs/KEYBINDINGS.md's "Default bindings" table
// (everything except the vim editor's own fixed keys, which are not part of
// the remappable keymap at all). Two rows can bind the same action to
// different sequences (for example detail.tab_next has both "<C-l>" and
// "gt"): Merge collapses that down to a user's single chosen sequence, in
// the same contexts, when the action is remapped.
//
// One notable translation from the docs table: the "Ctrl-h" row for
// detail.tab_prev is written here as "<BS>", not "<C-h>", because Normalize
// unifies KeyCtrlH with Backspace into a single <BS> key — a real terminal's
// Ctrl-H keypress is exactly what reaches the router as <BS>. Binding the
// literal parsed form of "<C-h>" here would never match anything Normalize
// actually produces.
//
// list.down/up/top/bottom/half_down/half_up are bound in ContextFiles too
// (M2 review round 2, item 14), using the exact same sequences as every
// other movable context: the Files tab's tree pane is a tview.TreeView,
// which already binds j/k/g/G (and, via 'K'/'J', parent/child navigation)
// itself, but gt/gT (tab switching) are also bound in ContextFiles, so a
// bare "g" always matches at least a Prefix there — it can never reach
// tview's own native single-"g"-means-home handling by falling through
// unconsumed (see keys.ctxTable.lookup/Sequencer.Feed: a key that is a
// Prefix of any bound sequence in the current context is always Consumed,
// regardless of whether it ever completes one). Binding list.top to "gg"
// here, like ContextList/ContextDiff/ContextDetail already do, resolves
// that: internal/ui's focusedListPane wraps the tree in a treeMovablePane
// adapter that implements movablePane by synthesising the tree's own
// native keys (j/k/PgDn/PgUp, and repeated k/j for top/bottom — see
// treeMovablePane.MoveTop's own doc comment for why not tview's native "g"/
// "G") through its InputHandler, rather than reimplementing tree traversal
// by hand — the visible behaviour is unchanged for j/k/PgDn/PgUp, and "gg"
// (not a lone "g") now reliably moves the cursor to the top instead of
// never firing at all.
var defaultTable = []defaultEntry{
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "j", ActionListDown},
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "k", ActionListUp},
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "gg", ActionListTop},
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "G", ActionListBottom},
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "<C-d>", ActionListHalfDown},
	{[]Context{ContextList, ContextFiles, ContextDiff, ContextDetail}, "<C-u>", ActionListHalfUp},

	{[]Context{ContextGlobal}, "<C-w>h", ActionGlobalFocusLeft},
	{[]Context{ContextGlobal}, "<C-w>l", ActionGlobalFocusRight},
	// Both directions toggle focus between the composer and whichever
	// detail-column pane it was opened over: bound in ContextComposer so
	// either key leaves it, and in every pane the composer can be opened
	// from so either key also *enters* it (a no-op when no composer is
	// open at all — see App.toggleComposerFocus).
	{[]Context{ContextComposer, ContextDetail, ContextFiles, ContextDiff}, "<C-w>j", ActionGlobalFocusDown},
	{[]Context{ContextComposer, ContextDetail, ContextFiles, ContextDiff}, "<C-w>k", ActionGlobalFocusUp},

	// Bound in ContextFiles/ContextDiff too (not just ContextDetail): the
	// Files tab's tree and diff panes are their own contexts, but tab
	// switching must still work no matter which of the detail column's
	// panes currently has focus.
	{[]Context{ContextDetail, ContextFiles, ContextDiff}, "<C-l>", ActionDetailTabNext},
	{[]Context{ContextDetail, ContextFiles, ContextDiff}, "gt", ActionDetailTabNext},
	{[]Context{ContextDetail, ContextFiles, ContextDiff}, "<BS>", ActionDetailTabPrev}, // Ctrl-h, see doc comment above
	{[]Context{ContextDetail, ContextFiles, ContextDiff}, "gT", ActionDetailTabPrev},

	{[]Context{ContextGlobal}, "<C-w>o", ActionGlobalToggleList},
	// Bound in ContextDiff too (not just ContextFiles): once the tree is
	// hidden, focus can only ever be on the diff, so the action to show it
	// again must be reachable from there.
	{[]Context{ContextFiles, ContextDiff}, "<C-w>t", ActionFilesToggleTree},
	{[]Context{ContextGlobal}, "R", ActionGlobalReload},

	{[]Context{ContextList}, "/", ActionListFilter},
	{[]Context{ContextList, ContextFiles}, "<Enter>", ActionListOpen},
	{[]Context{ContextList, ContextFiles}, "l", ActionListOpen},
	{[]Context{ContextList}, "n", ActionListNewPR},

	{[]Context{ContextDiff}, "V", ActionDiffVisual},
	{[]Context{ContextDiff, ContextPR}, "c", ActionDiffComment},
	{[]Context{ContextThread}, "c", ActionThreadReply},
	{[]Context{ContextThread}, "r", ActionThreadReply},
	{[]Context{ContextDiff}, "C", ActionDiffCommentFile},

	{[]Context{ContextComment, ContextPR}, "e", ActionCommentEdit},
	{[]Context{ContextComment, ContextPR}, "d", ActionCommentDelete},
	{[]Context{ContextThread}, "x", ActionThreadToggleResolved},
	{[]Context{ContextComment, ContextPR}, "a", ActionCommentReact},

	// "PR open" (docs/KEYBINDINGS.md), not just the PR tab: the pending
	// list is just as useful while looking at the diff, or with the tree
	// focused.
	{[]Context{ContextPR, ContextFiles, ContextDiff}, "p", ActionPRPending},
	{[]Context{ContextPR}, "S", ActionPRSubmit},
	{[]Context{ContextPR}, "E", ActionPREdit},

	{[]Context{ContextGlobal}, "o", ActionGlobalOpenBrowser},

	{[]Context{ContextDiff}, "za", ActionDiffFold},
	{[]Context{ContextDiff}, "zR", ActionDiffUnfoldAll},
	{[]Context{ContextDiff}, "zM", ActionDiffFoldAll},
	{[]Context{ContextDiff}, "zh", ActionDiffScrollLeft},
	{[]Context{ContextDiff}, "zl", ActionDiffScrollRight},
	{[]Context{ContextDiff}, "]c", ActionDiffNextThread},
	{[]Context{ContextDiff}, "[c", ActionDiffPrevThread},
	{[]Context{ContextDiff}, "]f", ActionDiffNextFile},
	{[]Context{ContextDiff}, "[f", ActionDiffPrevFile},

	{[]Context{ContextGlobal}, "?", ActionGlobalHelp},
	{[]Context{ContextGlobal}, "q", ActionGlobalQuit},
	{[]Context{ContextGlobal}, ":", ActionGlobalCommand},
}

// Defaults returns gprt's built-in keymap, reproducing docs/KEYBINDINGS.md's
// default bindings table exactly. Pass it to Merge along with a config's
// "keys:" overrides.
func Defaults() Keymap {
	var entries []binding
	for _, e := range defaultTable {
		seq := mustParse(e.sequence)
		for _, ctx := range e.ctxs {
			entries = append(entries, binding{ctx: ctx, seq: seq, action: e.action})
		}
	}
	km, err := build(entries)
	if err != nil {
		// A conflict here is a bug in defaultTable, not possible user
		// input; see mustParse's doc comment for the same reasoning.
		panic("keys: default table is inconsistent: " + err.Error())
	}
	return km
}
