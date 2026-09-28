package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/hirano00o/gprt/internal/model"
)

// compareFilesMax is the maximum number of files the REST "compare two
// commits" endpoint returns (GitHub's documented cap for this endpoint,
// with no pagination of its own): a response at exactly this count cannot
// be told apart from one GitHub actually cut off, hence CompareResult's own
// Truncated field.
const compareFilesMax = 300

// CompareResult is the changed files between two refs, as reported by the
// REST "compare two commits" endpoint.
type CompareResult struct {
	Files []model.ChangedFile
	// Truncated is set when the response held compareFilesMax files: the
	// endpoint has no pagination, so that count means the actual diff may
	// hold more files than were returned, indistinguishable from a diff
	// that happens to have exactly that many.
	Truncated bool
	RateLimit model.RateLimit
}

// compareResponse is the decoded shape of the REST "compare two commits"
// endpoint's JSON response, as far as CompareFiles needs it.
type compareResponse struct {
	Files []restFile `json:"files"`
}

// CompareFiles fetches the changed files between base and head in repo, via
// the REST "compare two commits" endpoint (GET
// {restBase}/repos/{owner}/{name}/compare/{base}...{head}). This is gprt's
// second (and last) use of REST rather than GraphQL: compare has no
// GraphQL equivalent that returns unified-diff patches, the same reason
// ChangedFiles (files.go) uses REST for "list pull request files" (see
// docs/DESIGN.md's decision on GraphQL-as-primary).
//
// It is built directly against the raw *http.Client in Client.rest, like
// ChangedFiles, rather than through api.RESTClient.
func (c *Client) CompareFiles(ctx context.Context, repo model.RepoRef, base, head string) (CompareResult, error) {
	reqURL := fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s",
		c.restBase, repo.Owner, repo.Name, escapeRef(base), escapeRef(head))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return CompareResult{}, classify(err)
	}

	resp, err := c.rest.Do(req)
	if err != nil {
		return CompareResult{}, classify(err)
	}
	defer func() { _ = resp.Body.Close() }()

	rateLimit := rateLimitFromHeaders(resp.Header)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompareResult{}, classify(api.HandleHTTPError(resp))
	}

	var body compareResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return CompareResult{}, fmt.Errorf("gh: decode compare response: %w", err)
	}

	files := make([]model.ChangedFile, len(body.Files))
	for i, item := range body.Files {
		files[i] = item.toModel()
	}

	return CompareResult{
		Files:     files,
		Truncated: len(files) >= compareFilesMax,
		RateLimit: rateLimit,
	}, nil
}

// escapeRef percent-encodes ref (a base or head branch name) one
// '/'-separated component at a time, preserving every literal '/' rather
// than encoding it to %2F: the compare endpoint's "{base}...{head}"
// segment is the last thing in its URL path, matched as a trailing
// wildcard and only ever split on the literal "...", so a '/' inside a
// branch name (a common naming convention, e.g. "feature/foo") is meant to
// reach GitHub unescaped - the same way it is typed directly into a
// browser's address bar. A single url.PathEscape call over the whole ref
// would instead turn every '/' into %2F, which is not reliably decoded
// back to '/' by GitHub's own routing before the "..." split happens
// (common practice for HTTP routers, which often keep %2F distinct from a
// literal '/' to avoid path-traversal ambiguity) - escaping only within
// each component sidesteps that without losing encoding of any other
// character a branch name could contain.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
