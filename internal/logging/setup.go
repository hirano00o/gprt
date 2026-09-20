package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// recentBufferSize is the number of Error-and-above entries kept for the
// ":messages" command.
const recentBufferSize = 50

// Options configures Setup.
type Options struct {
	// Debug enables writing JSON logs to a file under Dir. When false,
	// log output is discarded (stdout is reserved for the TUI), though
	// the returned logger still records recent errors in its Recorder.
	Debug bool
	// Dir is the directory the debug log file is created in. Only used
	// when Debug is true.
	Dir string
}

// Setup builds gprt's logger. When opts.Debug is true, it writes JSON
// records to "gprt.log" inside opts.Dir (creating the directory with mode
// 0700 and the file with mode 0600, appending to any existing file);
// otherwise log output is discarded. In both cases, records at
// slog.LevelError or above are additionally kept in a ring buffer
// retrievable via the returned logger's Handler().(*Recorder).Recent().
//
// The returned close function flushes and closes the log file; it is a
// no-op when opts.Debug is false. Callers must call it before the process
// exits.
func Setup(opts Options) (*slog.Logger, func() error, error) {
	if !opts.Debug {
		handler := NewRecorder(slog.DiscardHandler, recentBufferSize)
		return slog.New(handler), func() error { return nil }, nil
	}

	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("logging: create dir %s: %w", opts.Dir, err)
	}

	path := filepath.Join(opts.Dir, "gprt.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open %s: %w", path, err)
	}

	// slog.NewJSONHandler defaults to slog.LevelInfo, which would silently
	// drop Debug records even though the whole point of --debug is to see
	// them.
	jsonHandler := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRecorder(jsonHandler, recentBufferSize)
	return slog.New(handler), file.Close, nil
}
