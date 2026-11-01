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

	// A weekday morning in the feed.
	now := time.Now().In(warsaw)
	now = time.Date(now.Year(), now.Month(), now.Day()+2, 8, 0, 0, 0, warsaw)
	for _, q := range [][2]string{{"60103", "80416"}, {"33605", "80416"}, {"33605", "7500"}} {
		start := time.Now()
		got := tt.Connections(q[0], q[1], now, 5)
		t.Logf("%s -> %s: %d results in %v", tt.StationName(q[0]), tt.StationName(q[1]), len(got), time.Since(start).Round(time.Microsecond))
		if len(got) == 0 {
			t.Errorf("no connections %s -> %s", q[0], q[1])
		}
	}

	// Journeys, with changes; pairs of stations without a direct train
	// too.
	journeys := func(snap *realtime.Snapshot, now time.Time) {
		start := time.Now()
		ix := tt.index(snap, now)
		t.Logf("index: %d connections (%.1f MB), %d runs, built in %v",
			len(ix.conns), float64(len(ix.conns))*24/1e6, len(ix.runs), time.Since(start).Round(time.Millisecond))
		for _, q := range [][]string{
			{"60103", "80416"},          // Wrocław Główny - Kraków Główny
			{"60608", "19703"},          // Opole Główne - Toruń Główny
			{"60608", "33605"},          // Opole Główne - Warszawa Centralna
			{"60103", "5900"},           // Wrocław Główny - Gdynia Główna
			{"60608", "30601", "19703"}, // Opole Główne - Poznań Główny - Toruń Główny
		} {
			if tt.StationName(q[0]) == "" || tt.StationName(q[1]) == "" {
				continue
			}
			start := time.Now()
			got := tt.Journeys(Query{Stops: q, Now: now, N: 8, Change: 5 * time.Minute}, snap)
			t.Logf("Journeys %s -> %s: %d in %v", tt.StationName(q[0]), tt.StationName(q[1]), len(got), time.Since(start).Round(time.Microsecond))
			for _, j := range got {
				t.Logf("  %s", journeySummary(j))
			}
		}
	}
	journeys(nil, now)

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
	usable, fit, known := tt.Alignment(snap)
	t.Logf("Alignment: usable %v, %d of %d trips fit", usable, fit, known)
	now = snap.Timestamp
	journeys(snap, now)
	start = time.Now()
	got := tt.Live("33605", "80416", now, 5, snap)
	t.Logf("Live Warszawa Centralna -> Kraków Główny at %s in %v:", now.In(warsaw).Format("15:04"), time.Since(start).Round(time.Microsecond))
	for _, c := range got {
		t.Logf("  %s", liveSummary(c))
	}
}
