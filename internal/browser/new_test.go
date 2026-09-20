package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	clibrowser "github.com/cli/browser"
)

func TestNew_Run_CallsOnExitOnNonZeroExitWithoutBlockingOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on /bin/sh")
	}

	done := make(chan error, 1)
	o := New("", func(err error) { done <- err })
	o.Fallback = func(string) error {
		t.Fatal("Fallback called, want the configured launcher to be used")
		return nil
	}
	// A launcher that sleeps briefly, writes to stderr, then exits
	// non-zero: Open must return before the sleep completes (proving Run
	// does not block on Wait), and OnExit must still fire once it does.
	o.Config = `/bin/sh -c "sleep 0.2; echo boom 1>&2; exit 7"`

	start := time.Now()
	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Open() took %v, want it to return immediately after Start (not block on Wait)", elapsed)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("OnExit err = nil, want a non-zero exit error")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("OnExit err = %v, want it to include the captured stderr %q", err, "boom")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnExit was not called within the timeout")
	}
}

func TestNew_Run_NoOnExitCallOnSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on /bin/sh")
	}

	called := make(chan struct{}, 1)
	o := New("", func(error) { called <- struct{}{} })
	o.Config = `/bin/sh -c "exit 0"`

	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	select {
	case <-called:
		t.Fatal("OnExit was called after a zero exit, want it not to be called")
	case <-time.After(300 * time.Millisecond):
		// Expected: no call within the window.
	}
}

func TestNew_Run_CapsCapturedStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on /bin/sh")
	}

	done := make(chan error, 1)
	o := New("", func(err error) { done <- err })
	// A bounded loop that writes well past 4 KiB to stderr before exiting
	// non-zero. Deliberately avoids a pipeline (e.g. "yes | head"): a
	// trailing redirection like "cmd 1>&2" on the left side of a pipe is
	// applied after the pipe is wired up and silently breaks it, which
	// would leave "yes" writing to our stderr capture forever instead of
	// terminating. Single-quoted (not double-quoted) so internal/shellwords'
	// own $VAR expansion leaves "$i" and "$((i+1))" untouched for the
	// spawned /bin/sh -c to interpret itself, exactly like a real shell
	// would treat this Config value.
	o.Config = `/bin/sh -c 'i=0; while [ $i -lt 2000 ]; do echo line-$i-boom 1>&2; i=$((i+1)); done; exit 3'`

	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("OnExit err = nil, want a non-zero exit error")
		}
		if len(err.Error()) > maxCapturedStderr*2 {
			t.Errorf("OnExit err is %d bytes, want it capped near maxCapturedStderr (%d)", len(err.Error()), maxCapturedStderr)
		}
		if !strings.Contains(err.Error(), "truncat") {
			t.Errorf("OnExit err = %v, want it to note truncation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnExit was not called within the timeout")
	}
}

func TestNew_Run_SnapshotsOnExitAtCallTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on /bin/sh")
	}

	firstCalled := make(chan error, 1)
	o := New("", func(err error) { firstCalled <- err })
	o.Config = `/bin/sh -c "sleep 0.1; exit 5"`

	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Reassign OnExit right after Open returns, while the launched
	// process's background Wait is still in flight (it sleeps 0.1s
	// first). Run must have already captured the original callback into a
	// local before starting that goroutine: otherwise this write races
	// with the goroutine's read of the field (caught by -race), and/or
	// the wrong (reassigned) callback could end up being the one invoked.
	secondCalled := make(chan struct{}, 1)
	o.OnExit = func(error) { secondCalled <- struct{}{} }

	select {
	case err := <-firstCalled:
		if err == nil {
			t.Error("original OnExit err = nil, want a non-zero exit error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("original OnExit was not called within the timeout")
	}

	select {
	case <-secondCalled:
		t.Error("the reassigned OnExit was called, want only the callback captured at Open time")
	default:
	}
}

func TestNew_PointsCliBrowserStdoutAndStderrAwayFromTheProcess(t *testing.T) {
	before := clibrowser.Stdout
	defer func() { clibrowser.Stdout = before }()

	New("", nil)

	if clibrowser.Stdout == os.Stdout {
		t.Error("New() left cli/browser.Stdout pointed at os.Stdout, want it discarded (would corrupt the TUI)")
	}
	if clibrowser.Stderr == os.Stderr {
		t.Error("New() left cli/browser.Stderr pointed at os.Stderr, want it captured (would corrupt the TUI)")
	}
}

func TestNew_Fallback_IncludesCapturedStderrOnFailure(t *testing.T) {
	var providerName string
	switch runtime.GOOS {
	case "darwin":
		providerName = "open"
	case "linux":
		providerName = "xdg-open"
	default:
		t.Skipf("no fake-executable strategy for GOOS=%s", runtime.GOOS)
	}

	fakeDir := t.TempDir()
	fakePath := filepath.Join(fakeDir, providerName)
	script := "#!/bin/sh\necho fake-browser-failed 1>&2\nexit 1\n"
	if err := os.WriteFile(fakePath, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GPRT_BROWSER", "")
	t.Setenv("BROWSER", "")

	o := New("", nil)
	err := o.Open("https://example.com")
	if err == nil {
		t.Fatal("Open() error = nil, want the fake browser's failure to surface")
	}
	if !strings.Contains(err.Error(), "fake-browser-failed") {
		t.Errorf("Open() error = %v, want it to include the captured stderr", err)
	}
}
