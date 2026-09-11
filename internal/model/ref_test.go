package model

import "testing"

func TestRepoRef_NameWithOwner(t *testing.T) {
	tests := []struct {
		name string
		repo RepoRef
		want string
	}{
		{"typical", RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}, "hirano00o/gprt"},
		{"empty", RepoRef{}, "/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.repo.NameWithOwner(); got != tc.want {
				t.Errorf("NameWithOwner() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPRRef_Key(t *testing.T) {
	tests := []struct {
		name string
		ref  PRRef
		want string
	}{
		{
			"typical",
			PRRef{Repo: RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}, Number: 42},
			"github.com/hirano00o/gprt#42",
		},
		{
			"zero number",
			PRRef{Repo: RepoRef{Host: "github.com", Owner: "a", Name: "b"}, Number: 0},
			"github.com/a/b#0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.Key(); got != tc.want {
				t.Errorf("Key() = %q, want %q", got, tc.want)
			}
		})
	}
}
