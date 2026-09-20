// Package model defines the domain types shared across gprt's internal
// packages. Types in this package are pure data: they hold no network or UI
// dependencies and their few methods are simple derivations of the fields
// they already carry.
package model

import "fmt"

// RepoRef identifies a GitHub repository on a specific host.
type RepoRef struct {
	Host  string
	Owner string
	Name  string
}

// NameWithOwner returns the "owner/name" form used in GitHub UIs and search
// qualifiers.
func (r RepoRef) NameWithOwner() string {
	return r.Owner + "/" + r.Name
}

// PRRef identifies a single pull request within a repository.
type PRRef struct {
	Repo   RepoRef
	Number int
}

// Key returns a globally unique identifier in the form
// "host/owner/name#number", suitable for use as a map key or cache
// namespace segment.
func (r PRRef) Key() string {
	return fmt.Sprintf("%s/%s#%d", r.Repo.Host, r.Repo.NameWithOwner(), r.Number)
}
