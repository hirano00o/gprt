package keys

import "testing"

func feedSeq(t *testing.T, seq *Sequencer, notation string, ctxs []Context) Result {
	t.Helper()
	parsed, err := Parse(notation)
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", notation, err)
	}
	var last Result
	for _, k := range parsed {
		last = seq.Feed(k, ctxs)
	}
	return last
}

func TestSequencerSingleKey(t *testing.T) {
	seq := NewSequencer(Defaults())
	res := feedSeq(t, seq, "j", []Context{ContextList, ContextGlobal})
	if !res.Consumed || res.Action != ActionListDown || res.Count != 1 || res.Pending {
		t.Fatalf("Feed(j) = %+v, want a consumed exact list.down with count 1", res)
	}
}

func TestSequencerMultiKeyGG(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	first, err := Parse("g")
	if err != nil {
		t.Fatal(err)
	}
	res := seq.Feed(first[0], ctxs)
	if !res.Pending || !res.Consumed || res.Action != "" {
		t.Fatalf("Feed(g) = %+v, want Pending, Consumed, no action yet", res)
	}

	res = seq.Feed(first[0], ctxs) // second 'g'
	if !res.Consumed || res.Action != ActionListTop || res.Pending {
		t.Fatalf("Feed(g,g) = %+v, want a consumed exact list.top", res)
	}
}

func TestSequencerGtVsGg(t *testing.T) {
	t.Run("gt resolves detail.tab_next", func(t *testing.T) {
		seq := NewSequencer(Defaults())
		ctxs := []Context{ContextDetail, ContextGlobal}
		res := feedSeq(t, seq, "gt", ctxs)
		if !res.Consumed || res.Action != ActionDetailTabNext {
			t.Fatalf("Feed(g,t) = %+v, want detail.tab_next", res)
		}
	})

	t.Run("gg resolves list.top", func(t *testing.T) {
		seq := NewSequencer(Defaults())
		ctxs := []Context{ContextList, ContextGlobal}
		res := feedSeq(t, seq, "gg", ctxs)
		if !res.Consumed || res.Action != ActionListTop {
			t.Fatalf("Feed(g,g) = %+v, want list.top", res)
		}
	})
}

func TestSequencerCount(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	for _, r := range "3" {
		res := seq.Feed(Key{Kind: KindRune, Rune: r}, ctxs)
		if !res.Pending || !res.Consumed {
			t.Fatalf("Feed(%q) = %+v, want a pending count", string(r), res)
		}
	}
	res := seq.Feed(Key{Kind: KindRune, Rune: 'j'}, ctxs)
	if !res.Consumed || res.Action != ActionListDown || res.Count != 3 {
		t.Fatalf("Feed(3,j) = %+v, want list.down with count 3", res)
	}
}

func TestSequencerMultiDigitCount(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	seq.Feed(Key{Kind: KindRune, Rune: '1'}, ctxs)
	seq.Feed(Key{Kind: KindRune, Rune: '0'}, ctxs) // "0" continues a count once one has started
	res := seq.Feed(Key{Kind: KindRune, Rune: 'j'}, ctxs)
	if !res.Consumed || res.Count != 10 {
		t.Fatalf("Feed(1,0,j) = %+v, want count 10", res)
	}
}

func TestSequencerLeadingZeroIsNotACount(t *testing.T) {
	// "0" is unbound in the list context, so with no count already
	// pending it must fall through unconsumed rather than start a count,
	// matching vim's "0 moves to column 0" convention.
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}
	res := seq.Feed(Key{Kind: KindRune, Rune: '0'}, ctxs)
	if res.Consumed {
		t.Fatalf("Feed(0) = %+v, want unconsumed (0 is not bound)", res)
	}
}

func TestSequencerCtrlWThenL(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}
	res := feedSeq(t, seq, "<C-w>l", ctxs)
	if !res.Consumed || res.Action != ActionGlobalFocusRight {
		t.Fatalf("Feed(<C-w>l) = %+v, want global.focus_right", res)
	}
}

func TestSequencerEscResetsPending(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	g, _ := Parse("g")
	seq.Feed(g[0], ctxs)

	esc, _ := Parse("<Esc>")
	res := seq.Feed(esc[0], ctxs)
	if !res.Consumed {
		t.Fatalf("Feed(<Esc>) after a pending prefix = %+v, want Consumed (it cleared the pending state)", res)
	}

	// The buffer must now be empty: a fresh "j" resolves immediately
	// rather than being interpreted as a continuation of "g".
	res = seq.Feed(Key{Kind: KindRune, Rune: 'j'}, ctxs)
	if !res.Consumed || res.Action != ActionListDown {
		t.Fatalf("Feed(j) after Esc reset = %+v, want a fresh list.down", res)
	}
}

func TestSequencerUnboundKeyFallsThrough(t *testing.T) {
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextComment, ContextGlobal}
	res := seq.Feed(Key{Kind: KindRune, Rune: 'Q'}, ctxs)
	if res.Consumed {
		t.Fatalf("Feed(Q) = %+v, want unconsumed (Q is bound nowhere)", res)
	}
}

func TestSequencerAbandonedPrefixRetriesTheNewKeyAlone(t *testing.T) {
	// "g" starts a pending prefix (gg/gt/gT); pressing "j" next continues
	// no bound sequence, so the sequencer should abandon "g" and resolve
	// "j" as a fresh keypress rather than reporting the whole thing
	// unconsumed.
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	g, _ := Parse("g")
	seq.Feed(g[0], ctxs)

	res := seq.Feed(Key{Kind: KindRune, Rune: 'j'}, ctxs)
	if !res.Consumed || res.Action != ActionListDown {
		t.Fatalf("Feed(g,j) = %+v, want list.down (g abandoned, j resolved fresh)", res)
	}
}

func TestSequencerLoneLessThanIsNoMatchNotPending(t *testing.T) {
	// "<" renders as the single character "<", which is also the leading
	// character of every bracketed special key's own notation
	// ("<Enter>", "<C-d>", ...). A bound "<" key must resolve as NoMatch
	// immediately, not be mistaken for a Pending prefix of one of those.
	seq := NewSequencer(Defaults())
	ctxs := []Context{ContextList, ContextGlobal}

	res := seq.Feed(Key{Kind: KindRune, Rune: '<'}, ctxs)
	if res.Pending {
		t.Fatalf("Feed(<) = %+v, want NoMatch, not Pending", res)
	}
	if res.Consumed {
		t.Fatalf("Feed(<) = %+v, want unconsumed (< is bound nowhere)", res)
	}
}
