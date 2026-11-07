package store

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/inlineimg"
	"github.com/hirano00o/gprt/internal/model"
)

const (
	imgUUID   = "0b9a8c3e-1d2f-4a5b-9c6d-7e8f9a0b1c2d"
	imgAsset  = "https://github.com/user-attachments/assets/" + imgUUID
	imgSigned = "https://private-user-images.githubusercontent.com/1/2-" + imgUUID + ".png?jwt=abc"
	imgExt    = "https://example.com/a.png"
)

func (f *fakeGitHub) BodyHTML(ctx context.Context, ids []string) (map[string]string, model.RateLimit, error) {
	f.mu.Lock()
	f.bodyHTMLCalls = append(f.bodyHTMLCalls, slices.Clone(ids))
	fn := f.bodyHTMLFunc
	f.mu.Unlock()
	if fn == nil {
		return map[string]string{}, model.RateLimit{}, nil
	}
	return fn(ctx, ids)
}

func (f *fakeGitHub) bodyHTMLCallsSnapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.bodyHTMLCalls)
}

func (f *fakeGitHub) setBodyHTMLFunc(fn func(ctx context.Context, ids []string) (map[string]string, model.RateLimit, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodyHTMLFunc = fn
}

// fakeFetcher is an ImageFetcher recording its calls. When gate is non-nil
// every Fetch waits for it to be closed.
type fakeFetcher struct {
	mu   sync.Mutex
	urls []string
	err  error
	// errAt, when set, decides the outcome of the n-th call (1-based),
	// taking precedence over err.
	errAt     func(n int) error
	gate      chan struct{}
	active    int
	maxActive int
}

func (f *fakeFetcher) Fetch(ctx context.Context, url string) (inlineimg.Image, error) {
	f.mu.Lock()
	f.urls = append(f.urls, url)
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	gate, err := f.gate, f.err
	if f.errAt != nil {
		err = f.errAt(len(f.urls))
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return inlineimg.Image{}, ctx.Err()
		}
	}
	if err != nil {
		return inlineimg.Image{}, err
	}
	px := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	px.SetNRGBA(0, 0, color.NRGBA{R: 1, A: 255})
	return inlineimg.Image{Pixels: px, NaturalWidth: 640}, nil
}

func (f *fakeFetcher) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.urls)
}

func (f *fakeFetcher) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func (f *fakeFetcher) activeNow() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func imagePR(number int, id string) model.PullRequest {
	return model.PullRequest{
		ID:   id,
		Body: `<img alt="a" src="` + imgAsset + `">`,
		Timeline: []model.TimelineItem{
			{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{ID: "IC_1", Body: "![x](" + imgExt + ")"}},
			{Kind: model.TimelineKindIssueComment, IssueComment: &model.IssueComment{ID: "IC_2", Body: "plain text"}},
			{Kind: model.TimelineKindReview, Review: &model.Review{ID: "R_1", Body: "looks good"}},
			{Kind: model.TimelineKindReview, Review: &model.Review{ID: "R_2", Body: `<IMG src="` + imgExt + `">`}},
		},
		ReviewThreads: []model.ReviewThread{{
			ID: "T_1",
			Comments: []model.ReviewComment{
				{ID: "RC_1", Body: "![y](" + imgAsset + ")"},
				{ID: "RC_2", Body: "no image"},
			},
		}},
	}
}

// resolvingHTML is the bodyHTML the fake serves: every subject's image is
// rewritten to a fetchable URL.
func resolvingHTML(_ context.Context, ids []string) (map[string]string, model.RateLimit, error) {
	out := map[string]string{}
	for _, id := range ids {
		switch id {
		case "PR_1", "RC_1":
			out[id] = `<img src="` + imgSigned + `">`
		default:
			out[id] = `<img src="https://camo.githubusercontent.com/x" data-canonical-src="` + imgExt + `">`
		}
	}
	return out, model.RateLimit{}, nil
}

func newImageTestStore(t *testing.T, fetcher *fakeFetcher) (*Store, *fakeGitHub, *fakeDispatcher, *[]Event) {
	t.Helper()
	gitHub := newFakeGitHub()
	disp := newFakeDispatcher()
	s := newDetailTestStore(t, gitHub, disp)
	if fetcher != nil {
		s.deps.Images = fetcher
	}
	gitHub.setBodyHTMLFunc(resolvingHTML)
	openPRWithDetail(t, s, gitHub, disp, testDetailRef(1), imagePR(1, "PR_1"))
	return s, gitHub, disp, collectEvents(s)
}

// settleImage asks for an image the way the UI does, rebuilding after each
// round of results, until it is no longer loading.
func settleImage(t *testing.T, s *Store, disp *fakeDispatcher, subject, src string) ImageState {
	t.Helper()
	for range 10 {
		st := s.Image(subject, src)
		if st.Status != ImageLoading {
			return st
		}
		runUntilIdle(t, disp)
	}
	t.Fatalf("Image(%q, %q) still loading after 10 rounds", subject, src)
	return ImageState{}
}

func TestImage_WithoutFetcherIsNone(t *testing.T) {
	s, gitHub, disp, _ := newImageTestStore(t, nil)
	if got := s.Image("PR_1", imgAsset); got.Status != ImageNone {
		t.Errorf("Image() = %v, want ImageNone", got.Status)
	}
	runUntilIdle(t, disp)
	if n := len(gitHub.bodyHTMLCallsSnapshot()); n != 0 {
		t.Errorf("BodyHTML calls = %d, want 0", n)
	}
}

func TestImage_NoPullRequestIsNone(t *testing.T) {
	disp := newFakeDispatcher()
	s := newTestStore(t, config.Default(), newFakeGitHub(), disp)
	s.deps.Images = &fakeFetcher{}
	if got := s.Image("PR_1", imgAsset); got.Status != ImageNone {
		t.Errorf("Image() = %v, want ImageNone", got.Status)
	}
}

func TestImage_BatchesOnlySubjectsWithImages(t *testing.T) {
	s, gitHub, disp, _ := newImageTestStore(t, &fakeFetcher{})

	if got := s.Image("PR_1", imgAsset); got.Status != ImageLoading {
		t.Fatalf("first Image() = %v, want ImageLoading", got.Status)
	}
	if got := s.Image("IC_1", imgExt); got.Status != ImageLoading {
		t.Fatalf("Image() of another subject = %v, want ImageLoading", got.Status)
	}
	runUntilIdle(t, disp)

	calls := gitHub.bodyHTMLCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("BodyHTML calls = %d, want exactly 1 batch", len(calls))
	}
	got := slices.Clone(calls[0])
	slices.Sort(got)
	if want := []string{"IC_1", "PR_1", "RC_1", "R_2"}; !slices.Equal(got, want) {
		t.Errorf("batched ids = %v, want %v (subjects without an image excluded)", got, want)
	}
}

func TestImage_LoadsOnceAndEmitsEvents(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, _, disp, events := newImageTestStore(t, fetcher)
	before := len(*events)

	st := s.Image("PR_1", imgAsset)
	if st.Status != ImageLoading {
		t.Fatalf("Image() = %v, want ImageLoading", st.Status)
	}
	if len(*events) != before {
		t.Fatalf("Image() emitted an event synchronously: %v", (*events)[before:])
	}

	st = settleImage(t, s, disp, "PR_1", imgAsset)
	if st.Status != ImageLoaded || st.Image.NaturalWidth != 640 || st.Image.Pixels == nil {
		t.Fatalf("settled Image() = %+v, want a loaded image", st)
	}
	if got := fetcher.calls(); !slices.Equal(got, []string{imgSigned}) {
		t.Errorf("fetched urls = %v, want the resolved signed url once", got)
	}
	if countEventKind(*events, EventImagesChanged) == 0 {
		t.Error("no EventImagesChanged emitted")
	}

	n := countEventKind(*events, EventImagesChanged)
	_ = s.Image("PR_1", imgAsset)
	runUntilIdle(t, disp)
	if got := len(fetcher.calls()); got != 1 {
		t.Errorf("fetch count after re-asking = %d, want 1 (cached)", got)
	}
	if countEventKind(*events, EventImagesChanged) != n {
		t.Error("a cached lookup emitted another event")
	}
}

func TestImage_SameSrcAcrossSubjectsFetchesOnce(t *testing.T) {
	fetcher := &fakeFetcher{gate: make(chan struct{})}
	s, _, disp, _ := newImageTestStore(t, fetcher)

	_ = s.Image("PR_1", imgAsset)
	runUntilIdle(t, disp) // batch resolved, nothing fetched yet
	_ = s.Image("PR_1", imgAsset)
	_ = s.Image("PR_1", imgAsset)
	_ = s.Image("RC_1", imgAsset)
	runUntilIdle(t, disp) // RC_1's batch result; fetch is still gated

	if got := fetcher.calls(); len(got) != 1 {
		t.Errorf("fetches in flight = %v, want one for the shared src", got)
	}
	close(fetcher.gate)
	if st := settleImage(t, s, disp, "RC_1", imgAsset); st.Status != ImageLoaded {
		t.Errorf("Image() = %v, want ImageLoaded", st.Status)
	}
	if got := fetcher.calls(); len(got) != 1 {
		t.Errorf("fetch count = %d, want 1", len(got))
	}
}

func TestImage_PermanentFetchFailureIsCachedForTheSession(t *testing.T) {
	fetcher := &fakeFetcher{err: permErr{}}
	s, _, disp, _ := newImageTestStore(t, fetcher)

	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}
	_ = s.Image("PR_1", imgAsset)
	_ = s.Image("RC_1", imgAsset)
	runUntilIdle(t, disp)
	if got := fetcher.calls(); len(got) != 1 {
		t.Errorf("fetch attempts = %v, want exactly 1 (a permanent failure is not retried)", got)
	}
}

func TestImage_UnresolvableSrcFailsWithoutFetching(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, _, disp, _ := newImageTestStore(t, fetcher)

	if st := settleImage(t, s, disp, "PR_1", "https://example.com/never-rendered.png"); st.Status != ImageFailed {
		t.Errorf("Image() = %v, want ImageFailed", st.Status)
	}
	if got := fetcher.calls(); len(got) != 0 {
		t.Errorf("fetched %v, want nothing", got)
	}
}

func TestImage_BodyHTMLFailureFailsTheSubjectsOnce(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, gitHub, disp, _ := newImageTestStore(t, fetcher)
	gitHub.setBodyHTMLFunc(func(context.Context, []string) (map[string]string, model.RateLimit, error) {
		return nil, model.RateLimit{}, errors.New("graphql down")
	})

	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}
	_ = s.Image("IC_1", imgExt)
	runUntilIdle(t, disp)
	if n := len(gitHub.bodyHTMLCallsSnapshot()); n != 1 {
		t.Errorf("BodyHTML calls = %d, want 1 (no retry)", n)
	}
	if got := fetcher.calls(); len(got) != 0 {
		t.Errorf("fetched %v, want nothing", got)
	}
}

func TestImage_ReopeningThePullRequestRetriesAFailedBatch(t *testing.T) {
	s, gitHub, disp, _ := newImageTestStore(t, &fakeFetcher{})
	gitHub.setBodyHTMLFunc(func(context.Context, []string) (map[string]string, model.RateLimit, error) {
		return nil, model.RateLimit{}, errors.New("graphql down")
	})
	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}

	gitHub.setBodyHTMLFunc(resolvingHTML)
	openPRWithDetail(t, s, gitHub, disp, testDetailRef(2), imagePR(2, "PR_1"))
	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageLoaded {
		t.Errorf("Image() after reopening = %v, want ImageLoaded", st.Status)
	}
}

func TestImage_ResultForAnotherPullRequestIsHarmless(t *testing.T) {
	fetcher := &fakeFetcher{gate: make(chan struct{})}
	s, gitHub, disp, events := newImageTestStore(t, fetcher)
	gate := make(chan struct{})
	gitHub.setBodyHTMLFunc(func(ctx context.Context, ids []string) (map[string]string, model.RateLimit, error) {
		<-gate
		return resolvingHTML(ctx, ids)
	})

	_ = s.Image("PR_1", imgAsset) // batch for pull request 1 is now blocked in flight
	other := imagePR(2, "PR_2")
	other.Body = "no image here"
	openPRWithDetail(t, s, gitHub, disp, testDetailRef(2), other)
	n := countEventKind(*events, EventImagesChanged)
	close(gate)
	runUntilIdle(t, disp)

	if got := fetcher.calls(); len(got) != 0 {
		t.Errorf("a stale batch started fetches: %v", got)
	}
	if countEventKind(*events, EventImagesChanged) != n {
		t.Error("a stale batch emitted EventImagesChanged")
	}
}

func TestImage_FetchFinishingAfterPullRequestSwitchIsStillCached(t *testing.T) {
	fetcher := &fakeFetcher{gate: make(chan struct{})}
	s, gitHub, disp, _ := newImageTestStore(t, fetcher)

	_ = s.Image("PR_1", imgAsset)
	runUntilIdle(t, disp)
	_ = s.Image("PR_1", imgAsset) // fetch starts, gated
	deadline := time.Now().Add(2 * time.Second)
	for fetcher.activeNow() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	openPRWithDetail(t, s, gitHub, disp, testDetailRef(2), imagePR(2, "PR_1"))
	close(fetcher.gate)
	runUntilIdle(t, disp)

	if st := s.Image("PR_1", imgAsset); st.Status != ImageLoaded {
		t.Errorf("Image() = %v, want the finished fetch cached", st.Status)
	}
	if got := fetcher.calls(); len(got) != 1 {
		t.Errorf("fetches = %v, want 1", got)
	}
}

func TestImage_LimitsConcurrentFetches(t *testing.T) {
	fetcher := &fakeFetcher{gate: make(chan struct{})}
	s, gitHub, disp, _ := newImageTestStore(t, fetcher)
	body := ""
	html := ""
	for i := range 6 {
		body += fmt.Sprintf("![i](https://example.com/%d.png)\n", i)
		html += fmt.Sprintf(`<img src="https://camo.githubusercontent.com/%d" data-canonical-src="https://example.com/%d.png">`, i, i)
	}
	pr := imagePR(2, "PR_9")
	pr.Body = body
	gitHub.setBodyHTMLFunc(func(_ context.Context, ids []string) (map[string]string, model.RateLimit, error) {
		out := map[string]string{}
		for _, id := range ids {
			out[id] = html
		}
		return out, model.RateLimit{}, nil
	})
	openPRWithDetail(t, s, gitHub, disp, testDetailRef(2), pr)

	_ = s.Image("PR_9", "https://example.com/0.png")
	runUntilIdle(t, disp)
	for i := range 6 {
		_ = s.Image("PR_9", fmt.Sprintf("https://example.com/%d.png", i))
	}
	deadline := time.Now().Add(2 * time.Second)
	for fetcher.activeNow() < imageFetchWorkers && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // give an over-limit fetch the chance to show up
	if got := fetcher.maxConcurrent(); got != imageFetchWorkers {
		t.Errorf("max concurrent fetches = %d, want %d", got, imageFetchWorkers)
	}
	close(fetcher.gate)
	runUntilIdle(t, disp)
	if got := len(fetcher.calls()); got != 6 {
		t.Errorf("fetches = %d, want 6", got)
	}
}

func TestImageCache_EvictsOldestLoadedButKeepsFailures(t *testing.T) {
	c := newImageCache(2)
	img := func(w int) inlineimg.Image { return inlineimg.Image{NaturalWidth: w} }
	c.putFailed("bad")
	c.putLoaded("a", img(1))
	c.putLoaded("b", img(2))
	c.putLoaded("c", img(3))

	if _, ok := c.get("a"); ok {
		t.Error("oldest entry a was not evicted")
	}
	if st, ok := c.get("c"); !ok || st.Image.NaturalWidth != 3 {
		t.Errorf("get(c) = %+v, %v", st, ok)
	}
	if st, ok := c.get("bad"); !ok || st.Status != ImageFailed {
		t.Errorf("get(bad) = %+v, %v; want a retained failure", st, ok)
	}
	c.putLoaded("b", img(20)) // refreshing an entry keeps size bounded
	if st, _ := c.get("b"); st.Image.NaturalWidth != 20 {
		t.Errorf("get(b) = %+v, want the refreshed value", st)
	}
	if _, ok := c.get("c"); !ok {
		t.Error("refreshing b evicted c")
	}
}

// permErr is a fetch failure that retrying cannot fix, as inlineimg
// classifies unsupported or oversized images.
type permErr struct{}

func (permErr) Error() string   { return "unsupported image" }
func (permErr) Permanent() bool { return true }

// clock is a settable fake clock for Store.deps.Now.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// numberedHTML is a bodyHTML func whose signed URL differs on every call, as
// real ones do, so a test can tell which resolution a download used. extra
// maps additional canonical srcs to camo URLs, to simulate an edited body.
func numberedHTML(calls *int, extra map[string]string) func(context.Context, []string) (map[string]string, model.RateLimit, error) {
	return func(_ context.Context, ids []string) (map[string]string, model.RateLimit, error) {
		*calls++
		out := map[string]string{}
		for _, id := range ids {
			html := fmt.Sprintf(`<img src="%s?jwt=call%d">`, imgSigned, *calls)
			for canonical, camo := range extra {
				html += fmt.Sprintf(`<img src="%s" data-canonical-src="%s">`, camo, canonical)
			}
			out[id] = html
		}
		return out, model.RateLimit{}, nil
	}
}

func newClockedImageStore(t *testing.T, fetcher *fakeFetcher) (*Store, *fakeGitHub, *fakeDispatcher, *clock) {
	t.Helper()
	s, gitHub, disp, _ := newImageTestStore(t, fetcher)
	c := &clock{t: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}
	s.deps.Now = c.now
	return s, gitHub, disp, c
}

func TestImage_ReResolvesBeforeDownloadingWhenTheResolutionIsOlderThanFourMinutes(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, gitHub, disp, clk := newClockedImageStore(t, fetcher)
	calls := 0
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, map[string]string{imgExt: "https://camo.githubusercontent.com/x"}))

	_ = s.Image("IC_1", imgExt) // resolves the batch at t0; nothing downloaded yet
	runUntilIdle(t, disp)
	clk.advance(5 * time.Minute)

	if st := settleImage(t, s, disp, "IC_1", imgExt); st.Status != ImageLoaded {
		t.Fatalf("Image() = %v, want ImageLoaded", st.Status)
	}
	if calls != 2 {
		t.Errorf("BodyHTML calls = %d, want 2 (the stale resolution was refreshed before downloading)", calls)
	}
	if got := fetcher.calls(); len(got) != 1 || got[0] != "https://camo.githubusercontent.com/x" {
		t.Errorf("downloads = %v, want one", got)
	}
}

func TestImage_ReusesAFreshResolution(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, gitHub, disp, clk := newClockedImageStore(t, fetcher)
	calls := 0
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, nil))

	_ = s.Image("PR_1", imgAsset)
	runUntilIdle(t, disp)
	clk.advance(3*time.Minute + 59*time.Second)
	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageLoaded {
		t.Fatalf("Image() = %v, want ImageLoaded", st.Status)
	}
	if calls != 1 {
		t.Errorf("BodyHTML calls = %d, want 1", calls)
	}
	if got := fetcher.calls(); len(got) != 1 || !strings.HasSuffix(got[0], "jwt=call1") {
		t.Errorf("downloads = %v, want the first resolution's URL", got)
	}
}

func TestImage_ReResolvesOnceWhenASrcIsMissingFromAnOldResolution(t *testing.T) {
	const added = "https://example.com/added-by-an-edit.png"
	fetcher := &fakeFetcher{}
	s, gitHub, disp, clk := newClockedImageStore(t, fetcher)
	calls := 0
	extra := map[string]string{}
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, extra))

	_ = s.Image("IC_1", imgExt)
	runUntilIdle(t, disp)

	// Within 30 s the miss is just a miss: no new request.
	clk.advance(10 * time.Second)
	if st := s.Image("IC_1", added); st.Status != ImageFailed {
		t.Fatalf("Image() of an unknown src = %v, want ImageFailed", st.Status)
	}
	runUntilIdle(t, disp)
	if calls != 1 {
		t.Fatalf("BodyHTML calls = %d, want 1 within 30 s", calls)
	}

	// The comment was edited; GitHub's bodyHTML now has the new image.
	extra[added] = "https://camo.githubusercontent.com/added"
	clk.advance(25 * time.Second)
	if st := settleImage(t, s, disp, "IC_1", added); st.Status != ImageLoaded {
		t.Fatalf("Image() after re-resolving = %v, want ImageLoaded", st.Status)
	}
	if calls != 2 {
		t.Errorf("BodyHTML calls = %d, want exactly one re-resolution", calls)
	}

	// A src that stays missing costs one request, then is rate limited by the age.
	if st := settleImage(t, s, disp, "IC_1", "https://example.com/never.png"); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}
	callsAfter := calls
	for range 5 {
		_ = s.Image("IC_1", "https://example.com/never.png")
		runUntilIdle(t, disp)
	}
	if calls != callsAfter {
		t.Errorf("BodyHTML calls grew from %d to %d on repeated asks: a retry storm", callsAfter, calls)
	}
	for range 5 {
		clk.advance(31 * time.Second)
		_ = s.Image("IC_1", "https://example.com/never.png")
		runUntilIdle(t, disp)
	}
	if calls != callsAfter+1 {
		t.Errorf("BodyHTML calls grew from %d to %d as the resolution aged, want exactly one re-resolution for a src that never resolves", callsAfter, calls)
	}
}

func TestImage_PermanentFailureIsCachedImmediately(t *testing.T) {
	fetcher := &fakeFetcher{err: permErr{}}
	s, gitHub, disp, _ := newClockedImageStore(t, fetcher)
	calls := 0
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, nil))

	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}
	for range 3 {
		_ = s.Image("PR_1", imgAsset)
		runUntilIdle(t, disp)
	}
	if got := len(fetcher.calls()); got != 1 {
		t.Errorf("downloads = %d, want 1", got)
	}
	if calls != 1 {
		t.Errorf("BodyHTML calls = %d, want 1 (no re-resolution for a permanent failure)", calls)
	}
}

func TestImage_TransientFailureGetsOneMoreAttemptWithAFreshURL(t *testing.T) {
	fetcher := &fakeFetcher{errAt: func(n int) error {
		if n == 1 {
			return errors.New("fetch: status 403 Forbidden")
		}
		return nil
	}}
	s, gitHub, disp, _ := newClockedImageStore(t, fetcher)
	calls := 0
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, nil))

	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageLoaded {
		t.Fatalf("Image() = %v, want ImageLoaded after the retry", st.Status)
	}
	got := fetcher.calls()
	if len(got) != 2 || !strings.HasSuffix(got[0], "jwt=call1") || !strings.HasSuffix(got[1], "jwt=call2") {
		t.Errorf("downloads = %v, want the retry to use the re-resolved URL", got)
	}
	if calls != 2 {
		t.Errorf("BodyHTML calls = %d, want 2", calls)
	}
}

func TestImage_SecondTransientFailureIsCachedAndNothingLoopsAfterwards(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("fetch: status 403 Forbidden")}
	s, gitHub, disp, clk := newClockedImageStore(t, fetcher)
	calls := 0
	gitHub.setBodyHTMLFunc(numberedHTML(&calls, nil))

	if st := settleImage(t, s, disp, "PR_1", imgAsset); st.Status != ImageFailed {
		t.Fatalf("Image() = %v, want ImageFailed", st.Status)
	}
	// Rebuilds after every EventImagesChanged, then hours of redraws.
	for range 5 {
		_ = s.Image("PR_1", imgAsset)
		_ = s.Image("RC_1", imgAsset)
		runUntilIdle(t, disp)
		clk.advance(time.Hour)
	}
	if got := len(fetcher.calls()); got != 2 {
		t.Errorf("downloads = %d, want exactly 2 (the attempt and its one retry)", got)
	}
	if calls != 2 {
		t.Errorf("BodyHTML calls = %d, want exactly 2", calls)
	}
}
