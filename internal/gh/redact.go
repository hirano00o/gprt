package gh

import (
	"regexp"
	"strings"
)

// redactedPlaceholder replaces every credential redact removes.
const redactedPlaceholder = "[redacted]"

// authHeaderValueRE matches an "Authorization: token …" / "Authorization:
// Bearer …" header line's whole value, scheme included (group 1 captures
// everything up to and including "Authorization:" and its separating
// whitespace; the scheme and credential are left out of the capture so the
// entire value can be dropped and replaced by redactedPlaceholder).
var authHeaderValueRE = regexp.MustCompile(`(?i)(Authorization\s*:\s*)(?:token|bearer)\s+\S+`)

// redact returns chunk with any "Authorization: token …"/"Authorization:
// Bearer …" header line's credential replaced by "[redacted]", and every
// literal occurrence of token (gprt's own configured GitHub auth token,
// when non-empty) replaced the same way. It has no dependency on *Client
// or slog, so it is unit-tested directly against string fixtures.
//
// redact processes chunk line by line even though, in the one place it is
// used (slogWriter, wrapping go-gh's HTTP request/response logging), it
// never strictly needs to: go-gh's http_client.go builds its
// httpretty.Logger without setting Flusher, whose zero value is
// httpretty.NoBuffer; with NoBuffer, every print/printf/println call in
// httpretty's printer.go writes its fully-formatted output to the
// io.Writer in a single Write call, and printHeaders (same file) renders
// one complete "key: value" header line per printf call — so an
// Authorization header's name and its credential are never split across
// two Write calls in practice (verified against
// github.com/henvic/httpretty@v0.2.0/printer.go). Matching line by line
// rather than relying on that keeps it from being load-bearing: if either
// library ever changed how it buffers or flushes output, this would
// degrade to "redact whatever landed together in one chunk" rather than
// silently redacting nothing.
func redact(token, chunk string) string {
	lines := strings.Split(chunk, "\n")
	for i, line := range lines {
		lines[i] = authHeaderValueRE.ReplaceAllString(line, "${1}"+redactedPlaceholder)
	}
	result := strings.Join(lines, "\n")

	if token != "" {
		result = strings.ReplaceAll(result, token, redactedPlaceholder)
	}
	return result
}
