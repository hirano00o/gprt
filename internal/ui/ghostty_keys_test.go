//go:build ghostty

package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rivo/tview"
	"go.mitchellh.com/libghostty"

	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
)

func TestGhosttyStartupRendersPRTitle(t *testing.T) {
	g := newGhosttyApp(t, testAppOpts{}, nil)
	g.Eventually(t, func(s string) bool {
		return strings.Contains(s, "First PR") && strings.Contains(s, "Second PR")
	})
}

func TestGhosttyEnablesKittyDisambiguate(t *testing.T) {
	g := newGhosttyApp(t, testAppOpts{}, nil)
	g.Eventually(t, func(s string) bool { return strings.Contains(s, "First PR") })
	if got := g.KittyFlags(t); got&libghostty.KittyKeyDisambiguate == 0 {
		t.Fatalf("kitty keyboard flags = %#b, want the disambiguate bit set by tcell", got)
	}
}

// openCreateForm opens the create-PR form and dismisses the repository
// field's autocomplete drop-down, which swallows the first Esc. It waits for
// the viewer's repositories first: the event announcing them reopens the
// drop-down, so arriving after this Esc it would swallow the key under test.
func openCreateForm(t *testing.T, g *ghosttyApp, legacy bool) {
	t.Helper()
	g.Type(t, legacy, "n")
	waitFor(t, g.app.app, func() bool {
		return g.app.deps.Store.ViewerRepositories() != nil && g.app.createFormRepoSuggesting
	})
	g.Press(t, legacy, libghostty.KeyEscape, 0, "", 0)
	waitFor(t, g.app.app, func() bool {
		return !g.app.createFormRepoSuggesting && g.app.overlay == "createform"
	})
}

// waitForFormItem waits until the create form's focused item is want.
func waitForFormItem(t *testing.T, g *ghosttyApp, want int) {
	t.Helper()
	waitFor(t, g.app.app, func() bool {
		if g.app.createForm == nil {
			return false
		}
		item, _ := g.app.createForm.GetFocusedItemIndex()
		return item == want
	})
}

// TestGhosttyKeysMatchBetweenCSIuAndLegacy presses each pair of keys that
// Normalize collapses (Ctrl-H/Backspace, Ctrl-I/Tab, Ctrl-M/Enter, Ctrl-[/Esc)
// through both the kitty-keyboard encoding tcell negotiated and the plain
// legacy encoding, and expects the same effect on the real screen either way.
func TestGhosttyKeysMatchBetweenCSIuAndLegacy(t *testing.T) {
	const noFile = "no file selected"

	type step = func(t *testing.T, g *ghosttyApp, legacy bool)

	onFilesTab := func(t *testing.T, g *ghosttyApp, legacy bool) {
		g.Press(t, legacy, libghostty.KeyW, libghostty.ModCtrl, "", 'w')
		g.Type(t, legacy, "l")
		g.Type(t, legacy, "gt")
		g.Eventually(t, func(s string) bool { return strings.Contains(s, noFile) })
	}
	backOnPreviousTab := func(t *testing.T, g *ghosttyApp, _ bool) {
		g.Eventually(t, func(s string) bool {
			return !strings.Contains(s, noFile) && strings.Contains(s, "No checks")
		})
	}
	onList := func(t *testing.T, g *ghosttyApp, _ bool) {
		g.Eventually(t, func(s string) bool {
			return strings.Contains(s, "Enter open") && !strings.Contains(s, "c comment")
		})
	}
	prOpened := func(t *testing.T, g *ghosttyApp, _ bool) {
		g.Eventually(t, func(s string) bool { return strings.Contains(s, "c comment") })
	}
	onFirstFormItem := func(t *testing.T, g *ghosttyApp, legacy bool) {
		openCreateForm(t, g, legacy)
		waitForFormItem(t, g, 0)
	}
	onSecondFormItem := func(t *testing.T, g *ghosttyApp, _ bool) { waitForFormItem(t, g, 1) }
	formClosed := func(t *testing.T, g *ghosttyApp, _ bool) {
		waitFor(t, g.app.app, func() bool { return g.app.overlay == "" })
	}
	titleText := func(g *ghosttyApp) string {
		return g.app.createForm.GetFormItem(3).(*tview.InputField).GetText()
	}
	onTitleHoldingAB := func(t *testing.T, g *ghosttyApp, legacy bool) {
		openCreateForm(t, g, legacy)
		for item := 1; item <= 3; item++ {
			g.Press(t, legacy, libghostty.KeyTab, 0, "", 0)
			waitForFormItem(t, g, item)
		}
		g.Type(t, legacy, "ab")
		waitFor(t, g.app.app, func() bool { return titleText(g) == "ab" })
	}
	lastCharDeleted := func(t *testing.T, g *ghosttyApp, _ bool) {
		waitFor(t, g.app.app, func() bool { return titleText(g) == "a" })
	}
	onCancelButton := func(t *testing.T, g *ghosttyApp, legacy bool) {
		openCreateForm(t, g, legacy)
		for range 8 { // the five fields, then Edit body, Reviewers and Create
			g.Press(t, legacy, libghostty.KeyTab, 0, "", 0)
		}
		waitFor(t, g.app.app, func() bool {
			_, button := g.app.createForm.GetFocusedItemIndex()
			return button == 3
		})
	}

	tests := []struct {
		name      string
		key       libghostty.Key
		mods      libghostty.Mods
		unshifted rune
		wantCSIu  string
		// wantLegacy is empty when a legacy terminal has no byte of its own
		// for the key: Ctrl-I, Ctrl-M and Ctrl-[ are Tab, Enter and Esc there,
		// which their own cases already cover.
		wantLegacy string
		// before drives the app to a state where the key has a visible
		// effect and checks that the effect has not happened yet.
		before step
		// after waits for the key's effect.
		after step
	}{
		{
			name: "ctrl-h switches to the previous tab", key: libghostty.KeyH, mods: libghostty.ModCtrl, unshifted: 'h',
			wantCSIu: "\x1b[104;5u", wantLegacy: "\x08", before: onFilesTab, after: backOnPreviousTab,
		},
		{
			name: "backspace switches to the previous tab", key: libghostty.KeyBackspace,
			wantCSIu: "\x7f", wantLegacy: "\x7f", before: onFilesTab, after: backOnPreviousTab,
		},
		{
			name: "enter opens the PR", key: libghostty.KeyEnter,
			wantCSIu: "\r", wantLegacy: "\r", before: onList, after: prOpened,
		},
		{
			name: "ctrl-m opens the PR", key: libghostty.KeyM, mods: libghostty.ModCtrl, unshifted: 'm',
			wantCSIu: "\x1b[109;5u", before: onList, after: prOpened,
		},
		{
			name: "tab moves to the next form field", key: libghostty.KeyTab,
			wantCSIu: "\t", wantLegacy: "\t", before: onFirstFormItem, after: onSecondFormItem,
		},
		{
			name: "ctrl-i moves to the next form field", key: libghostty.KeyI, mods: libghostty.ModCtrl, unshifted: 'i',
			wantCSIu: "\x1b[105;5u", before: onFirstFormItem, after: onSecondFormItem,
		},
		{
			name: "esc closes the form", key: libghostty.KeyEscape,
			wantCSIu: "\x1b[27u", wantLegacy: "\x1b", before: onFirstFormItem, after: formClosed,
		},
		{
			name: "ctrl-[ closes the form", key: libghostty.KeyBracketLeft, mods: libghostty.ModCtrl, unshifted: '[',
			wantCSIu: "\x1b[91;5u", before: onFirstFormItem, after: formClosed,
		},
		{
			name: "ctrl-h deletes in a text field", key: libghostty.KeyH, mods: libghostty.ModCtrl, unshifted: 'h',
			wantCSIu: "\x1b[104;5u", wantLegacy: "\x08", before: onTitleHoldingAB, after: lastCharDeleted,
		},
		{
			name: "backspace deletes in a text field", key: libghostty.KeyBackspace,
			wantCSIu: "\x7f", wantLegacy: "\x7f", before: onTitleHoldingAB, after: lastCharDeleted,
		},
		{
			name: "enter presses the focused button", key: libghostty.KeyEnter,
			wantCSIu: "\r", wantLegacy: "\r", before: onCancelButton, after: formClosed,
		},
		{
			name: "ctrl-m presses the focused button", key: libghostty.KeyM, mods: libghostty.ModCtrl, unshifted: 'm',
			wantCSIu: "\x1b[109;5u", before: onCancelButton, after: formClosed,
		},
	}

	for _, tc := range tests {
		for _, mode := range []struct {
			name   string
			legacy bool
			want   string
		}{
			{"csi-u", false, tc.wantCSIu},
			{"legacy", true, tc.wantLegacy},
		} {
			if mode.want == "" {
				continue
			}
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				g := newGhosttyApp(t, testAppOpts{}, func(f *fakeGitHub) {
					f.SetViewerRepositories([]model.RepositorySummary{{Ref: fixtureRef(1).Repo}})
				})
				g.Eventually(t, func(s string) bool { return strings.Contains(s, "First PR") })

				tc.before(t, g, mode.legacy)
				sent := g.Press(t, mode.legacy, tc.key, tc.mods, "", tc.unshifted)
				if string(sent) != mode.want {
					t.Fatalf("encoded key = %q, want %q", sent, mode.want)
				}
				tc.after(t, g, mode.legacy)
			})
		}
	}
}

// TestGhosttyJapaneseTitleOccupiesTwoCellsPerCharacter checks the terminal's
// own grid: every character of a Japanese title must be a wide cell followed
// by a spacer tail, and the list border on that row must stay where it is on
// the next row, i.e. gprt's and the terminal's widths agree.
func TestGhosttyJapaneseTitleOccupiesTwoCellsPerCharacter(t *testing.T) {
	const title = "日本語タイトル"
	g := newGhosttyApp(t, testAppOpts{}, func(f *fakeGitHub) {
		f.SetResult(
			gh.BuildSearchQuery(model.SectionKindDirectReview, "", "open"),
			gh.SearchResult{Items: []model.PullRequest{fixturePR(1, title)}},
		)
	})
	g.Eventually(t, func(s string) bool { return strings.Contains(s, title) })

	titleRunes := []rune(title)
	row, first := -1, -1
	for y := uint32(0); y < ghosttyRows && row < 0; y++ {
		for x := uint16(0); x < ghosttyCols; x++ {
			if cp, _ := g.vt.cell(t, x, y); cp == titleRunes[0] {
				row, first = int(y), int(x)
				break
			}
		}
	}
	if row < 0 {
		t.Fatalf("title row not found in the grid:\n%s", g.Text())
	}

	for i, r := range titleRunes {
		x := uint16(first + 2*i)
		cp, wide := g.vt.cell(t, x, uint32(row))
		if cp != r || wide != libghostty.CellWideWide {
			t.Errorf("cell x=%d: got %q wide=%v, want %q as a wide cell", x, cp, wide, r)
		}
		if _, tail := g.vt.cell(t, x+1, uint32(row)); tail != libghostty.CellWideSpacerTail {
			t.Errorf("cell x=%d: wide=%v, want a spacer tail after %q", x+1, tail, r)
		}
	}

	lastBorder := func(y uint32) int {
		last := -1
		for x := uint16(0); x < ghosttyCols; x++ {
			if cp, _ := g.vt.cell(t, x, y); cp == '║' {
				last = int(x)
			}
		}
		return last
	}
	if got, want := lastBorder(uint32(row)), lastBorder(uint32(row)+1); got < 0 || got != want {
		t.Errorf("list border at x=%d on the title row, x=%d on the next row; want equal", got, want)
	}
}

// ctrlPunctuationCases are keys a CSI-u terminal reports as Ctrl-modified
// runes. A legacy terminal sends a control byte for the first five and
// nothing for the rest.
var ctrlPunctuationCases = []struct {
	name      string
	key       libghostty.Key
	mods      libghostty.Mods
	unshifted rune
}{
	{"ctrl-]", libghostty.KeyBracketRight, libghostty.ModCtrl, ']'},
	{"ctrl-5", libghostty.KeyDigit5, libghostty.ModCtrl, '5'},
	{"ctrl-6", libghostty.KeyDigit6, libghostty.ModCtrl, '6'},
	{"ctrl-/", libghostty.KeySlash, libghostty.ModCtrl, '/'},
	{"ctrl-space", libghostty.KeySpace, libghostty.ModCtrl, ' '},
	{"ctrl-;", libghostty.KeySemicolon, libghostty.ModCtrl, ';'},
	{"ctrl-shift-6", libghostty.KeyDigit6, libghostty.ModCtrl | libghostty.ModShift, '6'},
}

func TestGhosttyCtrlPunctuationTypesNothing(t *testing.T) {
	for _, tc := range ctrlPunctuationCases {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%v", tc.name, legacy), func(t *testing.T) {
				g := newGhosttyApp(t, testAppOpts{}, func(f *fakeGitHub) {
					f.SetViewerRepositories([]model.RepositorySummary{{Ref: fixtureRef(1).Repo}})
				})
				g.Eventually(t, func(s string) bool { return strings.Contains(s, "First PR") })
				openCreateForm(t, g, legacy)
				for item := 1; item <= 3; item++ {
					g.Press(t, legacy, libghostty.KeyTab, 0, "", 0)
					waitForFormItem(t, g, item)
				}

				g.Press(t, legacy, tc.key, tc.mods, "", tc.unshifted)
				g.Type(t, legacy, "z")

				var text string
				waitFor(t, g.app.app, func() bool {
					text = g.app.createForm.GetFormItem(3).(*tview.InputField).GetText()
					return strings.HasSuffix(text, "z")
				})
				if text != "z" {
					t.Errorf("title field text = %q, want only %q", text, "z")
				}
			})
		}
	}
}

func TestGhosttyCtrlPunctuationBindingMatchesBothTerminals(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			g := newGhosttyAppWithKeys(t, map[string]string{"global.help": "<C-]>"}, testAppOpts{}, nil)
			g.Eventually(t, func(s string) bool { return strings.Contains(s, "First PR") })

			g.Press(t, legacy, libghostty.KeyBracketRight, libghostty.ModCtrl, "", ']')

			waitFor(t, g.app.app, func() bool { return g.app.overlay == "help" })
		})
	}
}
