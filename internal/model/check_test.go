package model

import "testing"

func TestSummarize(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   ChecksSummary
	}{
		{"empty", nil, ChecksSummary{}},
		{
			"single success",
			[]Check{{Name: "build", Status: CheckStatusCompleted, Conclusion: CheckConclusionSuccess}},
			ChecksSummary{Total: 1, Success: 1},
		},
		{
			"mixed",
			[]Check{
				{Name: "build", Status: CheckStatusCompleted, Conclusion: CheckConclusionSuccess},
				{Name: "lint", Status: CheckStatusCompleted, Conclusion: CheckConclusionFailure},
				{Name: "deploy", Status: CheckStatusInProgress},
				{Name: "docs", Status: CheckStatusCompleted, Conclusion: CheckConclusionSkipped},
			},
			ChecksSummary{Total: 4, Success: 1, Failure: 1, Pending: 1, Skipped: 1},
		},
		{
			"queued counts as pending",
			[]Check{{Name: "build", Status: CheckStatusQueued}},
			ChecksSummary{Total: 1, Pending: 1},
		},
		{
			"neutral conclusion counts as skipped",
			[]Check{{Name: "build", Status: CheckStatusCompleted, Conclusion: CheckConclusionNeutral}},
			ChecksSummary{Total: 1, Skipped: 1},
		},
		{
			"every failure-like conclusion counts as failure",
			[]Check{
				{Name: "a", Status: CheckStatusCompleted, Conclusion: CheckConclusionFailure},
				{Name: "b", Status: CheckStatusCompleted, Conclusion: CheckConclusionError},
				{Name: "c", Status: CheckStatusCompleted, Conclusion: CheckConclusionCancelled},
				{Name: "d", Status: CheckStatusCompleted, Conclusion: CheckConclusionTimedOut},
				{Name: "e", Status: CheckStatusCompleted, Conclusion: CheckConclusionActionRequired},
				{Name: "f", Status: CheckStatusCompleted, Conclusion: CheckConclusionStartupFailure},
				{Name: "g", Status: CheckStatusCompleted, Conclusion: CheckConclusionStale},
			},
			ChecksSummary{Total: 7, Failure: 7},
		},
		{
			"a timed-out check must never be reported as green",
			[]Check{{Name: "build", Status: CheckStatusCompleted, Conclusion: CheckConclusionTimedOut}},
			ChecksSummary{Total: 1, Failure: 1},
		},
		{
			"unknown completed conclusion counts as failure, not ignored",
			[]Check{{Name: "build", Status: CheckStatusCompleted, Conclusion: CheckConclusion("SOMETHING_NEW")}},
			ChecksSummary{Total: 1, Failure: 1},
		},
		{
			"completed with empty conclusion counts as failure, not ignored",
			[]Check{{Name: "build", Status: CheckStatusCompleted, Conclusion: ""}},
			ChecksSummary{Total: 1, Failure: 1},
		},
		{
			"every non-completed status counts as pending",
			[]Check{
				{Name: "a", Status: CheckStatusQueued},
				{Name: "b", Status: CheckStatusInProgress},
				{Name: "c", Status: CheckStatusPending},
				{Name: "d", Status: CheckStatusWaiting},
				{Name: "e", Status: CheckStatusRequested},
				{Name: "f", Status: CheckStatusExpected},
			},
			ChecksSummary{Total: 6, Pending: 6},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Summarize(tc.checks); got != tc.want {
				t.Errorf("Summarize() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestChecksSummary_State(t *testing.T) {
	tests := []struct {
		name    string
		summary ChecksSummary
		want    ChecksState
	}{
		{"no checks", ChecksSummary{}, ChecksStateNone},
		{"all success", ChecksSummary{Total: 2, Success: 2}, ChecksStateSuccess},
		{"any failure wins", ChecksSummary{Total: 3, Success: 1, Failure: 1, Pending: 1}, ChecksStateFailure},
		{"pending without failure", ChecksSummary{Total: 2, Success: 1, Pending: 1}, ChecksStatePending},
		{"success and skipped only", ChecksSummary{Total: 2, Success: 1, Skipped: 1}, ChecksStateSuccess},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.summary.State(); got != tc.want {
				t.Errorf("State() = %q, want %q", got, tc.want)
			}
		})
	}
}
