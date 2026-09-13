package cache

import (
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestPRKey(t *testing.T) {
	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}
	ref := model.PRRef{Repo: repo, Number: 42}

	tests := []struct {
		name    string
		host    string
		login   string
		ref     model.PRRef
		wantErr bool
	}{
		{"valid", "github.com", "octocat", ref, false},
		{"empty host", "", "octocat", ref, true},
		{"empty login", "github.com", "", ref, true},
		{
			"empty owner",
			"github.com", "octocat",
			model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "", Name: "gprt"}, Number: 1},
			true,
		},
		{
			"empty name",
			"github.com", "octocat",
			model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "o", Name: ""}, Number: 1},
			true,
		},
		{"zero number", "github.com", "octocat", model.PRRef{Repo: repo, Number: 0}, true},
		{"negative number", "github.com", "octocat", model.PRRef{Repo: repo, Number: -1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := PRKey(tc.host, tc.login, tc.ref, "detail")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PRKey() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("PRKey() error = %v", err)
			}
			if k.Host != tc.host || k.Login != tc.login || k.PR == nil || *k.PR != tc.ref || k.Rest != "detail" {
				t.Errorf("PRKey() = %+v, want Host=%q Login=%q PR=%+v Rest=detail", k, tc.host, tc.login, tc.ref)
			}
		})
	}
}

func TestPRKey_DoesNotAliasCallersPRRef(t *testing.T) {
	ref := model.PRRef{Repo: model.RepoRef{Host: "github.com", Owner: "o", Name: "n"}, Number: 1}

	k, err := PRKey("github.com", "octocat", ref, "detail")
	if err != nil {
		t.Fatalf("PRKey() error = %v", err)
	}

	ref.Number = 999 // mutating the caller's copy must not affect k.PR
	if k.PR.Number != 1 {
		t.Errorf("k.PR.Number = %d, want 1 (PRKey must copy ref, not alias it)", k.PR.Number)
	}
}

func TestRepoKey(t *testing.T) {
	repo := model.RepoRef{Host: "github.com", Owner: "hirano00o", Name: "gprt"}

	tests := []struct {
		name    string
		host    string
		login   string
		repo    model.RepoRef
		wantErr bool
	}{
		{"valid", "github.com", "octocat", repo, false},
		{"empty host", "", "octocat", repo, true},
		{"empty login", "github.com", "", repo, true},
		{"empty owner", "github.com", "octocat", model.RepoRef{Owner: "", Name: "gprt"}, true},
		{"empty name", "github.com", "octocat", model.RepoRef{Owner: "o", Name: ""}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := RepoKey(tc.host, tc.login, tc.repo, "mentionable-users")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RepoKey() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("RepoKey() error = %v", err)
			}
			if k.Host != tc.host || k.Login != tc.login || k.Repo == nil || *k.Repo != tc.repo || k.Rest != "mentionable-users" {
				t.Errorf("RepoKey() = %+v, want Host=%q Login=%q Repo=%+v Rest=mentionable-users", k, tc.host, tc.login, tc.repo)
			}
			if k.PR != nil {
				t.Errorf("RepoKey() PR = %+v, want nil", k.PR)
			}
		})
	}
}

func TestRepoKey_DoesNotAliasCallersRepoRef(t *testing.T) {
	repo := model.RepoRef{Host: "github.com", Owner: "o", Name: "n"}

	k, err := RepoKey("github.com", "octocat", repo, "mentionable-users")
	if err != nil {
		t.Fatalf("RepoKey() error = %v", err)
	}

	repo.Name = "changed" // mutating the caller's copy must not affect k.Repo
	if k.Repo.Name != "n" {
		t.Errorf("k.Repo.Name = %q, want %q (RepoKey must copy repo, not alias it)", k.Repo.Name, "n")
	}
}

func TestSearchKey(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		login   string
		wantErr bool
	}{
		{"valid", "github.com", "octocat", false},
		{"empty host", "", "octocat", true},
		{"empty login", "github.com", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := SearchKey(tc.host, tc.login, "q")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SearchKey() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("SearchKey() error = %v", err)
			}
			if k.Host != tc.host || k.Login != tc.login || k.PR != nil || k.Rest != "q" {
				t.Errorf("SearchKey() = %+v, want Host=%q Login=%q PR=nil Rest=q", k, tc.host, tc.login)
			}
		})
	}
}
