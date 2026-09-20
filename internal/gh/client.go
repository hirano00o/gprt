// Package gh is gprt's GitHub API client: authentication resolved via
// go-gh, GraphQL queries embedded as .graphql documents, and responses
// mapped into internal/model types. See docs/DESIGN.md for the client's
// role in the overall architecture.
package gh

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
)

// defaultTimeout is applied when Options.Timeout is zero.
const defaultTimeout = 30 * time.Second

// Options configures a Client.
type Options struct {
	// Host is the GitHub host to talk to (for example "github.com" or a
	// GHES hostname). Empty resolves gh's own default host.
	Host string
	// Token is the GitHub authentication token. Empty resolves a token for
	// Host via go-gh's auth package (GH_TOKEN, GITHUB_TOKEN, gh's stored
	// config/keyring, or their *_ENTERPRISE_TOKEN equivalents on GHES).
	Token string
	// Timeout bounds every request. Defaults to 30 seconds.
	Timeout time.Duration
	// Logger receives HTTP request/response logs when Debug is true.
	Logger *slog.Logger
	// Debug enables verbose HTTP logging (request/response headers and
	// bodies) to Logger. The Authorization header's credential and every
	// literal occurrence of the resolved auth token are replaced with
	// "[redacted]" before anything reaches Logger (see redact), so the
	// token itself is never written to the debug log even though the rest
	// of the request (URL, other headers, the GraphQL query and
	// variables) is logged verbatim.
	Debug bool
	// Transport overrides the HTTP transport. Tests use this to redirect
	// requests to an httptest.Server; production leaves it nil.
	Transport http.RoundTripper
}

// Client is gprt's GitHub API client. It holds no mutable state and is
// safe for concurrent use.
type Client struct {
	host string
	gql  *api.GraphQLClient
	// rest is a raw *http.Client (built via api.NewHTTPClient, not
	// api.RESTClient) for the REST endpoints gprt needs: api.RESTClient's
	// helpers turn a 304 response into an error, which gprt needs to
	// observe directly to implement ETag revalidation (see files.go).
	rest *http.Client
	// restBase is the REST API base URL for host, e.g.
	// "https://api.github.com" or "https://ghes.example.com/api/v3" (see
	// restBaseURL).
	restBase string
}

// New builds a Client. Host and Token in opts are resolved via go-gh's auth
// package when left empty. New returns ErrNoToken when no token can be
// resolved for the target host, since every subsequent call would
// otherwise fail with an opaque 401.
func New(opts Options) (*Client, error) {
	host := opts.Host
	if host == "" {
		host, _ = auth.DefaultHost()
	}

	token := opts.Token
	if token == "" {
		token, _ = auth.TokenForHost(host)
	}
	if token == "" {
		return nil, ErrNoToken
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	gql, err := api.NewGraphQLClient(clientOptions(opts, host, token, timeout))
	if err != nil {
		return nil, classify(err)
	}

	rest, err := api.NewHTTPClient(restClientOptions(opts, host, token, timeout))
	if err != nil {
		return nil, classify(err)
	}

	return &Client{host: host, gql: gql, rest: rest, restBase: restBaseURL(host)}, nil
}

// Host returns the GitHub host this client talks to.
func (c *Client) Host() string {
	return c.host
}

// clientOptions builds the api.ClientOptions used to construct the
// underlying GraphQL client from opts and the already-resolved host,
// token, and timeout. Extracted from New so its LogIgnoreEnv behavior can
// be tested directly against the returned struct, without needing to
// observe go-gh's internal HTTP client/transport construction.
func clientOptions(opts Options, host, token string, timeout time.Duration) api.ClientOptions {
	clientOpts := api.ClientOptions{
		Host:        host,
		AuthToken:   token,
		Timeout:     timeout,
		Transport:   opts.Transport,
		EnableCache: false,
		// LogIgnoreEnv is always true, regardless of opts.Debug: without
		// it, go-gh's own NewHTTPClient reads the ambient GH_DEBUG
		// environment variable and, when a user's shell has it set
		// (common for gh users, independently of gprt's own --debug
		// flag), configures verbose HTTP logging straight to os.Stderr —
		// corrupting gprt's TUI, which owns the terminal. gprt's own
		// Debug flag below is the only thing allowed to turn on HTTP
		// logging, and even then only to opts.Logger, never to stderr.
		LogIgnoreEnv: true,
	}
	if opts.Debug && opts.Logger != nil {
		clientOpts.Log = slogWriter{logger: opts.Logger, token: token}
		clientOpts.LogVerboseHTTP = true
	}
	return clientOpts
}

// restClientOptions builds the api.ClientOptions used to construct the raw
// REST *http.Client: the same host/token/timeout/transport/debug-logging
// behaviour as clientOptions, but with the REST-specific Accept header
// ("application/vnd.github+json") in place of the GraphQL client's own
// default Accept header (which requests preview media types the REST
// "list pull request files" endpoint does not need).
func restClientOptions(opts Options, host, token string, timeout time.Duration) api.ClientOptions {
	restOpts := clientOptions(opts, host, token, timeout)
	restOpts.Headers = map[string]string{"Accept": "application/vnd.github+json"}
	return restOpts
}

// restBaseURL returns the REST API base URL for host: "https://api.<host>"
// for github.com and github.com-style hosts, or "https://<host>/api/v3" for
// a GitHub Enterprise Server host. This mirrors go-gh's own unexported
// restPrefix (pkg/api/rest_client.go) — reimplemented here because
// api.NewHTTPClient (used instead of api.RESTClient; see the Client.rest
// doc comment) builds a raw *http.Client with no REST path helper of its
// own. Unlike restPrefix, this does not special-case go-gh's own
// "garage.github.com"/"github.localhost" hosts: those are internal to
// go-gh's own test suite, not part of gprt's supported host set
// (github.com or a real GHES instance). It also does not honour a
// hypothetical api.ClientOptions.APIHost override — gprt's own Options has
// no such field today, and restBaseURL derives the REST endpoint purely
// from host, independent of whatever api.NewHTTPClient's own host/APIHost
// resolution might otherwise produce; this is an accepted limitation
// rather than a bug, since gprt has no current use case for APIHost.
func restBaseURL(host string) string {
	host = auth.NormalizeHostname(host)
	if auth.IsEnterprise(host) {
		return fmt.Sprintf("https://%s/api/v3", host)
	}
	return fmt.Sprintf("https://api.%s", host)
}

// slogWriter adapts an *slog.Logger to the io.Writer go-gh's ClientOptions.Log
// expects, so --debug's HTTP request/response logging lands in the same
// structured log file as everything else instead of a second, unstructured
// stream. Every chunk is passed through redact before it reaches the
// logger, so the auth token never lands on disk even in verbose mode.
type slogWriter struct {
	logger *slog.Logger
	// token is the resolved auth token, redacted out of every chunk
	// alongside any "Authorization: token …"/"Authorization: Bearer …"
	// header line (see redact's doc comment for why the header case, on
	// its own, is already believed unreachable in practice, and why this
	// still does not rely on that).
	token string
}

func (w slogWriter) Write(p []byte) (int, error) {
	w.logger.Debug(redact(w.token, strings.TrimRight(string(p), "\n")))
	return len(p), nil
}
