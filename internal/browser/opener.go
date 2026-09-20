// Package browser opens URLs in the user's web browser, resolving which
// browser to launch the way gprt wants (GPRT_BROWSER, then the config
// file, then $BROWSER) rather than relying on cli/browser's own resolution,
// which does not consult $BROWSER at all.
package browser

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	clibrowser "github.com/cli/browser"

	"github.com/hirano00o/gprt/internal/shellwords"
)

// launcherWaitDelay bounds how long Run's background Wait waits for the
// launched process's stdout/stderr pipes to close after the process itself
// exits. Without it, a launcher that forks a long-lived child (common for
// GUI browsers, which fork-and-detach so the launcher command can return
// immediately) could inherit the pipe and hold it open for as long as the
// browser stays running, delaying OnExit until the user closes the browser
// window — the opposite of "report the launch failure promptly".
const launcherWaitDelay = 5 * time.Second

// Opener opens URLs in a browser. Its fields are exported so tests can
// substitute fakes for Env, Run, and Fallback; production code should
// construct an Opener with New.
type Opener struct {
	// Config is the "browser" value from gprt's configuration file.
	Config string
	// Env looks up an environment variable by name, returning "" if
	// unset.
	Env func(name string) string
	// Run launches a specific browser command with the given arguments
	// (the URL is appended as the last argument). It returns as soon as
	// the process has started; Open does not wait for it to exit.
	Run func(name string, args ...string) error
	// Fallback opens a URL when no launcher could be resolved from
	// GPRT_BROWSER, Config, or $BROWSER.
	Fallback func(rawURL string) error
	// OnExit is called from a background goroutine when a launcher process
	// started by the default Run (see New) exits with a non-zero status;
	// the error includes any trimmed, size-capped stderr the process
	// wrote. It is not invoked for a zero exit, and tests that substitute
	// their own Run never trigger it. A nil OnExit silently discards
	// launcher exit failures — New requires callers to make that choice
	// explicitly rather than opting out by omission.
	OnExit func(err error)
}

// New returns an Opener wired to the real environment: os.Getenv, os/exec
// to launch a configured browser command, and cli/browser's
// platform-specific OpenURL as the fallback. onExit is stored as
// Opener.OnExit and receives a launcher's non-zero-exit error in the
// background; pass nil to discard such failures silently.
//
// cli/browser.Stdout and cli/browser.Stderr are process-global variables
// (not per-call options) in github.com/cli/browser: New points them at
// io.Discard and a private, size-bounded buffer respectively, so the
// fallback launcher never writes to gprt's own stdout/stderr (which would
// corrupt the TUI), and a failure's captured stderr is folded into the
// returned error. Because they are global, constructing a second Opener
// repoints them at that Opener's own buffer: an older Opener's Fallback,
// if it still runs afterward, keeps reading its own (now-disconnected)
// buffer, which no longer receives anything — its captured stderr quietly
// becomes empty, it does not "interleave" with the newer Opener's. gprt
// only ever constructs one Opener for the life of the process, so this is
// an accepted limitation rather than something worth a registry.
func New(configBrowser string, onExit func(error)) *Opener {
	fallbackStderr := newBoundedWriter(maxCapturedStderr)
	clibrowser.Stdout = io.Discard
	clibrowser.Stderr = fallbackStderr

	o := &Opener{
		Config: configBrowser,
		Env:    os.Getenv,
		OnExit: onExit,
		Fallback: func(rawURL string) error {
			fallbackStderr.Reset()
			if err := clibrowser.OpenURL(rawURL); err != nil {
				if s := strings.TrimSpace(fallbackStderr.String()); s != "" {
					return fmt.Errorf("browser: %w: %s", err, s)
				}
				return fmt.Errorf("browser: %w", err)
			}
			return nil
		},
	}
	o.Run = func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout = io.Discard
		stderr := newBoundedWriter(maxCapturedStderr)
		cmd.Stderr = stderr
		// Bounds Wait, not Start: without it, a launcher that forks a
		// long-lived child inheriting this pipe could delay the exit
		// report until that child (for example a GUI browser window)
		// itself closes. See the launcherWaitDelay doc comment.
		cmd.WaitDelay = launcherWaitDelay

		if err := cmd.Start(); err != nil {
			return err
		}
		// Snapshot OnExit now rather than reading o.OnExit from inside the
		// goroutine below: OnExit is an exported field a caller could
		// reassign at any time (for example right after Open returns),
		// which would otherwise race with that unsynchronized read.
		onExit := o.OnExit
		go func() {
			if err := cmd.Wait(); err != nil && onExit != nil {
				if s := strings.TrimSpace(stderr.String()); s != "" {
					err = fmt.Errorf("%w: %s", err, s)
				}
				onExit(err)
			}
		}()
		return nil
	}
	return o
}

// Open opens rawURL in a browser. rawURL must parse as an absolute
// http(s) URL; anything else (including empty, non-http(s) schemes such as
// "file:" or "javascript:", or a missing host) is rejected without
// attempting to launch anything.
//
// The launcher is resolved in order: $GPRT_BROWSER, then o.Config, then
// $BROWSER, each trimmed of surrounding whitespace so a value like a lone
// space is treated as unset rather than producing a launcher with zero
// fields. If none resolves, o.Fallback opens the URL with the OS default
// browser. Otherwise the launcher is split shell-style (so a quoted path
// containing a space, like `"/Applications/My Browser.app/.../browser"
// --flag`, stays one argument) and run with rawURL appended as the final
// argument.
func (o *Opener) Open(rawURL string) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}

	launcher := strings.TrimSpace(o.Env("GPRT_BROWSER"))
	if launcher == "" {
		launcher = strings.TrimSpace(o.Config)
	}
	if launcher == "" {
		launcher = strings.TrimSpace(o.Env("BROWSER"))
	}
	if launcher == "" {
		return o.Fallback(rawURL)
	}

	fields, err := shellwords.Split(launcher)
	if err != nil {
		return fmt.Errorf("browser: parse launcher %q: %w", launcher, err)
	}
	// A launcher that expands to nothing — or to an empty command name,
	// which is what a quoted, unset variable like "$BROWSER_CMD" yields —
	// means no launcher was really configured.
	if len(fields) == 0 || fields[0] == "" {
		return o.Fallback(rawURL)
	}

	if err := o.Run(fields[0], append(fields[1:], rawURL)...); err != nil {
		return fmt.Errorf("browser: run %q: %w", launcher, err)
	}
	return nil
}

// validateURL rejects anything that is not an absolute http or https URL.
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: invalid url %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser: unsupported scheme %q in %q", u.Scheme, rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("browser: missing host in %q", rawURL)
	}
	return nil
}
