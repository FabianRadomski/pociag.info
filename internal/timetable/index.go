package timetable

import (
	"cmp"
	"math"
	"slices"
	"time"

	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// index is every ride between two consecutive stops of a train ("a
// connection") over a few days, at live times where known, sorted by
// departure, for the connection scan in journeys.go. It is built for one
// realtime snapshot and rebuilt for the next.
type index struct {
	snap  *realtime.Snapshot // the snapshot asked for, which may not have been used
	start time.Time          // first scheduled departure covered
	base  int64              // Unix time that conn times count from
	conns []conn
	runs  []run
}

// conn is a ride from one stop of a run to its next stop. 24 bytes: a
// weekday has about 120,000.
type conn struct {
	dep, arr int32  // seconds since index.base
	from, to uint32 // station numbers
	run      uint32 // into index.runs
	stop     uint16 // index in the trip's StopTimes of the stop it leaves
	flags    uint8
}

const (
	noPickup  uint8 = 1 << iota // can't board at from
	noDropOff                   // can't get off at to
	lastOfRun                   // the run's last connection
)

// run is a trip on one service day.
type run struct {
	trip *gtfs.Trip
	date gtfs.Date
	live *realtime.Trip // nil without usable updates
	next []uint32       // runs the same train goes on as
}

// stopLive is a stop of a run at live times.
type stopLive struct {
	arr, dep            time.Time
	liveArr, liveDep    realtime.Event // zero when the time is scheduled
	noPickup, noDropOff bool
	ok                  bool // the stop has a time
}

// extraDays is how far the index reaches past the default search window,
// for later legs of journeys leaving near its end.
const extraDays = 2

// index returns the index for snap at now, building it if needed. A
// snapshot older than the one indexed is served from the newer index rather
// than rebuilding for it.
func (t *Timetable) index(snap *realtime.Snapshot, now time.Time) *index {
	start := now.Add(-maxDelay).Truncate(time.Hour)
	t.mu.Lock()
	defer t.mu.Unlock()
	if ix := t.idx; ix != nil && ix.start.Equal(start) &&
		(ix.snap == snap || snap != nil && ix.snap != nil && snap.Timestamp.Before(ix.snap.Timestamp)) {
		return ix
	}
	t.idx = nil // let the old index go before building the new one
	t.idx = t.build(snap, start)
	return t.idx
}

// Prepare builds the index for snap at now ahead of the first request that
// needs it.
func (t *Timetable) Prepare(snap *realtime.Snapshot, now time.Time) {
	t.index(snap, now)
}

// build indexes the connections scheduled to leave from start until extraDays
// after the default search window.
func (t *Timetable) build(snap *realtime.Snapshot, start time.Time) *index {
	ix := &index{snap: snap, start: start, base: start.Unix(), conns: make([]conn, 0, t.lastConns)}
	end := t.windowEnd(start).AddDate(0, 0, extraDays)
	live := t.Match(snap)

	type key struct {
		trip *gtfs.Trip
		date gtfs.Date
	}
	runOf := map[key]uint32{}
	first := gtfs.DateOf(start.In(warsaw)).AddDays(-t.daysBack)
	for date := first; date.At(0, warsaw).Before(end); date = date.AddDays(1) {
		day := date.At(0, warsaw).Unix()
		for _, trip := range t.trips {
			if !t.feed.Runs(trip.ServiceID, date) || len(trip.StopTimes) > math.MaxUint16 {
				continue
			}
			u, _ := live.Trip(trip.ID, date)
			if u != nil && u.Cancelled {
				continue
			}
			if u != nil && !fits(trip, u) {
				u = nil
			}
			r := uint32(len(ix.runs))
			if t.addConns(ix, r, trip, date, day, u, start.Unix(), end.Unix()) == 0 {
				continue
			}
			ix.runs = append(ix.runs, run{trip: trip, date: date, live: u})
			runOf[key{trip, date}] = r
		}
	}
	for i := range ix.runs {
		r := &ix.runs[i]
		for _, next := range t.continues[r.trip] {
			if k, ok := runOf[key{next, r.date}]; ok {
				r.next = append(r.next, k)
			}
		}
	}
	slices.SortFunc(ix.conns, func(a, b conn) int {
		return cmp.Or(cmp.Compare(a.dep, b.dep), cmp.Compare(a.arr, b.arr), cmp.Compare(a.run, b.run), cmp.Compare(a.stop, b.stop))
	})
	t.lastConns = len(ix.conns) + len(ix.conns)/16
	return ix
}

// addConns adds run r's connections that are scheduled to leave in
// [start, end), in Unix seconds, and returns how many. day is the Unix time
// the service day's times count from. The times are those of runTimes,
// worked out without its allocations, since this runs for every stop of
// every train on each snapshot.
func (t *Timetable) addConns(ix *index, r uint32, trip *gtfs.Trip, date gtfs.Date, day int64, u *realtime.Trip, start, end int64) int {
	stations := t.stopStation[trip]
	n := 0
	prev := -1
	var prevDep, last int64
	var prevNoPickup bool
	for i, st := range trip.StopTimes {
		if st.Arrival == gtfs.NoTime && st.Departure == gtfs.NoTime {
			continue
		}
		arr, dep := day+int64(offset(st, false)), day+int64(offset(st, true))
		noPick, noDrop := st.NoPickup, st.NoDropOff
		if u != nil {
			if ev := estimate(trip, date, u, st, false); !ev.Time.IsZero() {
				arr = ev.Time.Unix()
			}
			if ev := estimate(trip, date, u, st, true); !ev.Time.IsZero() {
				dep = ev.Time.Unix()
			}
			if skipped(u, st.Sequence) {
				noPick, noDrop = true, true
			}
		}
		if prev >= 0 {
			arr = max(arr, last)
		}
		dep = max(dep, arr)
		last = dep
		if prev >= 0 {
			if sched := day + int64(offset(trip.StopTimes[prev], true)); sched >= start && sched < end {
				c := conn{
					dep: int32(prevDep - ix.base), arr: int32(arr - ix.base),
					from: stations[prev], to: stations[i],
					run: r, stop: uint16(prev),
				}
				if prevNoPickup {
					c.flags |= noPickup
				}
				if noDrop {
					c.flags |= noDropOff
				}
				ix.conns = append(ix.conns, c)
				n++
			}
		}
		prev, prevDep, prevNoPickup = i, dep, noPick
	}
	if n > 0 {
		ix.conns[len(ix.conns)-1].flags |= lastOfRun
	}
	return n
}

// runTimes is every stop of trip on date, at live times from u where it has
// them (u may be nil). A skipped stop can't be boarded or left. Times never
// go backwards along the trip: an arrival is no earlier than the departure
// before it, and a departure no earlier than the arrival.
func runTimes(trip *gtfs.Trip, date gtfs.Date, u *realtime.Trip) []stopLive {
	out := make([]stopLive, len(trip.StopTimes))
	var prev time.Time
	for i, st := range trip.StopTimes {
		if st.Arrival == gtfs.NoTime && st.Departure == gtfs.NoTime {
			continue
		}
		s := stopLive{
			arr: date.At(offset(st, false), warsaw), dep: date.At(offset(st, true), warsaw),
			noPickup: st.NoPickup, noDropOff: st.NoDropOff, ok: true,
		}
		if u != nil {
			s.liveArr, s.liveDep = estimate(trip, date, u, st, false), estimate(trip, date, u, st, true)
			if !s.liveArr.Time.IsZero() {
				s.arr = s.liveArr.Time
			}
			if !s.liveDep.Time.IsZero() {
				s.dep = s.liveDep.Time
			}
			if skipped(u, st.Sequence) {
				s.noPickup, s.noDropOff = true, true
			}
		}
		if s.arr.Before(prev) {
			s.arr, s.liveArr = prev, realtime.Event{Time: prev}
		}
		if s.dep.Before(s.arr) {
			s.dep, s.liveDep = s.arr, realtime.Event{Time: s.arr}
		}
		prev = s.dep
		out[i] = s
	}
	return out
}
