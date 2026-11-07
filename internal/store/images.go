// Package store's image state (see Image) loads the images embedded in the
// open pull request's text so the UI can draw them.
//
// GitHub's own image URLs are not directly usable: an upload's URL needs a
// browser session when the repository is private. The GraphQL bodyHTML of the
// same text carries a rewritten URL that needs no authentication (see package
// inlineimg), so the first Image call for a pull request fetches the
// bodyHTML of every text in it that mentions an image, in one batch, and
// resolves each image URL against it. The images themselves are then
// downloaded on demand, a few at a time, by a fetcher that is never given
// GitHub credentials.
//
// A resolved URL is only good for about five minutes, so a resolution is
// refreshed (imageResolutionTTL) before it is used for a download once it is
// older than that, and again when a src it does not know turns up after a
// while (imageMissRetryAfter), which is what an edited text looks like. A
// download that fails for a reason a fresh URL may cure (a network error, an
// HTTP error status) is tried once more after re-resolving; failures of the
// content itself are final. Every retry is rate-limited by the age of the
// resolution or by a per-src attempt counter, so redrawing never loops.
//
// Image is called while the UI draws, so it never emits an event itself: it
// reports "loading" and starts the work, and each result arrives through
// Dispatch as EventImagesChanged, on which the UI rebuilds and asks again.
package store

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hirano00o/gprt/internal/inlineimg"
	"github.com/hirano00o/gprt/internal/model"
)

const (
	// imageCacheSize bounds the decoded images kept in memory. Each is up to
	// inlineimg.MaxWidth pixels wide (a 1200x800 one is 3.8 MB), so 48
	// keeps the cache around 200 MB at most. A pull request showing more than this at once
	// would re-fetch evicted ones as they are redrawn.
	imageCacheSize = 48
	// imageFetchWorkers bounds concurrent image downloads: a decode holds up
	// to 64 MB (the 16 MP pixel limit) plus its encoded bytes.
	imageFetchWorkers = 2
	// imageResolutionTTL is how old a resolution may be when a download
	// starts: signed URLs expire after about five minutes, so one minute of
	// margin covers the download itself.
	imageResolutionTTL = 4 * time.Minute
	// imageMissRetryAfter is how old a resolution must be before a src it
	// does not contain makes it worth fetching the bodyHTML again.
	imageMissRetryAfter = 30 * time.Second
)

// imageMentionRe is a cheap test for "this text may contain an image": a
// Markdown image or an HTML <img> tag. It over-approximates (a code span
// qualifies); the cost of a false positive is one more id in the batch.
var imageMentionRe = regexp.MustCompile(`(?i)<img\b|!\[`)

// ImageFetcher downloads and decodes one image. Implementations must not
// attach GitHub credentials to the request (see inlineimg.Fetcher).
type ImageFetcher interface {
	Fetch(ctx context.Context, url string) (inlineimg.Image, error)
}

// ImageStatus is the state of one image in an ImageState.
type ImageStatus int

// Known image statuses.
const (
	// ImageNone: images are disabled or no pull request is open.
	ImageNone ImageStatus = iota
	// ImageLoading: the image's URL or pixels are still being fetched.
	ImageLoading
	// ImageLoaded: ImageState.Image holds the pixels.
	ImageLoaded
	// ImageFailed: the image cannot be shown (unresolvable, unsupported, or
	// a failed download); it is final for this session, apart from the
	// bounded re-resolution described in the package comment.
	ImageFailed
)

// ImageState is Image's answer for one image.
type ImageState struct {
	Status ImageStatus
	// Image is set only when Status is ImageLoaded.
	Image inlineimg.Image
}

type subjectPhase int

const (
	subjectResolving subjectPhase = iota
	subjectResolved
	subjectFailed
)

// imageSubject is one text (pull request body, comment, review) whose
// bodyHTML is being or has been fetched.
type imageSubject struct {
	phase    subjectPhase
	resolver inlineimg.Resolver
	// at is when phase last left subjectResolving.
	at time.Time
}

// imageState is the Store's image bookkeeping, owned by the UI goroutine.
type imageState struct {
	// gen is bumped when the open pull request changes, so a batch started
	// for the previous one is dropped on arrival.
	gen int
	// subjects is keyed by node ID and holds the current pull request's
	// texts only (it is reset on every OpenPR/ClosePR, which also means a
	// failed batch is retried when the pull request is opened again).
	subjects map[string]imageSubject
	// fetching holds the original srcs with a download in flight, with the
	// subject whose resolution supplied the URL; it survives a pull request
	// switch, since a finished download is cached by src and useful to
	// whichever pull request asks next.
	fetching map[string]string
	// failedOnce counts srcs whose download failed transiently; the second
	// such failure is final.
	failedOnce map[string]bool
	// missed holds the srcs that already cost a re-resolution by being
	// absent from their text's bodyHTML; it is reset per pull request, so a
	// src that never resolves (e.g. a relative path) re-resolves only once
	// per visit instead of every imageMissRetryAfter.
	missed map[string]bool
	cache  *imageCache
	sem    chan struct{}
}

func newImageState() imageState {
	return imageState{
		subjects:   map[string]imageSubject{},
		fetching:   map[string]string{},
		failedOnce: map[string]bool{},
		missed:     map[string]bool{},
		cache:      newImageCache(imageCacheSize),
		sem:        make(chan struct{}, imageFetchWorkers),
	}
}

// imageCache holds decoded images keyed by the src written in the text,
// evicting the oldest beyond max, plus the srcs that failed. Failures are
// tiny and kept for the session so a broken image is never retried.
type imageCache struct {
	max    int
	loaded map[string]inlineimg.Image
	order  []string
	failed map[string]struct{}
}

func newImageCache(maxLoaded int) *imageCache {
	return &imageCache{max: maxLoaded, loaded: map[string]inlineimg.Image{}, failed: map[string]struct{}{}}
}

func (c *imageCache) get(src string) (ImageState, bool) {
	if img, ok := c.loaded[src]; ok {
		return ImageState{Status: ImageLoaded, Image: img}, true
	}
	if _, ok := c.failed[src]; ok {
		return ImageState{Status: ImageFailed}, true
	}
	return ImageState{}, false
}

func (c *imageCache) putLoaded(src string, img inlineimg.Image) {
	if _, ok := c.loaded[src]; !ok {
		c.order = append(c.order, src)
		for len(c.order) > c.max {
			delete(c.loaded, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.loaded[src] = img
}

func (c *imageCache) putFailed(src string) {
	c.failed[src] = struct{}{}
}

// resetImages forgets the per-pull-request bookkeeping; see imageState.
func (s *Store) resetImages() {
	s.images.gen++
	s.images.subjects = map[string]imageSubject{}
	s.images.missed = map[string]bool{}
}

// Image reports the state of the image written as src in the text identified
// by subjectID (the node ID of the pull request, comment, review, or review
// comment it appears in), starting whatever load it still needs. It is meant
// to be called repeatedly, from the drawing code, and cheap once settled.
func (s *Store) Image(subjectID, src string) ImageState {
	if s.deps.Images == nil {
		return ImageState{}
	}
	if st, ok := s.images.cache.get(src); ok {
		return st
	}
	if s.current == nil || s.currentPR == nil {
		return ImageState{}
	}

	sub, ok := s.images.subjects[subjectID]
	if !ok {
		s.startImageBatch(subjectID)
		return ImageState{Status: ImageLoading}
	}
	switch sub.phase {
	case subjectResolving:
		return ImageState{Status: ImageLoading}
	case subjectFailed:
		return ImageState{Status: ImageFailed}
	}
	if _, inFlight := s.images.fetching[src]; inFlight {
		return ImageState{Status: ImageLoading}
	}
	age := s.deps.Now().Sub(sub.at)
	url, ok := sub.resolver.URL(src)
	if !ok {
		if age > imageMissRetryAfter && !s.images.missed[src] {
			s.images.missed[src] = true
			s.reresolveSubject(subjectID)
			return ImageState{Status: ImageLoading}
		}
		return ImageState{Status: ImageFailed}
	}
	if age > imageResolutionTTL {
		s.reresolveSubject(subjectID)
		return ImageState{Status: ImageLoading}
	}
	s.startImageFetch(src, url, subjectID)
	return ImageState{Status: ImageLoading}
}

// reresolveSubject drops a subject's resolution and fetches its bodyHTML
// again; the next Image call for it finds the new one.
func (s *Store) reresolveSubject(subjectID string) {
	delete(s.images.subjects, subjectID)
	s.startImageBatch(subjectID)
}

// imageSubjectIDs lists the node IDs of the current pull request's texts that
// mention an image and have not been requested yet.
func (s *Store) imageSubjectIDs() []string {
	var ids []string
	add := func(id, body string) {
		if _, seen := s.images.subjects[id]; id != "" && !seen && imageMentionRe.MatchString(body) {
			ids = append(ids, id)
		}
	}
	pr := s.currentPR
	add(pr.ID, pr.Body)
	for _, item := range pr.Timeline {
		switch {
		case item.IssueComment != nil:
			add(item.IssueComment.ID, item.IssueComment.Body)
		case item.Review != nil:
			add(item.Review.ID, item.Review.Body)
		}
	}
	for _, t := range pr.ReviewThreads {
		for _, c := range t.Comments {
			add(c.ID, c.Body)
		}
	}
	return ids
}

// startImageBatch fetches the bodyHTML of every not-yet-requested text of the
// current pull request that mentions an image, plus asked itself (whose own
// text evidently does, whatever imageMentionRe thinks), in one go.
func (s *Store) startImageBatch(asked string) {
	ids := s.imageSubjectIDs()
	if !containsString(ids, asked) {
		ids = append(ids, asked)
	}
	for _, id := range ids {
		s.images.subjects[id] = imageSubject{phase: subjectResolving}
	}

	gen := s.images.gen
	ctx := s.baseCtx
	go func() {
		var html map[string]string
		var rl model.RateLimit
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: body_html: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "body_html", "err", err)
			}
			s.deps.Dispatch(func() { s.applyImageBatch(gen, ids, html, rl, err) })
		}()
		html, rl, err = s.deps.GitHub.BodyHTML(ctx, ids)
	}()
}

// applyImageBatch records one batch's bodyHTML. A batch for a pull request
// that is no longer open is dropped.
func (s *Store) applyImageBatch(gen int, ids []string, html map[string]string, rl model.RateLimit, err error) {
	if gen != s.images.gen {
		return
	}
	if err != nil {
		s.deps.Logger.Error("load image html failed", "err", err)
	} else {
		s.setRateLimit(rl)
	}
	now := s.deps.Now()
	for _, id := range ids {
		if err != nil {
			s.images.subjects[id] = imageSubject{phase: subjectFailed, at: now}
			continue
		}
		// A missing id (deleted since) resolves nothing, so its images fail.
		s.images.subjects[id] = imageSubject{phase: subjectResolved, resolver: inlineimg.Resolve(html[id]), at: now}
	}
	s.emit(Event{Kind: EventImagesChanged})
}

// startImageFetch downloads url in the background; at most imageFetchWorkers
// run at once.
func (s *Store) startImageFetch(src, url, subjectID string) {
	s.images.fetching[src] = subjectID
	ctx := s.baseCtx
	fetcher := s.deps.Images
	sem := s.images.sem

	go func() {
		var img inlineimg.Image
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("store: image fetch: panic: %v", r)
				s.deps.Logger.Error("goroutine panic", "op", "image_fetch", "err", err)
			}
			cancelled := ctx.Err() != nil
			s.deps.Dispatch(func() { s.applyImage(src, subjectID, img, err, cancelled) })
		}()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			err = ctx.Err()
			return
		}
		img, err = fetcher.Fetch(ctx, url)
	}()
}

// applyImage records one download's outcome. A failure of the content itself
// (an unsupported or oversized image) is final. Any other failure is most
// likely an expired or refused URL, so the first one drops the subject's
// resolution: the rebuild this event triggers asks again, which re-resolves
// it and downloads once more with a fresh URL. The second such failure is
// final too, which bounds the retries however often the UI redraws.
//
// Only the src is logged, never the signed URL, whose query string is a
// bearer token (and inlineimg's errors do not contain it either).
func (s *Store) applyImage(src, subjectID string, img inlineimg.Image, err error, cancelled bool) {
	delete(s.images.fetching, src)
	if cancelled {
		return
	}
	switch {
	case err == nil:
		s.images.cache.putLoaded(src, img)
	case inlineimg.IsPermanent(err) || s.images.failedOnce[src]:
		s.deps.Logger.Warn("load image failed", "src", src, "err", err)
		s.images.cache.putFailed(src)
	default:
		s.deps.Logger.Info("load image failed, trying again with a fresh URL", "src", src, "err", err)
		s.images.failedOnce[src] = true
		delete(s.images.subjects, subjectID)
	}
	s.emit(Event{Kind: EventImagesChanged})
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
