package timetable

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"pociag.info/internal/gtfs"
)

// benchFeed generates a busy line A-B-C-D-E running every day around
// 2026-10-22: per segment `local` trains a day spread over the clock, some
// running past midnight so daysBack is 1, plus `through` trains a day
// running over two or three segments.
func benchFeed(local, through int) fstest.MapFS {
	stations := []string{"A", "B", "C", "D", "E"}
	r := rand.New(rand.NewPCG(1, 2))
	var trips, stopTimes, dates strings.Builder
	trips.WriteString("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n")
	stopTimes.WriteString("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n")
	dates.WriteString("service_id,date,exception_type\n")
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for i := range 10 {
		fmt.Fprintf(&dates, "S,%s,1\n", start.AddDate(0, 0, i).Format("20060102"))
	}
	clock := func(s int) string { return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60) }
	n := 0
	addTrip := func(from, hops, dep int) {
		id := fmt.Sprintf("T%d", n)
		n++
		fmt.Fprintf(&trips, "%s,R,S,%s,KD,%s,\n", id, stations[from+hops], id)
		t := dep
		for k := 0; k <= hops; k++ {
			arr := t
			if k > 0 {
				arr = t + (20+r.IntN(70))*60
			}
			t = arr + r.IntN(3)*60
			fmt.Fprintf(&stopTimes, "%s,%d,%s,%s,%s\n", id, k, stations[from+k], clock(arr), clock(t))
		}
	}
	for seg := range len(stations) - 1 {
		for range local {
			addTrip(seg, 1, r.IntN(25*3600))
		}
	}
	for range through {
		hops := 2 + r.IntN(2)
		addTrip(r.IntN(len(stations)-hops), hops, r.IntN(25*3600))
	}
	return fstest.MapFS{
		"agency.txt": {Data: []byte("agency_id,agency_name,agency_url,agency_timezone\nKD,KD,https://x,Europe/Warsaw\n")},
		"routes.txt": {Data: []byte("route_id,agency_id,route_short_name,route_long_name,route_type\nR,KD,KD,KD,2\n")},
		"stops.txt": {Data: []byte("stop_id,stop_name,location_type,parent_station,platform_code\n" +
			"A,Alfa,1,,\nB,Beta,1,,\nC,Gamma,1,,\nD,Delta,1,,\nE,Epsilon,1,,\n")},
		"trips.txt":          {Data: []byte(trips.String())},
		"stop_times.txt":     {Data: []byte(stopTimes.String())},
		"calendar_dates.txt": {Data: []byte(dates.String())},
	}
}

func BenchmarkVia(b *testing.B) {
	feed, err := gtfs.Load(benchFeed(1000, 200))
	if err != nil {
		b.Fatal(err)
	}
	tt := New(feed)
	if tt.daysBack < 1 {
		b.Fatalf("daysBack = %d, want >= 1", tt.daysBack)
	}
	now := at("2026-10-22", "08:00")
	for _, stops := range [][]string{{"A", "B", "C"}, {"A", "B", "C", "D"}, {"A", "B", "C", "D", "E"}} {
		b.Run(fmt.Sprintf("via=%d", len(stops)-2), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if len(via(tt, stops, now, 10, 5*time.Minute, nil)) == 0 {
					b.Fatal("no journeys")
				}
			}
		})
	}
}
