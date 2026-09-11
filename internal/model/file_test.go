package model

import "testing"

func TestFileStatus_Values(t *testing.T) {
	tests := []struct {
		val  FileStatus
		want string
	}{
		{FileStatusAdded, "added"},
		{FileStatusRemoved, "removed"},
		{FileStatusModified, "modified"},
		{FileStatusRenamed, "renamed"},
		{FileStatusCopied, "copied"},
		{FileStatusChanged, "changed"},
		{FileStatusUnchanged, "unchanged"},
	}
	for _, tc := range tests {
		if string(tc.val) != tc.want {
			t.Errorf("FileStatus = %q, want %q", string(tc.val), tc.want)
		}
	}
}

func TestChangedFile_StatusFieldIsTyped(t *testing.T) {
	f := ChangedFile{Status: FileStatusAdded}
	if f.Status != FileStatusAdded {
		t.Errorf("Status = %v, want %v", f.Status, FileStatusAdded)
	}
}
