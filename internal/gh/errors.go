package gh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// ErrNoToken is returned by New when no GitHub authentication token can be
// resolved for the target host.
var ErrNoToken = errors.New(
	"gh: no GitHub token found; run `gh auth login` or set GH_TOKEN/GITHUB_TOKEN",
)

// Kind classifies a gh.Error so callers can react without inspecting
// message text.
type Kind string

// Known error kinds.
const (
	KindAuth        Kind = "auth"
	KindRateLimited Kind = "rate_limited"
	KindNotFound    Kind = "not_found"
	KindValidation  Kind = "validation"
	KindNetwork     Kind = "network"
	KindServer      Kind = "server"
	KindUnknown     Kind = "unknown"
)

// Error is the classified form of every error internal/gh returns from a
// client call. Err holds the original error from go-gh (or the underlying
// transport) so callers can still errors.Is/errors.As against it.
type Error struct {
	Kind       Kind
	Message    string
	RetryAfter time.Duration
	Err        error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gh: %s", e.Kind)
	}
	return fmt.Sprintf("gh: %s: %s", e.Kind, e.Message)
}

// Unwrap exposes the original error for errors.Is/errors.As.
func (e *Error) Unwrap() error {
	return e.Err
}

// classify turns an error returned by a go-gh API call into a *Error, so
// every internal/gh method reports a consistent, inspectable failure mode.
//
// context.Canceled and context.DeadlineExceeded pass through unchanged:
// wrapping them would make errors.Is(err, context.Canceled) require an
// extra Unwrap hop for no benefit, and callers already check those two
// sentinels directly to distinguish "the caller gave up" from "the API
// failed".
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		return classifyHTTPError(httpErr, err)
	}

	var gqlErr *api.GraphQLError
	if errors.As(err, &gqlErr) {
		return classifyGraphQLError(gqlErr, err)
	}

	// *url.Error is deliberately not matched separately here: it always
	// implements net.Error (Timeout/Temporary), so the branch above
	// already classifies it as KindNetwork. A dedicated *url.Error branch
	// after it would be unreachable dead code.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return &Error{Kind: KindNetwork, Message: netErr.Error(), Err: err}
	}

	return &Error{Kind: KindUnknown, Message: err.Error(), Err: err}
}

// classifyHTTPError maps a go-gh *api.HTTPError (REST or the GraphQL
// endpoint's own non-2xx responses) to a *Error by status code.
func classifyHTTPError(httpErr *api.HTTPError, orig error) error {
	switch {
	case httpErr.StatusCode == http.StatusUnauthorized:
		return &Error{Kind: KindAuth, Message: httpErr.Message, Err: orig}
	case httpErr.StatusCode == http.StatusForbidden:
		if isRateLimited(httpErr.Headers) {
			return &Error{
				Kind:       KindRateLimited,
				Message:    httpErr.Message,
				RetryAfter: retryAfter(httpErr.Headers),
				Err:        orig,
			}
		}
		return &Error{Kind: KindAuth, Message: httpErr.Message, Err: orig}
	case httpErr.StatusCode == http.StatusNotFound:
		return &Error{Kind: KindNotFound, Message: httpErr.Message, Err: orig}
	case httpErr.StatusCode == http.StatusUnprocessableEntity:
		return &Error{Kind: KindValidation, Message: httpErr.Message, Err: orig}
	case httpErr.StatusCode >= http.StatusInternalServerError:
		return &Error{Kind: KindServer, Message: httpErr.Message, Err: orig}
	default:
		return &Error{Kind: KindUnknown, Message: httpErr.Message, Err: orig}
	}
}

// isRateLimited reports whether a 403 response's headers indicate a
// rate-limit rejection rather than a plain authorization failure: either
// the primary rate limit is exhausted (X-RateLimit-Remaining: 0) or the
// secondary/abuse limiter asked for a delay (Retry-After present).
func isRateLimited(h http.Header) bool {
	return h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != ""
}

// retryAfter derives how long to wait before retrying from a 403 response's
// headers: Retry-After (seconds) takes precedence when present, falling
// back to the primary rate limit's reset time (X-RateLimit-Reset, a Unix
// epoch second). Returns 0 when neither header is present or parseable.
func retryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Until(time.Unix(epoch, 0))
		}
	}
	return 0
}

// classifyGraphQLError maps a go-gh *api.GraphQLError to a *Error using the
// first recognised error item's Type. Every error message is joined into
// Message so nothing is lost when the type is unrecognised.
func classifyGraphQLError(gqlErr *api.GraphQLError, orig error) error {
	messages := make([]string, 0, len(gqlErr.Errors))
	for _, item := range gqlErr.Errors {
		messages = append(messages, item.Message)
	}
	for _, item := range gqlErr.Errors {
		switch item.Type {
		case "NOT_FOUND":
			return &Error{Kind: KindNotFound, Message: item.Message, Err: orig}
		case "RATE_LIMITED":
			return &Error{Kind: KindRateLimited, Message: item.Message, Err: orig}
		case "FORBIDDEN", "INSUFFICIENT_SCOPES":
			return &Error{Kind: KindAuth, Message: item.Message, Err: orig}
		}
	}
	return &Error{Kind: KindUnknown, Message: strings.Join(messages, "; "), Err: orig}
}
