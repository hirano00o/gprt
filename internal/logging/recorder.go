// Package logging sets up gprt's debug logger: a file-backed slog.Logger
// when --debug is enabled, and always a ring buffer of the most recent
// error-and-above log entries for the ":messages" command.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is a single log record captured by a Recorder.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	// Attrs are the record's own key/value pairs plus any accumulated via
	// WithAttrs, with keys already qualified by their WithGroup prefix
	// (for example "component.err" for an "err" attr logged through
	// WithGroup("component")).
	Attrs []slog.Attr
}

// String renders the entry as "message key=value key=value ...", the form
// shown by the ":messages" command.
func (e Entry) String() string {
	var b strings.Builder
	b.WriteString(e.Message)
	for _, a := range e.Attrs {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
	}
	return b.String()
}

// Recorder is an slog.Handler that forwards every record to an inner
// handler while additionally keeping a fixed-size ring buffer of the most
// recent records at slog.LevelError or above, retrievable via Recent. It is
// safe for concurrent use.
type Recorder struct {
	inner slog.Handler

	// ring holds the buffer state behind a pointer so that WithAttrs and
	// WithGroup can return a Recorder wrapping a different inner handler
	// while all of them still read and write the very same buffer: a
	// plain slice field would be copied by value on clone, so appends
	// made through one clone would not be visible through another.
	ring *ringBuffer

	// attrs are the attrs accumulated so far via WithAttrs, with keys
	// already qualified by groupPrefix as it stood at each WithAttrs
	// call. groupPrefix is the group path (each segment followed by '.')
	// that WithGroup calls have built up, applied to attrs added from
	// here on: both the record's own attrs in Handle and any further
	// WithAttrs calls.
	attrs       []slog.Attr
	groupPrefix string
}

// ringBuffer is the mutex-protected ring buffer state shared by a Recorder
// and every handler derived from it via WithAttrs/WithGroup. size is fixed
// at construction and never changes, so it can be read without holding mu.
type ringBuffer struct {
	size int

	mu      sync.Mutex
	entries []Entry
	pos     int

	// writeFailOnce ensures at most one "debug log write failed" entry is
	// ever added to entries, however many times the inner handler fails:
	// otherwise a persistently broken log file would spam every other
	// slot in the ring with the same notice.
	writeFailOnce sync.Once
}

// NewRecorder wraps inner in a Recorder that keeps the last size entries at
// slog.LevelError or above.
func NewRecorder(inner slog.Handler, size int) *Recorder {
	return &Recorder{
		inner: inner,
		ring:  &ringBuffer{size: size, entries: make([]Entry, 0, size)},
	}
}

// Enabled reports whether the record should reach Handle: either the inner
// handler wants it, or it is at least slog.LevelError, in which case
// Recorder needs to see it for the ring buffer even if the inner handler
// (for example a discard handler in non-debug mode) would otherwise reject
// it.
func (r *Recorder) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelError || r.inner.Enabled(ctx, level)
}

// Handle appends the record to the ring buffer if its level is
// slog.LevelError or above, then forwards it to the inner handler only if
// the inner handler itself would have accepted it: Enabled may report true
// for records the inner handler does not want (see Enabled), and calling
// Handle on a handler for a level it rejected is outside the slog.Handler
// contract. If the inner handler's Handle returns an error, that error is
// always returned, and (the first time only) an entry describing the
// failure is also pushed into the ring, so it is visible in ":messages"
// even though it could not reach the log file.
func (r *Recorder) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= slog.LevelError {
		recordAttrs := make([]slog.Attr, 0, rec.NumAttrs())
		rec.Attrs(func(a slog.Attr) bool {
			recordAttrs = append(recordAttrs, qualify(r.groupPrefix, a))
			return true
		})
		attrs := make([]slog.Attr, 0, len(r.attrs)+len(recordAttrs))
		attrs = append(attrs, r.attrs...)
		attrs = append(attrs, recordAttrs...)
		r.ring.push(Entry{Time: rec.Time, Level: rec.Level, Message: rec.Message, Attrs: attrs})
	}

	if !r.inner.Enabled(ctx, rec.Level) {
		return nil
	}
	err := r.inner.Handle(ctx, rec)
	if err != nil {
		r.ring.writeFailOnce.Do(func() {
			r.ring.push(Entry{
				Time:    time.Now(),
				Level:   slog.LevelError,
				Message: fmt.Sprintf("debug log write failed: %s", err),
			})
		})
	}
	return err
}

// qualify prefixes a's key with prefix (the current group path), leaving
// the value untouched.
func qualify(prefix string, a slog.Attr) slog.Attr {
	return slog.Attr{Key: prefix + a.Key, Value: a.Value}
}

// Recent returns a copy of the currently buffered entries, oldest first.
func (r *Recorder) Recent() []Entry {
	return r.ring.snapshot()
}

// WithAttrs returns a Recorder that delegates to the inner handler's
// WithAttrs while sharing this Recorder's ring buffer and recording attrs
// (qualified by the current group prefix) so they appear in every future
// Entry from the returned handler.
func (r *Recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	qualified := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		qualified[i] = qualify(r.groupPrefix, a)
	}
	merged := make([]slog.Attr, 0, len(r.attrs)+len(qualified))
	merged = append(merged, r.attrs...)
	merged = append(merged, qualified...)
	return &Recorder{
		inner:       r.inner.WithAttrs(attrs),
		ring:        r.ring,
		attrs:       merged,
		groupPrefix: r.groupPrefix,
	}
}

// WithGroup returns a Recorder that delegates to the inner handler's
// WithGroup while sharing this Recorder's ring buffer; attrs added from
// here on (via WithAttrs or directly on a record) are qualified with name.
func (r *Recorder) WithGroup(name string) slog.Handler {
	return &Recorder{
		inner:       r.inner.WithGroup(name),
		ring:        r.ring,
		attrs:       r.attrs,
		groupPrefix: r.groupPrefix + name + ".",
	}
}

// push appends an entry to the ring buffer, evicting the oldest entry once
// the buffer reaches its configured capacity.
func (b *ringBuffer) push(e Entry) {
	if b.size == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) < b.size {
		b.entries = append(b.entries, e)
		return
	}
	b.entries[b.pos] = e
	b.pos = (b.pos + 1) % b.size
}

// snapshot returns a copy of the buffered entries, oldest first.
func (b *ringBuffer) snapshot() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Entry, len(b.entries))
	if len(b.entries) < b.size {
		copy(out, b.entries)
		return out
	}
	// The buffer has wrapped: b.pos is the index of the oldest entry.
	n := copy(out, b.entries[b.pos:])
	copy(out[n:], b.entries[:b.pos])
	return out
}
