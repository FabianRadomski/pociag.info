package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeOrigin imitates mkuran.pl behind Cloudflare: it serves a body with
// validators and answers conditional requests with 304.
type fakeOrigin struct {
	mu           sync.Mutex
	body         string
	etag         string
	lastModified string
	status       int // forced status, 0 = normal behaviour
	requests     []http.Header
}

func (o *fakeOrigin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, r.Header.Clone())

	if o.status != 0 {
		w.WriteHeader(o.status)
		return
	}
	if o.etag != "" {
		w.Header().Set("ETag", o.etag)
		if r.Header.Get("If-None-Match") == o.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if o.lastModified != "" {
		w.Header().Set("Last-Modified", o.lastModified)
		if o.etag == "" && r.Header.Get("If-Modified-Since") == o.lastModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Write([]byte(o.body))
}

func (o *fakeOrigin) set(fn func(o *fakeOrigin)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fn(o)
}

func (o *fakeOrigin) lastRequest(t *testing.T) http.Header {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.requests) == 0 {
		t.Fatal("origin received no requests")
	}
	return o.requests[len(o.requests)-1]
}

func (o *fakeOrigin) requestCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.requests)
}

func newOrigin(t *testing.T, o *fakeOrigin) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	return &Fetcher{URL: srv.URL, Client: srv.Client(), UserAgent: "pociag.info-test"}
}

func mustFetch(t *testing.T, f *Fetcher) Result {
	t.Helper()
	res, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return res
}

func TestFetch_FirstRequestDownloadsUnconditionally(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)

	res := mustFetch(t, f)

	if !res.Changed || string(res.Body) != "v1" {
		t.Errorf("got Changed=%v Body=%q, want true \"v1\"", res.Changed, res.Body)
	}
	h := o.lastRequest(t)
	if h.Get("If-None-Match") != "" || h.Get("If-Modified-Since") != "" {
		t.Errorf("first request was conditional: %v", h)
	}
	if h.Get("User-Agent") != "pociag.info-test" {
		t.Errorf("User-Agent = %q, want %q", h.Get("User-Agent"), "pociag.info-test")
	}
}

func TestFetch_UnchangedReturnsNotChanged(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)
	mustFetch(t, f)

	res := mustFetch(t, f)

	if res.Changed || res.Body != nil {
		t.Errorf("got Changed=%v Body=%q, want false nil", res.Changed, res.Body)
	}
	if got := o.lastRequest(t).Get("If-None-Match"); got != `"a"` {
		t.Errorf("If-None-Match = %q, want %q", got, `"a"`)
	}
}

func TestFetch_ChangedContentUpdatesValidator(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)
	mustFetch(t, f)

	o.set(func(o *fakeOrigin) { o.body, o.etag = "v2", `"b"` })
	res := mustFetch(t, f)
	if !res.Changed || string(res.Body) != "v2" {
		t.Fatalf("got Changed=%v Body=%q, want true \"v2\"", res.Changed, res.Body)
	}

	mustFetch(t, f)
	if got := o.lastRequest(t).Get("If-None-Match"); got != `"b"` {
		t.Errorf("If-None-Match after change = %q, want %q", got, `"b"`)
	}
}

func TestFetch_FallsBackToLastModified(t *testing.T) {
	lm := "Thu, 22 Oct 2026 15:46:34 GMT"
	o := &fakeOrigin{body: "v1", lastModified: lm}
	f := newOrigin(t, o)
	mustFetch(t, f)

	res := mustFetch(t, f)

	if res.Changed {
		t.Error("got Changed=true, want false")
	}
	if got := o.lastRequest(t).Get("If-Modified-Since"); got != lm {
		t.Errorf("If-Modified-Since = %q, want %q", got, lm)
	}
}

func TestFetch_ServerErrorKeepsValidators(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)
	mustFetch(t, f)

	o.set(func(o *fakeOrigin) { o.status = http.StatusBadGateway })
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch on 502: want error, got nil")
	}

	o.set(func(o *fakeOrigin) { o.status = 0 })
	res := mustFetch(t, f)
	if res.Changed {
		t.Error("after recovering from 502 with same content: got Changed=true, want false")
	}
}

func TestFetch_ResetForcesFullDownload(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)
	mustFetch(t, f)

	f.Reset()
	res := mustFetch(t, f)

	if !res.Changed || string(res.Body) != "v1" {
		t.Errorf("got Changed=%v Body=%q, want true \"v1\"", res.Changed, res.Body)
	}
	if got := o.lastRequest(t).Get("If-None-Match"); got != "" {
		t.Errorf("If-None-Match after Reset = %q, want empty", got)
	}
}

func TestFetch_HonoursContext(t *testing.T) {
	f := newOrigin(t, &fakeOrigin{body: "v1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.Fetch(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestPoller_StepDeliversOnlyChanges(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	var got []string
	p := &Poller{
		Fetcher:  newOrigin(t, o),
		OnChange: func(r Result) error { got = append(got, string(r.Body)); return nil },
	}

	for range 3 {
		if err := p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	o.set(func(o *fakeOrigin) { o.body, o.etag = "v2", `"b"` })
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Errorf("OnChange calls = %q, want [v1 v2]", got)
	}
}

func TestPoller_HandlerErrorRetriesFullDownload(t *testing.T) {
	o := &fakeOrigin{body: "garbage", etag: `"a"`}
	calls := 0
	p := &Poller{
		Fetcher: newOrigin(t, o),
		OnChange: func(Result) error {
			calls++
			if calls == 1 {
				return errors.New("parse failed")
			}
			return nil
		},
	}

	if err := p.Step(context.Background()); err == nil {
		t.Fatal("Step with failing handler: want error, got nil")
	}
	if err := p.Step(context.Background()); err != nil {
		t.Fatalf("second Step: %v", err)
	}

	if calls != 2 {
		t.Errorf("OnChange called %d times, want 2 (same body must be re-delivered)", calls)
	}
	if got := o.lastRequest(t).Get("If-None-Match"); got != "" {
		t.Errorf("retry after handler error was conditional (If-None-Match %q)", got)
	}
}

func TestPoller_FetchErrorSkipsHandler(t *testing.T) {
	o := &fakeOrigin{status: http.StatusServiceUnavailable}
	called := false
	p := &Poller{
		Fetcher:  newOrigin(t, o),
		OnChange: func(Result) error { called = true; return nil },
	}

	if err := p.Step(context.Background()); err == nil {
		t.Error("Step on 503: want error, got nil")
	}
	if called {
		t.Error("OnChange called despite fetch error")
	}
}

func TestPoller_RunPollsImmediatelyThenOnIntervalUntilCancelled(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	p := &Poller{
		Fetcher:  newOrigin(t, o),
		Interval: 10 * time.Millisecond,
		OnChange: func(Result) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for o.requestCount() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d requests after 2s, want >= 3", o.requestCount())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
}

func TestFetch_ToFile(t *testing.T) {
	o := &fakeOrigin{body: "v1", etag: `"a"`}
	f := newOrigin(t, o)
	f.File = filepath.Join(t.TempDir(), "feed.zip")

	res := mustFetch(t, f)

	if !res.Changed || res.Body != nil || res.File != f.File {
		t.Fatalf("got %+v; want Changed, no Body, File=%s", res, f.File)
	}
	if got, _ := os.ReadFile(f.File); string(got) != "v1" {
		t.Errorf("file contains %q, want v1", got)
	}
	if res := mustFetch(t, f); res.Changed || res.File != "" {
		t.Errorf("unchanged fetch = %+v; want no File", res)
	}
}

func TestFetch_ToFileLeavesNoPartialFileOnError(t *testing.T) {
	o := &fakeOrigin{status: http.StatusBadGateway}
	f := newOrigin(t, o)
	dir := t.TempDir()
	f.File = filepath.Join(dir, "feed.zip")

	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatal("want error on 502")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestFetch_SeededLastModifiedMakesFirstRequestConditional(t *testing.T) {
	lm := "Thu, 22 Oct 2026 00:51:51 GMT"
	o := &fakeOrigin{body: "v1", lastModified: lm}
	f := newOrigin(t, o)
	f.SetLastModified(time.Date(2026, 10, 22, 0, 51, 51, 0, time.UTC))

	res := mustFetch(t, f)

	if got := o.lastRequest(t).Get("If-Modified-Since"); got != lm {
		t.Errorf("If-Modified-Since = %q, want %q", got, lm)
	}
	if res.Changed {
		t.Error("got Changed=true, want false (server said 304)")
	}
}
