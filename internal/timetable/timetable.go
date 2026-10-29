// Package timetable answers "which direct trains go from A to B next?" from a
// static GTFS feed.
package timetable

import (
	"cmp"
	"slices"
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
	names     map[string]string
	// daysBack: trips with times past 24:00 belong to earlier service days.
	daysBack int
}

type stopRef struct {
	trip *gtfs.Trip
	idx  int // into trip.StopTimes
}

func New(feed *gtfs.Feed) *Timetable {
	t := &Timetable{
		feed:      feed,
		byStation: map[string][]stopRef{},
		names:     make(map[string]string, len(feed.Stations)),
	}
	var maxOffset time.Duration
	for _, trip := range feed.Trips {
		for i, st := range trip.StopTimes {
			s := t.stationOf(st.StopID)
			t.byStation[s] = append(t.byStation[s], stopRef{trip, i})
			maxOffset = max(maxOffset, st.Arrival, st.Departure)
		}
	}
	t.daysBack = int(maxOffset / (24 * time.Hour))
	for id, s := range feed.Stations {
		t.names[id] = s.Name
	}
	return t
}

func (t *Timetable) stationOf(stopID string) string {
	if s, ok := t.feed.Stops[stopID]; ok {
		return s.StationID
	}
	return stopID
}

// StationName returns the station's name, or "" if unknown.
func (t *Timetable) StationName(id string) string { return t.names[id] }

// StationNames returns station ID → name. The map must not be modified.
func (t *Timetable) StationNames() map[string]string { return t.names }

// Connections returns up to n direct rides from station `from` to station
// `to` departing at or after `after`, ordered by departure.
func (t *Timetable) Connections(from, to string, after time.Time, n int) []Connection {
	out := t.candidates(from, to, after)
	return out[:min(max(n, 0), len(out))]
}

// candidates returns every ride from `from` to `to` scheduled to depart at
// or after `since` within the search window, ordered by departure.
func (t *Timetable) candidates(from, to string, since time.Time) []Connection {
	first := gtfs.DateOf(since.In(warsaw)).AddDays(-t.daysBack)
	days := t.daysBack + searchDays + 1

	var out []Connection
	for _, ref := range t.byStation[from] {
		origin := ref.trip.StopTimes[ref.idx]
		if origin.NoPickup || origin.Departure == gtfs.NoTime {
			continue
		}
		dest, ok := t.alightingAt(ref.trip, ref.idx, to)
		if !ok {
			continue
		}
		for i := range days {
			date := first.AddDays(i)
			if !t.feed.Runs(ref.trip.ServiceID, date) {
				continue
			}
			if date.At(origin.Departure, warsaw).Before(since) {
				continue
			}
			out = append(out, t.connection(ref.trip, origin, dest, date))
		}
	}
	slices.SortFunc(out, func(a, b Connection) int {
		return cmp.Or(a.Departure.Compare(b.Departure), cmp.Compare(a.TripID, b.TripID))
	})
	return out
}

// alightingAt finds the first stop after idx at station `to` where
// passengers may get off.
func (t *Timetable) alightingAt(trip *gtfs.Trip, idx int, to string) (gtfs.StopTime, bool) {
	for _, st := range trip.StopTimes[idx+1:] {
		if t.stationOf(st.StopID) == to && !st.NoDropOff && st.Arrival != gtfs.NoTime {
			return st, true
		}
	}
	return gtfs.StopTime{}, false
}

func (t *Timetable) connection(trip *gtfs.Trip, origin, dest gtfs.StopTime, date gtfs.Date) Connection {
	route := t.feed.Routes[trip.RouteID]
	return Connection{
		Category:          cmp.Or(origin.Category, trip.Category),
		Number:            trip.Number,
		Name:              trip.Name,
		Carrier:           route.AgencyID,
		Bus:               route.Type == gtfs.RouteTypeBus,
		Departure:         date.At(origin.Departure, warsaw),
		Arrival:           date.At(dest.Arrival, warsaw),
		DeparturePlatform: origin.Platform,
		Destination:       trip.Headsign,
		TripID:            trip.ID,
		ServiceDate:       date,
		trip:              trip,
		origin:            origin,
		dest:              dest,
	}
}
