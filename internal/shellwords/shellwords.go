// Package shellwords splits a command line into argv-style fields using
// POSIX shell quoting and expansion rules, without ever touching the
// filesystem.
//
// It replaces github.com/google/shlex (archived in 2019) with
// mvdan.cc/sh/v3, the actively maintained Go shell parser, so a
// launcher/editor command configured by a user behaves the way a shell
// would: single quotes,
// double quotes, and backslash-escaping group characters into one field,
// and $VAR, ${VAR}, and a leading ~ expand against the process
// environment. Filename globbing (a*b, ~/*.go) is intentionally left
// disabled, so a launcher line can never silently expand to whatever files
// happen to match a pattern — it is always returned literally. Command
// substitution ($(...), `...`) and other non-word shell syntax are
// rejected as parse errors, since a configuration value is expected to be
// a plain command line, not a script.
package shellwords

import (
	"fmt"
	"os"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Split parses s as a single line of shell words and expands the result:
// fields are separated by unquoted whitespace, quoting groups characters
// into one field, and $VAR/${VAR}/~ expand via os.Getenv. Globbing is not
// performed. An empty or all-whitespace s returns (nil, nil). An
// unterminated quote, a command substitution, or any other shell construct
// beyond plain words and expansions is reported as an error.
func Split(s string) ([]string, error) {
	var words []*syntax.Word
	for w, err := range syntax.NewParser().WordsSeq(strings.NewReader(s)) {
		if err != nil {
			return nil, fmt.Errorf("shellwords: parse %q: %w", s, err)
		}
		words = append(words, w)
	}
	if len(words) == 0 {
		return nil, nil
	}

	cfg := &expand.Config{Env: expand.FuncEnviron(os.Getenv)}
	var fields []string
	for f, err := range expand.FieldsSeq(cfg, words...) {
		if err != nil {
			return nil, fmt.Errorf("shellwords: expand %q: %w", s, err)
		}
		fields = append(fields, f)
	}
	return fields, nil
}
