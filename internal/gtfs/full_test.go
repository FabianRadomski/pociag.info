package gtfs

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TestLoad_FullFeed loads a complete feed when POCIAG_FULL_GTFS points at an
// unpacked mkuran.pl GTFS directory, and reports time and heap usage.
func TestLoad_FullFeed(t *testing.T) {
	dir := os.Getenv("POCIAG_FULL_GTFS")
	if dir == "" {
		t.Skip("set POCIAG_FULL_GTFS to an unpacked feed to run")
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()

	feed, err := Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}

	elapsed := time.Since(start)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	n := 0
	for _, tr := range feed.Trips {
		n += len(tr.StopTimes)
	}
	t.Logf("loaded %d stations, %d stops, %d trips, %d stop times in %v; live heap +%d MB",
		len(feed.Stations), len(feed.Stops), len(feed.Trips), n, elapsed.Round(time.Millisecond),
		(after.HeapAlloc-before.HeapAlloc)>>20)
	runtime.KeepAlive(feed)
}
