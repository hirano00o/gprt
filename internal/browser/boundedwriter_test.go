package browser

import (
	"strings"
	"testing"
)

func TestBoundedWriter_KeepsUpToMaxBytes(t *testing.T) {
	w := newBoundedWriter(8)
	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write() = (%d, %v), want (5, nil)", n, err)
	}
	if w.String() != "hello" {
		t.Errorf("String() = %q, want %q", w.String(), "hello")
	}
}

func TestBoundedWriter_TruncatesAndNotes(t *testing.T) {
	w := newBoundedWriter(8)
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got := w.String()
	if !strings.HasPrefix(got, "01234567") {
		t.Errorf("String() = %q, want it to keep the first 8 bytes", got)
	}
	if !strings.Contains(got, "truncat") {
		t.Errorf("String() = %q, want it to note truncation", got)
	}
}

func TestBoundedWriter_TruncatesAcrossMultipleWrites(t *testing.T) {
	w := newBoundedWriter(4)
	if _, err := w.Write([]byte("ab")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := w.Write([]byte("cd")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := w.Write([]byte("ef")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got := w.String()
	if !strings.HasPrefix(got, "abcd") {
		t.Errorf("String() = %q, want it to keep the first 4 bytes across writes", got)
	}
	if !strings.Contains(got, "truncat") {
		t.Errorf("String() = %q, want it to note truncation", got)
	}
}

func TestBoundedWriter_NoTruncationNoteWhenUnderLimit(t *testing.T) {
	w := newBoundedWriter(1024)
	if _, err := w.Write([]byte("small")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := w.String(); got != "small" {
		t.Errorf("String() = %q, want %q with no truncation note", got, "small")
	}
}

func TestBoundedWriter_Reset(t *testing.T) {
	w := newBoundedWriter(4)
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	w.Reset()
	if got := w.String(); got != "" {
		t.Errorf("String() after Reset() = %q, want empty", got)
	}
	if _, err := w.Write([]byte("xy")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := w.String(); got != "xy" {
		t.Errorf("String() = %q, want %q (Reset must clear the truncated flag too)", got, "xy")
	}
}
