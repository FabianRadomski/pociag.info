// Package feeds holds the current static timetable and realtime snapshot,
// replacing them atomically as the pollers deliver new data.
package feeds

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"pociag.info/internal/fetch"
	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
	"pociag.info/internal/stations"
	"pociag.info/internal/timetable"
)

// Static is everything derived from one static GTFS feed.
type Static struct {
	Timetable *timetable.Timetable
	Stations  *stations.Index
}

// Store is safe for concurrent use; the zero value is empty. Static loads
// must not run concurrently with each other.
type Store struct {
	// StaticCache is where the last static zip that loaded successfully is
	// kept, for fallback and fast restarts. Empty disables both.
	StaticCache string

	static atomic.Pointer[Static]
	live   atomic.Pointer[realtime.Snapshot]
	// misaligned is set while the realtime feed doesn't fit the timetable.
	misaligned atomic.Bool

	beforeParse func() // test hook
}

// LoadStaticFile replaces the timetable with the GTFS zip at path.
//
// To keep peak memory at one timetable, the current one is dropped before
// parsing; pages answer "loading" for the few seconds this takes. On success
// the file becomes StaticCache; on failure the cached zip is loaded back, and
// the file is left in place. Failures to read it off disk wrap
// fetch.ErrTransient, so the poller tries the same file again.
func (s *Store) LoadStaticFile(path string) error {
	s.static.Store(nil)
	debug.FreeOSMemory()
	if s.beforeParse != nil {
		s.beforeParse()
	}

	st, err := loadZip(path)
	if err != nil {
		if transient(err) {
			err = fmt.Errorf("%w (%w)", err, fetch.ErrTransient)
		}
		if s.StaticCache != "" && path != s.StaticCache {
			prev, cacheErr := loadZip(s.StaticCache)
			if cacheErr != nil {
				return errors.Join(err, fmt.Errorf("fall back to cached static: %w", cacheErr))
			}
			s.static.Store(prev)
			s.prepare()
		}
		return err
	}
	s.static.Store(st)
	s.prepare()
	if s.StaticCache != "" && path != s.StaticCache {
		if err := os.Rename(path, s.StaticCache); err != nil {
			return fmt.Errorf("keep static cache: %w", err)
		}
	}
	return nil
}

// LoadCachedStatic loads StaticCache, returning its modification time to seed
// If-Modified-Since; fetch.Fetcher sets it to the server's Last-Modified. A
// missing cache yields an fs.ErrNotExist error.
func (s *Store) LoadCachedStatic() (time.Time, error) {
	info, err := os.Stat(s.StaticCache)
	if err != nil {
		return time.Time{}, err
	}
	st, err := loadZip(s.StaticCache)
	if err != nil {
		return time.Time{}, err
	}
	s.static.Store(st)
	s.prepare()
	return info.ModTime(), nil
}

// transient reports whether err came from the OS reading the file (EIO,
// EMFILE, ...) rather than from its contents. A missing file is not transient:
// loading it again cannot work.
func transient(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && !errors.Is(err, fs.ErrNotExist)
}

func loadZip(path string) (*Static, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open gtfs zip: %w", err)
	}
	defer zr.Close()
	feed, err := gtfs.Load(zr)
	if err != nil {
		return nil, err
	}
	tt := timetable.New(feed)
	return &Static{Timetable: tt, Stations: stations.NewIndex(tt.StationNames())}, nil
}

// LoadRealtime parses a GTFS-Realtime feed and makes it current, then
// prepares it (see prepare).
func (s *Store) LoadRealtime(b []byte) error {
	snap, err := realtime.Parse(b)
	if err != nil {
		return err
	}
	s.live.Store(snap)
	s.prepare()
	return nil
}

// prepare checks that the realtime feed fits the timetable, logging when
// that changes, and indexes the timetable for journeys at its live times,
// so that requests don't wait for it.
func (s *Store) prepare() {
	st, snap := s.static.Load(), s.live.Load()
	if st == nil {
		return
	}
	if snap != nil {
		usable, fit, known := st.Timetable.Alignment(snap)
		if s.misaligned.Swap(!usable) != !usable {
			if usable {
				slog.Info("realtime feed fits the timetable again", "fit", fit, "trips", known)
			} else {
				slog.Warn("realtime feed doesn't fit the timetable; ignoring it", "fit", fit, "trips", known)
			}
		}
	}
	st.Timetable.Prepare(snap, time.Now())
}

// Static returns the current timetable, or nil before the first load.
func (s *Store) Static() *Static { return s.static.Load() }

// Snapshot returns the current realtime data, or nil before the first load.
func (s *Store) Snapshot() *realtime.Snapshot { return s.live.Load() }
