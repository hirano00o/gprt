// Package ui composes gprt's tview application: the root layout (PR list,
// detail column, status bar), the single input-capture router that turns
// key events into actions via internal/ui/keys, and the glue that renders
// internal/store's state and dispatches its mutating calls. Widgets
// (internal/ui/widget) never handle input themselves, and internal/store
// never imports tview: App.Dispatch is the one bridge between them.
package ui

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/rivo/tview"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/drafts"
	"github.com/hirano00o/gprt/internal/logging"
	"github.com/hirano00o/gprt/internal/model"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui/editor"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
	"github.com/hirano00o/gprt/internal/ui/widget"
)

// Deps are the App's dependencies, supplied once at construction. Store is
// built by the caller (main) before the App exists — see App.Dispatch's
// doc comment for how the two are wired together despite that ordering.
type Deps struct {
	// Store owns gprt's PR list state and talks to GitHub; App only reads
	// it and calls its mutating methods, never touching its internals.
	Store *store.Store
	// Config is the loaded, validated user configuration.
	Config config.Config
	// Keymap is the merged (defaults + user overrides) keymap the router
	// resolves every keypress against.
	Keymap keys.Keymap
	// Icons is the selected glyph set (Unicode or Nerd Font).
	Icons theme.Icons
	// Browser opens a pull request's URL in the user's browser.
	Browser *browser.Opener
	// Logger receives structured logs for recovered panics and similar
	// operational events.
	Logger *slog.Logger
	// Recent returns the most recent error-and-above log entries, shown
	// by the ":messages" command.
	Recent func() []logging.Entry
	// Version is gprt's version string, shown by the help overlay.
	Version string
	// Drafts persists in-progress composer text across PR switches,
	// reloads, and restarts. A nil Drafts disables draft persistence
	// (composer text is still usable within a single session — only
	// write-through saving, restore, and the status bar's draft count
	// are skipped).
	Drafts *drafts.Store
}

// App is gprt's tview application: the root layout, the router, and the
// glue between internal/store's events and the widgets that render them.
type App struct {
	deps Deps
	app  *tview.Application

	root      *tview.Pages
	statusBar *statusBarView

	listView    *widget.ListView
	filterInput *tview.InputField
	listFlex    *tview.Flex

	detailColumn *tview.Flex
	tabBar       *tabBarView
	detailPages  *tview.Pages
	prView       *widget.DetailView
	currentTab   string

	// Files tab: a tree of changed files (left) and the diff of whichever
	// file is selected (right) — see files.go.
	filesFlex    *tview.Flex
	treeView     *tview.TreeView
	diffView     *widget.DiffView
	treeExpanded bool
	// currentFilePath is the path of the file currently shown in diffView,
	// or "" when nothing has been opened yet (no pull request open, or its
	// files have not loaded any entries yet).
	currentFilePath string
	// wantedFilePath is the path the user most recently asked to open
	// (openFile), kept separate from currentFilePath: when rebuildFileTree
	// cannot find it among the currently loaded files (its own page has
	// not arrived, or re-arrived after a reload, yet) it falls back to
	// some other node so the tree is never left with none at all — see
	// onTreeNodeChanged and lastFallbackNode — without losing track of
	// what to restore once the wanted file's data does arrive.
	wantedFilePath string
	// lastFallbackNode is the specific *tview.TreeNode rebuildFileTree
	// most recently gave the tree as that placeholder selection. The
	// tree's own SetChangedFunc fires for this transition exactly like it
	// would for real navigation; comparing against this lets
	// onTreeNodeChanged tell the two apart, one time only, so a
	// placeholder selection's own resulting event never overwrites
	// wantedFilePath.
	lastFallbackNode *tview.TreeNode
	// dirExpansion remembers each directory's own collapsed/expanded state
	// across rebuilds, keyed by path prefix exactly like
	// collectDirExpansion's own return value: a rebuild that catches the
	// tree in a transient, empty state (a force-push/"R" reload's own
	// resetFiles clears Store.Files() before the new page arrives — see
	// LoadFiles' doc comment) would otherwise collect an empty map right
	// then, discarding every collapse state the user had set, well before
	// the rebuild that actually has files to apply it to ever runs.
	// rebuildFileTree only overwrites this field when a rebuild's own
	// collectDirExpansion call is non-empty, so a transient empty rebuild
	// leaves it untouched for the next, real one to use instead.
	dirExpansion map[string]bool
	// filesForRef is the pull request Files-tab state (currentFilePath,
	// the tree) was last built for, so a switch to a *different* pull
	// request resets them instead of trying to resolve the previous PR's
	// file path against the new one's file list (see subscribeStore's
	// EventPRChanged handler).
	filesForRef *model.PRRef
	// filesWarned tracks whether FilesState().Warnings' current contents
	// have already been toasted once, matching warnedSections' role for
	// section warnings — reset once the warnings clear.
	filesWarned bool

	row          *tview.Flex
	listColumn   *tview.Flex
	listExpanded bool

	bottomPages *tview.Pages
	cmdLine     *tview.InputField
	cmdHistory  []string
	cmdHistIdx  int

	// searchInput is the "/" diff-search input field, a bottomPages page
	// like cmdLine (see openDiffSearch/closeDiffSearch, search.go).
	// searchRE is the currently active search's compiled pattern — nil
	// when none is active, including right after Esc clears the diff
	// highlight (router.go): diffSearchStep re-enables it via
	// diffView.SetSearch before jumping, rather than this field ever going
	// stale. searchPattern is the raw text last submitted (reused to
	// prefill the input, and in "no match"/toast messages). searchMatches
	// is computeSearchMatches' result for searchRE, ordered by
	// (sortedFiles index, hunk, line, byte column) — the order
	// diffSearchStep's cursor-relative scan relies on. searchIdx is the
	// index into searchMatches the cursor was last moved to, -1 before the
	// first jump. searchRef is the pull request the active search belongs
	// to (set in submitDiffSearch), so clearDiffSearchIfWrongPR
	// (storeevents.go, EventPRChanged) can drop a stale search on a genuine
	// PR switch without also dropping it on an EventPRChanged for the same
	// pull request.
	searchInput   *tview.InputField
	searchRE      *regexp.Regexp
	searchPattern string
	searchMatches []searchMatch
	searchIdx     int
	searchRef     model.PRRef

	// composerFlex is added to its own host (composerHost, composer.go —
	// detailColumn for every kind, or the create/edit-PR form's own
	// body slot for composerKindNewPRBody/composerKindEditPRBody; a title
	// line over composerEditor) while a composer is open, and nil
	// otherwise — see
	// openComposer/closeComposer in composer.go. composerReturnFocus is
	// whichever primitive had focus right before the composer opened, so
	// closing it (or a Ctrl-w j/k toggle) can restore it.
	composerFlex         *tview.Flex
	composerTitle        *tview.TextView
	composerEditor       *editor.Editor
	composerTarget       *composerTarget
	composerReturnFocus  tview.Primitive
	composerCtrlWPending bool
	// composerDraftSaveErrShown latches once a write-through draft save
	// fails for the currently open composer, so the error toasts once
	// per composer session rather than on every keystroke.
	composerDraftSaveErrShown bool
	// composerDirty is set the first time onComposerChange actually fires
	// for the currently open composer (a real user edit — see
	// editor.Editor.SetText's own suppression of it for a prefill/
	// restore) and reset on every openComposer. closeComposer's own
	// "keep" path (":q") only saves the draft when this is true, so
	// merely opening a composer and closing it again without typing
	// anything never creates one — a pre-existing draft, loaded at open
	// time and left untouched, still survives regardless, since not
	// re-saving it is a no-op, not a deletion.
	composerDirty bool
	// composerDraftGutterMarked mirrors, for the currently open composer's
	// own target, whether onComposerChange's own last write-through save
	// left a draft on disk (text != "") — set from openComposer's own
	// Drafts.Load result on open, updated on every onComposerChange call.
	// refreshDraftGutterIfNeeded (files.go) is only actually called when
	// this flips, not on every keystroke — see onComposerChange's own doc
	// comment for why a call on every keystroke would be wasteful.
	composerDraftGutterMarked bool
	// refreshCurrentFileCalls counts refreshCurrentFile's own calls, for
	// tests only (see its own doc comment in files.go) — never read
	// outside one.
	refreshCurrentFileCalls int
	// pendingSend is set the instant a composer's Send closes it (the
	// draft is deleted immediately — see composer.go's sendComposer) and
	// cleared once the resulting mutation's own EventMutationChanged
	// fires with Mutating() false: at most one can ever be outstanding,
	// since sendComposer refuses to send at all while a mutation is
	// already in flight.
	pendingSend *pendingSend

	// pendingMutation tracks a non-composer, single mutation triggered from
	// a command or dialog (:close, :reopen, :merge) whose outcome should
	// show a success/failure toast — pendingSend's own counterpart
	// (prcommands.go) for a mutation with no composer text of its own. At
	// most one can ever be outstanding, mirroring pendingSend's own
	// single-flight invariant.
	pendingMutation *pendingSimpleMutation

	seq *keys.Sequencer

	overlay    string // "" | "help" | "messages" | "confirm" | "choice" | "pending" | "threads" | "reaction" | "merge" | "editform" | "editlabels" | "editreviewers" | "createform"
	savedFocus tview.Primitive
	// confirms is the stack of currently open showConfirm dialogs (usually
	// just one; Ctrl-C-while-mutating can stack its own "Quit anyway?"
	// confirm on top of whichever confirm dialog — including pendinglist.go's
	// own delete/discard confirm — was already showing, see showConfirm's
	// own doc comment). a.overlay reads "confirm" for as long as this is
	// non-empty, regardless of depth; each *confirmFrame is a field, not a
	// showConfirm local, so a parent that replaces its focused primitive
	// while a dialog is up (openMergeForm swapping out its loading
	// placeholder) can retarget it via retargetConfirmReturn.
	confirms []*confirmFrame

	// Help overlay (overlay.go, "?"/":help"): helpFlex wraps helpView (the
	// scrollable binding list) and helpSearchInput (app.go's build, a
	// hidden row shown by openHelpSearch — the same ResizeItem trick as
	// the list filter/pending list). helpSearchInput itself is built once,
	// in buildStatusBar, alongside cmdLine/searchInput, so a focus-switch
	// comparison against it in the router is never made before help has
	// ever opened. helpSearchRE is the current search's compiled pattern
	// (nil with none active); helpMatchCount/helpMatchIdx are its total
	// match count and which one buildHelpText currently marks (both 0/0
	// with no search active) — see buildHelpText's own doc comment for why
	// this is a style tag on the rebuilt text rather than a tview region.
	helpView        *tview.TextView
	helpFlex        *tview.Flex
	helpSearchInput *tview.InputField
	helpSearchRE    *regexp.Regexp
	helpMatchCount  int
	helpMatchIdx    int

	// messagesView backs the ":messages" overlay (overlay.go): nil while
	// closed — the field-based "is this overlay open" check
	// overlayStillOpen (dialogs.go) needs, since a.overlay itself reads
	// "confirm" for as long as a Ctrl-C-while-mutating confirm is stacked
	// over it.
	messagesView *tview.TextView

	// Edit PR form (editform.go, "E"/pr.edit): editForm is nil while
	// closed. editFormOriginal snapshots the pull request's field values
	// at open time; Save/Esc both compute the live diff against it
	// on demand (editFormComputeDiff) rather than tracking a separate
	// "dirty" boolean. editFormSelectedLabelIDs/editFormSelectedReviewers
	// hold the form's own current selection, written by the stacked
	// editlabels.go/editreviewers.go overlays on confirm.
	//
	// editFormReturnFocus is a dedicated field — App.savedFocus/
	// restoreFocus() is deliberately not reused here, mirroring
	// composerReturnFocus's own doc comment: restoreFocus() is single-shot
	// (it nils savedFocus after using it), and when showConfirm still
	// shared that slot, closeEditForm reached via the "Discard changes?"
	// confirm's onConfirm found it already consumed and fell through to
	// the list instead of the pane the edit form was opened from — a real
	// bug the second M5 review round's own regression test caught.
	editForm                  *tview.Form
	editFormReturnFocus       tview.Primitive
	editFormRef               model.PRRef
	editFormRepo              model.RepoRef
	editFormOriginal          editFormSnapshot
	editFormTitleField        *tview.InputField
	editFormBaseField         *tview.InputField
	editFormDraftBox          *tview.Checkbox
	editFormSelectedLabelIDs  map[string]bool
	editFormSelectedReviewers map[string]model.Reviewer // keyed by ID
	// editFormBodySlot hosts editFormBodyView (the edit form's own
	// read-only body preview) until "Edit body" swaps it out for the vim
	// composer's own Flex (composerKindEditPRBody's host, composer.go's
	// composerHost), mirroring createFormBodySlot. editFormBody is the
	// form's own current body text, handed back by that composer and
	// diffed against editFormOriginal.body on Save.
	editFormBodySlot *tview.Flex
	editFormBodyView *tview.TextView
	editFormBody     string
	// editFormBranchSuggestions is what editFormBaseField's own
	// SetAutocompleteFunc callback returns synchronously on every
	// keystroke; editFormBranchTimer debounces the actual
	// Store.SearchBranches call by 300ms (docs/DESIGN.md's timer-callback
	// concurrency rule: it only ever dispatches into a store call).
	editFormBranchSuggestions []string
	// editFormBranchLastQuery is the text editFormBranchSuggestions was
	// actually resolved for — see editFormBranchAutocomplete's own doc
	// comment for why this guards against InputField.Autocomplete()'s own
	// call back into that same function re-arming a fresh debounce timer
	// forever.
	editFormBranchLastQuery string
	editFormBranchTimer     *time.Timer
	// editFormBaseSuggesting mirrors whether editFormBaseField's own
	// autocomplete drop-down is currently shown (wrapFormAutocomplete,
	// formnav.go) — routeEditFormKey consults it (forced false whenever
	// the field does not currently have focus) to decide whether Up/Down/
	// Tab/Backtab navigate the drop-down or the form itself.
	editFormBaseSuggesting bool
	// editFormSteps is the FIFO of Save's own in-flight mutation steps —
	// pendingMutation's single slot is not enough for a batch of more than
	// one mutation (see pendingSimpleMutation's own doc comment): each
	// entry's successVerb toasts once that specific mutation finishes;
	// editFormAnyFailed latches once any of them fails, so the form is
	// kept open (its own error toast already shown) rather than closed
	// once the last step finishes.
	editFormSteps     []editFormStep
	editFormAnyFailed bool

	// Edit-labels overlay (editlabels.go, stacked over "editform"):
	// editLabelsWorking is a copy of editFormSelectedLabelIDs mutated by
	// Space, applied back only on Enter (Esc/q discard it).
	editLabelsView    *tview.List
	editLabelsWorking map[string]bool

	// Edit-reviewers overlay (editreviewers.go, stacked over "editform" or
	// "createform" — see openReviewersOverlay's own doc comment):
	// reviewersOwner names which of the two opened it ("editform" or
	// "createform") and reviewersRepo names whose mentionable users/teams
	// to search; both are set by openReviewersOverlay and read back by
	// closeEditReviewersOverlay to know where to write the result and
	// which form to refocus. editReviewersWorking is a copy of that form's
	// own selected-reviewers set mutated by Space/team search, applied
	// back only on Enter (Esc/q discard it). editReviewersTeams is the
	// latest Store.SearchTeams result for the typed query.
	reviewersOwner           string
	reviewersRepo            model.RepoRef
	editReviewersFlex        *tview.Flex
	editReviewersSearch      *tview.InputField
	editReviewersView        *tview.List
	editReviewersWorking     map[string]model.Reviewer
	editReviewersTeams       []model.Team
	editReviewersSearchTimer *time.Timer

	// Merge dialog (mergedialog.go, ":merge"): mergeForm is nil while only
	// the "loading repository settings…" placeholder is showing
	// (mergeLoading true), rebuilt into the real *tview.Form once
	// RepositoryInfo resolves. mergeRef/mergeRepo name the pull request/
	// repository it was opened for, so a repository-metadata event for an
	// unrelated repository (onRepositoryMetadataChangedForMerge) is
	// ignored and Submit's own confirm re-checks mergeRef
	// (runSimpleMutation) before ever calling Store.Merge.
	mergeForm           *tview.Form
	mergeMethodDropDown *tview.DropDown
	mergeHeadlineField  *tview.InputField
	mergeBodyArea       *tview.TextArea
	// mergeSummaryView is nil until openMergeForm builds the real form
	// (the loading placeholder has no summary line at all); refreshed by
	// refreshMergeSummaryIfOpen on EventPRChanged so it never freezes at
	// whatever it read when the dialog first opened.
	mergeSummaryView *tview.TextView
	mergeRef         model.PRRef
	mergeRepo        model.RepoRef
	mergeLoading     bool
	// mergeMethods are the repository's allowed merge methods, in the
	// fixed order mergeMethodDropDown offers them (its own current
	// selection index into this slice).
	mergeMethods []model.MergeMethod

	// Create-PR form (createform.go, "n"/list.new_pr, only bound in
	// ContextList): createForm is nil while closed. Unlike every other
	// overlay, it is not bound to Store.CurrentRef() at all — an unrelated
	// pull request switch must never close it (see
	// composerKindNewPRBody's own doc comment for the body composer's
	// matching rule) — so there is no closeCreateFormIfWrongPR wired into
	// EventPRChanged.
	createForm *tview.Form
	// createFormLayout is the bordered two-column Flex actually added to
	// a.root ("createform" page): left, a Flex(rows) of createForm (its
	// own border/title removed — createFormLayout's own carries both)
	// over createFormBodySlot; right, createFormPreview. createForm
	// itself, not createFormLayout, still gets focus — see buildCreateForm.
	createFormLayout *tview.Flex
	// createFormBodySlot hosts createFormBodyView (the create-PR form's
	// own read-only body preview) until "Edit body" swaps it out for the
	// vim composer's own Flex in its place (composerKindNewPRBody's host,
	// composer.go's composerHost) — restored, refreshed from
	// createFormBody, once the composer closes.
	createFormBodySlot *tview.Flex
	createFormBodyView *tview.TextView
	// createFormPreview shows the diff between the chosen head and base
	// branches (scheduleCreateFormPreview, debounced via
	// createFormPreviewTimer), or a hint/error/loading placeholder in its
	// place. createFormCtrlWPending tracks a Ctrl-w chord's own pending
	// second key (routeCreateFormKey/routeCreateFormPreviewKey), moving
	// focus between the form and this preview — the createform overlay's
	// own analogue of composerCtrlWPending.
	createFormPreview      *tview.TextView
	createFormPreviewTimer *time.Timer
	createFormCtrlWPending bool
	createFormReturnFocus  tview.Primitive
	createFormRepoField    *tview.InputField
	createFormHeadField    *tview.InputField
	createFormBaseField    *tview.InputField
	createFormTitleField   *tview.InputField
	createFormDraftBox     *tview.Checkbox
	// createFormRepo/createFormRepoChosen are set the moment the
	// repository field's own text parses as a syntactically valid
	// "owner/name" (onCreateFormRepoChanged) — chosen, not merely typed:
	// an in-progress, incomplete string leaves createFormRepoChosen false.
	// createFormBaseApplied/createFormBodyApplied each latch true the
	// first time RepositoryInfo/Templates resolve for the currently chosen
	// repository, so a later, unrelated metadata refresh for the same
	// repository never re-stomps a base branch or body the user has since
	// edited themselves.
	createFormRepo              model.RepoRef
	createFormRepoChosen        bool
	createFormBaseApplied       bool
	createFormBodyApplied       bool
	createFormSelectedReviewers map[string]model.Reviewer // keyed by ID
	// createFormBody is the create-PR form's own body text, round-tripped
	// through the vim composer (openCreatePRBodyComposer/
	// sendFormBodyComposer, composerKindNewPRBody) rather than living in
	// a form field of its own.
	createFormBody string
	// createFormHeadSuggestions/createFormHeadLastQuery/createFormHeadTimer
	// and their createFormBase* counterparts mirror editFormBranchSuggestions/
	// editFormBranchLastQuery/editFormBranchTimer exactly (see
	// editFormBranchAutocomplete's own doc comment for the lastQuery guard
	// against InputField.Autocomplete()'s own self-recursion) — one
	// independent set per field, since the create form has two
	// autocompleted branch fields where the edit form has only one.
	createFormHeadSuggestions []string
	createFormHeadLastQuery   string
	createFormHeadTimer       *time.Timer
	createFormBaseSuggestions []string
	createFormBaseLastQuery   string
	createFormBaseTimer       *time.Timer
	// createFormRepoSuggesting/createFormHeadSuggesting/createFormBaseSuggesting
	// mirror editFormBaseSuggesting for each of the create form's own three
	// autocompleted fields.
	createFormRepoSuggesting bool
	createFormHeadSuggesting bool
	createFormBaseSuggesting bool
	// createFormPending tracks Submit's own single, unscoped
	// CreatePullRequest mutation from enqueue to its EventMutationChanged
	// resolution — see onCreateFormMutationChanged's own doc comment for
	// why created must be read, not assumed, at that point.
	createFormPending *createFormPending

	// choiceMenu/choiceOnChoose back the "choice" overlay (see
	// dialogs.go's showChoiceMenu/closeChoiceMenu): the send-mode picker
	// ("Add single comment" / "Add to review") and the "which comment?"
	// picker when several review comments are editable/deletable on one
	// thread.
	choiceMenu     *tview.List
	choiceOnChoose func(index int)

	// pendingListView/pendingListEntries back the "p" pending-review/drafts
	// list overlay (pendinglist.go): every pending review comment for the
	// current pull request followed by every local draft for it, in that
	// order, re-rendered on EventPRChanged/EventMutationChanged while open.
	pendingListView    *tview.List
	pendingListEntries []pendingListEntry
	// pendingListDraftsWarned tracks whether rebuildPendingList's current
	// Drafts.List error (if any) has already been toasted, mirroring
	// filesWarned/warnedSections' own "warn once, reset once resolved"
	// shape.
	pendingListDraftsWarned bool

	// threadListView/threadListEntries back the "t" review-threads list
	// overlay (threadlist.go): every review thread on the current pull
	// request, sorted by path then line then ID, re-rendered on
	// EventPRChanged/EventMutationChanged while open — the same shape as
	// pendingListView/pendingListEntries above, but read-only (no d/D
	// mutation path: editing/deleting/reacting to a thread is the diff's
	// own job, reached by jumping to it via Enter).
	threadListView    *tview.List
	threadListEntries []model.ReviewThread

	// reactionPickerView/reactionSubjectID back the "reaction" overlay
	// (reactionpicker.go's openReactionPicker/closeReactionPicker): the
	// eight known reactions for reactionSubjectID (the pull request
	// itself, an issue comment, a review, or a review comment), each row's
	// count/marker re-rendered on EventPRChanged while open.
	reactionPickerView *tview.List
	reactionSubjectID  string

	// rowIndex maps a ListRow.ID (a PR's PRRef.Key()) to the data needed
	// to preview it and to decide whether reaching it should trigger
	// Store.LoadMore, rebuilt every time buildRows runs.
	rowIndex map[string]*previewState
	// warnedSections tracks which sections' current warnings have already
	// been toasted, so a section's warnings are announced once, not on
	// every refresh. Cleared for a section once its warnings go away, so
	// a later, different batch of warnings is announced again.
	warnedSections map[string]bool
	// lastRenderedRef is the pull request ref renderPRTab last rendered
	// the PR tab's blocks for (nil when none has been open yet), used to
	// detect a switch to a different pull request so the PR tab's cursor
	// resets to the top instead of carrying over a position that happens
	// to share a block ID (block IDs are positional — "header",
	// "timeline:2", ... — not scoped to a pull request) with whatever the
	// previous one was showing.
	lastRenderedRef *model.PRRef

	previewTimer *time.Timer
	toastTimer   *time.Timer
	// toastSeq increments on every showToast call; a scheduled clear only
	// takes effect if it still matches, so a stale timer racing its own
	// Stop() call can never clear a newer toast. See clearToastIfCurrent.
	toastSeq    int
	spinnerStop chan struct{}
	// spinnerFrame is the last frame the spinner goroutine rendered, kept
	// so status-bar re-renders triggered by key events (see handleKey) do
	// not snap the spinner back to its first frame.
	spinnerFrame int
}

// previewState is the currently previewed/opened pull request, kept
// alongside the row index information needed for the "reached the last row
// of a section" LoadMore trigger.
type previewState struct {
	item          model.ListItem
	sectionIndex  int
	lastInSection bool
}

// New builds an App from deps. It constructs the underlying
// tview.Application and the whole widget tree immediately (so App.Dispatch
// is usable — and, in tests, a tcell.SimulationScreen can be installed —
// before Run is ever called), but starts no goroutines and makes no store
// calls until Run.
func New(deps Deps) *App {
	a := &App{
		deps:       deps,
		app:        tview.NewApplication(),
		currentTab: "pr",
		seq:        keys.NewSequencer(deps.Keymap),
		cmdHistIdx: -1,
	}
	a.build()
	return a
}

// Dispatch runs f on the UI goroutine, via the underlying
// tview.Application's QueueUpdateDraw. It exists so main can wire it into
// store.Deps.Dispatch despite the Store having to be built before the App:
// main declares a *App variable, builds the Store with a Dispatch closure
// that calls through that variable, then assigns the variable once New
// returns — safe because the Store makes no Dispatch call until Start runs,
// which App.Run only does after the App (and therefore the closure's
// target) already exists.
func (a *App) Dispatch(f func()) {
	a.app.QueueUpdateDraw(f)
}

// Run starts gprt's event loop: it starts the Store (loading the viewer and
// the first page of every section) and its auto-refresh ticker, sets the
// root primitive, and blocks in tview's event loop until the user quits or
// an unrecoverable error occurs. On return — however it happens — the
// Store's context is cancelled immediately; Run never waits for any
// goroutine the Store or App started (see docs/DESIGN.md's concurrency
// rule 9).
func (a *App) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.subscribeStore()
	a.deps.Store.Start(ctx)
	a.deps.Store.StartAutoRefresh(ctx, a.deps.Config.RefreshInterval)

	return a.app.SetRoot(a.root, true).Run()
}
