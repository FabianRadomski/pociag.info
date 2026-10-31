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
	dead := make([][]bool, len(segs))
	for i, seg := range segs {
		dead[i] = make([]bool, len(seg))
	}
	for _, first := range segs[0] {
		if first.Leaves().Before(now) {
			continue
		}
		if rest, ok := follow(segs[1:], dead[1:], first, change); ok {
			all = append(all, Journey{t.joinThrough(append([]Connection{first}, rest...), snap)})
		}
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

// follow continues a journey from prev through segs, taking at each segment
// the most preferred leg (see catchable) from which the rest can still be
// completed, so a dead end further on falls back to the next choice instead
// of losing the journey. dead[i][j] remembers that segs[i][j] leads nowhere.
func follow(segs [][]Connection, dead [][]bool, prev Connection, change time.Duration) ([]Connection, bool) {
	if len(segs) == 0 {
		return nil, true
	}
	for _, j := range catchable(segs[0], prev, change) {
		if dead[0][j] {
			continue
		}
		if rest, ok := follow(segs[1:], dead[1:], segs[0][j], change); ok {
			return append([]Connection{segs[0][j]}, rest...), true
		}
		dead[0][j] = true
	}
	return nil, false
}

// catchable returns the indices of the legs of seg, which is sorted by
// Leaves, catchable after prev: the same train further along its run, or one
// leaving at least `change` after prev arrives. They come earliest arrival
// first; ties go to the same train, then to the later departure.
func catchable(seg []Connection, prev Connection, change time.Duration) []int {
	// A train calling at a station twice gives a leg from each call; only
	// one from where prev gets off or later goes on, not back in time.
	same := func(c Connection) bool {
		return c.TripID == prev.TripID && c.ServiceDate == prev.ServiceDate && c.origin.Sequence >= prev.dest.Sequence
	}
	start, _ := slices.BinarySearchFunc(seg, prev.Arrives().Add(change), func(c Connection, t time.Time) int {
		return c.Leaves().Compare(t)
	})
	var out []int
	for i, c := range seg {
		if i >= start || same(c) {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b int) int {
		if c := seg[a].Arrives().Compare(seg[b].Arrives()); c != 0 {
			return c
		}
		if sa, sb := same(seg[a]), same(seg[b]); sa != sb {
			if sa {
				return -1
			}
			return 1
		}
		return b - a // later departure first
	})
	return out
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
