// Command pociag serves train departure boards for curl and browsers.
//
// It polls the static GTFS and GTFS-Realtime feeds from mkuran.pl with
// conditional requests and answers every page from memory.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"pociag.info/internal/feeds"
	"pociag.info/internal/fetch"
	"pociag.info/internal/server"
)

func main() {
	var (
		addr        = flag.String("addr", ":8080", "listen address")
		staticURL   = flag.String("static-url", "https://mkuran.pl/gtfs/polish_trains.zip", "static GTFS zip")
		realtimeURL = flag.String("realtime-url", "https://mkuran.pl/gtfs/polish_trains/updates.pb", "GTFS-Realtime trip updates")
		staticEvery = flag.Duration("static-every", 30*time.Minute, "how often to check the static feed")
		liveEvery   = flag.Duration("realtime-every", time.Minute, "how often to check the realtime feed")
		userAgent   = flag.String("user-agent", "pociag.info/dev", "User-Agent sent to the feed servers")
		dataDir     = flag.String("data-dir", "data", "directory for the downloaded static feed")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		slog.Error("data dir", "err", err)
		os.Exit(1)
	}
	store := feeds.Store{StaticCache: filepath.Join(*dataDir, "static.zip")}
	client := &http.Client{Timeout: 2 * time.Minute}
	staticFetcher := &fetch.Fetcher{
		URL: *staticURL, Client: client, UserAgent: *userAgent,
		File: filepath.Join(*dataDir, "incoming.zip"),
	}

	staticPoller := &fetch.Poller{
		Fetcher:  staticFetcher,
		Interval: *staticEvery,
		OnChange: logged("static", func(r fetch.Result) error { return store.LoadStaticFile(r.File) }),
	}
	realtimePoller := &fetch.Poller{
		Fetcher:  &fetch.Fetcher{URL: *realtimeURL, Client: client, UserAgent: *userAgent},
		Interval: *liveEvery,
		OnChange: logged("realtime", func(r fetch.Result) error { return store.LoadRealtime(r.Body) }),
	}
	go func() {
		// Serve the timetable from the last run first; the poller then only
		// downloads again if the server has a newer one. Same goroutine, so
		// static loads never overlap.
		start := time.Now()
		if modTime, err := store.LoadCachedStatic(); err == nil {
			staticFetcher.SetLastModified(modTime)
			slog.Info("feed loaded", "feed", "static (cached)", "took", time.Since(start).Round(time.Millisecond))
		} else if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("cached static feed unusable", "err", err)
		}
		staticPoller.Run(ctx)
	}()
	go realtimePoller.Run(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           &server.Server{Data: &store},
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	slog.Info("listening", "addr", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// logged wraps a loader to log how long loading took.
func logged(name string, load func(fetch.Result) error) func(fetch.Result) error {
	return func(r fetch.Result) error {
		start := time.Now()
		if err := load(r); err != nil {
			return err
		}
		slog.Info("feed loaded", "feed", name, "took", time.Since(start).Round(time.Millisecond))
		return nil
	}
}
