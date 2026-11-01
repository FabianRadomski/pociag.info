// Package timetable answers "which trains go from A to B next?" from a
// static GTFS feed and live updates: direct trains, and journeys with
// changes.
package timetable

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata" // the feed's local times are Polish; don't depend on host tzdata

	"pociag.info/internal/gtfs"
)

// searchDays is how far past the query date Connections looks for departures.
const searchDays = 2

var warsaw = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Connection is one direct ride between two stations.
type Connection struct {
	Category          string // e.g. "IC", "TLK", "BUS"
	Number            string // e.g. "6304/5"
	Name              string // e.g. "GROTTGER"
	Carrier           string // agency ID, e.g. "IC", "KD"
	Bus               bool   // replacement bus rather than a train
	Departure         time.Time
	Arrival           time.Time
	DeparturePlatform string
	Destination       string // trip headsign
	From, To          string // names of the stations where you board and alight

	// Keys for joining with GTFS-Realtime trip updates.
	TripID      string
	ServiceDate gtfs.Date

	Live *Live // nil without realtime data for this trip

	trip         *gtfs.Trip
	origin, dest gtfs.StopTime
}

// Timetable indexes a feed by station for fast lookups.
type Timetable struct {
	feed      *gtfs.Feed
	byStation map[string][]stopRef
	stationOf map[gtfs.Str]string // stop ID → its station
	names     map[string]string
	// daysBack: trips with times past 24:00 belong to earlier service days.
	daysBack int

	// For journeys: trips by ID, stations numbered from 0, the station
	// number of each of a trip's stops, and the trips a train goes on as.
	trips       []*gtfs.Trip
	stationNum  map[string]uint32
	stationIDs  []string // by number
	stopStation map[*gtfs.Trip][]uint32
	continues   map[*gtfs.Trip][]*gtfs.Trip

	mu        sync.Mutex // guards idx and lastConns, and is held while an index is built
	idx       *index
	lastConns int // capacity for the next index's connections
	// fit caches the alignment check of the latest snapshot.
	fit atomic.Pointer[alignment]
}

type stopRef struct {
	trip *gtfs.Trip
	idx  int // into trip.StopTimes
}

func New(feed *gtfs.Feed) *Timetable {
	t := &Timetable{
		feed:        feed,
		byStation:   map[string][]stopRef{},
		stationOf:   map[gtfs.Str]string{},
		names:       make(map[string]string, len(feed.Stations)),
		stationNum:  map[string]uint32{},
		stopStation: make(map[*gtfs.Trip][]uint32, len(feed.Trips)),
		continues:   map[*gtfs.Trip][]*gtfs.Trip{},
	}
	for _, trip := range feed.Trips {
		t.trips = append(t.trips, trip)
	}
	slices.SortFunc(t.trips, func(a, b *gtfs.Trip) int { return cmp.Compare(a.ID, b.ID) })
	var maxOffset gtfs.Time
	for _, trip := range t.trips {
		nums := make([]uint32, len(trip.StopTimes))
		for i, st := range trip.StopTimes {
			s, ok := t.stationOf[st.StopID]
			if !ok {
				s = feed.Str(st.StopID)
				if stop, ok := feed.Stops[s]; ok {
					s = stop.StationID
				}
				t.stationOf[st.StopID] = s
			}
			t.byStation[s] = append(t.byStation[s], stopRef{trip, i})
			maxOffset = max(maxOffset, st.Arrival, st.Departure)

			n, ok := t.stationNum[s]
			if !ok {
				n = uint32(len(t.stationIDs))
				t.stationNum[s] = n
				t.stationIDs = append(t.stationIDs, s)
			}
			nums[i] = n
		}
		t.stopStation[trip] = nums
		for _, id := range feed.Continues[trip.ID] {
			t.continues[trip] = append(t.continues[trip], feed.Trips[id])
		}
	}
	t.daysBack = int(maxOffset.Duration() / (24 * time.Hour))
	for id, s := range feed.Stations {
		t.names[id] = s.Name
	}
	return t
}

// StationName returns the station's name, or "" if unknown.
func (t *Timetable) StationName(id string) string { return t.names[id] }

// StationNames returns station ID → name. The map must not be modified.
func (t *Timetable) StationNames() map[string]string { return t.names }

// Connections returns up to n direct rides from station `from` to station
// `to` departing at or after `after`, ordered by departure.
func (t *Timetable) Connections(from, to string, after time.Time, n int) []Connection {
	out := t.candidates(from, to, after, time.Time{}, true, true)
	return out[:min(max(n, 0), len(out))]
}

// candidates returns every ride from `from` to `to` scheduled to depart at
// or after `since` within the search window, ordered by departure. A
// non-zero `until` past the window extends it with whatever departs up to
// `until`.
//
// With dropOff false (a via stop, ridden through or changed at), a ride ends
// at the train's first call at `to`, even one where nobody may get off; if
// it is one, the same train also gives a ride to its first later call at
// `to` that does let you off, to change there.
func (t *Timetable) candidates(from, to string, since, until time.Time, pickup, dropOff bool) []Connection {
	first, window := t.window(since)
	days := window
	// A service day's trains leave from its midnight on.
	for !until.IsZero() && !first.AddDays(days).At(0, warsaw).After(until) {
		days++
	}

	var out []Connection
	for _, ref := range t.byStation[from] {
		origin := ref.trip.StopTimes[ref.idx]
		if pickup && origin.NoPickup || origin.Departure == gtfs.NoTime {
			continue
		}
		dest, ok := t.alightingAt(ref.trip, ref.idx, to, dropOff)
		if !ok {
			continue
		}
		dests := []gtfs.StopTime{dest}
		if dest.NoDropOff {
			if off, ok := t.alightingAt(ref.trip, ref.idx, to, true); ok {
				dests = append(dests, off)
			}
		}
		for i := range days {
			date := first.AddDays(i)
			if !t.feed.Runs(ref.trip.ServiceID, date) {
				continue
			}
			dep := date.At(origin.Departure, warsaw)
			if dep.Before(since) || i >= window && dep.After(until) {
				continue
			}
			for _, dest := range dests {
				out = append(out, t.connection(ref.trip, origin, dest, date))
			}
		}
	}
	slices.SortFunc(out, func(a, b Connection) int {
		return cmp.Or(a.Departure.Compare(b.Departure), cmp.Compare(a.TripID, b.TripID), cmp.Compare(a.dest.Sequence, b.dest.Sequence))
	})
	return out
}

// window is the service days candidates searches by default: `days` of
// them from `first`.
func (t *Timetable) window(since time.Time) (first gtfs.Date, days int) {
	return gtfs.DateOf(since.In(warsaw)).AddDays(-t.daysBack), t.daysBack + searchDays + 1
}

// windowEnd is when the first service day after the default window starts:
// no train it leaves out is scheduled to leave earlier.
func (t *Timetable) windowEnd(since time.Time) time.Time {
	first, days := t.window(since)
	return first.AddDays(days).At(0, warsaw)
}

// alightingAt finds the first stop after idx at station `to` where
// passengers may get off, or with dropOff false, where the train calls at all.
func (t *Timetable) alightingAt(trip *gtfs.Trip, idx int, to string, dropOff bool) (gtfs.StopTime, bool) {
	for _, st := range trip.StopTimes[idx+1:] {
		if t.stationOf[st.StopID] == to && (!dropOff || !st.NoDropOff) && st.Arrival != gtfs.NoTime {
			return st, true
		}
	}
	return gtfs.StopTime{}, false
}

func (t *Timetable) connection(trip *gtfs.Trip, origin, dest gtfs.StopTime, date gtfs.Date) Connection {
	route := t.feed.Routes[trip.RouteID]
	return Connection{
		Category:          cmp.Or(t.feed.Str(origin.Category), trip.Category),
		Number:            trip.Number,
		Name:              trip.Name,
		Carrier:           route.AgencyID,
		Bus:               route.Type == gtfs.RouteTypeBus,
		Departure:         date.At(origin.Departure, warsaw),
		Arrival:           date.At(dest.Arrival, warsaw),
		DeparturePlatform: t.feed.Str(origin.Platform),
		Destination:       trip.Headsign,
		From:              t.names[t.stationOf[origin.StopID]],
		To:                t.names[t.stationOf[dest.StopID]],
		TripID:            trip.ID,
		ServiceDate:       date,
		trip:              trip,
		origin:            origin,
		dest:              dest,
	}
}
