package realtime

import (
	"os"
	"testing"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"

	"pociag.info/internal/gtfs"
)

var warsaw, _ = time.LoadLocation("Europe/Warsaw")

func clock(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, warsaw)
	if err != nil {
		panic(err)
	}
	return t
}

func event(at string, uncertainty *int32) *rt.TripUpdate_StopTimeEvent {
	return &rt.TripUpdate_StopTimeEvent{Time: proto.Int64(clock(at).Unix()), Uncertainty: uncertainty}
}

var (
	confirmed = proto.Int32(0)
	predicted = proto.Int32(1)
)

func encode(t *testing.T, msg *rt.FeedMessage) []byte {
	t.Helper()
	if msg.Header == nil {
		msg.Header = &rt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(uint64(clock("2026-10-22 20:26:57").Unix()))}
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tripEntity(id, tripID, startDate string, rel rt.TripDescriptor_ScheduleRelationship, stops ...*rt.TripUpdate_StopTimeUpdate) *rt.FeedEntity {
	return &rt.FeedEntity{
		Id: proto.String(id),
		TripUpdate: &rt.TripUpdate{
			Trip:           &rt.TripDescriptor{TripId: proto.String(tripID), StartDate: proto.String(startDate), ScheduleRelationship: rel.Enum()},
			StopTimeUpdate: stops,
		},
	}
}

func stopUpdate(seq uint32, arr, dep *rt.TripUpdate_StopTimeEvent) *rt.TripUpdate_StopTimeUpdate {
	return &rt.TripUpdate_StopTimeUpdate{StopSequence: proto.Uint32(seq), Arrival: arr, Departure: dep}
}

func mustParse(t *testing.T, b []byte) *Snapshot {
	t.Helper()
	s, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}

func TestParse_HeaderTimestamp(t *testing.T) {
	s := mustParse(t, encode(t, &rt.FeedMessage{}))

	if want := clock("2026-10-22 20:26:57"); !s.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", s.Timestamp, want)
	}
}

func TestParse_TripLookupByIDAndServiceDate(t *testing.T) {
	s := mustParse(t, encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		tripEntity("U1", "T1", "20261022", rt.TripDescriptor_SCHEDULED,
			stopUpdate(1, nil, event("2026-10-22 10:07:00", confirmed))),
	}}))

	if _, ok := s.Trip("T1", gtfs.Date{Year: 2026, Month: 10, Day: 22}); !ok {
		t.Error("Trip(T1, 10-22) not found")
	}
	if _, ok := s.Trip("T1", gtfs.Date{Year: 2026, Month: 10, Day: 23}); ok {
		t.Error("Trip(T1, 10-23) found; updates are per service date")
	}
	if _, ok := s.Trip("T2", gtfs.Date{Year: 2026, Month: 10, Day: 22}); ok {
		t.Error("Trip(T2) found")
	}
	var nilSnap *Snapshot
	if _, ok := nilSnap.Trip("T1", gtfs.Date{Year: 2026, Month: 10, Day: 22}); ok {
		t.Error("nil snapshot returned a trip")
	}
}

func TestParse_StopEvents(t *testing.T) {
	s := mustParse(t, encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		tripEntity("U1", "T1", "20261022", rt.TripDescriptor_SCHEDULED,
			// Deliberately out of order.
			stopUpdate(3, event("2026-10-22 12:05:00", nil), nil),
			stopUpdate(1, nil, event("2026-10-22 10:07:00", confirmed)),
			stopUpdate(2, event("2026-10-22 11:06:00", confirmed), event("2026-10-22 11:09:30", predicted)),
		),
	}}))
	trip, _ := s.Trip("T1", gtfs.Date{Year: 2026, Month: 10, Day: 22})

	if len(trip.Stops) != 3 || trip.Stops[0].Sequence != 1 || trip.Stops[2].Sequence != 3 {
		t.Fatalf("Stops not sorted by sequence: %+v", trip.Stops)
	}
	first, mid, last := trip.Stops[0], trip.Stops[1], trip.Stops[2]
	if !first.Arrival.Time.IsZero() || !first.Departure.Time.Equal(clock("2026-10-22 10:07:00")) || !first.Departure.Confirmed {
		t.Errorf("first = %+v; want no arrival, confirmed departure 10:07", first)
	}
	if !mid.Arrival.Confirmed || mid.Departure.Confirmed || !mid.Departure.Time.Equal(clock("2026-10-22 11:09:30")) {
		t.Errorf("mid = %+v; want confirmed arrival, predicted departure 11:09:30", mid)
	}
	if last.Arrival.Confirmed {
		t.Errorf("last = %+v; unset uncertainty must not count as confirmed", last)
	}
}

func TestTrip_StopAndLatest(t *testing.T) {
	s := mustParse(t, encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		tripEntity("U1", "T1", "20261022", rt.TripDescriptor_SCHEDULED,
			stopUpdate(2, nil, event("2026-10-22 10:07:00", confirmed)),
			stopUpdate(5, event("2026-10-22 11:00:00", predicted), nil),
		),
	}}))
	trip, _ := s.Trip("T1", gtfs.Date{Year: 2026, Month: 10, Day: 22})

	if _, ok := trip.Stop(3); ok {
		t.Error("Stop(3) found, want missing")
	}
	if st, ok := trip.Stop(5); !ok || st.Sequence != 5 {
		t.Errorf("Stop(5) = %+v, %v", st, ok)
	}
	for _, tc := range []struct {
		seq     int
		wantSeq int
		wantOK  bool
	}{{1, 0, false}, {2, 2, true}, {4, 2, true}, {5, 5, true}, {9, 5, true}} {
		st, ok := trip.Latest(tc.seq)
		if ok != tc.wantOK || (ok && st.Sequence != tc.wantSeq) {
			t.Errorf("Latest(%d) = seq %d, %v; want seq %d, %v", tc.seq, st.Sequence, ok, tc.wantSeq, tc.wantOK)
		}
	}
}

func TestParse_CancelledReplacementAndSkipped(t *testing.T) {
	skipped := stopUpdate(2, nil, nil)
	skipped.ScheduleRelationship = rt.TripUpdate_StopTimeUpdate_SKIPPED.Enum()
	s := mustParse(t, encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		tripEntity("U1", "CANCELLED", "20261022", rt.TripDescriptor_CANCELED),
		tripEntity("U2", "DETOUR", "20261022", rt.TripDescriptor_REPLACEMENT),
		tripEntity("U3", "SKIPS", "20261022", rt.TripDescriptor_SCHEDULED, skipped),
	}}))
	d := gtfs.Date{Year: 2026, Month: 10, Day: 22}

	if tr, _ := s.Trip("CANCELLED", d); tr == nil || !tr.Cancelled {
		t.Errorf("CANCELLED = %+v; want Cancelled", tr)
	}
	if tr, _ := s.Trip("DETOUR", d); tr == nil || !tr.Replacement || tr.Cancelled {
		t.Errorf("DETOUR = %+v; want Replacement only", tr)
	}
	if tr, _ := s.Trip("SKIPS", d); tr == nil || len(tr.Stops) != 1 || !tr.Stops[0].Skipped {
		t.Errorf("SKIPS = %+v; want one skipped stop", tr)
	}
}

func TestParse_IgnoresNonTripEntitiesAndMissingStartDate(t *testing.T) {
	s := mustParse(t, encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		{Id: proto.String("A1"), Alert: &rt.Alert{}},
		tripEntity("U1", "NODATE", "", rt.TripDescriptor_SCHEDULED),
		tripEntity("U2", "OK", "20261022", rt.TripDescriptor_SCHEDULED),
	}}))

	if s.Len() != 1 {
		t.Errorf("Len() = %d, want 1", s.Len())
	}
}

func TestParse_Invalid(t *testing.T) {
	if _, err := Parse([]byte("definitely not protobuf \xff\xff\xff")); err == nil {
		t.Error("Parse(garbage): want error, got nil")
	}
	bad := encode(t, &rt.FeedMessage{Entity: []*rt.FeedEntity{
		tripEntity("U1", "T1", "2026-10-22", rt.TripDescriptor_SCHEDULED),
	}})
	if _, err := Parse(bad); err == nil {
		t.Error("Parse with malformed start_date: want error, got nil")
	}
}

// Real mkuran.pl updates.pb captured 2026-10-22 20:26:57, trimmed to the
// trips in testdata/gtfs/wro-krk.
func TestParse_RealSnapshot(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/gtfs/wro-krk-updates.pb")
	if err != nil {
		t.Fatal(err)
	}
	s := mustParse(t, raw)

	if s.Len() != 76 {
		t.Errorf("Len() = %d, want 76", s.Len())
	}
	// SZKUNER left Wrocław Gł. (seq 18) 28 minutes late.
	trip, ok := s.Trip("PLK_IC_2026_354723233", gtfs.Date{Year: 2026, Month: 10, Day: 22})
	if !ok {
		t.Fatal("SZKUNER 10-22 missing")
	}
	st, ok := trip.Stop(18)
	if !ok || !st.Departure.Time.Equal(clock("2026-10-22 12:38:00")) || !st.Departure.Confirmed {
		t.Errorf("SZKUNER seq 18 = %+v, %v; want confirmed departure 12:38", st, ok)
	}
}
