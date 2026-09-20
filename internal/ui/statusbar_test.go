package ui

import (
	"testing"
	"time"
)

func TestRelativeTimeZeroReturnsEmpty(t *testing.T) {
	if got := relativeTime(time.Time{}); got != "" {
		t.Errorf("relativeTime(zero time) = %q, want empty (no reasonable relative time to show)", got)
	}
}

func TestRelativeTimeFutureReturnsJustNow(t *testing.T) {
	// A timestamp in the future (clock skew, or a zero-adjacent value that
	// rounds past "now" between construction and comparison) must never
	// render as a nonsensical, enormous negative duration.
	if got := relativeTime(time.Now().Add(time.Hour)); got != "just now" {
		t.Errorf("relativeTime(future time) = %q, want \"just now\"", got)
	}
}

func TestRelativeTimeRecentPastStillReportsSeconds(t *testing.T) {
	if got := relativeTime(time.Now().Add(-5 * time.Second)); got == "" || got == "just now" {
		t.Errorf("relativeTime(5s ago) = %q, want a normal \"Ns ago\" reading", got)
	}
}

// TestRelativeTimeAddsUnitsAboveHours covers days/months/years: the PR
// tab's "opened"/"updated" lines now show relativeTime against timestamps
// that can be arbitrarily old, unlike the status bar's own "last refresh",
// which is never more than a few minutes in the past.
func TestRelativeTimeAddsUnitsAboveHours(t *testing.T) {
	tests := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"just under a day", 23*time.Hour + 59*time.Minute, "23h ago"},
		{"a few days", 3 * 24 * time.Hour, "3d ago"},
		{"just under a month", 29 * 24 * time.Hour, "29d ago"},
		{"a couple months", 40 * 24 * time.Hour, "1mo ago"},
		{"just under a year", 300 * 24 * time.Hour, "10mo ago"},
		{"over a year", 400 * 24 * time.Hour, "1y ago"},
		{"several years", 800 * 24 * time.Hour, "2y ago"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeTime(time.Now().Add(-tc.ago)); got != tc.want {
				t.Errorf("relativeTime(%v ago) = %q, want %q", tc.ago, got, tc.want)
			}
		})
	}
}
