package browser

import "bytes"

// maxCapturedStderr is how much of a launched process's (or the fallback
// browser command's) stderr boundedWriter keeps. A browser or launcher
// left running (or one that is simply chatty) could otherwise write to its
// stderr pipe for hours; without a cap that would grow gprt's memory
// without bound for output nobody will ever read past the first few lines.
const maxCapturedStderr = 4 * 1024 // 4 KiB

// boundedWriter is an io.Writer that keeps only the first max bytes ever
// written to it, discarding the rest. Write always reports success for the
// whole input regardless of how much was actually kept: exec.Cmd never
// inspects a Stderr writer's returned error, so failing loudly here would
// only risk cmd.Wait erroring for a reason unrelated to the process it
// ran. Not safe for concurrent use without external synchronisation (a
// single command's stderr is only ever written by that command's own
// goroutine, so none is needed here).
type boundedWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

// newBoundedWriter returns a boundedWriter that keeps at most max bytes.
func newBoundedWriter(max int) *boundedWriter {
	return &boundedWriter{max: max}
}

// Write implements io.Writer, keeping only up to w.max bytes total across
// every call and noting whether anything was ever dropped.
func (w *boundedWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room <= 0 {
		if len(p) > 0 {
			w.truncated = true
		}
		return len(p), nil
	}

	n := len(p)
	if n > room {
		n = room
		w.truncated = true
	}
	w.buf.Write(p[:n])
	return len(p), nil
}

// String returns the captured bytes, with a truncation note appended if
// more was written than max ever kept.
func (w *boundedWriter) String() string {
	if w.truncated {
		return w.buf.String() + "... (truncated)"
	}
	return w.buf.String()
}

// Reset clears the writer back to empty, as if newly constructed.
func (w *boundedWriter) Reset() {
	w.buf.Reset()
	w.truncated = false
}
