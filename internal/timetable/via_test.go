package timetable

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"

	"pociag.info/internal/realtime"
)

// Synthetic feed for via journeys: stations Alfa, Beta, Gamma, Delta, all on
// 2026-10-22.
var viaFeed = fstest.MapFS{
	"agency.txt": {Data: []byte("agency_id,agency_name,agency_url,agency_timezone\n" +
		"KD,Koleje Dolnośląskie,https://x,Europe/Warsaw\n")},
	"routes.txt": {Data: []byte("route_id,agency_id,route_short_name,route_long_name,route_type\n" +
		"KD,KD,KD,KD,2\n")},
	"stops.txt": {Data: []byte("stop_id,stop_name,location_type,parent_station,platform_code\n" +
		"A,Alfa,1,,\nB,Beta,1,,\nC,Gamma,1,,\nD,Delta,1,,\n")},
	"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
		"AB0730,KD,S,Beta,KD,AB0730,\n" +
		"AB0800,KD,S,Beta,KD,AB0800,\n" +
		"AB0830,KD,S,Beta,KD,AB0830,\n" +
		"AB0900,KD,S,Beta,KD,AB0900,\n" +
		"BC0910,KD,S,Gamma,KD,BC0910,\n" +
		"BC0920,KD,S,Gamma,KD,BC0920,\n" +
		"BC1004,KD,S,Gamma,KD,BC1004,\n" +
		"BC1005,KD,S,Gamma,KD,BC1005,\n" +
		"THRU,KD,S,Gamma,KD,THRU,\n" +
		"CD1010,KD,S,Delta,KD,CD1010,\n" +
		"CD1140,KD,S,Delta,KD,CD1140,\n")},
	"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
		"AB0730,0,A,07:30:00,07:30:00\nAB0730,1,B,08:50:00,08:50:00\n" +
		"AB0800,0,A,08:00:00,08:00:00\nAB0800,1,B,09:00:00,09:00:00\n" +
		"AB0830,0,A,08:30:00,08:30:00\nAB0830,1,B,09:40:00,09:40:00\n" +
		"AB0900,0,A,09:00:00,09:00:00\nAB0900,1,B,10:00:00,10:00:00\n" +
		// BC0920 leaves after BC0910 but overtakes it.
		"BC0910,0,B,09:10:00,09:10:00\nBC0910,1,C,11:00:00,11:00:00\n" +
		"BC0920,0,B,09:20:00,09:20:00\nBC0920,1,C,10:00:00,10:00:00\n" +
		"BC1004,0,B,10:04:00,10:04:00\nBC1004,1,C,10:30:00,10:30:00\n" +
		"BC1005,0,B,10:05:00,10:05:00\nBC1005,1,C,11:30:00,11:30:00\n" +
		"THRU,0,A,11:00:00,11:00:00\nTHRU,1,B,12:00:00,12:02:00\nTHRU,2,C,13:00:00,13:00:00\n" +
		"CD1010,0,C,10:10:00,10:10:00\nCD1010,1,D,10:40:00,10:40:00\n" +
		"CD1140,0,C,11:40:00,11:40:00\nCD1140,1,D,12:00:00,12:00:00\n")},
	"calendar_dates.txt": {Data: []byte("service_id,date,exception_type\nS,20261022,1\n")},
}

// journeySummary renders a journey as "dep train from-to arr" legs joined
// by " | ", with live estimates where known.
func journeySummary(j Journey) string {
	legs := make([]string, len(j.Legs))
	for i, c := range j.Legs {
		legs[i] = fmt.Sprintf("%s %s %s-%s %s",
			c.Leaves().In(warsaw).Format("15:04"), c.Number, c.From, c.To, c.Arrives().In(warsaw).Format("15:04"))
	}
	return strings.Join(legs, " | ")
}

// via asks tt for journeys through stops, the way the via search was asked
// before journeys with changes replaced it.
func via(tt *Timetable, stops []string, now time.Time, n int, change time.Duration, snap *realtime.Snapshot) []Journey {
	return tt.Journeys(Query{Stops: stops, Now: now, N: n, Change: change}, snap)
}

func TestVia(t *testing.T) {
	tt := newTimetable(t, viaFeed)
	tests := []struct {
		name   string
		stops  []string
		now    string
		change time.Duration
		snap   *realtime.Snapshot
		want   []string
	}{
		{
			name:  "overtaking train preferred, exact change boundary, through train is one leg, beaten journey dropped",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			want: []string{
				"08:00 AB0800 Alfa-Beta 09:00 | 09:20 BC0920 Beta-Gamma 10:00",
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "shorter change catches an earlier train and beats a journey leaving earlier",
			stops: []string{"A", "B", "C"}, now: "08:15", change: 0,
			want: []string{
				"09:00 AB0900 Alfa-Beta 10:00 | 10:04 BC1004 Beta-Gamma 10:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "late first leg moves to a later connection",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			snap: snapshot(t, "AB0800", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:20", false), nil)),
			want: []string{
				"07:30 AB0730 Alfa-Beta 08:50 | 09:20 BC0920 Beta-Gamma 10:00",
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "cancelled leg is skipped",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			snap: snapshot(t, "BC0920", rt.TripDescriptor_CANCELED),
			want: []string{
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "two via stations",
			stops: []string{"A", "B", "C", "D"}, now: "00:00", change: 5 * time.Minute,
			want: []string{
				"08:00 AB0800 Alfa-Beta 09:00 | 09:20 BC0920 Beta-Gamma 10:00 | 10:10 CD1010 Gamma-Delta 10:40",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30 | 11:40 CD1140 Gamma-Delta 12:00",
			},
		},
		{
			name:  "no onward train",
			stops: []string{"A", "C", "B"}, now: "00:00", change: 5 * time.Minute,
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := via(tt, tc.stops, at("2026-10-22", tc.now), 10, tc.change, tc.snap)
			g := make([]string, len(got))
			for i, j := range got {
				g[i] = journeySummary(j)
			}
			if strings.Join(g, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s",
					strings.Join(g, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestVia_LimitsCount(t *testing.T) {
	tt := newTimetable(t, viaFeed)
	got := via(tt, []string{"A", "B", "C"}, at("2026-10-22", "00:00"), 2, 5*time.Minute, nil)
	if len(got) != 2 || got[0].Legs[0].Number != "AB0800" {
		t.Errorf("got %d journeys starting %v, want 2 starting AB0800", len(got), got)
	}
}

// TestVia_FallsBackWhenLaterLegFails: the fastest Beta-Gamma train strands
// the traveller at Gamma, but a slower one runs on through to Delta.
func TestVia_FallsBackWhenLaterLegFails(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"AB,KD,S,Beta,KD,AB,\nFAST,KD,S,Gamma,KD,FAST,\nSLOW,KD,S,Delta,KD,SLOW,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
			"AB,0,A,08:00:00,08:00:00\nAB,1,B,09:00:00,09:00:00\n" +
			"FAST,0,B,09:10:00,09:10:00\nFAST,1,C,09:58:00,09:58:00\n" +
			"SLOW,0,B,09:15:00,09:15:00\nSLOW,1,C,10:00:00,10:01:00\nSLOW,2,D,11:00:00,11:00:00\n")},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	got := via(newTimetable(t, feed), []string{"A", "B", "C", "D"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, nil)
	want := "08:00 AB Alfa-Beta 09:00 | 09:15 SLOW Beta-Delta 11:00"
	if len(got) != 1 || journeySummary(got[0]) != want {
		g := make([]string, len(got))
		for i, j := range got {
			g[i] = journeySummary(j)
		}
		t.Errorf("got %q, want [%q]", g, want)
	}
}

// TestVia_LoopTrainOnlyContinuesForward: a loop train calls at Beta twice;
// boarded at Alfa it reaches Beta on its second call, so it can't continue
// from the first, which it left before reaching Alfa.
func TestVia_LoopTrainOnlyContinuesForward(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"LOOP,KD,S,Gamma,KD,LOOP,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
			"LOOP,0,B,10:00:00,10:00:00\nLOOP,1,C,10:30:00,10:30:00\nLOOP,2,A,11:00:00,11:00:00\n" +
			"LOOP,3,B,11:30:00,11:30:00\nLOOP,4,C,12:00:00,12:00:00\n")},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, nil)
	want := "11:00 LOOP Alfa-Gamma 12:00"
	if len(got) != 1 || journeySummary(got[0]) != want {
		g := make([]string, len(got))
		for i, j := range got {
			g[i] = journeySummary(j)
		}
		t.Errorf("got %q, want [%q]", g, want)
	}
}

// TestVia_ThroughRestrictedStop: a train stays one ride through a via stop
// where it only sets down or only picks up, but nobody changes onto a train
// they can't board there, and the last stop must still let you off.
func TestVia_ThroughRestrictedStop(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"AB,KD,S,Beta,KD,AB,\nSETDOWN,KD,S,Gamma,KD,SETDOWN,\nPICKUP,KD,S,Gamma,KD,PICKUP,\nNOEND,KD,S,Gamma,KD,NOEND,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time,pickup_type,drop_off_type\n" +
			"AB,0,A,08:00:00,08:00:00,0,0\nAB,1,B,09:00:00,09:00:00,0,0\n" +
			"SETDOWN,0,A,09:00:00,09:00:00,0,0\nSETDOWN,1,B,10:00:00,10:00:00,1,0\nSETDOWN,2,C,11:00:00,11:00:00,0,0\n" +
			"PICKUP,0,A,12:00:00,12:00:00,0,0\nPICKUP,1,B,13:00:00,13:00:00,0,1\nPICKUP,2,C,14:00:00,14:00:00,0,0\n" +
			"NOEND,0,A,15:00:00,15:00:00,0,0\nNOEND,1,B,16:00:00,16:00:00,0,0\nNOEND,2,C,17:00:00,17:00:00,0,1\n")},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, nil)
	g := make([]string, len(got))
	for i, j := range got {
		g[i] = journeySummary(j)
	}
	want := []string{
		"09:00 SETDOWN Alfa-Gamma 11:00",
		"12:00 PICKUP Alfa-Gamma 14:00",
	}
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestVia_LaterTrainRunsOnThrough: the first Beta-Gamma train reaches Gamma
// sooner, but a slightly later one runs on to Delta without a change and
// gets there first.
func TestVia_LaterTrainRunsOnThrough(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"AB,KD,S,Beta,KD,AB,\nP,KD,S,Gamma,KD,P,\nQ,KD,S,Delta,KD,Q,\nCD,KD,S,Delta,KD,CD,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
			"AB,0,A,08:00:00,08:00:00\nAB,1,B,09:00:00,09:00:00\n" +
			"P,0,B,09:10:00,09:10:00\nP,1,C,10:00:00,10:00:00\n" +
			"Q,0,B,09:15:00,09:15:00\nQ,1,C,10:02:00,10:03:00\nQ,2,D,10:30:00,10:30:00\n" +
			"CD,0,C,10:10:00,10:10:00\nCD,1,D,11:00:00,11:00:00\n")},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	got := via(newTimetable(t, feed), []string{"A", "B", "C", "D"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, nil)
	want := "08:00 AB Alfa-Beta 09:00 | 09:15 Q Beta-Delta 10:30"
	if len(got) != 1 || journeySummary(got[0]) != want {
		g := make([]string, len(got))
		for i, j := range got {
			g[i] = journeySummary(j)
		}
		t.Errorf("got %q, want [%q]", g, want)
	}
}

// TestVia_OrderedByScheduledDeparture: like the direct board, journeys are
// listed by when they are scheduled to leave, so a delayed train near
// midnight stays under its own day's heading instead of after the next
// day's first train.
func TestVia_OrderedByScheduledDeparture(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"LATE,KD,S,Gamma,KD,LATE,\nEARLY,KD,S,Gamma,KD,EARLY,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
			"LATE,0,A,23:55:00,23:55:00\nLATE,1,B,24:10:00,24:11:00\nLATE,2,C,24:30:00,24:30:00\n" +
			"EARLY,0,A,24:05:00,24:05:00\nEARLY,1,B,24:20:00,24:21:00\nEARLY,2,C,24:40:00,24:40:00\n")},
		"calendar_dates.txt": viaFeed["calendar_dates.txt"],
	}
	next := func(clock string) *rt.TripUpdate_StopTimeEvent {
		return &rt.TripUpdate_StopTimeEvent{Time: proto.Int64(at("2026-10-23", clock).Unix())}
	}
	snap := snapshot(t, "LATE", rt.TripDescriptor_SCHEDULED,
		upd(0, nil, next("00:15")), upd(1, next("00:30"), next("00:31")), upd(2, next("00:50"), nil))
	got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-22", "23:00"), 10, 5*time.Minute, snap)
	g := make([]string, len(got))
	for i, j := range got {
		g[i] = journeySummary(j)
	}
	want := []string{
		"00:15 LATE Alfa-Gamma 00:50",
		"00:05 EARLY Alfa-Gamma 00:40",
	}
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestVia_SkippedViaStop: realtime says a train won't stop at the via
// station. Riding through it is still fine, but nobody gets off or on
// there. Skipping the journey's own first or last stop, or cancelling the
// whole train, does lose the ride.
func TestVia_SkippedViaStop(t *testing.T) {
	trips := "trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
		"X,KD,S,Gamma,KD,X,\nY,KD,S,Gamma,KD,Y,\n"
	x := "X,0,A,08:00:00,08:00:00\nX,1,B,09:00:00,09:00:00\nX,2,C,10:00:00,10:00:00\n"
	// Y would be the best change at Beta off X.
	y := "Y,0,B,09:10:00,09:10:00\nY,1,C,09:40:00,09:40:00\n"
	feed := func(stopTimes string) *Timetable {
		return newTimetable(t, fstest.MapFS{
			"agency.txt":         viaFeed["agency.txt"],
			"routes.txt":         viaFeed["routes.txt"],
			"stops.txt":          viaFeed["stops.txt"],
			"trips.txt":          {Data: []byte(trips)},
			"stop_times.txt":     {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" + stopTimes)},
			"calendar_dates.txt": viaFeed["calendar_dates.txt"],
		})
	}
	onlyX, withY := feed(x), feed(x+y)
	tests := []struct {
		name string
		tt   *Timetable
		snap *realtime.Snapshot
		want []string
	}{
		{"no realtime", onlyX, nil, []string{"08:00 X Alfa-Gamma 10:00"}},
		{"via stop skipped: still one ride", onlyX,
			snapshot(t, "X", rt.TripDescriptor_SCHEDULED, skip(1)),
			[]string{"08:00 X Alfa-Gamma 10:00"}},
		{"via stop skipped: the joined ride has live times", onlyX,
			snapshot(t, "X", rt.TripDescriptor_SCHEDULED, upd(0, nil, ev("08:10", true)), skip(1), upd(2, ev("10:12", false), nil)),
			[]string{"08:10 X Alfa-Gamma 10:12"}},
		{"first stop skipped", onlyX, snapshot(t, "X", rt.TripDescriptor_SCHEDULED, skip(0)), []string{}},
		{"last stop skipped", onlyX, snapshot(t, "X", rt.TripDescriptor_SCHEDULED, skip(2)), []string{}},
		{"train cancelled", onlyX, snapshot(t, "X", rt.TripDescriptor_CANCELED), []string{}},
		{"change at Beta", withY, nil, []string{"08:00 X Alfa-Beta 09:00 | 09:10 Y Beta-Gamma 09:40"}},
		{"no change at a skipped Beta", withY,
			snapshot(t, "X", rt.TripDescriptor_SCHEDULED, skip(1)),
			[]string{"08:00 X Alfa-Gamma 10:00"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := via(tc.tt, []string{"A", "B", "C"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, tc.snap)
			g := make([]string, len(got))
			for i, j := range got {
				g[i] = journeySummary(j)
				if c := j.Legs[0]; c.Live != nil && c.Live.Cancelled {
					t.Errorf("%s: first leg marked cancelled", g[i])
				}
			}
			if strings.Join(g, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// TestVia_ChangeAtLaterCall: R calls at Beta twice. The first time it only
// picks up, so you can't change there; the second time you can get off and
// change to Z. If R itself runs on to Gamma first, staying on wins, and the
// two ways onto R at Alfa make only one journey.
func TestVia_ChangeAtLaterCall(t *testing.T) {
	r := "R,0,A,08:00:00,08:00:00,0,0\nR,1,B,08:30:00,08:31:00,0,1\nR,2,D,09:00:00,09:00:00,0,0\nR,3,B,09:30:00,09:30:00,0,0\n"
	z := "Z,0,B,09:40:00,09:40:00,0,0\nZ,1,C,10:30:00,10:30:00,0,0\n"
	tests := []struct {
		name      string
		stopTimes string
		want      string
	}{
		{"change at the second call", r + z, "08:00 R Alfa-Beta 09:30 | 09:40 Z Beta-Gamma 10:30"},
		{"staying on wins, once", r + "R,4,C,10:20:00,10:20:00,0,0\n" + z, "08:00 R Alfa-Gamma 10:20"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			feed := fstest.MapFS{
				"agency.txt": viaFeed["agency.txt"],
				"routes.txt": viaFeed["routes.txt"],
				"stops.txt":  viaFeed["stops.txt"],
				"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
					"R,KD,S,Beta,KD,R,\nZ,KD,S,Gamma,KD,Z,\n")},
				"stop_times.txt":     {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time,pickup_type,drop_off_type\n" + tc.stopTimes)},
				"calendar_dates.txt": viaFeed["calendar_dates.txt"],
			}
			got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-22", "00:00"), 10, 5*time.Minute, nil)
			if len(got) != 1 || journeySummary(got[0]) != tc.want {
				g := make([]string, len(got))
				for i, j := range got {
					g[i] = journeySummary(j)
				}
				t.Errorf("got %q, want [%q]", g, tc.want)
			}
		})
	}
}

// TestVia_OnwardTrainAfterWindow: the only Alfa-Beta train runs on the last
// service day the search covers, and the only Beta-Gamma train the day
// after it arrives.
//
// candidates covers daysBack+searchDays+1 service days from the day of
// now-maxDelay minus daysBack. With now 2026-10-22 12:00 that day is 10-22:
//   - all times before 24:00: daysBack 0, days 10-22..10-24, so AB runs on
//     10-24 and BC on 10-25;
//   - AB arrives at 25:00: daysBack 1, days 10-21..10-24, so AB runs on
//     10-24, reaches Beta at 01:00 on 10-25, and BC runs on 10-26.
func TestVia_OnwardTrainAfterWindow(t *testing.T) {
	tests := []struct {
		name, abArrives, abDate, bcDate, want string
	}{
		{"same-day arrival", "21:00:00", "20261024", "20261025", "10-24 20:00 AB Alfa-Beta 21:00 | 10-25 07:00 BC Beta-Gamma 08:00"},
		{"arrival after midnight", "25:00:00", "20261024", "20261026", "10-24 20:00 AB Alfa-Beta 01:00 | 10-26 07:00 BC Beta-Gamma 08:00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			feed := fstest.MapFS{
				"agency.txt": viaFeed["agency.txt"],
				"routes.txt": viaFeed["routes.txt"],
				"stops.txt":  viaFeed["stops.txt"],
				"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
					"AB,KD,S1,Beta,KD,AB,\nBC,KD,S2,Gamma,KD,BC,\n")},
				"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
					"AB,0,A,20:00:00,20:00:00\nAB,1,B," + tc.abArrives + "," + tc.abArrives + "\n" +
					"BC,0,B,07:00:00,07:00:00\nBC,1,C,08:00:00,08:00:00\n")},
				"calendar_dates.txt": {Data: []byte("service_id,date,exception_type\nS1," + tc.abDate + ",1\nS2," + tc.bcDate + ",1\n")},
			}
			got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-22", "12:00"), 10, 5*time.Minute, nil)
			g := make([]string, len(got))
			for i, j := range got {
				legs := make([]string, len(j.Legs))
				for k, c := range j.Legs {
					legs[k] = c.Leaves().In(warsaw).Format("01-02 ") + journeySummary(Journey{[]Connection{c}})
				}
				g[i] = strings.Join(legs, " | ")
			}
			if len(g) != 1 || g[0] != tc.want {
				t.Errorf("got %q, want [%q]", g, tc.want)
			}
		})
	}
}

// TestVia_LateArrivalLooksPastWindow: within the window the only way on
// from Beta, BC1, arrives after the window ends, so a train past the
// window may beat it, and BC2 does. BC1 runs past midnight, so daysBack is
// 1 and with now 2026-10-27 12:00 the window is service days
// 10-26..10-29, ending 10-30 00:00 (clear of the 10-25 clock change).
// Asking for one journey, one is found
// within the window, so only its late arrival shows it may not be final.
func TestVia_LateArrivalLooksPastWindow(t *testing.T) {
	feed := fstest.MapFS{
		"agency.txt": viaFeed["agency.txt"],
		"routes.txt": viaFeed["routes.txt"],
		"stops.txt":  viaFeed["stops.txt"],
		"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
			"AB,KD,S1,Beta,KD,AB,\nBC1,KD,S1,Gamma,KD,BC1,\nBC2,KD,S2,Gamma,KD,BC2,\n")},
		"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
			"AB,0,A,20:00:00,20:00:00\nAB,1,B,21:00:00,21:00:00\n" +
			"BC1,0,B,22:00:00,22:00:00\nBC1,1,C,26:00:00,26:00:00\n" +
			"BC2,0,B,00:30:00,00:30:00\nBC2,1,C,01:00:00,01:00:00\n")},
		"calendar_dates.txt": {Data: []byte("service_id,date,exception_type\nS1,20261029,1\nS2,20261030,1\n")},
	}
	got := via(newTimetable(t, feed), []string{"A", "B", "C"}, at("2026-10-27", "12:00"), 1, 5*time.Minute, nil)
	want := "20:00 AB Alfa-Beta 21:00 | 00:30 BC2 Beta-Gamma 01:00"
	if len(got) != 1 || journeySummary(got[0]) != want {
		g := make([]string, len(got))
		for i, j := range got {
			g[i] = journeySummary(j)
		}
		t.Errorf("got %q, want [%q]", g, want)
	}
}
