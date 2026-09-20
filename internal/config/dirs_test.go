package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearDirEnv unsets every environment variable involved in directory
// resolution so each test starts from a known baseline.
func clearDirEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"GPRT_CONFIG_DIR",
		"XDG_CONFIG_HOME",
		"XDG_CACHE_HOME",
		"XDG_STATE_HOME",
		"HOME",
	} {
		t.Setenv(name, "")
	}
}

func TestConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("precedence rules target XDG/HOME on unix-like systems")
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    string
		homeDir string
	}{
		{
			name:    "GPRT_CONFIG_DIR takes precedence",
			env:     map[string]string{"GPRT_CONFIG_DIR": "/custom/gprt-config", "XDG_CONFIG_HOME": "/xdg/config"},
			homeDir: "/home/user",
			want:    "/custom/gprt-config",
		},
		{
			name:    "XDG_CONFIG_HOME used when GPRT_CONFIG_DIR unset",
			env:     map[string]string{"XDG_CONFIG_HOME": "/xdg/config"},
			homeDir: "/home/user",
			want:    "/xdg/config/gprt",
		},
		{
			name:    "falls back to HOME/.config/gprt",
			env:     map[string]string{},
			homeDir: "/home/user",
			want:    "/home/user/.config/gprt",
		},
		{
			name:    "non-absolute XDG_CONFIG_HOME ignored",
			env:     map[string]string{"XDG_CONFIG_HOME": "relative/path"},
			homeDir: "/home/user",
			want:    "/home/user/.config/gprt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDirEnv(t)
			t.Setenv("HOME", tc.homeDir)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := ConfigDir()
			if err != nil {
				t.Fatalf("ConfigDir() error = %v", err)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Errorf("ConfigDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigDir_NonAbsoluteGPRTConfigDirIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("precedence rules target XDG/HOME on unix-like systems")
	}

	clearDirEnv(t)
	t.Setenv("HOME", filepath.FromSlash("/home/user"))
	t.Setenv("GPRT_CONFIG_DIR", "relative/path")

	_, err := ConfigDir()
	if err == nil {
		t.Fatal("ConfigDir() error = nil, want an error naming GPRT_CONFIG_DIR")
	}
	if !strings.Contains(err.Error(), "GPRT_CONFIG_DIR") {
		t.Errorf("ConfigDir() error = %q, want it to name GPRT_CONFIG_DIR", err.Error())
	}
	if !strings.Contains(err.Error(), "relative/path") {
		t.Errorf("ConfigDir() error = %q, want it to include the offending value", err.Error())
	}
}

func TestCacheDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("precedence rules target XDG/HOME on unix-like systems")
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    string
		homeDir string
	}{
		{
			name:    "XDG_CACHE_HOME used when set",
			env:     map[string]string{"XDG_CACHE_HOME": "/xdg/cache"},
			homeDir: "/home/user",
			want:    "/xdg/cache/gprt",
		},
		{
			name:    "falls back to HOME/.cache/gprt",
			env:     map[string]string{},
			homeDir: "/home/user",
			want:    "/home/user/.cache/gprt",
		},
		{
			name:    "non-absolute XDG_CACHE_HOME ignored",
			env:     map[string]string{"XDG_CACHE_HOME": "relative"},
			homeDir: "/home/user",
			want:    "/home/user/.cache/gprt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDirEnv(t)
			t.Setenv("HOME", tc.homeDir)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := CacheDir()
			if err != nil {
				t.Fatalf("CacheDir() error = %v", err)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Errorf("CacheDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("precedence rules target XDG/HOME on unix-like systems")
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    string
		homeDir string
	}{
		{
			name:    "XDG_STATE_HOME used when set",
			env:     map[string]string{"XDG_STATE_HOME": "/xdg/state"},
			homeDir: "/home/user",
			want:    "/xdg/state/gprt",
		},
		{
			name:    "falls back to HOME/.local/state/gprt",
			env:     map[string]string{},
			homeDir: "/home/user",
			want:    "/home/user/.local/state/gprt",
		},
		{
			name:    "non-absolute XDG_STATE_HOME ignored",
			env:     map[string]string{"XDG_STATE_HOME": "relative"},
			homeDir: "/home/user",
			want:    "/home/user/.local/state/gprt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDirEnv(t)
			t.Setenv("HOME", tc.homeDir)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := StateDir()
			if err != nil {
				t.Fatalf("StateDir() error = %v", err)
			}
			if got != filepath.FromSlash(tc.want) {
				t.Errorf("StateDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDefaultPath(t *testing.T) {
	clearDirEnv(t)
	t.Setenv("HOME", filepath.FromSlash("/home/user"))

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	want := filepath.FromSlash("/home/user/.config/gprt/config.yaml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
