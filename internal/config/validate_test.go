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
