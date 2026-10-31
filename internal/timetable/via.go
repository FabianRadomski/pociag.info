package timetable

import (
	"slices"
	"time"

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
// through the stations in between, in order, ordered by departure. Each leg
// between consecutive stops is a direct train; at each stop the journey
// continues on the train that arrives earliest among those leaving at least
// `change` later. A train running through a stop stays one leg. Journeys
// beaten by one that leaves later and arrives no later are dropped. Times
// are live estimates where snap has them; snap may be nil.
func (t *Timetable) Via(stops []string, now time.Time, n int, change time.Duration, snap *realtime.Snapshot) []Journey {
	if n <= 0 || len(stops) < 2 {
		return nil
	}
	segs := make([][]Connection, len(stops)-1)
	for i := range segs {
		for _, c := range t.candidates(stops[i], stops[i+1], now.Add(-maxDelay)) {
			c.merge(snap)
			if c.Live == nil || !c.Live.Cancelled {
				segs[i] = append(segs[i], c)
			}
		}
		slices.SortStableFunc(segs[i], func(a, b Connection) int { return a.Leaves().Compare(b.Leaves()) })
	}

	var all []Journey
next:
	for _, first := range segs[0] {
		if first.Leaves().Before(now) {
			continue
		}
		legs := []Connection{first}
		for _, seg := range segs[1:] {
			leg, ok := nextLeg(seg, legs[len(legs)-1], change)
			if !ok {
				continue next
			}
			legs = append(legs, leg)
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
	return out[:min(n, len(out))]
}

// nextLeg picks the leg of seg, which is sorted by Leaves, that arrives
// earliest among those catchable after prev: the same train, or one leaving
// at least `change` after prev arrives. Ties go to the same train, then to
// the later departure.
func nextLeg(seg []Connection, prev Connection, change time.Duration) (Connection, bool) {
	var best Connection
	found, same := false, false
	if i := slices.IndexFunc(seg, func(c Connection) bool {
		return c.TripID == prev.TripID && c.ServiceDate == prev.ServiceDate
	}); i >= 0 {
		best, found, same = seg[i], true, true
	}
	start, _ := slices.BinarySearchFunc(seg, prev.Arrives().Add(change), func(c Connection, t time.Time) int {
		return c.Leaves().Compare(t)
	})
	for _, c := range seg[start:] {
		if found && c.Leaves().After(best.Arrives()) {
			break // can't arrive any earlier
		}
		switch {
		case !found || c.Arrives().Before(best.Arrives()):
			best, found, same = c, true, false
		case c.Arrives().Equal(best.Arrives()) && !same:
			best = c
		}
	}
	return best, found
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
