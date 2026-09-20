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
var defaultTable = []defaultEntry{
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "j", ActionListDown},
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "k", ActionListUp},
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "gg", ActionListTop},
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "G", ActionListBottom},
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "<C-d>", ActionListHalfDown},
	{[]Context{ContextList, ContextDiff, ContextFiles, ContextDetail}, "<C-u>", ActionListHalfUp},

	{[]Context{ContextGlobal}, "<C-w>h", ActionGlobalFocusLeft},
	{[]Context{ContextGlobal}, "<C-w>l", ActionGlobalFocusRight},
	{[]Context{ContextComposer}, "<C-w>j", ActionGlobalFocusDown},
	{[]Context{ContextComposer}, "<C-w>k", ActionGlobalFocusUp},

	{[]Context{ContextDetail}, "<C-l>", ActionDetailTabNext},
	{[]Context{ContextDetail}, "gt", ActionDetailTabNext},
	{[]Context{ContextDetail}, "<BS>", ActionDetailTabPrev}, // Ctrl-h, see doc comment above
	{[]Context{ContextDetail}, "gT", ActionDetailTabPrev},

	{[]Context{ContextGlobal}, "<C-w>o", ActionGlobalToggleList},
	{[]Context{ContextFiles}, "<C-w>t", ActionFilesToggleTree},
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

	{[]Context{ContextPR}, "p", ActionPRPending},
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
