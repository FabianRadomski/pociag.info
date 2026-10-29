// Package realtime parses GTFS-Realtime trip updates (mkuran.pl updates.pb).
//
// Updates are keyed by trip_id and service date and refer to stops by
// stop_sequence, which only matches the static feed published alongside the
// realtime one — keep both fresh.
package realtime

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"

	"pociag.info/internal/gtfs"
)

// Snapshot is one parsed feed.
type Snapshot struct {
	Timestamp time.Time
	trips     map[key]*Trip
}

type key struct {
	tripID string
	date   gtfs.Date
}

// Trip holds the updates for one trip on one service date.
type Trip struct {
	Cancelled   bool
	Replacement bool   // detour: the trip may not follow its scheduled stops
	Stops       []Stop // sorted by Sequence
}

// Stop is an update for one stop, identified by its GTFS stop_sequence.
type Stop struct {
	Sequence           int
	Arrival, Departure Event
	Skipped            bool
}

// Event is an actual (Confirmed) or predicted time; zero Time means absent.
type Event struct {
	Time      time.Time
	Confirmed bool
}

// Parse decodes a GTFS-Realtime FeedMessage. Entities other than trip
// updates, and trip updates without a start date, are ignored.
func Parse(b []byte) (*Snapshot, error) {
	var msg rt.FeedMessage
	if err := proto.Unmarshal(b, &msg); err != nil {
		return nil, fmt.Errorf("decode gtfs-realtime: %w", err)
	}
	s := &Snapshot{
		Timestamp: time.Unix(int64(msg.GetHeader().GetTimestamp()), 0),
		trips:     make(map[key]*Trip, len(msg.Entity)),
	}
	for _, e := range msg.Entity {
		tu := e.GetTripUpdate()
		if tu == nil || tu.GetTrip().GetStartDate() == "" {
			continue
		}
		date, err := parseDate(tu.GetTrip().GetStartDate())
		if err != nil {
			return nil, fmt.Errorf("entity %s: %w", e.GetId(), err)
		}
		s.trips[key{tu.GetTrip().GetTripId(), date}] = convertTrip(tu)
	}
	return s, nil
}

func convertTrip(tu *rt.TripUpdate) *Trip {
	rel := tu.GetTrip().GetScheduleRelationship()
	t := &Trip{
		Cancelled:   rel == rt.TripDescriptor_CANCELED,
		Replacement: rel == rt.TripDescriptor_REPLACEMENT,
		Stops:       make([]Stop, 0, len(tu.StopTimeUpdate)),
	}
	for _, u := range tu.StopTimeUpdate {
		t.Stops = append(t.Stops, Stop{
			Sequence:  int(u.GetStopSequence()),
			Arrival:   convertEvent(u.Arrival),
			Departure: convertEvent(u.Departure),
			Skipped:   u.GetScheduleRelationship() == rt.TripUpdate_StopTimeUpdate_SKIPPED,
		})
	}
	slices.SortFunc(t.Stops, func(a, b Stop) int { return cmp.Compare(a.Sequence, b.Sequence) })
	return t
}

// convertEvent maps mkuran.pl's uncertainty convention: 0 = actual
// (the JSON feed's "confirmed"), 1 = prediction.
func convertEvent(ev *rt.TripUpdate_StopTimeEvent) Event {
	if ev == nil || ev.Time == nil {
		return Event{}
	}
	return Event{
		Time:      time.Unix(ev.GetTime(), 0),
		Confirmed: ev.Uncertainty != nil && ev.GetUncertainty() == 0,
	}
}

func parseDate(s string) (gtfs.Date, error) {
	t, err := time.Parse("20060102", s)
	if err != nil {
		return gtfs.Date{}, fmt.Errorf("bad start_date %q", s)
	}
	return gtfs.DateOf(t), nil
}

// Len returns the number of trips with updates.
func (s *Snapshot) Len() int {
	if s == nil {
		return 0
	}
	return len(s.trips)
}

// Trip returns the updates for a trip on a service date. A nil Snapshot has
// no trips.
func (s *Snapshot) Trip(tripID string, date gtfs.Date) (*Trip, bool) {
	if s == nil {
		return nil, false
	}
	t, ok := s.trips[key{tripID, date}]
	return t, ok
}

// Stop returns the update for exactly this stop_sequence.
func (t *Trip) Stop(seq int) (Stop, bool) {
	i, ok := slices.BinarySearchFunc(t.Stops, seq, func(s Stop, seq int) int { return cmp.Compare(s.Sequence, seq) })
	if !ok {
		return Stop{}, false
	}
	return t.Stops[i], true
}

// Latest returns the last update at or before stop_sequence seq; per the
// GTFS-Realtime spec its delay carries over to later stops without updates.
func (t *Trip) Latest(seq int) (Stop, bool) {
	i, found := slices.BinarySearchFunc(t.Stops, seq, func(s Stop, seq int) int { return cmp.Compare(s.Sequence, seq) })
	if found {
		return t.Stops[i], true
	}
	if i == 0 {
		return Stop{}, false
	}
	return t.Stops[i-1], true
}
