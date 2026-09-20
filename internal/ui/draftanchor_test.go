package ui

import (
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestFormatLineAnchor(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		side            model.DiffSide
		startLine, line int
		want            string
	}{
		{"single line", "pkg/a.go", model.DiffSideRight, 0, 12, "pkg/a.go:RIGHT:12"},
		{"start equal to line is still single", "pkg/a.go", model.DiffSideLeft, 12, 12, "pkg/a.go:LEFT:12"},
		{"range", "pkg/a.go", model.DiffSideRight, 12, 15, "pkg/a.go:RIGHT:12-15"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatLineAnchor(tc.path, tc.side, tc.startLine, tc.line); got != tc.want {
				t.Errorf("formatLineAnchor(%q, %q, %d, %d) = %q, want %q", tc.path, tc.side, tc.startLine, tc.line, got, tc.want)
			}
		})
	}
}

func TestParseLineAnchor(t *testing.T) {
	tests := []struct {
		name                string
		anchor              string
		wantPath            string
		wantSide            model.DiffSide
		wantStart, wantLine int
		wantOK              bool
	}{
		{"single line", "pkg/a.go:RIGHT:12", "pkg/a.go", model.DiffSideRight, 0, 12, true},
		{"range", "pkg/a.go:LEFT:12-15", "pkg/a.go", model.DiffSideLeft, 12, 15, true},
		{"path containing a colon", "weird:path.go:RIGHT:3", "weird:path.go", model.DiffSideRight, 0, 3, true},
		{"unknown side rejected", "pkg/a.go:UP:12", "", "", 0, 0, false},
		{"non-numeric line rejected", "pkg/a.go:RIGHT:x", "", "", 0, 0, false},
		{"no colon at all rejected", "pkg/a.go", "", "", 0, 0, false},
		{"only one colon rejected", "pkg/a.go:12", "", "", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, side, start, line, ok := parseLineAnchor(tc.anchor)
			if ok != tc.wantOK {
				t.Fatalf("parseLineAnchor(%q) ok = %v, want %v", tc.anchor, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if path != tc.wantPath || side != tc.wantSide || start != tc.wantStart || line != tc.wantLine {
				t.Errorf("parseLineAnchor(%q) = (%q, %q, %d, %d), want (%q, %q, %d, %d)",
					tc.anchor, path, side, start, line, tc.wantPath, tc.wantSide, tc.wantStart, tc.wantLine)
			}
		})
	}
}

func TestFormatThenParseLineAnchorRoundTrips(t *testing.T) {
	tests := []struct {
		path            string
		side            model.DiffSide
		startLine, line int
	}{
		{"pkg/a.go", model.DiffSideRight, 0, 12},
		{"pkg/a.go", model.DiffSideLeft, 12, 15},
	}
	for _, tc := range tests {
		anchor := formatLineAnchor(tc.path, tc.side, tc.startLine, tc.line)
		path, side, start, line, ok := parseLineAnchor(anchor)
		if !ok {
			t.Fatalf("parseLineAnchor(%q) ok = false, want true", anchor)
		}
		if path != tc.path || side != tc.side || start != tc.startLine || line != tc.line {
			t.Errorf("round trip of (%q, %q, %d, %d) = (%q, %q, %d, %d)",
				tc.path, tc.side, tc.startLine, tc.line, path, side, start, line)
		}
	}
}
