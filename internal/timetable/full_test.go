package timetable

import (
	"os"
	"testing"
	"time"

	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// TestFullFeed times indexing and queries on a complete feed when
// POCIAG_FULL_GTFS points at an unpacked mkuran.pl GTFS directory, and live
// queries when POCIAG_FULL_RT points at a matching updates.pb.
func TestFullFeed(t *testing.T) {
	dir := os.Getenv("POCIAG_FULL_GTFS")
	if dir == "" {
		t.Skip("set POCIAG_FULL_GTFS to an unpacked feed to run")
	}
	feed, err := gtfs.Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tt := New(feed)
	t.Logf("New: %v", time.Since(start).Round(time.Millisecond))

	for _, q := range [][2]string{{"60103", "80416"}, {"33605", "80416"}, {"33605", "7500"}} {
		start := time.Now()
		got := tt.Connections(q[0], q[1], time.Date(2026, 10, 22, 8, 0, 0, 0, warsaw), 5)
		t.Logf("%s -> %s: %d results in %v", tt.StationName(q[0]), tt.StationName(q[1]), len(got), time.Since(start).Round(time.Microsecond))
		if len(got) == 0 {
			t.Errorf("no connections %s -> %s", q[0], q[1])
		}
	}

	rtPath := os.Getenv("POCIAG_FULL_RT")
	if rtPath == "" {
		return
	}
	raw, err := os.ReadFile(rtPath)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	snap, err := realtime.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("realtime.Parse: %d trips in %v", snap.Len(), time.Since(start).Round(time.Millisecond))
	now := snap.Timestamp
	start = time.Now()
	got := tt.Live("33605", "80416", now, 5, snap)
	t.Logf("Live Warszawa Centralna -> Kraków Główny at %s in %v:", now.In(warsaw).Format("15:04"), time.Since(start).Round(time.Microsecond))
	for _, c := range got {
		t.Logf("  %s", liveSummary(c))
	}
}
