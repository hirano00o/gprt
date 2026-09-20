package model

import "testing"

func TestMergeMethod_Values(t *testing.T) {
	tests := []struct {
		val  MergeMethod
		want string
	}{
		{MergeMethodMerge, "MERGE"},
		{MergeMethodSquash, "SQUASH"},
		{MergeMethodRebase, "REBASE"},
	}
	for _, tc := range tests {
		if string(tc.val) != tc.want {
			t.Errorf("MergeMethod = %q, want %q", string(tc.val), tc.want)
		}
	}
}
