// Package gtfs loads a static GTFS feed (as published at mkuran.pl/gtfs)
// into memory.
//
// Only calendar_dates.txt is used for service days; the mkuran.pl feed has
// no calendar.txt.
package gtfs

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Time is a stop time in seconds since the service day start; it may pass
// 24h. Stop times use it rather than time.Duration to stay small.
type Time int32

// NoTime marks a missing arrival or departure time.
const NoTime Time = -1

func (t Time) Duration() time.Duration { return time.Duration(t) * time.Second }

// Str is a string interned in the feed; Feed.Str returns its text and the
// zero Str is "". Stop times hold Strs instead of strings: at ~460k of them,
// 16-byte string headers would be most of the feed's memory.
type Str uint32

// RouteType values from routes.txt.
type RouteType int

const (
	RouteTypeRail RouteType = 2
	RouteTypeBus  RouteType = 3
)

type Feed struct {
	Agencies map[string]Agency
	Routes   map[string]Route
	Stations map[string]Station // location_type=1, plus parentless stops
	Stops    map[string]Stop    // every stop, including stations
	Trips    map[string]*Trip

	services map[string]map[Date]struct{}
	strs     []string // indexed by Str
}

type Agency struct {
	ID, Name string
}

type Route struct {
	ID, AgencyID, ShortName, LongName string
	Type                              RouteType
}

type Station struct {
	ID, Name string
}

type Stop struct {
	ID, Name  string
	StationID string // parent station, or the stop's own ID if it has none
}

type Trip struct {
	ID, RouteID, ServiceID string
	Headsign               string
	Category               string // plk_category_code
	Number                 string // plk_train_number
	Name                   string // plk_train_name
	StopTimes              []StopTime
}

type StopTime struct {
	StopID             Str
	Sequence           uint32 // GTFS-Realtime stop_sequence is a uint32 too
	Arrival, Departure Time   // NoTime if absent
	NoPickup           bool   // pickup_type=1: can't board here
	NoDropOff          bool   // drop_off_type=1: can't alight here
	Platform, Track    Str
	Category           Str // plk_category_code at this stop
}

// Date is a calendar date (a GTFS service day).
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns t's date in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

func (d Date) AddDays(n int) Date {
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 12, 0, 0, 0, time.UTC))
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// At converts a GTFS stop time on this service day to an instant. Per the
// spec, times count from "noon minus 12h", which differs from midnight on
// DST switch days.
func (d Date) At(offset Time, loc *time.Location) time.Time {
	noon := time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, loc)
	return noon.Add(-12 * time.Hour).Add(offset.Duration())
}

// Str returns the text of an interned string.
func (f *Feed) Str(s Str) string { return f.strs[s] }

// Runs reports whether a service operates on the given date.
func (f *Feed) Runs(serviceID string, d Date) bool {
	_, ok := f.services[serviceID][d]
	return ok
}

// Load reads a feed from a directory or an opened zip (both are fs.FS).
func Load(fsys fs.FS) (*Feed, error) {
	f := &Feed{
		Agencies: map[string]Agency{},
		Routes:   map[string]Route{},
		Stations: map[string]Station{},
		Stops:    map[string]Stop{},
		Trips:    map[string]*Trip{},
		services: map[string]map[Date]struct{}{},
		strs:     []string{""},
	}
	steps := []func(fs.FS) error{f.loadAgencies, f.loadRoutes, f.loadStops, f.loadTrips, f.loadStopTimes, f.loadCalendarDates}
	for _, step := range steps {
		if err := step(fsys); err != nil {
			return nil, err
		}
	}
	n := 0
	for _, t := range f.Trips {
		slices.SortFunc(t.StopTimes, func(a, b StopTime) int { return cmp.Compare(a.Sequence, b.Sequence) })
		n += len(t.StopTimes)
	}
	// Move all stop times into one exactly sized array, dropping the spare
	// capacity append left in each trip's slice.
	all := make([]StopTime, 0, n)
	for _, t := range f.Trips {
		start := len(all)
		all = append(all, t.StopTimes...)
		t.StopTimes = all[start:len(all):len(all)]
	}
	return f, nil
}

func (f *Feed) loadAgencies(fsys fs.FS) error {
	return readTable(fsys, "agency.txt", []string{"agency_name"}, func(r row) error {
		f.Agencies[r.get("agency_id")] = Agency{ID: r.get("agency_id"), Name: r.get("agency_name")}
		return nil
	})
}

func (f *Feed) loadRoutes(fsys fs.FS) error {
	return readTable(fsys, "routes.txt", []string{"route_id", "route_type"}, func(r row) error {
		typ, err := strconv.Atoi(r.get("route_type"))
		if err != nil {
			return r.errorf("route_type %q: %v", r.get("route_type"), err)
		}
		f.Routes[r.get("route_id")] = Route{
			ID: r.get("route_id"), AgencyID: r.get("agency_id"),
			ShortName: r.get("route_short_name"), LongName: r.get("route_long_name"),
			Type: RouteType(typ),
		}
		return nil
	})
}

func (f *Feed) loadStops(fsys fs.FS) error {
	return readTable(fsys, "stops.txt", []string{"stop_id", "stop_name"}, func(r row) error {
		id, name, parent := r.get("stop_id"), r.get("stop_name"), r.get("parent_station")
		station := parent
		if station == "" {
			station = id
		}
		f.Stops[id] = Stop{ID: id, Name: name, StationID: station}

		switch r.get("location_type") {
		case "1":
			f.Stations[id] = Station{ID: id, Name: name}
		case "", "0":
			if parent == "" {
				f.Stations[id] = Station{ID: id, Name: name}
			}
		}
		return nil
	})
}

func (f *Feed) loadTrips(fsys fs.FS) error {
	return readTable(fsys, "trips.txt", []string{"trip_id", "route_id", "service_id"}, func(r row) error {
		f.Trips[r.get("trip_id")] = &Trip{
			ID: r.get("trip_id"), RouteID: r.get("route_id"), ServiceID: r.get("service_id"),
			Headsign: r.get("trip_headsign"),
			Category: r.get("plk_category_code"),
			Number:   r.get("plk_train_number"),
			Name:     r.get("plk_train_name"),
		}
		return nil
	})
}

func (f *Feed) loadStopTimes(fsys fs.FS) error {
	// ~460k rows share a few thousand distinct stop IDs, platforms and
	// categories; each is stored once.
	ids := map[string]Str{"": 0}
	intern := func(s string) Str {
		if id, ok := ids[s]; ok {
			return id
		}
		id := Str(len(f.strs))
		s = strings.Clone(s) // don't keep the whole CSV line alive
		f.strs = append(f.strs, s)
		ids[s] = id
		return id
	}
	required := []string{"trip_id", "stop_id", "stop_sequence", "arrival_time", "departure_time"}
	return readTable(fsys, "stop_times.txt", required, func(r row) error {
		trip, ok := f.Trips[r.get("trip_id")]
		if !ok {
			return r.errorf("unknown trip_id %q", r.get("trip_id"))
		}
		seq, err := strconv.ParseUint(r.get("stop_sequence"), 10, 32)
		if err != nil {
			return r.errorf("stop_sequence %q: %v", r.get("stop_sequence"), err)
		}
		arr, err := parseTime(r.get("arrival_time"))
		if err != nil {
			return r.errorf("%v", err)
		}
		dep, err := parseTime(r.get("departure_time"))
		if err != nil {
			return r.errorf("%v", err)
		}
		trip.StopTimes = append(trip.StopTimes, StopTime{
			StopID: intern(r.get("stop_id")), Sequence: uint32(seq),
			Arrival: arr, Departure: dep,
			NoPickup:  r.get("pickup_type") == "1",
			NoDropOff: r.get("drop_off_type") == "1",
			Platform:  intern(r.get("platform")), Track: intern(r.get("track")),
			Category: intern(r.get("plk_category_code")),
		})
		return nil
	})
}

func (f *Feed) loadCalendarDates(fsys fs.FS) error {
	return readTable(fsys, "calendar_dates.txt", []string{"service_id", "date", "exception_type"}, func(r row) error {
		d, err := parseDate(r.get("date"))
		if err != nil {
			return r.errorf("%v", err)
		}
		id := r.get("service_id")
		switch r.get("exception_type") {
		case "1":
			if f.services[id] == nil {
				f.services[id] = map[Date]struct{}{}
			}
			f.services[id][d] = struct{}{}
		case "2":
			delete(f.services[id], d)
		}
		return nil
	})
}

// parseTime parses "H:MM:SS"; hours may exceed 23. Empty means NoTime.
func parseTime(s string) (Time, error) {
	if s == "" {
		return NoTime, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad time %q", s)
		}
		n[i] = v
	}
	return Time(n[0]*3600 + n[1]*60 + n[2]), nil
}

func parseDate(s string) (Date, error) {
	t, err := time.Parse("20060102", s)
	if err != nil {
		return Date{}, fmt.Errorf("bad date %q", s)
	}
	return DateOf(t), nil
}

// row is one CSV record with header-based column lookup; GTFS columns may
// come in any order and optional ones may be missing.
type row struct {
	file string
	line int
	cols map[string]int
	rec  []string
}

func (r row) get(col string) string {
	i, ok := r.cols[col]
	if !ok || i >= len(r.rec) {
		return ""
	}
	return r.rec[i]
}

func (r row) errorf(format string, args ...any) error {
	return fmt.Errorf("%s line %d: %s", r.file, r.line, fmt.Sprintf(format, args...))
}

func readTable(fsys fs.FS, name string, required []string, fn func(row) error) error {
	file, err := fsys.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	cr := csv.NewReader(file)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		return fmt.Errorf("%s: read header: %w", name, err)
	}
	cols := make(map[string]int, len(header))
	for i, h := range header {
		cols[strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")] = i
	}
	for _, c := range required {
		if _, ok := cols[c]; !ok {
			return fmt.Errorf("%s: missing column %s", name, c)
		}
	}

	r := row{file: name, line: 1, cols: cols}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		r.line++
		if err != nil {
			return fmt.Errorf("%s line %d: %w", name, r.line, err)
		}
		r.rec = rec
		if err := fn(r); err != nil {
			return err
		}
	}
}
