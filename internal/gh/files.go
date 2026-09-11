package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/hirano00o/gprt/internal/model"
)

// filesPerPage is the page size requested from the REST "list pull request
// files" endpoint, GitHub's maximum for that endpoint.
const filesPerPage = 100

// FilesResult is one page of a pull request's changed files.
type FilesResult struct {
	// Files is empty when NotModified is true: the caller already holds
	// the previous, still-valid page.
	Files []model.ChangedFile
	// ETag identifies this page's content for a future If-None-Match
	// revalidation. Empty when the response carried no ETag header.
	ETag string
	// NotModified reports a 304 response: the etag passed to ChangedFiles
	// is still valid, and Files is empty.
	NotModified bool
	// HasNext reports whether the response's Link header advertised a
	// rel="next" page.
	HasNext bool
	// RateLimit is derived from the response's X-RateLimit-* headers.
	RateLimit model.RateLimit
}

// restFile is the decoded shape of one element of the REST "list pull
// request files" endpoint's JSON array response.
type restFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	SHA              string `json:"sha"`
	// Patch is a pointer so a JSON response that omits the "patch" key
	// entirely (GitHub does this for binary files and files too large to
	// diff) is distinguishable from one that sets it to an empty string:
	// only the former means HasPatch should be false.
	Patch *string `json:"patch"`
}

// toModel maps one restFile to model.ChangedFile.
func (f restFile) toModel() model.ChangedFile {
	cf := model.ChangedFile{
		Path:         f.Filename,
		PreviousPath: f.PreviousFilename,
		Status:       model.FileStatus(f.Status),
		Additions:    f.Additions,
		Deletions:    f.Deletions,
		SHA:          f.SHA,
	}
	if f.Patch != nil {
		cf.Patch = *f.Patch
		cf.HasPatch = true
	}
	return cf
}

// ChangedFiles fetches one page of ref's changed files via the REST "list
// pull request files" endpoint. When etag is non-empty, it is sent as
// If-None-Match; a 304 response is reported as FilesResult{NotModified:
// true} with no Files, rather than as an error (api.RESTClient's own Do
// helpers turn a 304 into an error, which is why this method builds the
// request against the raw *http.Client in Client.rest directly instead).
func (c *Client) ChangedFiles(ctx context.Context, ref model.PRRef, page int, etag string) (FilesResult, error) {
	if page < 1 {
		page = 1
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=%d&page=%d",
		c.restBase, ref.Repo.Owner, ref.Repo.Name, ref.Number, filesPerPage, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FilesResult{}, classify(err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.rest.Do(req)
	if err != nil {
		return FilesResult{}, classify(err)
	}
	defer func() { _ = resp.Body.Close() }()

	rateLimit := rateLimitFromHeaders(resp.Header)

	if resp.StatusCode == http.StatusNotModified {
		if etag == "" {
			// A 304 is only ever a valid response to a conditional
			// request: without an If-None-Match header there was nothing
			// for the server to revalidate against, so this is a protocol
			// violation on the server's (or an intermediary's) part, not
			// "nothing changed" - treating it as a routine empty success
			// would silently discard whatever page this was meant to be.
			return FilesResult{}, &Error{
				Kind:    KindUnknown,
				Message: "unexpected 304 Not Modified response with no If-None-Match request header sent",
			}
		}
		return FilesResult{NotModified: true, ETag: etag, RateLimit: rateLimit}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FilesResult{}, classify(api.HandleHTTPError(resp))
	}

	var items []restFile
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return FilesResult{}, fmt.Errorf("gh: decode changed files response: %w", err)
	}

	files := make([]model.ChangedFile, len(items))
	for i, item := range items {
		files[i] = item.toModel()
	}

	return FilesResult{
		Files:     files,
		ETag:      resp.Header.Get("ETag"),
		HasNext:   linkHasNext(resp.Header.Get("Link")),
		RateLimit: rateLimit,
	}, nil
}

// rateLimitFromHeaders derives a model.RateLimit from a REST response's
// X-RateLimit-Remaining/X-RateLimit-Reset headers. Known is false when
// X-RateLimit-Remaining is absent (a response GitHub did not rate-limit at
// all, or a malformed/unexpected response), matching model.RateLimit's own
// documented meaning for Known.
func rateLimitFromHeaders(h http.Header) model.RateLimit {
	remaining := h.Get("X-RateLimit-Remaining")
	if remaining == "" {
		return model.RateLimit{}
	}
	rem, err := strconv.Atoi(remaining)
	if err != nil {
		return model.RateLimit{}
	}

	var resetAt time.Time
	if reset := h.Get("X-RateLimit-Reset"); reset != "" {
		if epoch, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetAt = time.Unix(epoch, 0)
		}
	}
	return model.RateLimit{Remaining: rem, ResetAt: resetAt, Known: true}
}

// linkHasNext reports whether a REST response's Link header
// (RFC 5988/8288: comma-separated "<url>; rel=\"name\"" segments) advertises
// a rel="next" page.
func linkHasNext(link string) bool {
	for _, segment := range strings.Split(link, ",") {
		if strings.Contains(segment, `rel="next"`) {
			return true
		}
	}
	return false
}
