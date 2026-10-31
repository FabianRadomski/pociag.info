package timetable

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"

	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// lineFeed is a feed on stations Alfa-Beta-Gamma-Delta-Epsilon-Fi running on
// 2026-10-22, with the given trips ("ID,headsign") and stop times
// ("trip,seq,stop,arr,dep"), and optional transfers.txt rows.
func lineFeed(trips, stopTimes, transfers string) *gtfs.Feed {
	fsys := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": {Data: []byte("route_id,agency_id,route_short_name,route_long_name,route_type\nKD,KD,KD,KD,2\nBUS,KD,BUS,BUS,3\n")},
		"stops.txt": {Data: []byte("stop_id,stop_name,location_type,parent_station,platform_code\n" +
			"A,Alfa,1,,\nB,Beta,1,,\nB_BUS,Beta,0,B,\nC,Gamma,1,,\nD,Delta,1,,\nE,Epsilon,1,,\nF,Fi,1,,\n")},
		"trips.txt":          {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" + trips)},
		"stop_times.txt":     {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" + stopTimes)},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	if transfers != "" {
		fsys["transfers.txt"] = &fstest.MapFile{Data: []byte("from_stop_id,to_stop_id,from_trip_id,to_trip_id,transfer_type\n" + transfers)}
	}
	feed, err := gtfs.Load(fsys)
	if err != nil {
		panic(err)
	}
	return feed
}

func summaries(js []Journey) string {
	g := make([]string, len(js))
	for i, j := range js {
		g[i] = journeySummary(j)
	}
	return strings.Join(g, "\n")
}

func assertJourneys(t *testing.T, got []Journey, want ...string) {
	t.Helper()
	if g, w := summaries(got), strings.Join(want, "\n"); g != w {
		t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s", strings.ReplaceAll(g, "\n", "\n  "), strings.ReplaceAll(w, "\n", "\n  "))
	}
}

func query(stops ...string) Query {
	return Query{Stops: stops, Now: at("2026-10-22", "06:00"), N: 10, Change: 5 * time.Minute}
}

// trip writes a trip's rows for lineFeed: id, then stop and time pairs; a
// time "hh:mm" is both arrival and departure.
func trip(id string, stopsAndTimes ...string) (string, string) {
	var sb strings.Builder
	for i := 0; i < len(stopsAndTimes); i += 2 {
		fmt.Fprintf(&sb, "%s,%d,%s,%s:00,%s:00\n", id, i/2, stopsAndTimes[i], stopsAndTimes[i+1], stopsAndTimes[i+1])
	}
	return id + ",KD,S,x,KD," + id + ",\n", sb.String()
}

func feedOf(trips ...[2]string) *Timetable {
	var ts, sts strings.Builder
	for _, t := range trips {
		ts.WriteString(t[0])
		sts.WriteString(t[1])
	}
	return New(lineFeed(ts.String(), sts.String(), ""))
}

func tr(id string, stopsAndTimes ...string) [2]string {
	a, b := trip(id, stopsAndTimes...)
	return [2]string{a, b}
}

// TestJourneys_FindsChanges: without a direct train, a journey changes
// where it has to, and the direct board stays empty.
func TestJourneys_FindsChanges(t *testing.T) {
	tt := feedOf(
		tr("AB", "A", "08:00", "B", "09:00"),
		tr("BC", "B", "09:10", "C", "10:00"),
		tr("CD", "C", "10:05", "D", "10:30"),
		tr("CD2", "C", "10:04", "D", "10:20"), // too soon after BC
	)
	assertJourneys(t, tt.Journeys(query("A", "D"), nil),
		"08:00 AB Alfa-Beta 09:00 | 09:10 BC Beta-Gamma 10:00 | 10:05 CD Gamma-Delta 10:30")
	if got := tt.Connections("A", "D", at("2026-10-22", "06:00"), 10); len(got) != 0 {
		t.Errorf("direct connections: %v", got)
	}
}

// TestJourneys_SlowerDirectTrainKept: a direct train is listed even when a
// journey with a change leaving later gets there first, but not a journey
// with a change that a direct train beats.
func TestJourneys_SlowerDirectTrainKept(t *testing.T) {
	tt := feedOf(
		tr("SLOW", "A", "08:00", "B", "09:30", "C", "11:00"),
		tr("AB", "A", "08:10", "B", "09:00"),
		tr("BC", "B", "09:10", "C", "10:00"),
		// Leaves later and arrives as early as AB+BC2 with no change.
		tr("AB2", "A", "12:00", "B", "12:30"),
		tr("BC2", "B", "12:40", "C", "13:30"),
		tr("FAST", "A", "12:05", "C", "13:30"),
	)
	assertJourneys(t, tt.Journeys(query("A", "C"), nil),
		"08:00 SLOW Alfa-Gamma 11:00",
		"08:10 AB Alfa-Beta 09:00 | 09:10 BC Beta-Gamma 10:00",
		"12:05 FAST Alfa-Gamma 13:30",
	)
}

// TestJourneys_OnePerDeparture: of the journeys leaving on the same train,
// the one arriving first is listed, even with more changes.
func TestJourneys_OnePerDeparture(t *testing.T) {
	tt := feedOf(
		tr("X", "A", "08:00", "B", "09:00", "C", "10:00"),
		tr("Y", "B", "09:10", "C", "09:40"),
	)
	assertJourneys(t, tt.Journeys(query("A", "C"), nil),
		"08:00 X Alfa-Beta 09:00 | 09:10 Y Beta-Gamma 09:40")
}

// TestJourneys_MaxChanges: three changes are fine and four too many,
// except that each via station allows one more; Direct allows none.
func TestJourneys_MaxChanges(t *testing.T) {
	tt := feedOf(
		tr("AB", "A", "08:00", "B", "08:30"),
		tr("BC", "B", "08:40", "C", "09:10"),
		tr("CD", "C", "09:20", "D", "09:50"),
		tr("DE", "D", "10:00", "E", "10:30"),
		tr("EF", "E", "10:40", "F", "11:10"),
	)
	assertJourneys(t, tt.Journeys(query("A", "E"), nil),
		"08:00 AB Alfa-Beta 08:30 | 08:40 BC Beta-Gamma 09:10 | 09:20 CD Gamma-Delta 09:50 | 10:00 DE Delta-Epsilon 10:30")
	if got := tt.Journeys(query("A", "F"), nil); len(got) != 0 {
		t.Errorf("A-F takes four changes: got %s", summaries(got))
	}
	assertJourneys(t, tt.Journeys(query("A", "C", "F"), nil),
		"08:00 AB Alfa-Beta 08:30 | 08:40 BC Beta-Gamma 09:10 | 09:20 CD Gamma-Delta 09:50 | 10:00 DE Delta-Epsilon 10:30 | 10:40 EF Epsilon-Fi 11:10")
	q := query("A", "C", "F")
	q.Direct = true
	if got := tt.Journeys(q, nil); len(got) != 0 {
		t.Errorf("Direct: got %s", summaries(got))
	}
}

// TestJourneys_Direct: only trains without a change.
func TestJourneys_Direct(t *testing.T) {
	tt := feedOf(
		tr("X", "A", "08:00", "B", "09:00", "C", "10:00"),
		tr("Y", "B", "09:10", "C", "09:40"),
		tr("Z", "A", "07:00", "B", "07:30"),
	)
	q := query("A", "C")
	q.Direct = true
	assertJourneys(t, tt.Journeys(q, nil), "08:00 X Alfa-Gamma 10:00")
}

// TestJourneys_SplitTrain: a train split into two trips (transfers.txt type
// 1) needs no change time between them and isn't a change; each part is a
// leg of its own. A real change at the same place does need the time.
func TestJourneys_SplitTrain(t *testing.T) {
	trips := "RAIL,KD,S,Gamma,KD,100,\nBUS,BUS,S,Gamma,BUS,100,\nOTHER,KD,S,Gamma,KD,200,\n"
	stopTimes := "RAIL,0,A,08:00:00,08:00:00\nRAIL,1,B,09:00:00,09:00:00\n" +
		"BUS,0,B_BUS,09:02:00,09:02:00\nBUS,1,C,10:00:00,10:00:00\n" +
		"OTHER,0,B,09:02:00,09:02:00\nOTHER,1,D,10:00:00,10:00:00\n"
	tt := New(lineFeed(trips, stopTimes, "B,B_BUS,RAIL,BUS,1\n"))
	q := query("A", "C")
	q.Direct = true
	got := tt.Journeys(q, nil)
	assertJourneys(t, got, "08:00 100 Alfa-Beta 09:00 | 09:02 100 Beta-Gamma 10:00")
	if len(got) == 1 && !got[0].Legs[1].Bus {
		t.Error("bus part not marked as a bus")
	}
	if got := tt.Journeys(query("A", "D"), nil); len(got) != 0 {
		t.Errorf("2 minutes to change to OTHER: got %s", summaries(got))
	}
}

// TestJourneys_Live: live times decide which changes can be made.
func TestJourneys_Live(t *testing.T) {
	tt := feedOf(
		tr("AB", "A", "08:00", "B", "09:00"),
		tr("BC", "B", "09:10", "C", "10:00"),
		tr("BC2", "B", "10:10", "C", "11:00"),
	)
	late := snapshot(t, "AB", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:08", false), nil))
	assertJourneys(t, tt.Journeys(query("A", "C"), late),
		"08:00 AB Alfa-Beta 09:08 | 10:10 BC2 Beta-Gamma 11:00")
	// BC is late too, enough to make it.
	b, _ := proto.Marshal(&rt.FeedMessage{
		Header: &rt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")},
		Entity: []*rt.FeedEntity{
			{Id: proto.String("1"), TripUpdate: &rt.TripUpdate{
				Trip:           &rt.TripDescriptor{TripId: proto.String("AB"), StartDate: proto.String("20261022")},
				StopTimeUpdate: []*rt.TripUpdate_StopTimeUpdate{upd(1, ev("09:08", false), nil)}}},
			{Id: proto.String("2"), TripUpdate: &rt.TripUpdate{
				Trip:           &rt.TripDescriptor{TripId: proto.String("BC"), StartDate: proto.String("20261022")},
				StopTimeUpdate: []*rt.TripUpdate_StopTimeUpdate{upd(0, nil, ev("09:15", false))}}},
		},
	})
	both, err := realtime.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	assertJourneys(t, tt.Journeys(query("A", "C"), both),
		"08:00 AB Alfa-Beta 09:08 | 09:15 BC Beta-Gamma 10:05")
}

// TestJourneys_LiveTimesInOrder: a prediction that would have the train
// leave a stop before it arrives there is moved up, so no change is made
// on it that can't be.
func TestJourneys_LiveTimesInOrder(t *testing.T) {
	tt := feedOf(
		tr("AB", "A", "08:00", "B", "09:00", "C", "09:30"),
		tr("CD", "C", "09:40", "D", "10:00"),
	)
	// Predicted 50 minutes late at B but leaving it on time, which would
	// bring it to C on time, in time for CD.
	snap := snapshot(t, "AB", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:50", false), ev("09:00", false)))
	assertJourneys(t, tt.Journeys(query("A", "D"), snap))
	assertJourneys(t, tt.Journeys(query("A", "C"), snap), "08:00 AB Alfa-Gamma 09:50")
}

// TestJourneys_MisalignedSnapshotIgnored: a snapshot whose stop sequences
// don't exist in the static feed isn't used, neither for journeys nor for
// the board.
func TestJourneys_MisalignedSnapshotIgnored(t *testing.T) {
	tt := feedOf(tr("AB", "A", "08:00", "B", "09:00"))
	snap := snapshot(t, "AB", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:30", false), nil), upd(2, ev("09:40", false), nil))
	if usable, fit, known := tt.Alignment(snap); usable || fit != 0 || known != 1 {
		t.Errorf("Alignment = %v, %d, %d; want false, 0, 1", usable, fit, known)
	}
	if tt.Match(snap) != nil {
		t.Error("Match kept a misaligned snapshot")
	}
	assertJourneys(t, tt.Journeys(query("A", "B"), snap), "08:00 AB Alfa-Beta 09:00")

	good := snapshot(t, "AB", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:30", false), nil))
	if tt.Match(good) != good {
		t.Error("Match dropped an aligned snapshot")
	}
	assertJourneys(t, tt.Journeys(query("A", "B"), good), "08:00 AB Alfa-Beta 09:30")
}

// TestJourneys_RealData: in the Wrocław-Kraków sample, at the time of its
// realtime snapshot, the journeys are the direct board's trains (see
// TestLive_RealData_EveningBoard), ODRA leaving 4 minutes late.
func TestJourneys_RealData(t *testing.T) {
	tt := newTimetable(t, nil)
	snap := loadRealSnapshot(t)
	if usable, fit, known := tt.Alignment(snap); !usable {
		t.Fatalf("sample snapshot misaligned: %d of %d trips fit", fit, known)
	}
	got := tt.Journeys(Query{Stops: []string{wroclawGlowny, krakowGlowny}, Now: at("2026-10-22", "20:27"), N: 3, Change: 5 * time.Minute}, snap)
	assertJourneys(t, got,
		"20:57 73004/5 Wrocław Główny-Kraków Główny 23:43",
		"03:10 73010/1 Wrocław Główny-Kraków Główny 06:33",
		"05:10 6304/5 Wrocław Główny-Kraków Główny 08:15",
	)
}

// reference is an independent search for checking Journeys: the earliest
// arrival at the last of stops after passing the ones in between in order,
// leaving the first at or after dep, with at most maxLegs legs. It rides
// whole runs in rounds, one more leg each, on scheduled times, using only
// connections the index for a search at now covers.
func reference(tt *Timetable, stops []string, now, dep time.Time, maxLegs int, change time.Duration) time.Time {
	start := now.Add(-maxDelay).Truncate(time.Hour)
	end := tt.windowEnd(start).AddDate(0, 0, extraDays)
	states := len(stops) - 1
	next := func(v int, st string) int {
		if v < states-1 && stops[v+1] == st {
			return v + 1
		}
		return v
	}
	arr := map[string][]time.Time{}
	get := func(m map[string][]time.Time, st string, v int) time.Time {
		if m[st] == nil {
			return time.Time{}
		}
		return m[st][v]
	}
	set := func(st string, v int, t time.Time) {
		if arr[st] == nil {
			arr[st] = make([]time.Time, states)
		}
		if a := arr[st][v]; a.IsZero() || t.Before(a) {
			arr[st][v] = t
		}
	}
	first := gtfs.DateOf(start.In(warsaw)).AddDays(-tt.daysBack)
	for k := 1; k <= maxLegs; k++ {
		prev := map[string][]time.Time{}
		for st, ts := range arr {
			prev[st] = slices.Clone(ts)
		}
		for date := first; date.At(0, warsaw).Before(end); date = date.AddDays(1) {
			for _, trip := range tt.trips {
				if !tt.feed.Runs(trip.ServiceID, date) {
					continue
				}
				riding := -1
				for _, st := range trip.StopTimes {
					station := tt.stationOf[st.StopID]
					if riding >= 0 && st.Arrival != gtfs.NoTime {
						riding = next(riding, station)
						if !st.NoDropOff {
							set(station, riding, date.At(st.Arrival, warsaw))
						}
					}
					if st.Departure == gtfs.NoTime {
						riding = -1
						continue
					}
					d := date.At(st.Departure, warsaw)
					if !st.NoPickup && !d.Before(start) && d.Before(end) {
						for v := states - 1; v > riding; v-- {
							ready := get(prev, station, v)
							if !ready.IsZero() {
								ready = ready.Add(change)
							}
							if station == stops[0] && v == 0 {
								ready = dep
							}
							if !ready.IsZero() && !ready.After(d) {
								riding = v
								break
							}
						}
					}
					if !d.Before(end) {
						riding = -1
					}
				}
			}
		}
	}
	return get(arr, stops[len(stops)-1], states-1)
}

// TestJourneys_MatchReference runs the steps Journeys documents with the
// reference search in place of the connection scan, on a busy generated
// line, and expects the same journeys.
func TestJourneys_MatchReference(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	feed, err := gtfs.Load(benchFeed(25, 12))
	if err != nil {
		t.Fatal(err)
	}
	tt := New(feed)
	change := 5 * time.Minute
	const n = 5
	checked := map[int]int{}
	for _, stops := range [][]string{{"A", "E"}, {"A", "C"}, {"B", "E"}, {"E", "A"}, {"A", "C", "E"}, {"A", "B", "D"}} {
		maxLegs := 1 + MaxChanges + len(stops) - 2
		// Departures from the first stop, to find the latest one that
		// still makes an arrival.
		var deps []time.Time
		for _, trip := range tt.trips {
			for _, st := range trip.StopTimes {
				if tt.stationOf[st.StopID] == stops[0] && !st.NoPickup {
					for d := 17; d <= 28; d++ {
						date := gtfs.Date{Year: 2026, Month: 10, Day: d}
						if tt.feed.Runs(trip.ServiceID, date) {
							deps = append(deps, date.At(st.Departure, warsaw))
						}
					}
				}
			}
		}
		slices.SortFunc(deps, func(a, b time.Time) int { return a.Compare(b) })

		for _, now := range []time.Time{at("2026-10-22", "05:00"), at("2026-10-23", "17:30"), at("2026-10-24", "23:00")} {
			var want []string
			tau := now
			for len(want) < n {
				pick, pickDep, pickArr := -1, time.Time{}, time.Time{}
				var prevArr time.Time
				for l := 1; l <= maxLegs; l++ {
					a := reference(tt, stops, now, tau, l, change)
					if a.IsZero() || !prevArr.IsZero() && !a.Before(prevArr) {
						continue
					}
					prevArr = a
					var d time.Time
					for _, dep := range deps {
						if !dep.Before(tau) && !dep.After(a) && reference(tt, stops, now, dep, l, change).Equal(a) {
							d = dep
						}
					}
					if pick < 0 || !d.After(pickDep) {
						pick, pickDep, pickArr = l, d, a
					}
				}
				if pick < 0 {
					break
				}
				want = append(want, fmt.Sprintf("%s %s %d", pickDep.Format("01-02 15:04"), pickArr.Format("01-02 15:04"), pick-1))
				checked[pick-1]++
				tau = pickDep.Add(time.Second)
			}
			got := tt.Journeys(Query{Stops: stops, Now: now, N: n, Change: change}, nil)
			g := make([]string, len(got))
			for i, j := range got {
				g[i] = fmt.Sprintf("%s %s %d", j.Leaves().In(warsaw).Format("01-02 15:04"), j.Arrives().In(warsaw).Format("01-02 15:04"), len(j.Legs)-1)
				for k := 1; k < len(j.Legs); k++ {
					a, b := j.Legs[k-1], j.Legs[k]
					if a.To != b.From || b.Leaves().Before(a.Arrives().Add(change)) {
						t.Errorf("%v at %v: impossible change in %s", stops, now, journeySummary(j))
					}
				}
			}
			if strings.Join(g, "\n") != strings.Join(want, "\n") {
				t.Errorf("%v at %v:\n got:\n  %s\nwant:\n  %s", stops, now, strings.Join(g, "\n  "), strings.Join(want, "\n  "))
			}
		}
	}
	t.Logf("journeys checked by number of changes: %v", checked)
	if checked[0] == 0 || checked[1] == 0 || checked[2] == 0 {
		t.Error("the generated line doesn't exercise journeys with 0, 1 and 2 changes")
	}
}

func TestRunChainCycle(t *testing.T) {
	// 0 goes on as 1 or 3; 1 and 2 loop back into each other; 3 goes on as 4.
	ix := &index{runs: []run{
		{next: []uint32{1, 3}},
		{next: []uint32{2}},
		{next: []uint32{1}},
		{next: []uint32{4}},
		{},
	}}
	if got, want := runChain(ix, 0, 4), []uint32{0, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("runChain(0, 4) = %v, want %v", got, want)
	}
	if got := runChain(ix, 1, 4); got != nil {
		t.Errorf("runChain(1, 4) = %v, want nil", got)
	}
}
