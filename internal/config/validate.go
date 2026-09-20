package config

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// minRefreshInterval is the shortest allowed RefreshInterval, chosen to
// avoid hammering the GitHub API from an accidental typo like "5s".
const minRefreshInterval = 30 * time.Second

// validIcons and validListStates are the accepted enum values for the
// corresponding config keys.
var (
	validIcons      = map[string]bool{"unicode": true, "nerd": true}
	validListStates = map[string]bool{"open": true, "closed": true, "merged": true, "all": true}
)

// Validate checks the configuration for internal consistency: enum values,
// numeric ranges, and required fields on nested structures. All problems
// are collected and returned together via errors.Join, so a user fixing
// their config sees every issue in one run instead of one at a time.
func (c Config) Validate() error {
	var errs []error

	if !validIcons[c.Icons] {
		errs = append(errs, fmt.Errorf("icons: must be \"unicode\" or \"nerd\", got %q", c.Icons))
	}
	if !validListStates[c.List.State] {
		errs = append(errs, fmt.Errorf("list.state: must be one of \"open\", \"closed\", \"merged\", \"all\", got %q", c.List.State))
	}
	if c.RefreshInterval < minRefreshInterval {
		errs = append(errs, fmt.Errorf("refresh_interval: must be at least %s, got %s", minRefreshInterval, c.RefreshInterval))
	}
	if c.TabWidth < 1 || c.TabWidth > 16 {
		errs = append(errs, fmt.Errorf("tab_width: must be between 1 and 16, got %d", c.TabWidth))
	}
	for i, s := range c.List.Sections {
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("list.sections[%d]: name must not be empty", i))
		}
		if s.Query == "" {
			errs = append(errs, fmt.Errorf("list.sections[%d]: query must not be empty", i))
		}
	}
	// Keys is a map, so iterate in a fixed order: map iteration order is
	// randomized in Go, which would otherwise make the joined error
	// message (and its tests) nondeterministic.
	actions := make([]string, 0, len(c.Keys))
	for action := range c.Keys {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		if c.Keys[action] == "" {
			errs = append(errs, fmt.Errorf("keys[%s]: key sequence must not be empty", action))
		}
	}

	return errors.Join(errs...)
}
