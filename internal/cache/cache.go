// Package cache is gprt's on-disk, ETag-aware HTTP/GraphQL response cache.
// It is namespaced per host/login/repository/pull-request so that a
// mutation on one pull request can invalidate exactly its own cached
// entries. Never store request headers or tokens.
package cache

import (
	"errors"
	"fmt"
	"time"

	"github.com/hirano00o/gprt/internal/model"
)

// Key identifies one cached response. PR is nil for search and list
// results, which are namespaced per host/login instead of per pull
// request. Rest disambiguates entries within that namespace (for example a
// serialized search query or REST path) and is never stored in the clear:
// only its SHA-256 hash appears in the on-disk file name.
//
// Callers should build a Key with PRKey or SearchKey rather than a struct
// literal: both validate the fields that make up the on-disk path, so a
// caller bug (an empty host, say) fails loudly instead of quietly landing
// every such key in the same escaped path segment.
type Key struct {
	Host  string
	Login string
	PR    *model.PRRef
	Rest  string
}

// PRKey builds a Key scoped to one pull request. It validates that host,
// login, and the PR's repository owner and name are non-empty and that its
// number is positive, since all of them become on-disk path segments.
func PRKey(host, login string, ref model.PRRef, rest string) (Key, error) {
	if host == "" {
		return Key{}, errors.New("cache: PRKey: host must not be empty")
	}
	if login == "" {
		return Key{}, errors.New("cache: PRKey: login must not be empty")
	}
	if ref.Repo.Owner == "" {
		return Key{}, errors.New("cache: PRKey: PR repo owner must not be empty")
	}
	if ref.Repo.Name == "" {
		return Key{}, errors.New("cache: PRKey: PR repo name must not be empty")
	}
	if ref.Number <= 0 {
		return Key{}, fmt.Errorf("cache: PRKey: PR number must be positive, got %d", ref.Number)
	}
	return Key{Host: host, Login: login, PR: &ref, Rest: rest}, nil
}

// SearchKey builds a Key scoped to search/list results for one host/login.
// It validates that both are non-empty, since they become on-disk path
// segments.
func SearchKey(host, login, rest string) (Key, error) {
	if host == "" {
		return Key{}, errors.New("cache: SearchKey: host must not be empty")
	}
	if login == "" {
		return Key{}, errors.New("cache: SearchKey: login must not be empty")
	}
	return Key{Host: host, Login: login, Rest: rest}, nil
}

// Entry is one cached response body together with the metadata needed to
// revalidate or display it.
type Entry struct {
	Body      []byte
	ETag      string
	FetchedAt time.Time
}
