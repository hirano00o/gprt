// Package drafts persists in-progress comment/review/PR-body text so it
// survives a pull request switch, a reload, or a restart. It has no
// dependency on tview, gh, or store: a Store is keyed purely by Key and
// reads/writes small JSON files under a caller-supplied directory (see
// internal/config.StateDir).
package drafts

import "fmt"

// Kind identifies what a draft's text is for.
type Kind string

// Known draft kinds.
const (
	// KindComment is a general PR-level comment (an IssueComment) or a
	// new review comment on a diff line/range, keyed by its target
	// anchor (see Key.Anchor).
	KindComment Kind = "comment"
	// KindReply is a reply to an existing review thread, keyed by the
	// thread's ID.
	KindReply Kind = "reply"
	// KindFile is a whole-file review comment, keyed by the file's path.
	KindFile Kind = "file"
	// KindReview is a pending review's own body.
	KindReview Kind = "review"
	// KindPRBody is an in-progress edit of the pull request's own body.
	KindPRBody Kind = "pr_body"
	// KindEdit is an in-progress edit of an existing comment, keyed by
	// that comment's ID.
	KindEdit Kind = "edit"
	// KindNewPR is the create-PR form's own in-progress body, for a pull
	// request that does not exist yet. Key.PR for this kind is a
	// repository, not an existing pull request: a model.PRRef built from
	// the chosen repository with Number 0 (a number GitHub never actually
	// issues), so its Key() ("host/owner/name#0") can never collide with a
	// real pull request's own draft, whatever kind that is.
	KindNewPR Kind = "new_pr"
)

// Key identifies one draft: the pull request it belongs to, what kind of
// text it is, and where within that pull request it is anchored.
type Key struct {
	// PR is the pull request the draft belongs to, in model.PRRef.Key()
	// form ("host/owner/repo#number").
	PR   string
	Kind Kind
	// Anchor disambiguates drafts of the same Kind on the same PR: for
	// example "issue" for a general PR comment, "path:RIGHT:12-15" for a
	// line/range review comment, a thread ID for a reply, or a comment ID
	// for an edit.
	Anchor string
}

// String returns a stable, unique-per-value textual form of k
// ("pr|kind|anchor"), used to derive the on-disk file name (see filePath).
// It is not itself parsed back into a Key: the on-disk format stores PR,
// Kind, and Anchor as separate JSON fields (see fileFormat) so a "|" that
// happened to occur inside PR or Anchor could never corrupt round-tripping.
func (k Key) String() string {
	return fmt.Sprintf("%s|%s|%s", k.PR, k.Kind, k.Anchor)
}
