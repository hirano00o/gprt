//go:build ghostty

package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/clipperhouse/displaywidth"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

// ghosttyRawWidth is how many cells a bare terminal advances its cursor after
// printing s: Ghostty's own width for it, which tcell's cursor repositioning
// hides in the app's grid. grapheme sets mode 2027 first, for comparison only.
func ghosttyRawWidth(t *testing.T, s string, grapheme bool) int {
	t.Helper()
	var reply []byte
	vt := newVTTerm(t, func(b []byte) { reply = append(reply, b...) })
	if grapheme {
		vt.write([]byte("\x1b[?2027h"))
	}
	vt.write([]byte("\x1b[H" + s + "\x1b[6n"))
	var row, col int
	if _, err := fmt.Sscanf(string(reply), "\x1b[%d;%dR", &row, &col); err != nil {
		t.Fatalf("cursor position reply %q: %v", reply, err)
	}
	return col - 1
}

// TestGhosttyTitleWidthsAgreeWithTheTerminal renders a title made of one
// grapheme cluster followed by an ASCII marker, and checks that every width
// source gives the cluster the same number of cells as gprt's displaywidth,
// which lays the row out: tcell, tview's uniseg, the marker's column in the
// terminal's grid, and the cursor advance of a bare terminal with mode 2027
// (grapheme clusters) on.
//
// The cursor advance with mode 2027 off, libghostty-vt's default and what any
// terminal without grapheme clustering does, is where Ghostty differs for some
// clusters. Those cases carry the width they are known to measure in
// wantRawWidth and skip only when that is exactly what is measured, so a
// changed measurement, or a gap that closed, fails the test instead. tcell
// moves the cursor explicitly after a cluster, so the marker column cannot
// show the gap; only the cursor advance does. A stock Ghostty defaults to
// grapheme-width-method=unicode and would not show these gaps.
func TestGhosttyTitleWidthsAgreeWithTheTerminal(t *testing.T) {
	const gapReason = "Ghostty counts the cluster in pieces with mode 2027 off, which tcell never turns on, while gprt, tcell and uniseg count one cluster"
	tests := []struct {
		name    string
		cluster string
		// knownGap is why Ghostty's width with mode 2027 off differs from
		// gprt's, and wantRawWidth the width it is known to measure.
		knownGap     string
		wantRawWidth int
	}{
		{name: "wide emoji", cluster: "\U0001F600"},
		{name: "VS16 heart", cluster: "\u2764\uFE0F", knownGap: "Ghostty keeps U+2764 U+FE0F narrow with mode 2027 off", wantRawWidth: 1},
		{name: "ZWJ family", cluster: "\U0001F468\u200D\U0001F469\u200D\U0001F467", knownGap: gapReason, wantRawWidth: 6},
		{name: "combining acute", cluster: "e\u0301"},
		{name: "skin tone modifier", cluster: "\U0001F44D\U0001F3FD", knownGap: gapReason, wantRawWidth: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title := tc.cluster + "XYZ"
			g := newGhosttyApp(t, testAppOpts{}, func(f *fakeGitHub) {
				f.SetResult(
					gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open"),
					gh.SearchResult{Items: []model.PullRequest{fixturePR(1, title)}},
				)
			})
			g.Eventually(t, func(s string) bool { return strings.Contains(s, "XYZ") })

			// Look only in the list pane, left of the top-right corner of its
			// frame, so the preview pane cannot supply the marker.
			listRight := -1
			for x := uint16(0); x < ghosttyCols; x++ {
				if cp, _ := g.vt.cell(t, x, 0); cp == '╗' {
					listRight = int(x)
					break
				}
			}
			if listRight < 0 {
				t.Fatalf("list pane frame not found:\n%s", g.Text())
			}
			row, xMarker, xHash := -1, -1, -1
			for y := uint32(0); y < ghosttyRows && row < 0; y++ {
				for x := uint16(0); int(x)+2 < listRight; x++ {
					a, _ := g.vt.cell(t, x, y)
					b, _ := g.vt.cell(t, x+1, y)
					c, _ := g.vt.cell(t, x+2, y)
					if a == 'X' && b == 'Y' && c == 'Z' {
						row, xMarker = int(y), int(x)
						break
					}
				}
			}
			if row < 0 {
				t.Fatalf("marker not found in the list pane:\n%s", g.Text())
			}
			for x := 0; x < listRight; x++ {
				if cp, _ := g.vt.cell(t, uint16(x), uint32(row)); cp == '#' {
					xHash = x
					break
				}
			}
			// The row reads "#1 " and then the title.
			markerOffset := xMarker - (xHash + 3)

			sim := tcell.NewSimulationScreen("")
			if err := sim.Init(); err != nil {
				t.Fatal(err)
			}
			defer sim.Fini()
			_, tcellWidth := sim.Put(0, 0, tc.cluster, tcell.StyleDefault)

			rawWidth := ghosttyRawWidth(t, tc.cluster, false)
			rawWidthMode2027 := ghosttyRawWidth(t, tc.cluster, true)
			gprtWidth := displaywidth.String(tc.cluster)
			unisegWidth := tview.TaggedStringWidth(tc.cluster)
			t.Logf("%-20s gprt(displaywidth)=%d tcell=%d uniseg=%d ghostty(as is, mode2027=%v)=%d ghostty(mode2027 on)=%d marker-offset=%d",
				tc.name, gprtWidth, tcellWidth, unisegWidth, g.GraphemeClusterMode(t), rawWidth, rawWidthMode2027, markerOffset)

			if tcellWidth != gprtWidth {
				t.Errorf("tcell gives the cluster %d cells, gprt computed %d", tcellWidth, gprtWidth)
			}
			if unisegWidth != gprtWidth {
				t.Errorf("uniseg gives the cluster %d cells, gprt computed %d", unisegWidth, gprtWidth)
			}
			if markerOffset != gprtWidth {
				t.Errorf("marker at x=%d: the terminal's grid gave the cluster %d cells, gprt computed %d", xMarker, markerOffset, gprtWidth)
			}
			if rawWidthMode2027 != gprtWidth {
				t.Errorf("Ghostty with mode 2027 on advances %d cells for the cluster, gprt computed %d", rawWidthMode2027, gprtWidth)
			}

			switch {
			case tc.knownGap == "" && rawWidth != gprtWidth:
				t.Errorf("Ghostty (mode 2027 off) advances %d cells for the cluster, gprt computed %d", rawWidth, gprtWidth)
			case tc.knownGap != "" && rawWidth == gprtWidth:
				t.Errorf("knownGap no longer applies, remove it: Ghostty now agrees with gprt at %d cells", gprtWidth)
			case tc.knownGap != "" && rawWidth != tc.wantRawWidth:
				t.Errorf("Ghostty (mode 2027 off) advances %d cells, the known gap expects %d", rawWidth, tc.wantRawWidth)
			case tc.knownGap != "" && !t.Failed():
				t.Skipf("known gap: %s (gprt=%d tcell=%d uniseg=%d ghostty=%d)",
					tc.knownGap, gprtWidth, tcellWidth, unisegWidth, rawWidth)
			}
		})
	}
}
