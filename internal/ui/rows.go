package ui

import (
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"

	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// group is one section's rows as store.Rows returns them, gathered so a
// section header can be built with its final item count in one pass.
type group struct {
	section model.Section
	state   store.SectionState
	items   []store.Row // RowItem entries only
}

// buildRows turns the Store's current Rows into widget.ListRows, and
// rebuilds a.rowIndex (used by the preview debounce and the LoadMore
// trigger) to match.
func (a *App) buildRows() []widget.ListRow {
	storeRows := a.deps.Store.Rows()
	stateFor := sectionStateIndex(a.deps.Store.SectionStates())

	var groups []group
	for _, r := range storeRows {
		switch r.Kind {
		case store.RowHeader:
			groups = append(groups, group{section: r.Section, state: stateFor[r.Section]})
		case store.RowItem:
			last := len(groups) - 1
			if last < 0 {
				// Store.Rows always emits a section header before its
				// items; an orphan item would be a store bug, so it is
				// skipped rather than allowed to panic the draw path.
				a.deps.Logger.Error("list row without a section header", "pr", r.Item.PR.Ref.Key())
				continue
			}
			groups[last].items = append(groups[last].items, r)
		}
	}

	viewer := a.deps.Store.Viewer().Login
	sections := a.deps.Store.Sections()
	sectionIndex := make(map[model.Section]int, len(sections))
	for i, s := range sections {
		sectionIndex[s] = i
	}

	rows := make([]widget.ListRow, 0, len(storeRows))
	rowIndex := make(map[string]*previewState, len(storeRows))
	for _, g := range groups {
		rows = append(rows, headerListRow(g.section, g.state, len(g.items), a.deps.Icons))
		for i, r := range g.items {
			id := r.Item.PR.Ref.Key()
			rows = append(rows, itemListRow(r, viewer, a.deps.Icons))
			rowIndex[id] = &previewState{
				item:          r.Item,
				sectionIndex:  sectionIndex[g.section],
				lastInSection: i == len(g.items)-1,
			}
		}
	}
	a.rowIndex = rowIndex
	return rows
}

// sectionStateIndex indexes states by their Section, for O(1) lookup while
// building header rows.
func sectionStateIndex(states []store.SectionState) map[model.Section]store.SectionState {
	out := make(map[model.Section]store.SectionState, len(states))
	for _, s := range states {
		out[s.Section] = s
	}
	return out
}

// headerListRow renders one section heading: the section marker, its name,
// the (post-filter) item count, and stale/loading/warning/error indicators.
func headerListRow(sec model.Section, state store.SectionState, count int, icons theme.Icons) widget.ListRow {
	style := theme.Header
	if state.Stale {
		style = theme.Stale
	}
	spans := []widget.Span{
		{Text: fmt.Sprintf("%s %s (%d)", icons.SectionMarker, sec.Name, count), Style: style},
	}
	if state.Loading {
		spans = append(spans, widget.Span{Text: " " + icons.Loading, Style: theme.Muted})
	}
	if len(state.Warnings) > 0 {
		spans = append(spans, widget.Span{
			Text:  fmt.Sprintf(" ⚠ %d", len(state.Warnings)),
			Style: theme.Warning,
		})
	}
	if state.Err != nil {
		spans = append(spans, widget.Span{Text: " !", Style: theme.Error})
	}
	return widget.ListRow{
		ID:    "section:" + sec.Name,
		Lines: [][]widget.Span{spans},
	}
}

// itemListRow renders one pull request as two lines:
//
//	<rollup icon> <state icon> #123 Title
//	  owner/repo · author · ⇢ reviewer reviewer
func itemListRow(row store.Row, viewerLogin string, icons theme.Icons) widget.ListRow {
	pr := row.Item.PR

	titleStyle := theme.Base
	if row.Stale {
		titleStyle = theme.Stale
	}

	line1 := []widget.Span{
		{Text: rollupIcon(pr.RollupState, icons) + " ", Style: theme.RollupStyle(pr.RollupState)},
		{Text: stateIcon(pr, icons) + " ", Style: titleStyle},
		{Text: fmt.Sprintf("#%d %s", pr.Ref.Number, pr.Title), Style: titleStyle},
	}

	line2 := []widget.Span{
		{Text: "  ", Style: theme.Base},
		{Text: pr.Ref.Repo.NameWithOwner(), Style: tcell.StyleDefault.Foreground(theme.ColorFor(pr.Ref.Repo.NameWithOwner()))},
		{Text: " · ", Style: theme.Muted},
		{Text: pr.Author.Login, Style: authorStyle(pr.Author.Login, viewerLogin)},
	}
	if reviewers := reviewerSpans(pr, viewerLogin); len(reviewers) > 0 {
		line2 = append(line2, widget.Span{Text: " · ⇢ ", Style: theme.Muted})
		line2 = append(line2, reviewers...)
	}

	return widget.ListRow{
		ID:         pr.Ref.Key(),
		Selectable: true,
		Lines:      [][]widget.Span{line1, line2},
	}
}

func rollupIcon(state model.StatusState, icons theme.Icons) string {
	switch state {
	case model.StatusStateSuccess:
		return icons.RollupSuccess
	case model.StatusStateFailure, model.StatusStateError:
		return icons.RollupFailure
	case model.StatusStatePending, model.StatusStateExpected:
		return icons.RollupPending
	default:
		return icons.RollupNone
	}
}

func stateIcon(pr *model.PullRequest, icons theme.Icons) string {
	switch {
	case pr.IsDraft:
		return icons.Draft
	case pr.State == model.PRStateMerged:
		return icons.Merged
	case pr.State == model.PRStateClosed:
		return icons.Closed
	default:
		return icons.Open
	}
}

func authorStyle(login, viewerLogin string) tcell.Style {
	style := tcell.StyleDefault.Foreground(theme.ColorFor(login))
	if login != "" && login == viewerLogin {
		style = style.Bold(true)
	}
	return style
}

// reviewerSpans renders every reviewer worth showing: everyone still
// requested (dimmed until they submit a review) plus everyone who already
// submitted one (coloured by its outcome), sorted by login for stable
// output. The viewer's own login is bold, as it is in the author column.
func reviewerSpans(pr *model.PullRequest, viewerLogin string) []widget.Span {
	type reviewer struct {
		login     string
		state     model.ReviewState
		requested bool
	}
	byLogin := make(map[string]*reviewer, len(pr.ReviewRequests)+len(pr.LatestReviews))
	for _, r := range pr.ReviewRequests {
		byLogin[r.Login] = &reviewer{login: r.Login, requested: true}
	}
	for _, rv := range pr.LatestReviews {
		if existing, ok := byLogin[rv.Author.Login]; ok {
			existing.state = rv.State
		} else {
			byLogin[rv.Author.Login] = &reviewer{login: rv.Author.Login, state: rv.State}
		}
	}
	if len(byLogin) == 0 {
		return nil
	}

	logins := make([]string, 0, len(byLogin))
	for login := range byLogin {
		logins = append(logins, login)
	}
	sort.Strings(logins)

	spans := make([]widget.Span, 0, len(logins))
	for i, login := range logins {
		r := byLogin[login]
		if i > 0 {
			spans = append(spans, widget.Span{Text: " ", Style: theme.Muted})
		}
		style := theme.ReviewerStyle(r.state, r.requested)
		if login != "" && login == viewerLogin {
			style = style.Bold(true)
		}
		spans = append(spans, widget.Span{Text: login, Style: style})
	}
	return spans
}
