package feeds

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"pociag.info/internal/fetch"
)

// zipDir packs the GTFS sample like mkuran.pl ships it: files at the root.
func zipDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	paths, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no GTFS files in %s: %v", dir, err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create(filepath.Base(p))
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sampleZipFile writes the GTFS sample as a zip into dir.
func sampleZipFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "incoming.zip")
	if err := os.WriteFile(path, zipDir(t, "../../testdata/gtfs/wro-krk"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func garbageFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "incoming.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sampleUpdates(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/gtfs/wro-krk-updates.pb")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertResolves(t *testing.T, s *Store) {
	t.Helper()
	st := s.Static()
	if st == nil {
		t.Fatal("Static() is nil")
	}
	got, err := st.Stations.Resolve("Wroclaw")
	if err != nil || got.ID != "60103" {
		t.Errorf("Stations.Resolve(Wroclaw) = %+v, %v; want 60103", got, err)
	}
	if name := st.Timetable.StationName("80416"); name != "Kraków Główny" {
		t.Errorf("Timetable.StationName(80416) = %q", name)
	}
}

func TestStore_EmptyUntilLoaded(t *testing.T) {
	var s Store
	if s.Static() != nil || s.Snapshot() != nil {
		t.Error("new Store should have no data")
	}
}

func TestStore_LoadStaticFileKeepsItAsCache(t *testing.T) {
	dir := t.TempDir()
	s := Store{StaticCache: filepath.Join(dir, "static.zip")}
	incoming := sampleZipFile(t, dir)

	if err := s.LoadStaticFile(incoming); err != nil {
		t.Fatalf("LoadStaticFile: %v", err)
	}

	assertResolves(t, &s)
	if _, err := os.Stat(s.StaticCache); err != nil {
		t.Errorf("cache not written: %v", err)
	}
	if _, err := os.Stat(incoming); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("incoming file still there (err %v); it should have become the cache", err)
	}
}

func TestStore_DropsOldTimetableBeforeParsing(t *testing.T) {
	dir := t.TempDir()
	s := Store{StaticCache: filepath.Join(dir, "static.zip")}
	if err := s.LoadStaticFile(sampleZipFile(t, dir)); err != nil {
		t.Fatal(err)
	}

	var during *Static
	s.beforeParse = func() { during = s.Static() }
	if err := s.LoadStaticFile(sampleZipFile(t, dir)); err != nil {
		t.Fatal(err)
	}

	if during != nil {
		t.Error("old timetable still served while the new one was parsed")
	}
	assertResolves(t, &s)
}

func TestStore_BrokenZipFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	s := Store{StaticCache: filepath.Join(dir, "static.zip")}
	if err := s.LoadStaticFile(sampleZipFile(t, dir)); err != nil {
		t.Fatal(err)
	}

	if err := s.LoadStaticFile(garbageFile(t, dir)); err == nil {
		t.Fatal("LoadStaticFile(garbage): want error")
	}

	assertResolves(t, &s)
	if _, err := (&Store{StaticCache: s.StaticCache}).LoadCachedStatic(); err != nil {
		t.Errorf("cache damaged by failed load: %v", err)
	}
}

func TestStore_BrokenZipWithoutCacheLeavesNothing(t *testing.T) {
	var s Store
	if err := s.LoadStaticFile(sampleZipFile(t, t.TempDir())); err != nil {
		t.Fatal(err)
	}

	if err := s.LoadStaticFile(garbageFile(t, t.TempDir())); err == nil {
		t.Fatal("want error")
	}
	if s.Static() != nil {
		t.Error("Static() should be nil without a cache to fall back to")
	}
}

func TestStore_BrokenZipReportsFailedFallback(t *testing.T) {
	dir := t.TempDir()
	s := Store{StaticCache: filepath.Join(dir, "static.zip")}

	err := s.LoadStaticFile(garbageFile(t, dir))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want it to include the missing cache", err)
	}
	if s.Static() != nil {
		t.Error("Static() should be nil when nothing loaded")
	}
}

func TestStore_ReadErrorIsTransient(t *testing.T) {
	// Reading a directory fails in the OS (EISDIR), like EIO would.
	err := (&Store{}).LoadStaticFile(t.TempDir())
	if !errors.Is(err, fetch.ErrTransient) {
		t.Errorf("err = %v, want fetch.ErrTransient", err)
	}
}

func TestStore_BadContentIsNotTransient(t *testing.T) {
	dir := t.TempDir()
	for name, path := range map[string]string{
		"garbage": garbageFile(t, dir),
		"missing": filepath.Join(dir, "nope.zip"),
	} {
		err := (&Store{}).LoadStaticFile(path)
		if err == nil || errors.Is(err, fetch.ErrTransient) {
			t.Errorf("%s: err = %v, want a non-transient error", name, err)
		}
	}
}

func TestStore_LoadCachedStatic(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "static.zip")

	if _, err := (&Store{StaticCache: cache}).LoadCachedStatic(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no cache yet: err = %v, want fs.ErrNotExist", err)
	}

	first := Store{StaticCache: cache}
	if err := first.LoadStaticFile(sampleZipFile(t, dir)); err != nil {
		t.Fatal(err)
	}

	// A restarted process picks the timetable up from disk.
	second := Store{StaticCache: cache}
	modTime, err := second.LoadCachedStatic()
	if err != nil {
		t.Fatalf("LoadCachedStatic: %v", err)
	}
	if modTime.IsZero() {
		t.Error("modTime is zero; it seeds If-Modified-Since")
	}
	assertResolves(t, &second)
}

func TestStore_LoadRealtime(t *testing.T) {
	var s Store
	if err := s.LoadRealtime(sampleUpdates(t)); err != nil {
		t.Fatalf("LoadRealtime: %v", err)
	}
	before := s.Snapshot()
	if before.Len() != 76 {
		t.Errorf("Snapshot().Len() = %d, want 76", before.Len())
	}

	if err := s.LoadRealtime([]byte("\xff\xff\xff garbage")); err == nil {
		t.Error("LoadRealtime(garbage): want error")
	}
	if s.Snapshot() != before {
		t.Error("failed load replaced the previous snapshot")
	}
}
