// createform.go implements the "n" (list.new_pr, bound only in ContextList)
// create-pull-request form: a bordered dialog (createFormLayout) with a
// tview.Form (createForm) — a repository picker (fuzzy-autocompleted over
// Store.ViewerRepositories(), an exact "owner/name" not in that list also
// accepted), head/base branch fields (debounced Store.SearchBranches
// autocomplete, mirroring the edit-PR form's own base field), a title
// field, an "Edit body" button opening the vim composer
// (composerKindNewPRBody, composer.go) directly inside the form's own body
// slot (createFormBodySlot), a "Reviewers" button reusing the edit-PR
// form's own stacked overlay (editreviewers.go), and a Draft checkbox — on
// the left, and a live diff preview between the chosen head and base
// branches (createFormPreview, scheduleCreateFormPreview) on the right. Esc
// asks to discard first only once something has been entered
// (createFormComputeDirty); Ctrl-s validates locally and calls
// Store.CreatePullRequest.
//
// Unlike every other overlay in this package, the create-PR form is not
// bound to Store.CurrentRef() at all: it can be opened with no pull
// request current, and an unrelated pull request switch while it is open
// must never close it — so, deliberately, there is no
// closeCreateFormIfWrongPR wired into EventPRChanged.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/sahilm/fuzzy"

	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// createFormBranchDebounce is how long createFormHeadAutocomplete/
// createFormBaseAutocomplete wait after the last keystroke before calling
// Store.SearchBranches — a package var (not a const) so tests can shorten
// it, mirroring editFormBranchDebounce's own test-only override.
var createFormBranchDebounce = 300 * time.Millisecond

// createFormReviewersButtonIndex is the create form's "Reviewers" button's
// fixed index (buildCreateForm adds it second, right after "Edit body").
const createFormReviewersButtonIndex = 1

// createFormPending tracks submitCreateForm's own single, unscoped
// CreatePullRequest mutation from enqueue to its EventMutationChanged
// resolution: created is set once EventPullRequestCreated actually fires
// (onPullRequestCreated) — which, for a reviewer-request failure, happens
// strictly before Store.MutationError() is set at all (pr_create.go's own
// apply closure emits EventPullRequestCreated first, then conditionally
// sets mutationErr — both synchronously, inside the same call). So
// onCreateFormMutationChanged, reacting to the EventMutationChanged that
// follows both, is the first point MutationError() can be read to build a
// single, combined "created #N; requesting reviewers failed: …" toast,
// rather than either missing the reviewer failure entirely or losing the
// "created" context to a plain, generic error toast.
type createFormPending struct {
	created bool
	ref     model.PRRef
}

// openCreatePRForm implements "n" (list.new_pr): refused (a no-op) while
// another overlay is already open, matching openEditPRForm's/
// openReactionPicker's own rule. Loads the viewer's own repositories
// (EnsureViewerRepositories) for the repository field's own autocomplete.
func (a *App) openCreatePRForm() {
	if a.overlay != "" {
		return
	}
	a.createFormReturnFocus = a.app.GetFocus()
	a.deps.Store.EnsureViewerRepositories()
	a.buildCreateForm()
}

// createFormFieldsHeight is the fixed row count buildCreateForm's own left
// column gives createForm (its border/title removed — createFormLayout's
// own carries both instead), mirroring tview.Form.Draw's own vertical
// layout arithmetic exactly for its 5 single-line items (item height 1 +
// itemPadding 1, advanced once per item — the form's own default,
// vertical, non-horizontal layout) plus one single-line buttons row drawn
// immediately after the last item with no extra blank line (itemPadding !=
// 0): 5*2 items rows + 1 buttons row. This assumes the buttons row itself
// never wraps, which buildCreateForm's own 2:1 left:right column split is
// sized wide enough for at any reasonably sized terminal — tview.Form's own
// vertical layout does not wrap an overflowing buttons row to a second
// line at all, it simply stops drawing whatever no longer fits, so this
// would otherwise silently hide "Cancel" rather than merely misalign it.
const createFormFieldsHeight = 2*5 + 1

// createFormPreviewIncompleteHint/createFormPreviewLoadingHint are
// createFormPreview's own placeholder text while there is nothing to
// compare yet, or a comparison is in flight — see scheduleCreateFormPreview.
const (
	createFormPreviewIncompleteHint = "(choose repository, head and base to preview)"
	createFormPreviewLoadingHint    = "(loading…)"
)

// buildCreateForm constructs and shows the form/body/preview layout itself
// (createFormLayout): a bordered two-column Flex — left, a Flex(rows) of
// createForm over createFormBodySlot (createFormBodyView, a read-only
// preview of createFormBody, until "Edit body" swaps in the vim composer's
// own Flex in its place, composerHost/openComposer/closeComposer,
// composer.go); right, createFormPreview, the diff preview
// (scheduleCreateFormPreview). createForm itself, not createFormLayout,
// keeps its own border/title removed — createFormLayout's own single
// border/title, " Create pull request ", covers the whole dialog instead.
func (a *App) buildCreateForm() {
	a.overlay = "createform"

	repoField := tview.NewInputField().SetLabel("Repository")
	wireFormAutocomplete(repoField, &a.createFormRepoSuggesting, a.createFormRepoAutocomplete)
	repoField.SetChangedFunc(a.onCreateFormRepoChanged)

	head := tview.NewInputField().SetLabel("Head branch")
	wireFormAutocomplete(head, &a.createFormHeadSuggesting, a.createFormHeadAutocomplete)
	head.SetChangedFunc(a.onCreateFormBranchFieldChanged)
	base := tview.NewInputField().SetLabel("Base branch")
	wireFormAutocomplete(base, &a.createFormBaseSuggesting, a.createFormBaseAutocomplete)
	base.SetChangedFunc(a.onCreateFormBranchFieldChanged)
	title := tview.NewInputField().SetLabel("Title")
	draft := tview.NewCheckbox().SetLabel("Draft")

	form := tview.NewForm()
	form.AddFormItem(repoField)
	form.AddFormItem(head)
	form.AddFormItem(base)
	form.AddFormItem(title)
	form.AddFormItem(draft)
	// "Edit body" (index 0) and "Reviewers (N selected)" (index 1,
	// createFormReviewersButtonIndex) must stay first, in this order:
	// closeEditReviewersOverlay (editreviewers.go) updates the latter by
	// that fixed index.
	form.AddButton("Edit body", a.openCreatePRBodyComposer)
	form.AddButton(a.createFormReviewersButtonText(), a.openCreateReviewersOverlay)
	form.AddButton("Create", a.submitCreateForm)
	form.AddButton("Cancel", a.cancelCreateForm)

	bodyView := tview.NewTextView().SetDynamicColors(true)
	bodyView.SetBorder(true).SetTitle(" Body ")
	bodySlot := tview.NewFlex().SetDirection(tview.FlexRow)
	bodySlot.AddItem(bodyView, 0, 1, false)

	left := tview.NewFlex().SetDirection(tview.FlexRow)
	left.AddItem(form, createFormFieldsHeight, 0, true)
	left.AddItem(bodySlot, 0, 1, false)

	preview := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	preview.SetBorder(true).SetTitle(" diff (base...head) ")
	preview.SetText(createFormPreviewIncompleteHint)

	layout := tview.NewFlex().SetDirection(tview.FlexColumn)
	layout.AddItem(left, 0, 2, true)
	layout.AddItem(preview, 0, 1, false)
	layout.SetBorder(true).SetTitle(" Create pull request ")

	a.createForm = form
	a.createFormLayout = layout
	a.createFormBodySlot = bodySlot
	a.createFormBodyView = bodyView
	a.createFormPreview = preview
	a.createFormRepoField = repoField
	a.createFormHeadField = head
	a.createFormBaseField = base
	a.createFormTitleField = title
	a.createFormDraftBox = draft
	a.createFormSelectedReviewers = map[string]model.Reviewer{}

	a.refreshCreateFormBodyView()

	a.root.AddPage("createform", layout, true, true)
	a.app.SetFocus(form)
}

// createFormReviewersButtonText renders the "Reviewers" button's own
// "(N selected)" label, mirroring editFormReviewersButtonText.
func (a *App) createFormReviewersButtonText() string {
	return fmt.Sprintf("Reviewers (%d selected)", len(a.createFormSelectedReviewers))
}

// createFormRepoAutocomplete is createFormRepoField's own SetAutocompleteFunc
// callback: a fuzzy match (github.com/sahilm/fuzzy) of the typed text
// against Store.ViewerRepositories()'s own nameWithOwner (an empty text
// returns every repository, matching mentionCandidates' own "empty prefix"
// convention), or a single "(loading repositories…)" placeholder row while
// that list has not resolved yet (EnsureViewerRepositories, called when
// the form opens).
func (a *App) createFormRepoAutocomplete(text string) []string {
	repos := a.deps.Store.ViewerRepositories()
	if repos == nil {
		return []string{"(loading repositories…)"}
	}
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Ref.NameWithOwner()
	}
	if text == "" {
		return names
	}
	matches := fuzzy.Find(text, names)
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = names[m.Index]
	}
	return out
}

// createFormParseRepo parses text as "owner/name" (model.RepoRef.NameWithOwner's
// own inverse): both parts must be non-empty, and name must not itself
// contain another "/", so "a/b/c" is rejected as ambiguous rather than
// silently parsed as owner "a", name "b/c".
func createFormParseRepo(text string) (owner, name string, ok bool) {
	owner, name, found := strings.Cut(text, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// findViewerRepository returns repos' entry matching ref by RepoRef, if
// any — used by submitCreateForm to detect an archived repository
// (model.RepositoryInfo, unlike model.RepositorySummary, carries no
// IsArchived field of its own).
func findViewerRepository(repos []model.RepositorySummary, ref model.RepoRef) (model.RepositorySummary, bool) {
	for _, r := range repos {
		if r.Ref == ref {
			return r, true
		}
	}
	return model.RepositorySummary{}, false
}

// findViewerRepositoryByName is findViewerRepository's own lookup by
// "owner/name" alone, before any Host is known for a repository the viewer
// typed by hand: returns the first ViewerRepositories() entry whose own
// owner/name matches, so its already-resolved Host is reused instead of
// guessed.
func findViewerRepositoryByName(repos []model.RepositorySummary, owner, name string) (model.RepositorySummary, bool) {
	for _, r := range repos {
		if r.Ref.Owner == owner && r.Ref.Name == name {
			return r, true
		}
	}
	return model.RepositorySummary{}, false
}

// onCreateFormRepoChanged is createFormRepoField's own SetChangedFunc
// callback — fires both for a real keystroke and for choosing a suggestion
// from the autocomplete dropdown, since selecting one also goes through
// InputField.SetText (see its own doc comment: "Calling this function will
// also trigger a changed event"). Parses the field's current text as
// "owner/name" and, once it is one — chosen, not merely typed, so an
// in-progress, incomplete string leaves createFormRepoChosen false —
// resolves it against Store.ViewerRepositories() (reusing that entry's own
// Host) or, for an exact "owner/name" not found there, builds a RepoRef
// from Store.Host() directly ("typing an exact owner/name not in the list
// is also accepted"). Switching to a genuinely *different* repository
// (already having one chosen before) clears every selection scoped to the
// old one — reviewers, the Head/Base fields and their own autocomplete
// caches — a real bug found in review: none of these were cleared before,
// so a repository switch silently carried the previous repository's own
// branch names/reviewers into the new one's submission. Loads the chosen
// repository's metadata/mentionable users; applyCreateFormRepositoryInfoIfResolved
// then preselects the Base branch/prefills the body once RepositoryInfo/
// Templates resolve — here, synchronously, if a repository visited earlier
// this session is already fresh.
func (a *App) onCreateFormRepoChanged(text string) {
	owner, name, ok := createFormParseRepo(text)
	if !ok {
		a.createFormRepoChosen = false
		a.scheduleCreateFormPreview()
		return
	}
	repos := a.deps.Store.ViewerRepositories()
	ref := model.RepoRef{Host: a.deps.Store.Host(), Owner: owner, Name: name}
	if summary, found := findViewerRepositoryByName(repos, owner, name); found {
		ref = summary.Ref
	}
	if a.createFormRepoChosen && ref == a.createFormRepo {
		return
	}
	switchedRepo := a.createFormRepoChosen // false only for the very first choice
	if switchedRepo {
		// The body draft is keyed by repository, so the previous
		// repository's draft would otherwise outlive this form: closeCreateForm
		// only deletes the draft of whichever repository is chosen at close
		// time, and the "p" pending list never lists KindNewPR drafts.
		a.deleteCreateFormBodyDraft(a.createFormBodyDraftKey())
	}
	a.createFormRepo = ref
	a.createFormRepoChosen = true
	a.createFormBaseApplied = false
	a.createFormBodyApplied = false
	if switchedRepo {
		a.createFormSelectedReviewers = map[string]model.Reviewer{}
		if a.createForm != nil {
			a.createForm.GetButton(createFormReviewersButtonIndex).SetLabel(a.createFormReviewersButtonText())
		}
		a.createFormHeadField.SetText("")
		a.createFormBaseField.SetText("")
		a.createFormHeadSuggestions = nil
		a.createFormHeadLastQuery = ""
		a.createFormBaseSuggestions = nil
		a.createFormBaseLastQuery = ""
	}
	a.deps.Store.EnsureRepositoryMetadata(ref)
	a.deps.Store.EnsureMentionableUsers(ref)
	a.applyCreateFormRepositoryInfoIfResolved(ref)
	a.scheduleCreateFormPreview()
}

// applyCreateFormRepositoryInfoIfResolved preselects the Base branch with
// repo's default branch, and prefills the body with its first pull request
// template (only if the body is still empty), the first time repo's
// RepositoryInfo/Templates resolve after being chosen —
// createFormBaseApplied/createFormBodyApplied each latch true so a later,
// unrelated metadata refresh for the same repository never re-applies
// either over something the user has since edited themselves. A no-op
// unless the create form is open, repo is the currently chosen repository,
// and EnsureRepositoryMetadata has actually resolved it yet.
func (a *App) applyCreateFormRepositoryInfoIfResolved(repo model.RepoRef) {
	if a.createForm == nil || !a.createFormRepoChosen || repo != a.createFormRepo {
		return
	}
	info, ok := a.deps.Store.RepositoryInfo(repo)
	if !ok {
		return
	}
	if !a.createFormBaseApplied {
		a.createFormBaseApplied = true
		a.createFormBaseField.SetText(info.DefaultBranch)
	}
	if !a.createFormBodyApplied {
		a.createFormBodyApplied = true
		if templates, ok := a.deps.Store.Templates(repo); ok && len(templates) > 0 && a.createFormBody == "" {
			a.createFormBody = templates[0].Body
		}
	}
}

// createFormPreviewDebounce is how long scheduleCreateFormPreview waits
// after the last repository/head/base change before calling
// Store.CompareBranches — a package var (not a const) so tests can shorten
// it, mirroring createFormBranchDebounce.
var createFormPreviewDebounce = 300 * time.Millisecond

// onCreateFormBranchFieldChanged is the Head/Base fields' own shared
// SetChangedFunc callback, alongside onCreateFormRepoChanged: any of the
// three changing (a real keystroke, or an autocomplete selection — see
// wireFormAutocomplete's own doc comment for why that also fires this)
// reschedules the diff preview.
func (a *App) onCreateFormBranchFieldChanged(string) {
	a.scheduleCreateFormPreview()
}

// scheduleCreateFormPreview (re)starts the create-PR form's own debounced
// diff-preview refresh, called whenever the repository, head, or base field
// changes. Running Store.CompareBranches — a real network round trip,
// unlike the repository field's own purely local ViewerRepositories()
// filtering — once per keystroke would be wasteful, so this debounces it
// exactly like createFormHeadAutocomplete debounces Store.SearchBranches.
// Shows createFormPreviewIncompleteHint immediately, with no debounce at
// all, whenever the repository is not yet chosen or either branch field is
// still blank: there is nothing to compare yet, so there is no fetch to
// debounce in the first place.
func (a *App) scheduleCreateFormPreview() {
	if a.createFormPreviewTimer != nil {
		a.createFormPreviewTimer.Stop()
		a.createFormPreviewTimer = nil
	}
	if a.createForm == nil {
		return
	}
	repo := a.createFormRepo
	head := strings.TrimSpace(a.createFormHeadField.GetText())
	base := strings.TrimSpace(a.createFormBaseField.GetText())
	if !a.createFormRepoChosen || head == "" || base == "" {
		a.createFormPreview.SetText(createFormPreviewIncompleteHint)
		return
	}
	a.createFormPreview.SetText(createFormPreviewLoadingHint)
	a.createFormPreviewTimer = time.AfterFunc(createFormPreviewDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.deps.Store.CompareBranches(repo, base, head, func(res gh.CompareResult, err error) {
				a.applyCreateFormPreview(repo, base, head, res, err)
			})
		})
	})
}

// applyCreateFormPreview renders the CompareBranches result triggered for
// (repo, base, head) into createFormPreview — dropped silently when the
// create form has since closed, or the repository/head/base no longer
// match what this particular call was made for: Store.CompareBranches' own
// generation token only ever drops a result superseded by a *later*
// CompareBranches call, not one still in flight when the user keeps typing
// before scheduleCreateFormPreview's own debounce next fires, so this
// re-checks the triple itself rather than relying on that alone.
func (a *App) applyCreateFormPreview(repo model.RepoRef, base, head string, res gh.CompareResult, err error) {
	if a.createForm == nil || !a.createFormRepoChosen || a.createFormRepo != repo {
		return
	}
	if strings.TrimSpace(a.createFormHeadField.GetText()) != head || strings.TrimSpace(a.createFormBaseField.GetText()) != base {
		return
	}
	if err != nil {
		a.createFormPreview.SetText(tview.Escape(err.Error()))
		return
	}
	a.createFormPreview.SetText(renderCompareResult(res))
}

// styleColorTag returns style's own foreground colour's tcell name (falling
// back to its "#rrggbb" CSS hex form for a colour with no W3C name) — the
// exact string a tview dynamic-colour tag ("[name]") needs, since tview
// feeds a tag's contents straight into tcell.GetColor, which accepts both
// forms.
func styleColorTag(style tcell.Style) string {
	fg, _, _ := style.Decompose()
	return fg.Name(true)
}

// renderCompareResult renders res as createFormPreview's own dynamic-colour
// text: one "path  +A -D" header line per file (compareFileHeader spells
// out a renamed/removed file's own status too), followed by its patch —
// "+"/"-" lines and "@@" hunk headers coloured via theme.Success/Error/Info
// respectively, every other line (context, or the patch itself entirely
// absent for a binary/too-large file) left plain. Every line is
// tview.Escape'd: a real diff's own content can contain literal "["
// characters, which dynamic colours would otherwise misparse as the start
// of a tag.
func renderCompareResult(res gh.CompareResult) string {
	if len(res.Files) == 0 {
		return "(no differences)"
	}
	addTag := styleColorTag(theme.Success)
	delTag := styleColorTag(theme.Error)
	hunkTag := styleColorTag(theme.Info)

	var b strings.Builder
	for i, f := range res.Files {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s  [%s]+%d[-] [%s]-%d[-]\n", tview.Escape(compareFileHeader(f)), addTag, f.Additions, delTag, f.Deletions)
		if !f.HasPatch {
			b.WriteString("(binary or too large to display)\n")
			continue
		}
		for _, line := range strings.Split(f.Patch, "\n") {
			escaped := tview.Escape(line)
			switch {
			case strings.HasPrefix(line, "@@"):
				fmt.Fprintf(&b, "[%s]%s[-]\n", hunkTag, escaped)
			case strings.HasPrefix(line, "+"):
				fmt.Fprintf(&b, "[%s]%s[-]\n", addTag, escaped)
			case strings.HasPrefix(line, "-"):
				fmt.Fprintf(&b, "[%s]%s[-]\n", delTag, escaped)
			default:
				b.WriteString(escaped)
				b.WriteByte('\n')
			}
		}
	}
	if res.Truncated {
		b.WriteString("\n(more files changed than shown; GitHub's own compare endpoint truncated this response)\n")
	}
	return b.String()
}

// compareFileHeader renders one changed file's own path for
// renderCompareResult's header line: a renamed file spells out both sides,
// a removed file is marked as such — every other status (added, modified,
// …) is unambiguous from its own +/- counts alone.
func compareFileHeader(f model.ChangedFile) string {
	switch f.Status {
	case model.FileStatusRenamed:
		return f.PreviousPath + " -> " + f.Path + " (renamed)"
	case model.FileStatusRemoved:
		return f.Path + " (removed)"
	default:
		return f.Path
	}
}

// createFormHeadAutocomplete is createFormHeadField's own SetAutocompleteFunc
// callback, mirroring editFormBranchAutocomplete's shape (see its own doc
// comment for the lastQuery guard against InputField.Autocomplete()'s own
// self-recursion) against the create form's own chosen repository
// (createFormRepo) instead of an existing pull request's.
func (a *App) createFormHeadAutocomplete(text string) []string {
	if text == a.createFormHeadLastQuery {
		return a.createFormHeadSuggestions
	}
	if a.createFormHeadTimer != nil {
		a.createFormHeadTimer.Stop()
	}
	repo := a.createFormRepo
	a.createFormHeadTimer = time.AfterFunc(createFormBranchDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.deps.Store.SearchBranches(repo, text, func(branches []model.Branch, err error) {
				if err != nil || a.createFormHeadField == nil {
					return
				}
				names := make([]string, len(branches))
				for i, b := range branches {
					names[i] = b.Name
				}
				a.createFormHeadSuggestions = names
				a.createFormHeadLastQuery = text
				a.createFormHeadField.Autocomplete()
			})
		})
	})
	return a.createFormHeadSuggestions
}

// createFormBaseAutocomplete mirrors createFormHeadAutocomplete for the
// base-branch field.
func (a *App) createFormBaseAutocomplete(text string) []string {
	if text == a.createFormBaseLastQuery {
		return a.createFormBaseSuggestions
	}
	if a.createFormBaseTimer != nil {
		a.createFormBaseTimer.Stop()
	}
	repo := a.createFormRepo
	a.createFormBaseTimer = time.AfterFunc(createFormBranchDebounce, func() {
		a.app.QueueUpdateDraw(func() {
			a.deps.Store.SearchBranches(repo, text, func(branches []model.Branch, err error) {
				if err != nil || a.createFormBaseField == nil {
					return
				}
				names := make([]string, len(branches))
				for i, b := range branches {
					names[i] = b.Name
				}
				a.createFormBaseSuggestions = names
				a.createFormBaseLastQuery = text
				a.createFormBaseField.Autocomplete()
			})
		})
	})
	return a.createFormBaseSuggestions
}

// createFormBodyDraftKey returns the create-PR form's own body draft key,
// scoped to the currently chosen repository (drafts.KindNewPR — see its
// own doc comment for why a sentinel "#0" pull request number can never
// collide with a real pull request's own draft).
func (a *App) createFormBodyDraftKey() drafts.Key {
	ref := model.PRRef{Repo: a.createFormRepo, Number: 0}
	return drafts.Key{PR: ref.Key(), Kind: drafts.KindNewPR, Anchor: "body"}
}

// deleteCreateFormBodyDraft removes the body draft stored under key, the
// create-PR form's only cleanup path for it (the "p" pending list never
// lists KindNewPR drafts). A missing draft — the common case: the body
// composer was never opened, or the draft was already deleted on a
// successful creation — is not an error.
func (a *App) deleteCreateFormBodyDraft(key drafts.Key) {
	if a.deps.Drafts == nil {
		return
	}
	if err := a.deps.Drafts.Delete(key); err != nil {
		a.showErrorToast("draft delete failed: " + err.Error())
	}
}

// openCreatePRBodyComposer opens the "Edit body" composer
// (composerKindNewPRBody) directly inside the create-PR form's own dialog:
// openComposer's own host-swap (composerHost, composer.go) mounts the
// composer's Flex in the form's body slot (createFormBodySlot) in place of
// its read-only body view, rather than removing the form's own root Pages
// page the way an earlier revision of this did — both the form's other
// fields (repository/head/base/title/draft) and its diff preview stay
// visible, and live, the whole time the body is being edited.
// composerReturnFocus is cleared right after opening so Ctrl-w j/k (which
// would otherwise move focus back to whatever field had it before "Edit
// body") is a no-op instead: the only way out of this composer is its own
// :w/Ctrl-s/:q/:q!.
func (a *App) openCreatePRBodyComposer() {
	if a.createForm == nil {
		return
	}
	if !a.createFormRepoChosen {
		// The body draft is keyed by repository: without one chosen, the
		// draft would be filed under a zero-value key that no later form
		// could ever find again (and that a bare Esc would silently
		// delete), so the body waits for a repository like the reviewers
		// overlay does.
		a.showToast("choose a repository first", theme.Warning)
		return
	}
	a.openComposer(composerTarget{
		kind:     composerKindNewPRBody,
		ref:      model.PRRef{Repo: a.createFormRepo, Number: 0},
		title:    "New pull request body",
		draftKey: a.createFormBodyDraftKey(),
	}, a.createFormBody)
	a.composerReturnFocus = nil
}

// refreshCreateFormBodyView updates the create-PR form's own read-only body
// view (createFormBodySlot's default content, swapped out for the body
// composer's own Flex while "Edit body" is open) from the current
// createFormBody: a dim placeholder while it is still empty, the body text
// itself otherwise (tview.Escape'd, since createFormBodyView has dynamic
// colours on for the placeholder).
func (a *App) refreshCreateFormBodyView() {
	if a.createFormBody == "" {
		a.createFormBodyView.SetText(fmt.Sprintf("[%s](empty — press Edit body)[-]", styleColorTag(theme.Muted)))
		return
	}
	a.createFormBodyView.SetText(tview.Escape(a.createFormBody))
}

// restoreCreateFormBodySlot re-adds the create-PR form's own read-only body
// view to its body slot in place of the composer's own Flex (already
// removed by closeComposer, composer.go) and focuses the form itself —
// undoing openCreatePRBodyComposer's own swap the other way. A no-op if the
// create form was somehow closed already (should not normally happen:
// nothing else can close it while its own body composer has focus).
func (a *App) restoreCreateFormBodySlot() {
	if a.createForm == nil {
		return
	}
	a.refreshCreateFormBodyView()
	a.createFormBodySlot.AddItem(a.createFormBodyView, 0, 1, false)
	a.app.SetFocus(a.createForm)
}

// openCreateReviewersOverlay opens the "editreviewers" overlay for the
// create-PR form (editreviewers.go's shared implementation), refused with
// a toast when no repository has been chosen yet — there would be nothing
// to search mentionable users/teams against.
func (a *App) openCreateReviewersOverlay() {
	if !a.createFormRepoChosen {
		a.showToast("choose a repository first", theme.Warning)
		return
	}
	a.openReviewersOverlay("createform", a.createFormRepo, a.createFormSelectedReviewers)
}

// createFormComputeDirty reports whether the user has entered anything at
// all into the form yet — used by cancelCreateForm's own Esc dirty check.
// Unlike editFormComputeDiff (which diffs the live form against an
// existing pull request), the create-PR form's own "original" is simply
// nothing entered at all, so no snapshot type is needed here.
func (a *App) createFormComputeDirty() bool {
	return a.createFormRepoField.GetText() != "" ||
		a.createFormHeadField.GetText() != "" ||
		a.createFormBaseField.GetText() != "" ||
		a.createFormTitleField.GetText() != "" ||
		a.createFormBody != "" ||
		len(a.createFormSelectedReviewers) > 0 ||
		a.createFormDraftBox.IsChecked()
}

// cancelCreateForm implements Esc/the form's "Cancel" button: refused with
// a toast while the form's own body composer is open (a real bug found in
// review otherwise — see submitCreateForm's own matching guard for why),
// closes immediately when nothing has been entered, otherwise asks to
// discard via a confirm dialog first — mirroring cancelEditForm's own
// shape.
func (a *App) cancelCreateForm() {
	if a.composerTarget != nil && a.composerTarget.kind == composerKindNewPRBody {
		a.showToast("finish editing the body first", theme.Warning)
		return
	}
	if !a.createFormComputeDirty() {
		a.closeCreateForm()
		return
	}
	a.showConfirm("Discard this pull request?", "Discard", a.closeCreateForm)
}

// closeCreateForm closes the create-PR form (a no-op when it is not open),
// deletes its own body draft (found missing in review — KindNewPR drafts
// have no other cleanup path, since the "p" pending list only ever
// enumerates the *current* pull request's own drafts, never a not-yet-created
// one's), and restores focus. Unlike closeEditForm, this is never triggered
// by a pull request switch (EventPRChanged) — see the App.createForm
// field's own doc comment.
func (a *App) closeCreateForm() {
	if a.createForm == nil {
		return
	}
	bodyDraftKey := a.createFormBodyDraftKey()
	a.closeEditReviewersOverlay(false)

	if a.createFormHeadTimer != nil {
		a.createFormHeadTimer.Stop()
		a.createFormHeadTimer = nil
	}
	if a.createFormBaseTimer != nil {
		a.createFormBaseTimer.Stop()
		a.createFormBaseTimer = nil
	}
	if a.createFormPreviewTimer != nil {
		a.createFormPreviewTimer.Stop()
		a.createFormPreviewTimer = nil
	}
	a.root.RemovePage("createform")
	a.overlay = ""
	a.createForm = nil
	a.createFormLayout = nil
	a.createFormBodySlot = nil
	a.createFormBodyView = nil
	a.createFormPreview = nil
	a.createFormCtrlWPending = false
	a.createFormRepoField = nil
	a.createFormHeadField = nil
	a.createFormBaseField = nil
	a.createFormTitleField = nil
	a.createFormDraftBox = nil
	a.createFormRepo = model.RepoRef{}
	a.createFormRepoChosen = false
	a.createFormBaseApplied = false
	a.createFormBodyApplied = false
	a.createFormSelectedReviewers = nil
	a.createFormBody = ""
	a.createFormHeadSuggestions = nil
	a.createFormHeadLastQuery = ""
	a.createFormBaseSuggestions = nil
	a.createFormBaseLastQuery = ""
	a.createFormRepoSuggesting = false
	a.createFormHeadSuggesting = false
	a.createFormBaseSuggesting = false
	a.deleteCreateFormBodyDraft(bodyDraftKey)
	if a.createFormReturnFocus != nil {
		a.app.SetFocus(a.createFormReturnFocus)
		a.createFormReturnFocus = nil
	} else {
		a.focusList()
	}
}

// splitReviewerIDs splits selected into user/team ID slices by
// model.Reviewer.Kind, mirroring editFormComputeDiff's own reviewer-splitting
// loop for the edit-PR form.
func splitReviewerIDs(selected map[string]model.Reviewer) (userIDs, teamIDs []string) {
	for _, r := range selected {
		if r.Kind == model.ReviewerKindTeam {
			teamIDs = append(teamIDs, r.ID)
		} else {
			userIDs = append(userIDs, r.ID)
		}
	}
	return userIDs, teamIDs
}

// submitCreateForm implements the form's "Create" button (and Ctrl-s):
// refused with a toast while the form's own body composer is open (its
// containing "createform" root page is removed for as long as the
// composer is — see openCreatePRBodyComposer — so an unguarded Submit
// reachable at that point would act on stale field values without the
// user ever seeing the form itself; a real bug found in review, see
// composer.go's own docs for the matching Ctrl-w h/l fix that normally
// prevents focus from ever reaching here in the first place — this is the
// defensive second layer). Otherwise validates locally — a chosen,
// metadata-resolved, non-archived repository; non-empty, differing
// head/base branches (leading/trailing whitespace trimmed, another
// omission found in review); a non-blank title — before calling
// Store.CreatePullRequest. Every validation failure only ever toasts and
// returns without enqueueing anything, so the form stays open with its
// values intact either way, matching a real creation failure's own "keep
// the form open" rule (see onCreateFormMutationChanged).
func (a *App) submitCreateForm() {
	if a.composerTarget != nil && a.composerTarget.kind == composerKindNewPRBody {
		a.showToast("finish editing the body first", theme.Warning)
		return
	}
	if a.deps.Store.Mutating() {
		a.showToast("a mutation is already in progress; try again shortly", theme.Warning)
		return
	}
	if !a.createFormRepoChosen {
		a.showToast("choose a repository", theme.Warning)
		return
	}
	info, ok := a.deps.Store.RepositoryInfo(a.createFormRepo)
	if !ok {
		// A previous EnsureRepositoryMetadata attempt may simply still be
		// in flight, or may have already failed outright — either way,
		// startRepositoryMetadataFetch's own in-flight/freshness dedup
		// (internal/store/repository.go) makes calling it again here safe
		// and, for the failed case, is what actually lets Submit ever
		// resolve: without this, only reopening the form (a fresh
		// EnsureRepositoryMetadata call from openCreatePRForm) could ever
		// retry a failed fetch — a real bug found in review, since
		// re-typing the identical "owner/name" is a no-op (see
		// onCreateFormRepoChanged's own ref-unchanged short-circuit) and
		// simply pressing Create again never did anything either.
		a.deps.Store.EnsureRepositoryMetadata(a.createFormRepo)
		a.showToast("repository settings not loaded yet; retrying — try again in a moment", theme.Warning)
		return
	}
	if summary, found := findViewerRepository(a.deps.Store.ViewerRepositories(), a.createFormRepo); found && summary.IsArchived {
		a.showToast("cannot create a pull request in an archived repository", theme.Warning)
		return
	}
	head := strings.TrimSpace(a.createFormHeadField.GetText())
	base := strings.TrimSpace(a.createFormBaseField.GetText())
	if head == "" || base == "" {
		a.showToast("head and base branches are required", theme.Warning)
		return
	}
	if head == base {
		a.showToast("head and base branches must differ", theme.Warning)
		return
	}
	title := strings.TrimSpace(a.createFormTitleField.GetText())
	if title == "" {
		a.showToast("title is required", theme.Warning)
		return
	}

	userIDs, teamIDs := splitReviewerIDs(a.createFormSelectedReviewers)
	in := gh.CreatePullRequestInput{
		RepositoryID: info.ID,
		BaseRefName:  base,
		HeadRefName:  head,
		Title:        title,
		Body:         a.createFormBody,
		Draft:        a.createFormDraftBox.IsChecked(),
	}
	a.createFormPending = &createFormPending{}
	a.deps.Store.CreatePullRequest(in, userIDs, teamIDs)
}

// onPullRequestCreated reacts to store.EventPullRequestCreated for a
// submission tracked in createFormPending — nothing else in gprt creates a
// pull request, so a nil createFormPending here means this fired for a
// different reason entirely (should not normally happen; ignored
// defensively). Closes the form (which also deletes its own body draft —
// see closeCreateForm's own doc comment), opens the new pull request, and
// focuses the detail column; the outcome toast itself is left to
// onCreateFormMutationChanged, which alone knows by then whether a
// follow-up reviewer request also failed.
func (a *App) onPullRequestCreated(ref model.PRRef) {
	if a.createFormPending == nil {
		return
	}
	a.createFormPending.created = true
	a.createFormPending.ref = ref

	a.closeCreateForm()
	a.deps.Store.OpenPR(ref)
	a.focusDetail()
}

// onCreateFormMutationChanged reacts to store.EventMutationChanged for a
// submission tracked in createFormPending: !created means CreatePullRequest
// itself failed (onPullRequestCreated never ran; the form is still open,
// its values intact) and Store.MutationError() is the creation's own
// error. created means the pull request now exists; MutationError() here
// (read only now — see createFormPending's own doc comment for why it
// cannot be read any earlier) is instead a follow-up RequestReviewers
// failure, reported alongside the pull request's own number rather than
// masking that it succeeded.
func (a *App) onCreateFormMutationChanged() {
	if a.deps.Store.Mutating() || a.createFormPending == nil {
		return
	}
	pending := a.createFormPending
	a.createFormPending = nil

	err := a.deps.Store.MutationError()
	switch {
	case !pending.created:
		if err != nil {
			a.showToast("not created: "+err.Error(), theme.Error)
		}
	case err != nil:
		a.showToast(fmt.Sprintf("created #%d; requesting reviewers failed: %s", pending.ref.Number, err.Error()), theme.Error)
	default:
		a.showToast(fmt.Sprintf("created #%d", pending.ref.Number), theme.Success)
	}
}

// routeCreateFormKey handles the "createform" overlay. While the diff
// preview has focus (Ctrl-w l below), it delegates to
// routeCreateFormPreviewKey instead of everything that follows. Otherwise:
// forwards every key to the Form's own InputHandler (InputField/Checkbox/
// buttons), rewritten first by rewriteFormNavKey (formnav.go) so Up/Down
// also move between items exactly like Tab/Backtab already do — except
// while the currently focused Repository/Head/Base field's own autocomplete
// drop-down is shown
// (createFormRepoSuggesting/createFormHeadSuggesting/createFormBaseSuggesting,
// each kept in sync with tview's own, otherwise unexported, "is the
// drop-down populated" state by wrapFormAutocomplete, forced false here
// whenever its own field does not currently have focus — see
// wrapFormAutocomplete's own doc comment for why that alone is enough to
// never leave it stale), in which case Tab/Backtab instead navigate the
// drop-down's own candidates and Enter is left to tview's own native
// "select the highlighted candidate" handling. Ctrl-w l moves focus to the
// preview (routeCreateFormPreviewKey below then handles the next key on).
// Esc cancels (asking to discard when dirty) — except while a drop-down is
// shown, when a literal Escape must only close it: tview's own
// InputField.InputHandler does so directly on Escape, bypassing
// SetAutocompleteFunc entirely, so this router resets the tracked state
// itself here to stay in sync, rather than leaving it stale for the next
// routed key — and Ctrl-s submits.
func (a *App) routeCreateFormKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	if a.app.GetFocus() != a.createFormRepoField {
		a.createFormRepoSuggesting = false
	}
	if a.app.GetFocus() != a.createFormHeadField {
		a.createFormHeadSuggesting = false
	}
	if a.app.GetFocus() != a.createFormBaseField {
		a.createFormBaseSuggesting = false
	}

	if a.app.GetFocus() == a.createFormPreview {
		return a.routeCreateFormPreviewKey(ev, normalized)
	}

	suggesting := a.createFormRepoSuggesting || a.createFormHeadSuggesting || a.createFormBaseSuggesting

	for _, k := range normalized {
		if a.createFormCtrlWPending {
			a.createFormCtrlWPending = false
			if isPlainRune(k, 'l') {
				a.app.SetFocus(a.createFormPreview)
				return nil
			}
			continue
		}
		if isCtrlW(k) {
			a.createFormCtrlWPending = true
			continue
		}
		if isEscKey(k) {
			if suggesting {
				a.createFormRepoSuggesting = false
				a.createFormHeadSuggesting = false
				a.createFormBaseSuggesting = false
				return ev
			}
			a.cancelCreateForm()
			return nil
		}
		if isCtrlS(k) {
			a.submitCreateForm()
			return nil
		}
	}
	return rewriteFormNavKey(ev, suggesting)
}

// routeCreateFormPreviewKey handles the "createform" overlay while its own
// diff preview has focus (Ctrl-w l, routeCreateFormKey above): every key
// not consumed here is returned unchanged, reaching the TextView's own
// native j/k/g/G/h/l/PgUp/PgDn/arrow scrolling directly —
// rewriteFormNavKey's Up/Down-as-Tab rewriting must not apply here, unlike
// everywhere else in this overlay. Ctrl-w h and a literal Esc both return
// focus to the form instead of scrolling, or — Esc's usual meaning
// everywhere else in this overlay — cancelling it.
func (a *App) routeCreateFormPreviewKey(ev *tcell.EventKey, normalized []keys.Key) *tcell.EventKey {
	for _, k := range normalized {
		if a.createFormCtrlWPending {
			a.createFormCtrlWPending = false
			if isPlainRune(k, 'h') {
				a.app.SetFocus(a.createForm)
				return nil
			}
			continue
		}
		switch {
		case isCtrlW(k):
			a.createFormCtrlWPending = true
			continue
		case isEscKey(k):
			a.app.SetFocus(a.createForm)
			return nil
		}
	}
	return ev
}
