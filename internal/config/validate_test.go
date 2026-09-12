package config

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // substring expected in the error, "" means no error
	}{
		{"valid default", func(c *Config) {}, ""},
		{"invalid icons", func(c *Config) { c.Icons = "emoji" }, "icons:"},
		{"invalid list state", func(c *Config) { c.List.State = "draft" }, "list.state:"},
		{"refresh interval too short", func(c *Config) { c.RefreshInterval = 29 * time.Second }, "refresh_interval:"},
		{"refresh interval exactly minimum is valid", func(c *Config) { c.RefreshInterval = 30 * time.Second }, ""},
		{"tab width too small", func(c *Config) { c.TabWidth = 0 }, "tab_width:"},
		{"tab width too large", func(c *Config) { c.TabWidth = 17 }, "tab_width:"},
		{"tab width boundaries are valid", func(c *Config) { c.TabWidth = 1 }, ""},
		{
			"section missing name",
			func(c *Config) { c.List.Sections = []Section{{Name: "", Query: "is:open"}} },
			"list.sections[0]",
		},
		{
			"section missing query",
			func(c *Config) { c.List.Sections = []Section{{Name: "Backend", Query: ""}} },
			"list.sections[0]",
		},
		{
			"section valid",
			func(c *Config) { c.List.Sections = []Section{{Name: "Backend", Query: "org:acme"}} },
			"",
		},
		{
			"key with empty sequence",
			func(c *Config) { c.Keys = map[string]string{"list.filter": ""} },
			"keys[list.filter]",
		},
		{
			"key with non-empty sequence is valid",
			func(c *Config) { c.Keys = map[string]string{"list.filter": "/"} },
			"",
		},
		{
			"unknown highlight style",
			func(c *Config) { c.HighlightStyle = "not-a-real-chroma-style" },
			"is not a chroma style name",
		},
		{
			"known highlight style is valid",
			func(c *Config) { c.HighlightStyle = "monokai" },
			"",
		},
		{
			"empty highlight style is valid (theme.SetHighlightStyle treats it as \"use the default\")",
			func(c *Config) { c.HighlightStyle = "" },
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestConfig_Validate_HighlightStyleErrorMentionsExamples guards against
// the highlight_style error regressing to a bare "unknown style %q" with no
// pointer to where a valid name actually comes from (M2 review round 3,
// item 27): a user hitting this at startup has no obvious way to discover
// chroma's own style names otherwise.
func TestConfig_Validate_HighlightStyleErrorMentionsExamples(t *testing.T) {
	c := Default()
	c.HighlightStyle = "not-a-real-chroma-style"

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
	for _, want := range []string{"chroma", "github-dark", "monokai", "dracula"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() error %q does not mention %q", err.Error(), want)
		}
	}
}

func TestConfig_Validate_MultipleProblemsJoined(t *testing.T) {
	c := Default()
	c.Icons = "emoji"
	c.TabWidth = 99

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
	for _, want := range []string{"icons:", "tab_width:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() error %q does not contain %q", err.Error(), want)
		}
	}
}
