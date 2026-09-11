package keys

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDefaultsBuildsWithoutError(t *testing.T) {
	// Defaults panics on an inconsistent table; simply calling it and
	// exercising a lookup is the regression test for that invariant.
	km := Defaults()
	action, kind := km.Lookup(ContextList, mustParse("j"))
	if kind != Exact || action != ActionListDown {
		t.Fatalf("Lookup(list, j) = (%q, %v), want (%q, Exact)", action, kind, ActionListDown)
	}
}

func TestKeymapLookup(t *testing.T) {
	km := Defaults()

	t.Run("exact match in the specific context", func(t *testing.T) {
		action, kind := km.Lookup(ContextList, mustParse("gg"))
		if kind != Exact || action != ActionListTop {
			t.Fatalf("Lookup(list, gg) = (%q, %v), want (%q, Exact)", action, kind, ActionListTop)
		}
	})

	t.Run("prefix of a longer sequence", func(t *testing.T) {
		_, kind := km.Lookup(ContextList, mustParse("g"))
		if kind != Prefix {
			t.Fatalf("Lookup(list, g) kind = %v, want Prefix", kind)
		}
	})

	t.Run("falls back from specific context to global", func(t *testing.T) {
		action, kind := km.Lookup(ContextList, mustParse("?"))
		if kind != Exact || action != ActionGlobalHelp {
			t.Fatalf("Lookup(list, ?) = (%q, %v), want (%q, Exact)", action, kind, ActionGlobalHelp)
		}
	})

	t.Run("unbound sequence in the specific context and global", func(t *testing.T) {
		action, kind := km.Lookup(ContextComment, mustParse("Q"))
		if kind != NoMatch || action != "" {
			t.Fatalf("Lookup(comment, Q) = (%q, %v), want (\"\", NoMatch)", action, kind)
		}
	})

	t.Run("backspace normalizes to the same binding as Ctrl-H", func(t *testing.T) {
		ev := tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone)
		action, kind := km.Lookup(ContextDetail, Normalize(ev))
		if kind != Exact || action != ActionDetailTabPrev {
			t.Fatalf("Lookup(detail, <BS>) = (%q, %v), want (%q, Exact)", action, kind, ActionDetailTabPrev)
		}
	})
}

func TestMergeOverridesReplaceAllContextsOfAnAction(t *testing.T) {
	km, err := Merge(Defaults(), map[string]string{"list.down": "<C-n>"})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	for _, ctx := range []Context{ContextList, ContextDiff, ContextFiles} {
		if action, kind := km.Lookup(ctx, mustParse("<C-n>")); kind != Exact || action != ActionListDown {
			t.Errorf("Lookup(%v, <C-n>) = (%q, %v), want (%q, Exact)", ctx, action, kind, ActionListDown)
		}
		if _, kind := km.Lookup(ctx, mustParse("j")); kind != NoMatch {
			t.Errorf("Lookup(%v, j) kind = %v after remap, want NoMatch (old binding removed)", ctx, kind)
		}
	}
}

func TestMergeUnknownAction(t *testing.T) {
	_, err := Merge(Defaults(), map[string]string{"bogus.action": "x"})
	if err == nil {
		t.Fatal("Merge with an unknown action ID returned nil error")
	}
	if !strings.Contains(err.Error(), "bogus.action") {
		t.Errorf("error %q does not mention the offending action", err)
	}
}

func TestMergeUnparsableSequence(t *testing.T) {
	_, err := Merge(Defaults(), map[string]string{"global.reload": "<Bogus>"})
	if err == nil {
		t.Fatal("Merge with an unparsable sequence returned nil error")
	}
}

func TestMergeDuplicateSequenceConflict(t *testing.T) {
	// Rebinding list.up to "j" collides with list.down's existing "j" in
	// every context the two actions share (list, diff, files).
	_, err := Merge(Defaults(), map[string]string{"list.up": "j"})
	if err == nil {
		t.Fatal("Merge with a duplicate sequence returned nil error")
	}
	if !strings.Contains(err.Error(), "list.down") || !strings.Contains(err.Error(), "list.up") {
		t.Errorf("error %q does not mention both conflicting actions", err)
	}
}

func TestMergePrefixConflict(t *testing.T) {
	// Rebinding list.filter to "g" makes it a prefix of the existing "gg"
	// binding (list.top) in the same (list) context.
	_, err := Merge(Defaults(), map[string]string{"list.filter": "g"})
	if err == nil {
		t.Fatal("Merge with a prefix conflict returned nil error")
	}
	if !strings.Contains(err.Error(), "prefix") {
		t.Errorf("error %q does not describe a prefix conflict", err)
	}
}

func TestMergeJoinsMultipleErrors(t *testing.T) {
	_, err := Merge(Defaults(), map[string]string{
		"bogus.action":  "x",
		"global.reload": "<Bogus>",
	})
	if err == nil {
		t.Fatal("Merge with two independent errors returned nil error")
	}
	if !strings.Contains(err.Error(), "bogus.action") {
		t.Errorf("joined error %q missing the unknown-action complaint", err)
	}
	if !strings.Contains(err.Error(), "Bogus") {
		t.Errorf("joined error %q missing the unparsable-sequence complaint", err)
	}
}

func TestMergeRejectsSequenceStartingWithEsc(t *testing.T) {
	_, err := Merge(Defaults(), map[string]string{"global.reload": "<Esc>R"})
	if err == nil {
		t.Fatal("Merge with a sequence starting with <Esc> returned nil error")
	}
	if !strings.Contains(err.Error(), "global.reload") {
		t.Errorf("error %q does not name the offending action", err)
	}
	if !strings.Contains(err.Error(), "Esc") {
		t.Errorf("error %q does not mention Esc", err)
	}
}

func TestMergeRejectsBareEscSequence(t *testing.T) {
	// <Esc> alone is just as unreachable: Sequencer.Feed intercepts Esc
	// before ever resolving the pending buffer against the keymap.
	_, err := Merge(Defaults(), map[string]string{"global.reload": "<Esc>"})
	if err == nil {
		t.Fatal("Merge with a bare <Esc> sequence returned nil error")
	}
}

func TestMergeRejectsSequenceStartingWithDigit(t *testing.T) {
	_, err := Merge(Defaults(), map[string]string{"global.reload": "3R"})
	if err == nil {
		t.Fatal("Merge with a sequence starting with a digit returned nil error")
	}
	if !strings.Contains(err.Error(), "global.reload") {
		t.Errorf("error %q does not name the offending action", err)
	}
	if !strings.Contains(err.Error(), "digit") {
		t.Errorf("error %q does not mention the digit restriction", err)
	}
}

func TestMergeRejectsContextSequenceEqualToGlobal(t *testing.T) {
	// list.new_pr defaults to "n" in ContextList; rebinding it to
	// "<C-w>h" (global.focus_left's sequence) would make the two
	// indistinguishable whenever the list has focus.
	_, err := Merge(Defaults(), map[string]string{"list.new_pr": "<C-w>h"})
	if err == nil {
		t.Fatal("Merge with a context sequence equal to a global one returned nil error")
	}
	if !strings.Contains(err.Error(), "list.new_pr") {
		t.Errorf("error %q does not name the offending action", err)
	}
}

func TestMergeRejectsContextSequencePrefixingGlobal(t *testing.T) {
	// "<C-w>" would be a prefix of global's "<C-w>h"/"<C-w>l"/"<C-w>o":
	// Keymap.Lookup and Sequencer.Feed both return as soon as the
	// specific context yields any non-NoMatch result, so list.filter's
	// own exact match on "<C-w>" would fire immediately and the global
	// Ctrl-w bindings would never be reachable while the list has focus.
	_, err := Merge(Defaults(), map[string]string{"list.filter": "<C-w>"})
	if err == nil {
		t.Fatal("Merge with a context sequence prefixing a global one returned nil error")
	}
	if !strings.Contains(err.Error(), "list.filter") {
		t.Errorf("error %q does not name the offending action", err)
	}
}

func TestMergeRejectsGlobalSequencePrefixingContext(t *testing.T) {
	// Rebinding global.reload to "z" would make it a prefix of every
	// "z*" binding in ContextDiff (za, zR, zM, zh, zl): the context's own
	// bindings could never resolve while diff has focus, since global's
	// exact match on "z" alone is never even reached — ContextDiff's own
	// Prefix result for "z" wins first, and it would stay pending
	// forever waiting for a second key that would never arrive because
	// the *global* table treats "z" as complete.
	_, err := Merge(Defaults(), map[string]string{"global.reload": "z"})
	if err == nil {
		t.Fatal("Merge with a global sequence prefixing a context one returned nil error")
	}
	if !strings.Contains(err.Error(), "global.reload") {
		t.Errorf("error %q does not name the offending action", err)
	}
}

func TestKeymapLookupLoneLessThanIsNotAFalsePrefixOfEnter(t *testing.T) {
	// "<" is a complete, unbound rune key in its own right; it must not
	// be mistaken for a prefix of "<Enter>", "<C-d>", etc. merely because
	// their *rendered* vim notation also starts with the character '<'.
	km := Defaults()
	_, kind := km.Lookup(ContextList, []Key{{Kind: KindRune, Rune: '<'}})
	if kind != NoMatch {
		t.Fatalf("Lookup(list, <) kind = %v, want NoMatch", kind)
	}
}
