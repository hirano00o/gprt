package logging

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetup_DebugCreatesLogFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	logger, closeFn, err := Setup(Options{Debug: true, Dir: dir})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := closeFn(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	})

	logger.Info("hello from debug mode")

	logPath := filepath.Join(dir, "gprt.log")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", logPath, err)
	}

	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("log file mode = %o, want %o", got, 0o600)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", dir, err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Errorf("log dir mode = %o, want %o", got, 0o700)
		}
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) == 0 {
		t.Error("log file is empty, want at least one JSON record")
	}
}

func TestSetup_DebugRecordsDebugLevelMessages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	logger, closeFn, err := Setup(Options{Debug: true, Dir: dir})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := closeFn(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	})

	logger.Debug("debug-only message")

	data, err := os.ReadFile(filepath.Join(dir, "gprt.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "debug-only message") {
		t.Errorf("log file = %q, want it to contain the Debug-level record (--debug must record Debug and above)", data)
	}
}

func TestSetup_NonDebugCreatesNoFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	logger, closeFn, err := Setup(Options{Debug: false, Dir: dir})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := closeFn(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	})

	logger.Info("should be discarded")
	logger.Error("should still be recorded in the ring buffer")

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(%s) error = %v, want os.ErrNotExist (no dir created in non-debug mode)", dir, err)
	}
}

func TestSetup_RecentCapturesErrorsRegardlessOfDebug(t *testing.T) {
	for _, debug := range []bool{true, false} {
		logger, closeFn, err := Setup(Options{Debug: debug, Dir: t.TempDir()})
		if err != nil {
			t.Fatalf("Setup(Debug=%v) error = %v", debug, err)
		}

		logger.Error("boom")

		rec, ok := logger.Handler().(*Recorder)
		if !ok {
			t.Fatalf("Setup(Debug=%v) logger.Handler() is %T, want *Recorder", debug, logger.Handler())
		}
		entries := rec.Recent()
		if len(entries) != 1 || entries[0].Message != "boom" {
			t.Errorf("Setup(Debug=%v) Recent() = %+v, want a single 'boom' entry", debug, entries)
		}

		if err := closeFn(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	}
}
