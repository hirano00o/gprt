package drafts

import "testing"

func TestKey_String_Format(t *testing.T) {
	k := Key{PR: "github.com/owner/repo#42", Kind: KindComment, Anchor: "issue"}
	want := "github.com/owner/repo#42|comment|issue"
	if got := k.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestKey_String_Uniqueness(t *testing.T) {
	keys := []Key{
		{PR: "github.com/owner/repo#1", Kind: KindComment, Anchor: "issue"},
		{PR: "github.com/owner/repo#2", Kind: KindComment, Anchor: "issue"},
		{PR: "github.com/owner/repo#1", Kind: KindReply, Anchor: "issue"},
		{PR: "github.com/owner/repo#1", Kind: KindComment, Anchor: "src/a.go:RIGHT:12-15"},
	}
	seen := make(map[string]Key, len(keys))
	for _, k := range keys {
		s := k.String()
		if other, dup := seen[s]; dup {
			t.Errorf("String() collision: %+v and %+v both produced %q", other, k, s)
		}
		seen[s] = k
	}
}
