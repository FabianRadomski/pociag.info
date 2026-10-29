// Package feeds holds the current static timetable and realtime snapshot,
// replacing them atomically as the pollers deliver new data.
package feeds

import (
	"archive/zip"
	"fmt"
	"os"
	"runtime/debug"
	"sync/atomic"
	"time"

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

	beforeParse func() // test hook
}

// LoadStaticFile replaces the timetable with the GTFS zip at path.
//
// To keep peak memory at one timetable, the current one is dropped before
// parsing; pages answer "loading" for the few seconds this takes. On success
// the file becomes StaticCache; on failure the cached zip is loaded back.
func (s *Store) LoadStaticFile(path string) error {
	s.static.Store(nil)
	debug.FreeOSMemory()
	if s.beforeParse != nil {
		s.beforeParse()
	}

	st, err := loadZip(path)
	if err != nil {
		if s.StaticCache != "" && path != s.StaticCache {
			if prev, cacheErr := loadZip(s.StaticCache); cacheErr == nil {
				s.static.Store(prev)
			}
		}
		return err
	}
	s.static.Store(st)
	if s.StaticCache != "" && path != s.StaticCache {
		if err := os.Rename(path, s.StaticCache); err != nil {
			return fmt.Errorf("keep static cache: %w", err)
		}
	}
	return nil
}

// LoadCachedStatic loads StaticCache, returning its modification time to seed
// If-Modified-Since. A missing cache yields an fs.ErrNotExist error.
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
	return info.ModTime(), nil
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

// LoadRealtime parses a GTFS-Realtime feed and makes it current.
func (s *Store) LoadRealtime(b []byte) error {
	snap, err := realtime.Parse(b)
	if err != nil {
		return err
	}
	s.live.Store(snap)
	return nil
}

// Static returns the current timetable, or nil before the first load.
func (s *Store) Static() *Static { return s.static.Load() }

// Snapshot returns the current realtime data, or nil before the first load.
func (s *Store) Snapshot() *realtime.Snapshot { return s.live.Load() }
