package gh

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestClassify_Nil(t *testing.T) {
	if got := classify(nil); got != nil {
		t.Fatalf("classify(nil) = %v, want nil", got)
	}
}

func TestClassify_ContextErrorsPassThroughUnchanged(t *testing.T) {
	tests := []error{context.Canceled, context.DeadlineExceeded}
	for _, want := range tests {
		if got := classify(want); got != want {
			t.Errorf("classify(%v) = %v, want unchanged", want, got)
		}
	}
}

func TestClassify_NetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"url.Error", &url.Error{Op: "Get", URL: "https://example.com", Err: errors.New("boom")}},
		{"net.Error", &timeoutError{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.err)
			var ghErr *Error
			if !errors.As(got, &ghErr) {
				t.Fatalf("classify(%v) = %v (%T), want *Error", tc.err, got, got)
			}
			if ghErr.Kind != KindNetwork {
				t.Errorf("Kind = %v, want %v", ghErr.Kind, KindNetwork)
			}
			if !errors.Is(got, tc.err) {
				t.Errorf("classify result does not wrap the original error")
			}
		})
	}
}

func TestClassify_HTTPError(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		headers        http.Header
		wantKind       Kind
		wantRetryAfter time.Duration
	}{
		{"401 is auth", http.StatusUnauthorized, nil, KindAuth, 0},
		{"403 without rate limit headers is auth", http.StatusForbidden, http.Header{}, KindAuth, 0},
		{
			"403 with zero remaining is rate limited",
			http.StatusForbidden,
			http.Header{"X-Ratelimit-Remaining": []string{"0"}, "Retry-After": []string{"30"}},
			KindRateLimited,
			30 * time.Second,
		},
		{
			"403 with retry-after but nonzero remaining is rate limited",
			http.StatusForbidden,
			http.Header{"X-Ratelimit-Remaining": []string{"10"}, "Retry-After": []string{"5"}},
			KindRateLimited,
			5 * time.Second,
		},
		{"404 is not found", http.StatusNotFound, nil, KindNotFound, 0},
		{"422 is validation", http.StatusUnprocessableEntity, nil, KindValidation, 0},
		{"500 is server", http.StatusInternalServerError, nil, KindServer, 0},
		{"503 is server", http.StatusServiceUnavailable, nil, KindServer, 0},
		{"418 is unknown", http.StatusTeapot, nil, KindUnknown, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			httpErr := &api.HTTPError{StatusCode: tc.status, Headers: tc.headers, Message: "boom"}
			got := classify(httpErr)
			var ghErr *Error
			if !errors.As(got, &ghErr) {
				t.Fatalf("classify() = %v (%T), want *Error", got, got)
			}
			if ghErr.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", ghErr.Kind, tc.wantKind)
			}
			if ghErr.RetryAfter != tc.wantRetryAfter {
				t.Errorf("RetryAfter = %v, want %v", ghErr.RetryAfter, tc.wantRetryAfter)
			}
			if !errors.Is(got, httpErr) {
				t.Errorf("classify result does not wrap the original error")
			}
		})
	}
}

func TestClassify_HTTPError_RateLimitFromResetHeader(t *testing.T) {
	reset := time.Now().Add(45 * time.Second).Unix()
	httpErr := &api.HTTPError{
		StatusCode: http.StatusForbidden,
		Headers: http.Header{
			"X-Ratelimit-Remaining": []string{"0"},
			"X-Ratelimit-Reset":     []string{itoa(reset)},
		},
	}
	got := classify(httpErr)
	var ghErr *Error
	if !errors.As(got, &ghErr) {
		t.Fatalf("classify() = %v (%T), want *Error", got, got)
	}
	if ghErr.Kind != KindRateLimited {
		t.Fatalf("Kind = %v, want %v", ghErr.Kind, KindRateLimited)
	}
	if ghErr.RetryAfter <= 0 || ghErr.RetryAfter > 46*time.Second {
		t.Errorf("RetryAfter = %v, want roughly 45s", ghErr.RetryAfter)
	}
}

func TestClassify_GraphQLError(t *testing.T) {
	tests := []struct {
		name     string
		errType  string
		wantKind Kind
	}{
		{"NOT_FOUND", "NOT_FOUND", KindNotFound},
		{"RATE_LIMITED", "RATE_LIMITED", KindRateLimited},
		{"FORBIDDEN", "FORBIDDEN", KindAuth},
		{"INSUFFICIENT_SCOPES", "INSUFFICIENT_SCOPES", KindAuth},
		{"unrecognised type", "SOMETHING_ELSE", KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gqlErr := &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Message: "nope", Type: tc.errType}}}
			got := classify(gqlErr)
			var ghErr *Error
			if !errors.As(got, &ghErr) {
				t.Fatalf("classify() = %v (%T), want *Error", got, got)
			}
			if ghErr.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", ghErr.Kind, tc.wantKind)
			}
		})
	}
}

func TestClassify_GraphQLError_JoinsMessagesWhenUnknown(t *testing.T) {
	gqlErr := &api.GraphQLError{Errors: []api.GraphQLErrorItem{
		{Message: "first problem", Type: "SOMETHING"},
		{Message: "second problem", Type: "SOMETHING"},
	}}
	got := classify(gqlErr)
	var ghErr *Error
	if !errors.As(got, &ghErr) {
		t.Fatalf("classify() = %v (%T), want *Error", got, got)
	}
	if ghErr.Message != "first problem; second problem" {
		t.Errorf("Message = %q, want joined messages", ghErr.Message)
	}
}

func TestClassify_UnknownError(t *testing.T) {
	orig := errors.New("mystery")
	got := classify(orig)
	var ghErr *Error
	if !errors.As(got, &ghErr) {
		t.Fatalf("classify() = %v (%T), want *Error", got, got)
	}
	if ghErr.Kind != KindUnknown {
		t.Errorf("Kind = %v, want %v", ghErr.Kind, KindUnknown)
	}
	if !errors.Is(got, orig) {
		t.Errorf("classify result does not wrap the original error")
	}
}

func TestError_ErrorAndUnwrap(t *testing.T) {
	orig := errors.New("root cause")
	err := &Error{Kind: KindAuth, Message: "auth failed", Err: orig}
	if err.Unwrap() != orig {
		t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), orig)
	}
	if err.Error() == "" {
		t.Error("Error() must not be empty")
	}
}

// timeoutError is a minimal net.Error for TestClassify_NetworkErrors.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
