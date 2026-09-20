package editor

import "testing"

// TestClustersTreatsAZWJEmojiSequenceAsOneCluster pins clusters' handling
// of a ZWJ ("zero width joiner") emoji sequence — the switch from the
// unmaintained uniseg dependency to uax29/v2/graphemes (see decisis #169)
// must not split a family emoji like "👨‍👩‍👧" into its individual member
// emoji: word/char motions (h, l, x, …) must still treat the whole
// sequence as a single character.
func TestClustersTreatsAZWJEmojiSequenceAsOneCluster(t *testing.T) {
	text := "a👨‍👩‍👧b"
	cs := clusters(text)

	want := []string{"a", "👨‍👩‍👧", "b"}
	if len(cs) != len(want) {
		t.Fatalf("clusters(%q) produced %d clusters, want %d: %#v", text, len(cs), len(want), cs)
	}
	for i, w := range want {
		if got := text[cs[i].start:cs[i].end]; got != w {
			t.Errorf("clusters(%q)[%d] = %q, want %q", text, i, got, w)
		}
	}
}
