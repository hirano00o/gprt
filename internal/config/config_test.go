package config

import (
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	got := Default()

	want := Config{
		Host:            "",
		RefreshInterval: 5 * time.Minute,
		Icons:           "unicode",
		Editor:          "",
		Browser:         "",
		HighlightStyle:  "github-dark",
		TabWidth:        4,
		List:            ListConfig{State: "open"},
	}

	if got.Host != want.Host {
		t.Errorf("Host = %q, want %q", got.Host, want.Host)
	}
	if got.RefreshInterval != want.RefreshInterval {
		t.Errorf("RefreshInterval = %v, want %v", got.RefreshInterval, want.RefreshInterval)
	}
	if got.Icons != want.Icons {
		t.Errorf("Icons = %q, want %q", got.Icons, want.Icons)
	}
	if got.HighlightStyle != want.HighlightStyle {
		t.Errorf("HighlightStyle = %q, want %q", got.HighlightStyle, want.HighlightStyle)
	}
	if got.TabWidth != want.TabWidth {
		t.Errorf("TabWidth = %d, want %d", got.TabWidth, want.TabWidth)
	}
	if got.List.State != want.List.State {
		t.Errorf("List.State = %q, want %q", got.List.State, want.List.State)
	}
	if len(got.List.Sections) != 0 {
		t.Errorf("List.Sections = %v, want empty", got.List.Sections)
	}
	if len(got.Keys) != 0 {
		t.Errorf("Keys = %v, want empty", got.Keys)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Default().Validate() = %v, want nil", err)
	}
}
