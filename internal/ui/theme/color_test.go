package theme

import "testing"

func TestColorForIsStable(t *testing.T) {
	keys := []string{"hirano00o/gprt", "octocat/hello-world", "alice", "bob", ""}
	for _, k := range keys {
		first := ColorFor(k)
		for range 10 {
			if got := ColorFor(k); got != first {
				t.Fatalf("ColorFor(%q) is not stable across calls: %v then %v", k, first, got)
			}
		}
	}
}

func TestColorForDistributesAcrossThePalette(t *testing.T) {
	seen := map[string]bool{}
	// A handful of distinct keys should not all collide onto one colour;
	// this is not a strict proof of a good hash, just a smoke test that
	// ColorFor is not a constant function.
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		seen[ColorFor(k).String()] = true
	}
	if len(seen) < 2 {
		t.Fatalf("ColorFor produced only %d distinct colour(s) across 8 different keys", len(seen))
	}
}
