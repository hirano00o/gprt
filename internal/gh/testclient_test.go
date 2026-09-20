package gh

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// rewriteTransport redirects every request to base after go-gh's own
// transport layers have already run. go-gh's header round-tripper decides
// whether to attach the Authorization header by comparing the request's
// original host against the client's configured Host *before* handing the
// request down to the caller-supplied Transport, so rewriting the
// scheme/host here (rather than by pointing Options.Host itself at the test
// server) preserves that check while still routing the request to the
// fixture server.
type rewriteTransport struct {
	base *url.URL
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.base.Scheme
	req.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newTestClient starts an httptest.Server driven by handler and returns a
// Client wired to it via rewriteTransport, plus the server for cleanup.
// Callers should defer srv.Close().
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(handler)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	c, err := New(Options{
		Host:      "example.com",
		Token:     "test-token",
		Transport: rewriteTransport{base: base},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c, srv
}
