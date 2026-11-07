// Package inlineimg resolves and downloads the images embedded in pull
// request text so a terminal UI can draw them. It has no UI dependency: it
// hands back decoded pixels and leaves the drawing to the caller.
//
// A body's own image URL is usually not fetchable. GitHub inserts uploads as
// https://github.com/user-attachments/assets/<uuid>, which needs a browser
// session for a private repository, and external images may be blocked by
// mixed-content or hotlink rules. The GraphQL bodyHTML field of the same
// object rewrites each <img> to a URL that works without authentication
// (a signed private-user-images URL, or a camo proxy URL), so Resolve maps
// the URL written in the Markdown back to that rewritten one.
package inlineimg

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

var (
	imgTagRe = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	attrRe   = regexp.MustCompile(`(?is)([a-z][a-z0-9_:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	assetRe  = regexp.MustCompile(`/user-attachments/assets/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)
	uuidInRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// Resolver maps an image URL as written in a body to a fetchable URL, using
// the bodyHTML GitHub rendered for the same body.
type Resolver struct {
	byCanonical map[string]string
	byAsset     map[string]string
	bySrc       map[string]string
}

// Resolve builds a Resolver from a bodyHTML document. Attribute values are
// HTML-unescaped, so the URLs it returns are usable as they are.
func Resolve(bodyHTML string) Resolver {
	r := Resolver{
		byCanonical: map[string]string{},
		byAsset:     map[string]string{},
		bySrc:       map[string]string{},
	}
	for _, tag := range imgTagRe.FindAllString(bodyHTML, -1) {
		attrs := map[string]string{}
		for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2] + m[3])
		}
		src := attrs["src"]
		if src == "" {
			continue
		}
		if canonical := attrs["data-canonical-src"]; canonical != "" {
			r.byCanonical[canonical] = src
		}
		if id := pathUUID(src); id != "" {
			r.byAsset[id] = src
		}
		r.bySrc[src] = src
	}
	return r
}

// URL returns the fetchable URL for src, an image URL as written in the
// body. In order of preference it matches the rewritten image whose
// data-canonical-src is src, then (for a github.com upload) the one carrying
// the same asset UUID, then one whose src is src itself.
func (r Resolver) URL(src string) (string, bool) {
	if u, ok := r.byCanonical[src]; ok {
		return u, true
	}
	if m := assetRe.FindStringSubmatch(src); m != nil {
		if u, ok := r.byAsset[strings.ToLower(m[1])]; ok {
			return u, true
		}
	}
	u, ok := r.bySrc[src]
	return u, ok
}

// pathUUID returns the first UUID in rawURL's path (not its query, where a
// signed URL's token lives), lower-cased, or "" when there is none.
func pathUUID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(uuidInRe.FindString(u.Path))
}
