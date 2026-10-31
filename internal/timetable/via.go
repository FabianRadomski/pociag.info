package timetable

import (
	"cmp"
	"slices"
	"time"

	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// Journey is a trip made of one or more legs, changing trains between them.
type Journey struct {
	Legs []Connection
}

// Leaves is the best estimate of when the journey starts.
func (j Journey) Leaves() time.Time { return j.Legs[0].Leaves() }

// Arrives is the best estimate of when the journey ends.
func (j Journey) Arrives() time.Time { return j.Legs[len(j.Legs)-1].Arrives() }

// Via returns up to n journeys from stops[0] to the last stop that pass
// through the stations in between, in order, ordered by scheduled
// departure. Each leg between consecutive stops is a direct train; at each
// stop the journey continues on a train leaving at least `change` later,
// picked so that the whole journey arrives earliest. A train running through
// a stop stays one leg. Journeys beaten by one that leaves later and arrives
// no later are dropped. Times are live estimates where snap has them; snap
// may be nil.
func (t *Timetable) Via(stops []string, now time.Time, n int, change time.Duration, snap *realtime.Snapshot) []Journey {
	if n <= 0 || len(stops) < 2 {
		return nil
	}
	// Via stops may forbid boarding or alighting: such legs still serve a
	// train ridden straight through, and plan keeps changes off them.
	//
	// Legs near the end of the window arrive after it, so each later
	// segment also looks past the window, for trains leaving up to the end
	// of the day after the previous segment's last arrival.
	segs := make([][]Connection, len(stops)-1)
	var until time.Time
	for i := range segs {
		if i > 0 {
			if len(segs[i-1]) == 0 {
				return nil
			}
			until = dayAfter(segs[i-1])
		}
		for _, c := range t.candidates(stops[i], stops[i+1], now.Add(-maxDelay), until, i == 0, i == len(segs)-1) {
			if mergeVia(&c, snap, i == 0, i == len(segs)-1) {
				segs[i] = append(segs[i], c)
			}
		}
		slices.SortStableFunc(segs[i], func(a, b Connection) int { return a.Leaves().Compare(b.Leaves()) })
	}

	var all []Journey
	ends := plan(segs, change)
	for j, first := range segs[0] {
		if first.Leaves().Before(now) || !ends[0][j].ok {
			continue
		}
		legs := []Connection{first}
		for i, k := 1, ends[0][j].next; k >= 0; i++ {
			legs = append(legs, segs[i][k])
			k = ends[i][k].next
		}
		all = append(all, Journey{t.joinThrough(legs, snap)})
	}

	// Among journeys leaving at the same time the earliest arrival comes
	// last, so walking backwards it is the one kept.
	slices.SortStableFunc(all, func(a, b Journey) int {
		if c := a.Leaves().Compare(b.Leaves()); c != 0 {
			return c
		}
		return b.Arrives().Compare(a.Arrives())
	})
	var out []Journey
	for i := len(all) - 1; i >= 0; i-- {
		if len(out) > 0 && !all[i].Arrives().Before(out[len(out)-1].Arrives()) {
			continue
		}
		out = append(out, all[i])
	}
	slices.Reverse(out)
	// List them like the direct board, by scheduled departure, so a delayed
	// train keeps its place under its own day's heading.
	slices.SortStableFunc(out, func(a, b Journey) int { return a.Legs[0].Departure.Compare(b.Legs[0].Departure) })
	return out[:min(n, len(out))]
}

// dayAfter is midnight at the end of the day after the last of legs
// arrives, scheduled or live.
func dayAfter(legs []Connection) time.Time {
	var last time.Time
	for _, c := range legs {
		for _, a := range []time.Time{c.Arrival, c.Arrives()} {
			if a.After(last) {
				last = a
			}
		}
	}
	y, m, d := last.In(warsaw).Date()
	return time.Date(y, m, d+2, 0, 0, 0, 0, warsaw)
}

// mergeVia attaches realtime data to a leg of a via journey and reports
// whether the leg can still be used. A skipped via stop doesn't cancel the
// ride, it only stops anyone getting off or on there, like a stop that only
// picks up or sets down; joinThrough later merges the whole ride again.
// Only a cancelled train or a skipped first or last stop of the journey
// drops the leg.
func mergeVia(c *Connection, snap *realtime.Snapshot, first, last bool) bool {
	u, ok := snap.Trip(c.TripID, c.ServiceDate)
	if !ok {
		return true
	}
	if u.Cancelled {
		return false
	}
	if skipped(u, c.origin.Sequence) {
		if first {
			return false
		}
		c.origin.NoPickup = true
	}
	if skipped(u, c.dest.Sequence) {
		if last {
			return false
		}
		c.dest.NoDropOff = true
	}
	c.Live = c.estimates(u)
	return true
}

// end is the best way to finish a journey from a leg: the index of the leg
// to take in the next segment (-1 after the last one) and when the last stop
// is reached.
type end struct {
	next    int
	arrives time.Time
	ok      bool
}

// plan finds, for every leg of every segment, the way on that reaches the
// last stop earliest, not the one reaching the next stop earliest: a later
// train may run on through it and save a change. Each segment must be
// sorted by Leaves. It works from the last segment back, so the best way on
// from each leg of the next segment is already known; a suffix minimum over
// that segment then gives the best train to change onto from any time, and
// the same train is looked up by trip. O(M log M) for M legs per segment.
func plan(segs [][]Connection, change time.Duration) [][]end {
	ends := make([][]end, len(segs))
	last := len(segs) - 1
	ends[last] = make([]end, len(segs[last]))
	for j, c := range segs[last] {
		ends[last][j] = end{next: -1, arrives: c.Arrives(), ok: true}
	}
	for i := last - 1; i >= 0; i-- {
		next, nextEnds := segs[i+1], ends[i+1]
		wayAt := func(k int) way {
			return way{idx: k, final: nextEnds[k].arrives, next: next[k].Arrives()}
		}
		// suf[k] is the best leg at index k or later to change onto, or -1.
		suf := make([]int, len(next)+1)
		suf[len(next)] = -1
		for k := len(next) - 1; k >= 0; k-- {
			suf[k] = suf[k+1]
			if !next[k].origin.NoPickup && nextEnds[k].ok && (suf[k] < 0 || wayAt(k).compare(wayAt(suf[k])) < 0) {
				suf[k] = k
			}
		}
		// A train calling at a station twice gives a leg from each call, so
		// a train may have more than one leg here.
		byTrain := map[trainKey][]int{}
		for k, c := range next {
			if nextEnds[k].ok {
				key := trainKey{c.TripID, c.ServiceDate}
				byTrain[key] = append(byTrain[key], k)
			}
		}

		ends[i] = make([]end, len(segs[i]))
		for j, prev := range segs[i] {
			// Staying on: the same train from where prev gets off or later,
			// not back in time. It ignores pickup and drop-off rules.
			same := func(c Connection) bool {
				return c.TripID == prev.TripID && c.ServiceDate == prev.ServiceDate && c.origin.Sequence >= prev.dest.Sequence
			}
			var best way
			found := false
			consider := func(w way) {
				if !found || w.compare(best) < 0 {
					best, found = w, true
				}
			}
			if !prev.dest.NoDropOff {
				start, _ := slices.BinarySearchFunc(next, prev.Arrives().Add(change), func(c Connection, t time.Time) int {
					return c.Leaves().Compare(t)
				})
				if k := suf[start]; k >= 0 {
					w := wayAt(k)
					w.same = same(next[k])
					consider(w)
				}
			}
			for _, k := range byTrain[trainKey{prev.TripID, prev.ServiceDate}] {
				if same(next[k]) {
					w := wayAt(k)
					w.same = true
					consider(w)
				}
			}
			if found {
				ends[i][j] = end{next: best.idx, arrives: best.final, ok: true}
			}
		}
	}
	return ends
}

type trainKey struct {
	trip string
	date gtfs.Date
}

// way is a leg of a segment considered as the next one to ride.
type way struct {
	idx   int       // into the segment, which is sorted by Leaves
	final time.Time // when the journey reaches the last stop taking it
	next  time.Time // when it reaches the next stop
	same  bool      // it is the train the traveller is already on
}

// compare orders ways on, best first: the earliest arrival at the last
// stop, then at the next stop, then staying on the same train, then the
// later departure.
func (a way) compare(b way) int {
	return cmp.Or(
		a.final.Compare(b.final),
		a.next.Compare(b.next),
		boolFirst(a.same, b.same),
		cmp.Compare(b.idx, a.idx),
	)
}

// boolFirst orders true before false.
func boolFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}

// joinThrough merges consecutive legs on the same train into one.
func (t *Timetable) joinThrough(legs []Connection, snap *realtime.Snapshot) []Connection {
	out := []Connection{legs[0]}
	for _, c := range legs[1:] {
		last := &out[len(out)-1]
		if c.TripID != last.TripID || c.ServiceDate != last.ServiceDate {
			out = append(out, c)
			continue
		}
		*last = t.connection(last.trip, last.origin, c.dest, last.ServiceDate)
		last.merge(snap)
	}
	return out
}
