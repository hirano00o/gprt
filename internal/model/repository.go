package model

// MergeMethod is one of GitHub's three pull request merge strategies,
// selected by the "M" merge dialog (M5) and sent as
// MergePullRequestInput's mergeMethod.
type MergeMethod string

// Known merge methods, matching GraphQL's PullRequestMergeMethod enum
// exactly (confirmed via introspection).
const (
	MergeMethodMerge  MergeMethod = "MERGE"
	MergeMethodSquash MergeMethod = "SQUASH"
	MergeMethodRebase MergeMethod = "REBASE"
)

// RepositoryInfo is a repository's metadata needed to drive the edit/merge/
// create-PR forms (M5): its allowed merge strategies, default branch, and
// the viewer's own permission level. Loaded lazily by
// Store.EnsureRepositoryMetadata, not on every OpenPR.
type RepositoryInfo struct {
	ID                  string
	Ref                 RepoRef
	DefaultBranch       string
	MergeCommitAllowed  bool
	SquashMergeAllowed  bool
	RebaseMergeAllowed  bool
	DeleteBranchOnMerge bool
	// ViewerPermission is GitHub's own coarse-grained permission string for
	// the viewer on this repository (for example "ADMIN", "WRITE",
	// "READ"), as returned verbatim by the repository query.
	ViewerPermission string
}

// Branch is one of a repository's branches (GraphQL's Ref, refPrefix:
// "refs/heads/"), used by the base/head branch picker.
type Branch struct {
	Name    string
	HeadOID string
}

// Team is an organization team, usable as a review-request target
// alongside an individual User.
type Team struct {
	ID   string
	Slug string
	Name string
}

// PullRequestTemplate is one of a repository's pull request templates
// (GraphQL's PullRequestTemplate), offered when creating a new pull
// request. Filename distinguishes multiple templates (for example
// ".github/PULL_REQUEST_TEMPLATE/bug.md"); Filename is empty for the
// single root-level "PULL_REQUEST_TEMPLATE.md" template GitHub also
// supports.
type PullRequestTemplate struct {
	Filename string
	Body     string
}

// RepositorySummary is the minimal repository shape needed to populate the
// create-pull-request repository picker's candidate list (Store's
// ViewerRepositories), filtered locally by the UI as the user types.
type RepositorySummary struct {
	Ref           RepoRef
	ID            string
	DefaultBranch string
	IsArchived    bool
}
