package timetable

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"

	"pociag.info/internal/realtime"
)

// liveSummary renders the realtime part of a connection: "!" is an actual
// time, "~" a prediction, "?" unknown.
func liveSummary(c Connection) string {
	s := fmt.Sprintf("%s %s", c.Departure.In(warsaw).Format("01-02 15:04"), cmpOr(c.Name, c.Number))
	switch {
	case c.Live == nil:
		return s + " no live data"
	case c.Live.Cancelled:
		return s + " CANCELLED"
	}
	return s + " dep " + eventSummary(c.Live.Departure, c.Departure) + " arr " + eventSummary(c.Live.Arrival, c.Arrival)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func eventSummary(ev realtime.Event, scheduled time.Time) string {
	if ev.Time.IsZero() {
		return "?"
	}
	mark := "~"
	if ev.Confirmed {
		mark = "!"
	}
	return fmt.Sprintf("%+dm%s", int(ev.Time.Sub(scheduled).Round(time.Minute).Minutes()), mark)
}

func assertLive(t *testing.T, got []Connection, want []string) {
	t.Helper()
	g := make([]string, len(got))
	for i, c := range got {
		g[i] = liveSummary(c)
	}
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("live connections mismatch\n got:\n  %s\nwant:\n  %s",
			strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

// Helpers to build GTFS-Realtime snapshots for the synthetic feed.

func ev(clock string, confirmed bool) *rt.TripUpdate_StopTimeEvent {
	u := int32(1)
	if confirmed {
		u = 0
	}
	return &rt.TripUpdate_StopTimeEvent{Time: proto.Int64(at("2026-10-22", clock).Unix()), Uncertainty: proto.Int32(u)}
}

func upd(seq uint32, arr, dep *rt.TripUpdate_StopTimeEvent) *rt.TripUpdate_StopTimeUpdate {
	return &rt.TripUpdate_StopTimeUpdate{StopSequence: proto.Uint32(seq), Arrival: arr, Departure: dep}
}

func skip(seq uint32) *rt.TripUpdate_StopTimeUpdate {
	u := upd(seq, nil, nil)
	u.ScheduleRelationship = rt.TripUpdate_StopTimeUpdate_SKIPPED.Enum()
	return u
}

func snapshot(t *testing.T, tripID string, rel rt.TripDescriptor_ScheduleRelationship, stops ...*rt.TripUpdate_StopTimeUpdate) *realtime.Snapshot {
	t.Helper()
	b, err := proto.Marshal(&rt.FeedMessage{
		Header: &rt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")},
		Entity: []*rt.FeedEntity{{
			Id: proto.String("U"),
			TripUpdate: &rt.TripUpdate{
				Trip:           &rt.TripDescriptor{TripId: proto.String(tripID), StartDate: proto.String("20261022"), ScheduleRelationship: rel.Enum()},
				StopTimeUpdate: stops,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := realtime.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLive_Synthetic(t *testing.T) {
	tt := newTimetable(t, synthetic)
	// MAIN on 10-22: A_1 seq 0 dep 10:00, B seq 1 11:00/11:05, C seq 2 arr 12:00.
	tests := []struct {
		name     string
		from, to string
		now      string
		n        int
		snap     *realtime.Snapshot
		want     []string
	}{
		{
			name: "no snapshot: schedule only",
			from: "A", to: "C", now: "00:00", n: 10, snap: nil,
			want: []string{
				"10-22 10:00 MAIN no live data",
				"10-22 13:00 4000 no live data",
				"10-23 10:00 MAIN no live data",
			},
		},
		{
			name: "actual departure and predicted arrival",
			from: "A", to: "C", now: "00:00", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED,
				upd(0, nil, ev("10:07", true)),
				upd(1, ev("11:06", true), ev("11:09", true)),
				upd(2, ev("12:05", false), nil)),
			want: []string{"10-22 10:00 MAIN dep +7m! arr +5m~"},
		},
		{
			name: "delay carries over to later stops without updates, as a prediction",
			from: "A", to: "C", now: "00:00", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, upd(0, nil, ev("10:07", true))),
			want: []string{"10-22 10:00 MAIN dep +7m! arr +7m~"},
		},
		{
			name: "no update at or before the origin: departure unknown",
			from: "A", to: "C", now: "00:00", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, upd(1, nil, ev("11:10", false))),
			want: []string{"10-22 10:00 MAIN dep ? arr +5m~"},
		},
		{
			name: "scheduled before now but not departed yet: still listed",
			from: "A", to: "C", now: "10:05", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, upd(0, nil, ev("10:07", false))),
			want: []string{"10-22 10:00 MAIN dep +7m~ arr +7m~"},
		},
		{
			name: "scheduled before now and already departed: dropped",
			from: "A", to: "C", now: "10:05", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, upd(0, nil, ev("10:03", true))),
			want: []string{"10-22 13:00 4000 no live data"},
		},
		{
			name: "cancelled trip is shown as cancelled",
			from: "A", to: "C", now: "00:00", n: 2,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_CANCELED),
			want: []string{"10-22 10:00 MAIN CANCELLED", "10-22 13:00 4000 no live data"},
		},
		{
			name: "cancelled trip in the past is dropped",
			from: "A", to: "C", now: "10:30", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_CANCELED),
			want: []string{"10-22 13:00 4000 no live data"},
		},
		{
			name: "skipped origin cancels the connection",
			from: "A", to: "C", now: "00:00", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, skip(0)),
			want: []string{"10-22 10:00 MAIN CANCELLED"},
		},
		{
			name: "skipped stop elsewhere doesn't affect the connection",
			from: "B", to: "C", now: "11:00", n: 1,
			snap: snapshot(t, "MAIN", rt.TripDescriptor_SCHEDULED, skip(0), upd(1, nil, ev("11:05", true))),
			want: []string{"10-22 11:05 MAIN dep +0m! arr +0m~"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tt.Live(tc.from, tc.to, at("2026-10-22", tc.now), tc.n, tc.snap)
			assertLive(t, got, tc.want)
		})
	}
}

// Real snapshot captured at 2026-10-22 20:26:57 alongside the static sample;
// expectations from an independent decode of the same file.
func loadRealSnapshot(t *testing.T) *realtime.Snapshot {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/gtfs/wro-krk-updates.pb")
	if err != nil {
		t.Fatal(err)
	}
	s, err := realtime.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLive_RealData_LateTrainStillUpcoming(t *testing.T) {
	tt := newTimetable(t, nil)

	// SZKUNER was due out of Wrocław at 12:10 but left at 12:38.
	got := tt.Live(wroclawGlowny, krakowGlowny, at("2026-10-22", "12:30"), 2, loadRealSnapshot(t))

	assertLive(t, got, []string{
		"10-22 12:10 SZKUNER dep +28m! arr +35m!",
		"10-22 13:10 GALICJA dep +5m! arr +3m!",
	})
}

func TestLive_RealData_EveningBoard(t *testing.T) {
	tt := newTimetable(t, nil)

	// At snapshot time CHEŁMOŃSKI (19:14, +32) and GWAREK (20:10) have left.
	got := tt.Live(wroclawGlowny, krakowGlowny, at("2026-10-22", "20:27"), 2, loadRealSnapshot(t))

	assertLive(t, got, []string{
		"10-22 20:53 ODRA dep +4m~ arr +0m~",
		"10-23 03:10 URSA dep +0m~ arr +0m~",
	})
}

func TestLive_NonPositiveNReturnsNothing(t *testing.T) {
	tt := newTimetable(t, synthetic)

	for _, n := range []int{0, -1} {
		if got := tt.Live("A", "C", at("2026-10-22", "00:00"), n, nil); len(got) != 0 {
			t.Errorf("Live(n=%d) returned %d connections, want 0", n, len(got))
		}
	}
}
