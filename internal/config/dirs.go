// Package config resolves gprt's configuration, cache, and state
// directories and loads/validates the YAML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigDir returns the directory holding gprt's configuration file,
// following gh's precedence: $GPRT_CONFIG_DIR, then
// $XDG_CONFIG_HOME/gprt, then $HOME/.config/gprt.
//
// $GPRT_CONFIG_DIR is gprt's own variable: if it is set but not an
// absolute path, that is a misconfiguration and is reported as an error
// rather than silently falling back to the next precedence level.
// $XDG_CONFIG_HOME, by contrast, is governed by the XDG Base Directory
// specification, which says a relative value "is invalid... should be
// ignored" — so a non-absolute $XDG_CONFIG_HOME is silently treated as
// unset here, matching every other tool that follows the spec.
//
// os.UserConfigDir and adrg/xdg are deliberately not used: on macOS they
// resolve to ~/Library/Application Support, which diverges from gh's own
// ~/.config/gh that gprt piggybacks on for authentication.
func ConfigDir() (string, error) {
	if v := os.Getenv("GPRT_CONFIG_DIR"); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("config: GPRT_CONFIG_DIR is set but not an absolute path: %q", v)
		}
		return v, nil
	}
	if dir := absEnv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "gprt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gprt"), nil
}

// CacheDir returns the directory holding gprt's on-disk cache, following
// gh's precedence: $XDG_CACHE_HOME/gprt, then $HOME/.cache/gprt.
//
// Per the XDG Base Directory specification, a $XDG_CACHE_HOME that is set
// but not an absolute path is invalid and is silently treated as unset,
// rather than reported as an error (gprt has no variable of its own at
// this precedence level to be stricter about).
func CacheDir() (string, error) {
	if dir := absEnv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "gprt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "gprt"), nil
}

// StateDir returns the directory holding gprt's mutable state (drafts, debug
// log), following gh's precedence: $XDG_STATE_HOME/gprt, then
// $HOME/.local/state/gprt.
//
// Per the XDG Base Directory specification, a $XDG_STATE_HOME that is set
// but not an absolute path is invalid and is silently treated as unset,
// rather than reported as an error (gprt has no variable of its own at
// this precedence level to be stricter about).
func StateDir() (string, error) {
	if dir := absEnv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "gprt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "gprt"), nil
}

// DefaultPath returns the default path to gprt's configuration file:
// "config.yaml" inside ConfigDir(). It returns ConfigDir's error unchanged
// (for example an invalid $GPRT_CONFIG_DIR) without adding context of its
// own.
func DefaultPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// absEnv returns the value of the named environment variable if it is set
// and an absolute path, or "" otherwise.
func absEnv(name string) string {
	v := os.Getenv(name)
	if v == "" || !filepath.IsAbs(v) {
		return ""
	}
	return v
}
