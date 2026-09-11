package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigExplicitPathMissingIsAnError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.yaml")

	_, err := loadConfig(missing)
	if err == nil {
		t.Fatal("loadConfig with an explicit, missing --config path returned a nil error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the missing path", err)
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error %q does not say \"no such file\"", err)
	}
}

func TestLoadConfigExplicitPathThatExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("host: example.com\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig(%q) returned an error: %v", path, err)
	}
	if cfg.Host != "example.com" {
		t.Errorf("cfg.Host = %q, want %q", cfg.Host, "example.com")
	}
}

func TestLoadConfigExplicitBrokenSymlinkReportsConfigLoadsOwnError(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join(dir, "missing-target.yaml"), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// A broken symlink is a real directory entry, so loadConfig's own
	// existence check must not short-circuit it with a generic "no such
	// file"; config.Load reports it with a more specific error instead.
	_, err := loadConfig(link)
	if err == nil {
		t.Fatal("loadConfig with a broken symlink returned a nil error")
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Errorf("error %q looks like loadConfig's own existence check fired instead of config.Load's broken-symlink error", err)
	}
}

func TestLoadConfigImplicitPathMissingKeepsDefaults(t *testing.T) {
	// With --config left unset, a missing config.yaml under the resolved
	// config directory is not an error: it falls back to config.Default(),
	// unlike an explicit --config path pointing at a file that does not
	// exist.
	t.Setenv("GPRT_CONFIG_DIR", t.TempDir())

	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig(\"\") with no config file present returned an error: %v", err)
	}
	if cfg.Icons != "unicode" {
		t.Errorf("cfg.Icons = %q, want the default %q", cfg.Icons, "unicode")
	}
}
