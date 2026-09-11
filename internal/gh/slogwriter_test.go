package gh

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// captureHandler is a minimal slog.Handler that records every record's
// message, so tests can assert on what actually reached the logger.
type captureHandler struct {
	messages *[]string
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.messages = append(*h.messages, r.Message)
	return nil
}

func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

func TestSlogWriter_RedactsBeforeLogging(t *testing.T) {
	var messages []string
	logger := slog.New(captureHandler{messages: &messages})
	w := slogWriter{logger: logger, token: "ghp_secret123"}

	chunk := "> Authorization: token ghp_secret123\n"
	n, err := w.Write([]byte(chunk))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(chunk) {
		t.Errorf("Write() n = %d, want %d", n, len(chunk))
	}

	if len(messages) != 1 {
		t.Fatalf("logger received %d messages, want 1", len(messages))
	}
	if strings.Contains(messages[0], "ghp_secret123") {
		t.Errorf("logged message still contains the token: %q", messages[0])
	}
	want := "> Authorization: [redacted]"
	if messages[0] != want {
		t.Errorf("logged message = %q, want %q", messages[0], want)
	}
}
