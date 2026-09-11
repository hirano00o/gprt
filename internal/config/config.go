package config

import "time"

// Config is gprt's user-configurable settings, loaded from
// $GPRT_CONFIG_DIR/config.yaml (or the XDG/HOME fallback resolved by
// ConfigDir). Every key is optional; unspecified keys keep the values from
// Default.
type Config struct {
	// Host is the GitHub host to talk to. The empty string means the
	// GitHub client resolves it later (gh's default host).
	Host string `yaml:"host"`
	// RefreshInterval is how often the list and the open pull request are
	// refreshed in the background. Must be at least 30 seconds.
	RefreshInterval time.Duration `yaml:"refresh_interval"`
	// Icons selects the icon set used in the UI: "unicode" or "nerd".
	Icons string `yaml:"icons"`
	// Editor overrides $EDITOR for the ":e" external editor command.
	Editor string `yaml:"editor"`
	// Browser overrides $BROWSER for opening URLs.
	Browser string `yaml:"browser"`
	// HighlightStyle is the chroma style name used for diff syntax
	// highlighting.
	HighlightStyle string `yaml:"highlight_style"`
	// TabWidth is the number of columns a tab expands to when rendering
	// diffs. Must be between 1 and 16 inclusive. A non-integral YAML value
	// (for example 2.5) is silently truncated by the YAML decoder before
	// Validate ever sees it, so tab_width: 2.5 becomes 2 with no error.
	TabWidth int `yaml:"tab_width"`
	// List configures the pull request list view.
	List ListConfig `yaml:"list"`
	// Keys maps an action ID to the key sequence (vim notation) that
	// triggers it, overriding the built-in default for that action.
	Keys map[string]string `yaml:"keys"`
}

// ListConfig configures the pull request list view.
type ListConfig struct {
	// State filters the list by pull request state: "open", "closed"
	// (closed but not merged), "merged", or "all".
	State string `yaml:"state"`
	// Sections are user-defined search sections appended after the
	// built-in ones (direct review requests, team review requests, mine,
	// involved).
	Sections []Section `yaml:"sections"`
}

// Section is a single user-defined list section, identified by a display
// name and a GitHub search query.
type Section struct {
	Name  string `yaml:"name"`
	Query string `yaml:"query"`
}

// Default returns gprt's built-in default configuration.
func Default() Config {
	return Config{
		RefreshInterval: 5 * time.Minute,
		Icons:           "unicode",
		HighlightStyle:  "github-dark",
		TabWidth:        4,
		List: ListConfig{
			State: "open",
		},
	}
}
