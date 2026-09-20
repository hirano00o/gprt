package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/goccy/go-yaml"
)

// ParseError reports a YAML parse or strict-decoding failure encountered
// while loading the configuration file. Error returns yaml.FormatError's
// human-readable, source-annotated text; Unwrap returns the underlying
// error so callers can still use errors.Is/errors.As against the cause
// (for example a *yaml.UnknownFieldError) without losing the formatted
// message shown to the user.
type ParseError struct {
	Path      string
	Err       error
	Formatted string
}

// Error returns the formatted, source-annotated parse error text.
func (e *ParseError) Error() string { return e.Formatted }

// Unwrap returns the underlying YAML error.
func (e *ParseError) Unwrap() error { return e.Err }

// Load reads and parses the configuration file at path. A missing file is
// not an error: it returns Default() unchanged, matching gprt's "every key
// is optional" design. A path that exists as a symlink but whose target
// does not (a broken symlink) is treated as a genuine error rather than
// "no config", since silently falling back to defaults there would hide a
// real misconfiguration from the user.
//
// An empty document (zero bytes, blank lines, comments only, or just a
// "---" marker) also returns Default() unchanged: goccy/go-yaml's decoder
// overwrites its destination with a zero value before reporting io.EOF for
// an empty document, so the partially-zeroed struct it leaves behind is
// discarded in favour of a fresh Default() rather than returned as-is.
//
// A file containing more than one YAML document (separated by "---") is
// rejected: the decoder would otherwise silently parse only the first
// document and ignore the rest. A single trailing "---" with nothing
// meaningful after it is not a second document (decoding it reports
// io.EOF, same as an empty file) and is accepted.
//
// Otherwise, parsing starts from Default() so that keys omitted from the
// file keep their default value. Unknown keys are rejected (strict mode)
// to catch typos early. The result is validated before it is returned.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			return Config{}, fmt.Errorf("config %s: broken symlink (target does not exist)", path)
		}
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}

	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict())
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return Default(), nil
		}
		return Config{}, &ParseError{
			Path:      path,
			Err:       err,
			Formatted: fmt.Sprintf("config %s:\n%s", path, yaml.FormatError(err, false, true)),
		}
	}

	var extra Config
	if extraErr := dec.Decode(&extra); !errors.Is(extraErr, io.EOF) {
		multiErr := errors.New("multiple YAML documents; expected exactly one")
		return Config{}, &ParseError{
			Path:      path,
			Err:       multiErr,
			Formatted: fmt.Sprintf("config %s: %s", path, multiErr),
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}

	return cfg, nil
}
