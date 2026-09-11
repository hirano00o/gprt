package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hirano00o/gprt/internal/model"
)

func TestReviewerStyle(t *testing.T) {
	tests := []struct {
		name      string
		state     model.ReviewState
		requested bool
		want      tcell.Style
	}{
		{"approved", model.ReviewStateApproved, false, Success},
		{"changes requested", model.ReviewStateChangesRequested, false, Error},
		{"commented", model.ReviewStateCommented, false, Base},
		{"dismissed", model.ReviewStateDismissed, false, Muted},
		{"pending own review", model.ReviewStatePending, false, Pending},
		{"requested, not yet reviewed", "", true, Stale},
		{"not requested, no review at all", "", false, Base},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReviewerStyle(tc.state, tc.requested); got != tc.want {
				t.Errorf("ReviewerStyle(%q, %v) = %v, want %v", tc.state, tc.requested, got, tc.want)
			}
		})
	}
}

func TestRollupStyle(t *testing.T) {
	tests := []struct {
		name  string
		state model.StatusState
		want  tcell.Style
	}{
		{"success", model.StatusStateSuccess, Success},
		{"failure", model.StatusStateFailure, Error},
		{"error", model.StatusStateError, Error},
		{"pending", model.StatusStatePending, Pending},
		{"expected", model.StatusStateExpected, Pending},
		{"none", "", Muted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RollupStyle(tc.state); got != tc.want {
				t.Errorf("RollupStyle(%q) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}
