// Package fetch downloads remote feeds politely: conditional GETs so an
// unchanged feed costs a 304, and a poller that hands new bodies to a parser.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Fetcher downloads one URL, remembering validators between calls.
// It is not safe for concurrent use.
type Fetcher struct {
	URL       string
	Client    *http.Client // nil means http.DefaultClient
	UserAgent string
	// File, if set, receives the body instead of memory; it is replaced
	// only after a complete download, and its modification time is set to
	// the response's Last-Modified so it can seed SetLastModified later.
	File string

	etag         string
	lastModified string
}

// Result of a fetch. Body (or File, when Fetcher.File is set) is only set
// when Changed is true.
type Result struct {
	Body    []byte
	File    string
	Changed bool
}

// SetLastModified seeds the validator, e.g. from a file kept across restarts.
func (f *Fetcher) SetLastModified(t time.Time) {
	f.lastModified = t.UTC().Format(http.TimeFormat)
}

// Fetch downloads the URL unless the server says it hasn't changed.
func (f *Fetcher) Fetch(ctx context.Context) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return Result{}, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	if f.etag != "" {
		req.Header.Set("If-None-Match", f.etag)
	} else if f.lastModified != "" {
		req.Header.Set("If-Modified-Since", f.lastModified)
	}

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetch %s: %w", f.URL, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return Result{}, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		// Drain a little so the keep-alive connection can be reused.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return Result{}, fmt.Errorf("fetch %s: %s", f.URL, resp.Status)
	}

	res := Result{Changed: true}
	if f.File != "" {
		modTime, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
		if err := writeFile(f.File, resp.Body, modTime); err != nil {
			return Result{}, fmt.Errorf("fetch %s: %w", f.URL, err)
		}
		res.File = f.File
	} else {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return Result{}, fmt.Errorf("fetch %s: read body: %w", f.URL, err)
		}
		res.Body = body
	}
	f.etag = resp.Header.Get("ETag")
	f.lastModified = resp.Header.Get("Last-Modified")
	return res, nil
}

// writeFile streams r into a temporary file next to path and renames it into
// place, so path never holds a partial download. A non-zero modTime becomes
// the file's modification time.
func writeFile(path string, r io.Reader, modTime time.Time) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".part-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if !modTime.IsZero() {
		if err := os.Chtimes(tmp.Name(), modTime, modTime); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}

// ErrTransient, wrapped in an OnChange error, says the body is fine but could
// not be handled this time (e.g. a disk error), so it is worth handling again.
var ErrTransient = errors.New("transient")

// Poller fetches on an interval and passes changed bodies to OnChange.
type Poller struct {
	Fetcher  *Fetcher
	Interval time.Duration
	OnChange func(Result) error
	Logger   *slog.Logger // nil means slog.Default()

	retry *Result // body whose handling failed with ErrTransient
}

// Step performs a single poll. If OnChange rejects a body, the fetcher keeps
// its validators: re-downloading the same bytes would fail the same way, so
// the next delivery is whatever the server publishes next. A rejection that
// wraps ErrTransient is different: the next Step hands the same Result to
// OnChange again instead of fetching, since a conditional request would only
// get a 304 for the body we never managed to use.
func (p *Poller) Step(ctx context.Context) error {
	res := p.retry
	p.retry = nil
	if res == nil {
		r, err := p.Fetcher.Fetch(ctx)
		if err != nil || !r.Changed {
			return err
		}
		res = &r
	}
	if err := p.OnChange(*res); err != nil {
		if errors.Is(err, ErrTransient) {
			p.retry = res
		}
		return fmt.Errorf("handle %s: %w", p.Fetcher.URL, err)
	}
	return nil
}

// Run polls immediately and then every Interval until ctx is cancelled.
// Errors are logged; the last successfully handled body stays in effect.
func (p *Poller) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		if err := p.Step(ctx); err != nil && ctx.Err() == nil {
			log.Warn("poll failed", "url", p.Fetcher.URL, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
