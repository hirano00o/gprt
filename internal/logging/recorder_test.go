package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorder_LevelFiltering(t *testing.T) {
	rec := NewRecorder(slog.DiscardHandler, 50)
	logger := slog.New(rec)

	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	got := rec.Recent()
	if len(got) != 1 {
		t.Fatalf("Recent() has %d entries, want 1 (only Error level should be recorded)", len(got))
	}
	if got[0].Message != "error message" {
		t.Errorf("Recent()[0].Message = %q, want %q", got[0].Message, "error message")
	}
	if got[0].Level != slog.LevelError {
		t.Errorf("Recent()[0].Level = %v, want %v", got[0].Level, slog.LevelError)
	}
	if got[0].Time.IsZero() {
		t.Error("Recent()[0].Time is zero, want a timestamp")
	}
}

func TestRecorder_RingBufferEviction(t *testing.T) {
	rec := NewRecorder(slog.DiscardHandler, 3)
	logger := slog.New(rec)

	for i := range 5 {
		logger.Error(fmt.Sprintf("message-%d", i))
	}

	got := rec.Recent()
	if len(got) != 3 {
		t.Fatalf("Recent() has %d entries, want 3 (ring buffer size)", len(got))
	}
	// The oldest two entries (message-0, message-1) should have been
	// evicted; the three most recent remain, oldest first.
	wantMessages := []string{"message-2", "message-3", "message-4"}
	for i, e := range got {
		if e.Message != wantMessages[i] {
			t.Errorf("Recent()[%d].Message = %q, want %q", i, e.Message, wantMessages[i])
		}
	}
}

func TestRecorder_DelegatesToInnerHandler(t *testing.T) {
	inner := &countingHandler{}
	rec := NewRecorder(inner, 50)
	logger := slog.New(rec)

	logger.Info("hello")
	logger.Error("boom")

	if inner.count != 2 {
		t.Errorf("inner handler saw %d records, want 2 (every record forwarded regardless of ring buffer)", inner.count)
	}
}

func TestRecorder_WithAttrsAndWithGroupDelegateAndShareRing(t *testing.T) {
	inner := &countingHandler{}
	rec := NewRecorder(inner, 50)

	withAttrs := rec.WithAttrs([]slog.Attr{slog.String("k", "v")})
	withGroup := withAttrs.WithGroup("g")

	logger := slog.New(withGroup)
	logger.Error("boom")

	if inner.count != 1 {
		t.Errorf("inner handler saw %d records, want 1", inner.count)
	}

	got := rec.Recent()
	if len(got) != 1 || got[0].Message != "boom" {
		t.Errorf("Recent() = %+v, want a single 'boom' entry (ring is shared across WithAttrs/WithGroup)", got)
	}
}

func TestRecorder_Handle_CapturesRecordAttrs(t *testing.T) {
	rec := NewRecorder(slog.DiscardHandler, 50)
	logger := slog.New(rec)

	logger.Error("failed", "err", errors.New("boom"))

	got := rec.Recent()
	if len(got) != 1 {
		t.Fatalf("Recent() has %d entries, want 1", len(got))
	}
	if len(got[0].Attrs) != 1 || got[0].Attrs[0].Key != "err" {
		t.Fatalf("Attrs = %+v, want a single %q attr", got[0].Attrs, "err")
	}
	if s := got[0].String(); !strings.Contains(s, "err=boom") {
		t.Errorf("String() = %q, want it to contain %q", s, "err=boom")
	}
}

func TestRecorder_Handle_CapturesWithAttrsAndWithGroupQualifiedKeys(t *testing.T) {
	rec := NewRecorder(slog.DiscardHandler, 50)
	grouped := rec.WithGroup("component").WithAttrs([]slog.Attr{slog.String("name", "gh")})
	logger := slog.New(grouped)

	logger.Error("failed", "err", errors.New("boom"))

	got := rec.Recent()
	if len(got) != 1 {
		t.Fatalf("Recent() has %d entries, want 1", len(got))
	}
	s := got[0].String()
	if !strings.Contains(s, "component.name=gh") {
		t.Errorf("String() = %q, want it to contain %q (WithAttrs before the record)", s, "component.name=gh")
	}
	if !strings.Contains(s, "component.err=boom") {
		t.Errorf("String() = %q, want it to contain %q (record attrs inherit the active group)", s, "component.err=boom")
	}
}

func TestRecorder_Handle_InnerWriteFailureRecordedOnce(t *testing.T) {
	inner := &erroringHandler{}
	rec := NewRecorder(inner, 50)
	logger := slog.New(rec)

	logger.Error("first")
	logger.Error("second")

	if inner.calls != 2 {
		t.Fatalf("inner handler saw %d calls, want 2", inner.calls)
	}

	got := rec.Recent()
	var failureNotices int
	for _, e := range got {
		if strings.Contains(e.Message, "debug log write failed") {
			failureNotices++
		}
	}
	if failureNotices != 1 {
		t.Errorf("found %d 'debug log write failed' entries, want exactly 1 (recorded once per Recorder)", failureNotices)
	}
	if len(got) != 3 {
		t.Errorf("Recent() has %d entries, want 3 (first, second, one failure notice)", len(got))
	}
}

func TestRecorder_Handle_ReturnsInnerErrorEveryTime(t *testing.T) {
	inner := &erroringHandler{}
	rec := NewRecorder(inner, 50)

	r := slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)
	if err := rec.Handle(context.Background(), r); err == nil {
		t.Error("Handle() error = nil, want the inner handler's error")
	}
	if err := rec.Handle(context.Background(), r); err == nil {
		t.Error("Handle() error = nil (second call), want the inner handler's error every time, not just once")
	}
}

func TestRecorder_ConcurrentHandleAndWithAttrs(t *testing.T) {
	rec := NewRecorder(slog.DiscardHandler, 50)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := rec.WithAttrs([]slog.Attr{slog.String("k", "v")})
			logger := slog.New(h)
			for range 20 {
				logger.Error("boom")
			}
		}()
	}
	wg.Wait()
}

// erroringHandler is a minimal slog.Handler whose Handle always fails,
// used to verify Recorder records (once) and still propagates (every
// time) a write failure from the wrapped handler.
type erroringHandler struct {
	calls int
}

func (h *erroringHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *erroringHandler) Handle(context.Context, slog.Record) error {
	h.calls++
	return errors.New("disk full")
}

func (h *erroringHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *erroringHandler) WithGroup(string) slog.Handler { return h }

// countingHandler is a minimal slog.Handler that counts how many records it
// receives, used to assert that Recorder forwards every record to the
// wrapped handler.
type countingHandler struct {
	count int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.count++
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }
