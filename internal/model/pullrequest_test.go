package model

import "testing"

func TestMergeableState_Values(t *testing.T) {
	tests := []struct {
		val  MergeableState
		want string
	}{
		{MergeableStateMergeable, "MERGEABLE"},
		{MergeableStateConflicting, "CONFLICTING"},
		{MergeableStateUnknown, "UNKNOWN"},
	}
	for _, tc := range tests {
		if string(tc.val) != tc.want {
			t.Errorf("MergeableState = %q, want %q", string(tc.val), tc.want)
		}
	}
}

func TestMergeStateStatus_Values(t *testing.T) {
	tests := []struct {
		val  MergeStateStatus
		want string
	}{
		{MergeStateStatusBehind, "BEHIND"},
		{MergeStateStatusBlocked, "BLOCKED"},
		{MergeStateStatusClean, "CLEAN"},
		{MergeStateStatusDirty, "DIRTY"},
		{MergeStateStatusDraft, "DRAFT"},
		{MergeStateStatusHasHooks, "HAS_HOOKS"},
		{MergeStateStatusUnknown, "UNKNOWN"},
		{MergeStateStatusUnstable, "UNSTABLE"},
	}
	for _, tc := range tests {
		if string(tc.val) != tc.want {
			t.Errorf("MergeStateStatus = %q, want %q", string(tc.val), tc.want)
		}
	}
}

func TestPullRequest_MergeFieldsAreTyped(t *testing.T) {
	pr := PullRequest{
		Mergeable:        MergeableStateMergeable,
		MergeStateStatus: MergeStateStatusClean,
	}
	if pr.Mergeable != MergeableStateMergeable {
		t.Errorf("Mergeable = %v, want %v", pr.Mergeable, MergeableStateMergeable)
	}
	if pr.MergeStateStatus != MergeStateStatusClean {
		t.Errorf("MergeStateStatus = %v, want %v", pr.MergeStateStatus, MergeStateStatusClean)
	}
}
