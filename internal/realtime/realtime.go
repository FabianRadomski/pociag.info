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
	"google.golang.org/protobuf/encoding/protowire"
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
	Replacement bool // detour: the trip may not follow its scheduled stops
	stops       []stop
}

// stop is a Stop packed into 24 bytes instead of 80; a feed has ~250k.
type stop struct {
	arr, dep int64  // Unix seconds
	seq      uint32 // as in the feed; Stop.Sequence holds it without loss
	flags    uint8
}

const (
	hasArr uint8 = 1 << iota
	hasDep
	arrConfirmed
	depConfirmed
	skipped
)

func packStop(s Stop) stop {
	p := stop{seq: uint32(s.Sequence)}
	set := func(bit uint8, on bool) {
		if on {
			p.flags |= bit
		}
	}
	set(hasArr, !s.Arrival.Time.IsZero())
	set(hasDep, !s.Departure.Time.IsZero())
	set(arrConfirmed, s.Arrival.Confirmed)
	set(depConfirmed, s.Departure.Confirmed)
	set(skipped, s.Skipped)
	p.arr, p.dep = s.Arrival.Time.Unix(), s.Departure.Time.Unix()
	return p
}

func (p stop) unpack() Stop {
	event := func(unix int64, has, confirmed uint8) Event {
		if p.flags&has == 0 {
			return Event{}
		}
		return Event{Time: time.Unix(unix, 0), Confirmed: p.flags&confirmed != 0}
	}
	return Stop{
		Sequence:  int(p.seq),
		Arrival:   event(p.arr, hasArr, arrConfirmed),
		Departure: event(p.dep, hasDep, depConfirmed),
		Skipped:   p.flags&skipped != 0,
	}
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

// FeedMessage field numbers.
const (
	headerField protowire.Number = 1
	entityField protowire.Number = 2
)

// Parse decodes a GTFS-Realtime FeedMessage. Entities other than trip
// updates, and trip updates without a start date, are ignored.
//
// Entities are decoded one at a time: decoding mkuran.pl's ~7MB feed in one
// go builds ~100MB of message objects, too much for a small server.
func Parse(b []byte) (*Snapshot, error) {
	s := &Snapshot{trips: map[key]*Trip{}}
	header := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, fmt.Errorf("decode gtfs-realtime: %w", protowire.ParseError(n))
		}
		b = b[n:]
		if typ != protowire.BytesType || num != headerField && num != entityField {
			n = protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, fmt.Errorf("decode gtfs-realtime: %w", protowire.ParseError(n))
			}
			b = b[n:]
			continue
		}
		v, n := protowire.ConsumeBytes(b)
		if n < 0 {
			return nil, fmt.Errorf("decode gtfs-realtime: %w", protowire.ParseError(n))
		}
		b = b[n:]

		if num == headerField {
			var h rt.FeedHeader
			if err := proto.Unmarshal(v, &h); err != nil {
				return nil, fmt.Errorf("decode gtfs-realtime header: %w", err)
			}
			s.Timestamp = time.Unix(int64(h.GetTimestamp()), 0)
			header = true
			continue
		}
		var e rt.FeedEntity
		if err := proto.Unmarshal(v, &e); err != nil {
			return nil, fmt.Errorf("decode gtfs-realtime entity: %w", err)
		}
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
	if !header {
		return nil, fmt.Errorf("decode gtfs-realtime: missing header")
	}
	return s, nil
}

func convertTrip(tu *rt.TripUpdate) *Trip {
	rel := tu.GetTrip().GetScheduleRelationship()
	t := &Trip{
		Cancelled:   rel == rt.TripDescriptor_CANCELED,
		Replacement: rel == rt.TripDescriptor_REPLACEMENT,
		stops:       make([]stop, 0, len(tu.StopTimeUpdate)),
	}
	for _, u := range tu.StopTimeUpdate {
		t.stops = append(t.stops, packStop(Stop{
			Sequence:  int(u.GetStopSequence()),
			Arrival:   convertEvent(u.Arrival),
			Departure: convertEvent(u.Departure),
			Skipped:   u.GetScheduleRelationship() == rt.TripUpdate_StopTimeUpdate_SKIPPED,
		}))
	}
	slices.SortFunc(t.stops, func(a, b stop) int { return cmp.Compare(a.seq, b.seq) })
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

// Stops returns the trip's updates sorted by Sequence.
func (t *Trip) Stops() []Stop {
	out := make([]Stop, len(t.stops))
	for i, p := range t.stops {
		out[i] = p.unpack()
	}
	return out
}

// Stop returns the update for exactly this stop_sequence.
func (t *Trip) Stop(seq int) (Stop, bool) {
	i, ok := t.search(seq)
	if !ok {
		return Stop{}, false
	}
	return t.stops[i].unpack(), true
}

// Latest returns the last update at or before stop_sequence seq; per the
// GTFS-Realtime spec its delay carries over to later stops without updates.
func (t *Trip) Latest(seq int) (Stop, bool) {
	i, found := t.search(seq)
	if found {
		return t.stops[i].unpack(), true
	}
	if i == 0 {
		return Stop{}, false
	}
	return t.stops[i-1].unpack(), true
}

func (t *Trip) search(seq int) (int, bool) {
	return slices.BinarySearchFunc(t.stops, seq, func(p stop, seq int) int { return cmp.Compare(int(p.seq), seq) })
}
