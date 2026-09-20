package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoad_MissingFileReturnsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("Load() = %+v, want %+v", got, Default())
	}
}

func TestLoad_BrokenSymlinkIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "does-not-exist.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := Load(link)
	if err == nil {
		t.Fatal("Load() error = nil, want an error for a broken symlink, not \"no config\"")
	}
	if !strings.Contains(err.Error(), link) {
		t.Errorf("Load() error = %q, want it to mention the path %q", err.Error(), link)
	}
}

func TestLoad_EmptyFileReturnsDefault(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{"zero bytes", ""},
		{"blank lines only", "\n\n\n"},
		{"comment only", "# just a comment\n"},
		{"document marker only", "---\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigFile(t, tc.contents)

			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got, Default()) {
				t.Errorf("Load() = %+v, want Default() %+v (an empty file must not zero the config)", got, Default())
			}
		})
	}
}

func TestLoad_MultipleDocumentsRejected(t *testing.T) {
	path := writeConfigFile(t, "host: a.example.com\n---\nicons: nerd\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want an error for multiple YAML documents")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("Load() error = %q, want it to mention multiple YAML documents", err.Error())
	}
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("Load() error = %v (%T), want a *ParseError", err, err)
	}
}

func TestLoad_TrailingDocumentMarkerAccepted(t *testing.T) {
	path := writeConfigFile(t, "host: a.example.com\n---\n")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want a trailing bare \"---\" with nothing after it to be accepted", err)
	}
	if got.Host != "a.example.com" {
		t.Errorf("Host = %q, want %q", got.Host, "a.example.com")
	}
}

func TestLoad_FullFile(t *testing.T) {
	path := writeConfigFile(t, `
host: github.example.com
refresh_interval: 90s
icons: nerd
editor: nvim
browser: firefox
highlight_style: monokai
tab_width: 2
list:
  state: all
  sections:
    - name: Backend
      query: "org:acme label:backend"
keys:
  list.filter: "/"
  pr.submit: "S"
`)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{
		Host:            "github.example.com",
		RefreshInterval: 90 * time.Second,
		Icons:           "nerd",
		Editor:          "nvim",
		Browser:         "firefox",
		HighlightStyle:  "monokai",
		TabWidth:        2,
		List: ListConfig{
			State:    "all",
			Sections: []Section{{Name: "Backend", Query: "org:acme label:backend"}},
		},
		Keys: map[string]string{"list.filter": "/", "pr.submit": "S"},
	}

	if got.Host != want.Host || got.RefreshInterval != want.RefreshInterval || got.Icons != want.Icons ||
		got.Editor != want.Editor || got.Browser != want.Browser || got.HighlightStyle != want.HighlightStyle ||
		got.TabWidth != want.TabWidth || got.List.State != want.List.State {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	if len(got.List.Sections) != 1 || got.List.Sections[0] != want.List.Sections[0] {
		t.Errorf("List.Sections = %+v, want %+v", got.List.Sections, want.List.Sections)
	}
	if got.Keys["list.filter"] != "/" || got.Keys["pr.submit"] != "S" {
		t.Errorf("Keys = %+v, want %+v", got.Keys, want.Keys)
	}
}

func TestLoad_PartialFileKeepsDefaults(t *testing.T) {
	path := writeConfigFile(t, `
host: github.example.com
`)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	def := Default()
	if got.Host != "github.example.com" {
		t.Errorf("Host = %q, want %q", got.Host, "github.example.com")
	}
	if got.RefreshInterval != def.RefreshInterval {
		t.Errorf("RefreshInterval = %v, want default %v", got.RefreshInterval, def.RefreshInterval)
	}
	if got.Icons != def.Icons {
		t.Errorf("Icons = %q, want default %q", got.Icons, def.Icons)
	}
	if got.TabWidth != def.TabWidth {
		t.Errorf("TabWidth = %d, want default %d", got.TabWidth, def.TabWidth)
	}
	if got.List.State != def.List.State {
		t.Errorf("List.State = %q, want default %q", got.List.State, def.List.State)
	}
}

func TestLoad_UnknownKeyRejected(t *testing.T) {
	path := writeConfigFile(t, `
hostt: github.example.com
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for unknown key")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load() error = %q, want it to mention the path %q", err.Error(), path)
	}
}

func TestLoad_UnknownKeyReturnsUnwrappableParseError(t *testing.T) {
	path := writeConfigFile(t, `
hostt: github.example.com
`)

	_, err := Load(path)
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Load() error = %v (%T), want it to be a *ParseError", err, err)
	}
	if parseErr.Path != path {
		t.Errorf("ParseError.Path = %q, want %q", parseErr.Path, path)
	}
	if parseErr.Err == nil {
		t.Error("ParseError.Err is nil, want the underlying yaml error preserved for errors.Is/As")
	}
	if parseErr.Error() != parseErr.Formatted {
		t.Errorf("Error() = %q, want the formatted text %q", parseErr.Error(), parseErr.Formatted)
	}
}

func TestLoad_InvalidConfigRejected(t *testing.T) {
	path := writeConfigFile(t, `
icons: emoji
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "icons:") {
		t.Errorf("Load() error = %q, want it to mention the icons rule", err.Error())
	}
}

// writeConfigFile writes contents to a config.yaml under a fresh temp
// directory and returns its path.
func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
