package gh

import (
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestNew_NoTokenForUnknownHost(t *testing.T) {
	// "gprt-test.invalid" is not a host gh (or any environment variable) has
	// a token configured for, so this is deterministic regardless of the
	// machine running the test.
	_, err := New(Options{Host: "gprt-test.invalid"})
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("New() error = %v, want ErrNoToken", err)
	}
}

func TestNew_ExplicitHostAndToken(t *testing.T) {
	c, err := New(Options{Host: "example.com", Token: "test-token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := c.Host(); got != "example.com" {
		t.Errorf("Host() = %q, want %q", got, "example.com")
	}
}

func TestClientOptions_AlwaysIgnoresGHDebugEnv(t *testing.T) {
	// GH_DEBUG is gh's own ambient env var: go-gh's NewHTTPClient reads it
	// whenever LogIgnoreEnv is not set, regardless of gprt's own Debug
	// flag, and would otherwise configure verbose HTTP logging straight
	// to os.Stderr -- corrupting the TUI, which owns the terminal.
	t.Setenv("GH_DEBUG", "api")

	opts := clientOptions(Options{}, "example.com", "test-token", defaultTimeout)
	if !opts.LogIgnoreEnv {
		t.Error("LogIgnoreEnv = false, want true regardless of GH_DEBUG or Options.Debug")
	}
	if opts.Log != nil {
		t.Errorf("Log = %v, want nil when Debug is false", opts.Log)
	}
	if opts.LogVerboseHTTP {
		t.Error("LogVerboseHTTP = true, want false when Debug is false")
	}
}

func TestClientOptions_DebugEnablesLoggingViaOptionsLoggerOnly(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	opts := clientOptions(Options{Debug: true, Logger: logger}, "example.com", "test-token", defaultTimeout)
	if !opts.LogIgnoreEnv {
		t.Error("LogIgnoreEnv = false, want true")
	}
	if opts.Log == nil {
		t.Error("Log = nil, want set when Debug is true and Logger is provided")
	}
	if !opts.LogVerboseHTTP {
		t.Error("LogVerboseHTTP = false, want true when Debug is true")
	}
}

func TestClientOptions_UsesGivenHostTokenAndTimeout(t *testing.T) {
	opts := clientOptions(Options{}, "example.com", "test-token", 7*time.Second)
	if opts.Host != "example.com" {
		t.Errorf("Host = %q, want %q", opts.Host, "example.com")
	}
	if opts.AuthToken != "test-token" {
		t.Errorf("AuthToken = %q, want %q", opts.AuthToken, "test-token")
	}
	if opts.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want %v", opts.Timeout, 7*time.Second)
	}
	if opts.EnableCache {
		t.Error("EnableCache = true, want false")
	}
}

func TestNew_DefaultTimeout(t *testing.T) {
	// A client built without an explicit Timeout must still work end to
	// end (the default is applied rather than left at zero/no-timeout, but
	// there is no exported way to inspect the resolved timeout directly,
	// so this just guards against New failing when Timeout is omitted).
	c, err := New(Options{Host: "example.com", Token: "test-token", Transport: http.DefaultTransport})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c == nil {
		t.Fatal("New() returned a nil client with no error")
	}
}
