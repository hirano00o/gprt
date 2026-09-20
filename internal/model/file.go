package model

// FileStatus is how a file changed between a pull request's base and head,
// as reported by the REST "list pull request files" endpoint.
type FileStatus string

// Known file statuses.
const (
	FileStatusAdded     FileStatus = "added"
	FileStatusRemoved   FileStatus = "removed"
	FileStatusModified  FileStatus = "modified"
	FileStatusRenamed   FileStatus = "renamed"
	FileStatusCopied    FileStatus = "copied"
	FileStatusChanged   FileStatus = "changed"
	FileStatusUnchanged FileStatus = "unchanged"
)

// ChangedFile is one file changed by a pull request, as reported by the
// REST "list pull request files" endpoint.
type ChangedFile struct {
	Path         string
	PreviousPath string
	Status       FileStatus
	Additions    int
	Deletions    int
	Patch        string
	HasPatch     bool
	SHA          string
}
