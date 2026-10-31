package timetable

import (
	"cmp"
	"math"
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

// MaxChanges is how many changes a journey may have, besides one more at
// each via station.
const MaxChanges = 3

// Query asks for journeys.
type Query struct {
	Stops  []string      // station IDs: from, the via stations in order, to
	Now    time.Time     // journeys leave at or after it
	N      int           // how many
	Change time.Duration // minimum time to change trains
	Direct bool          // only journeys without changes
}

// Journeys returns up to n journeys from the first stop to the last that
// pass through the stations in between, in order, ordered by scheduled
// departure. A journey may pass a via station on a train or change there.
//
// Journeys are found with the Connection Scan Algorithm over the index: the
// earliest arrival for each number of changes, then the latest departure
// that still makes it, repeated from just after that departure. A journey
// is kept if no journey with fewer changes arrives as early, so a slower
// direct train is still listed next to a faster one with a change; of
// journeys leaving at the same time only the one arriving first is kept.
//
// Changing trains takes at least q.Change at the same station; staying on
// the same train takes none, including where the feed splits a train into
// two trips. Times are live where snap has them, if it fits the timetable;
// snap may be nil.
func (t *Timetable) Journeys(q Query, snap *realtime.Snapshot) []Journey {
	if q.N <= 0 || len(q.Stops) < 2 {
		return nil
	}
	nums := make([]uint32, len(q.Stops))
	for i, id := range q.Stops {
		n, ok := t.stationNum[id]
		if !ok {
			return nil
		}
		nums[i] = n
	}
	ix := t.index(snap, q.Now)
	maxLegs := 1
	if !q.Direct {
		maxLegs += MaxChanges + len(q.Stops) - 2
	}
	s := newSearch(ix, len(t.stationIDs), nums, maxLegs, q.Change)

	// best[l] is the journey with at most l legs that arrives first,
	// leaving as late as possible; it stays the best until its departure.
	type result struct {
		known, found bool
		arr, dep     int32
		legs         []leg
	}
	best := make([]result, maxLegs+1)
	need := make([]bool, maxLegs+1)
	var out []Journey
	tau := clampTime(q.Now.Unix() - ix.base)
	for len(out) < q.N {
		anyNeed := false
		for l := 1; l <= maxLegs; l++ {
			need[l] = !best[l].known || best[l].found && best[l].dep < tau
			anyNeed = anyNeed || need[l]
		}
		if anyNeed {
			s.forward(tau, need)
			arrs := make([]int32, maxLegs+1)
			for l := 1; l <= maxLegs; l++ {
				arrs[l] = s.arrival(l)
			}
			for l := 1; l <= maxLegs; l++ {
				if !need[l] {
					continue
				}
				best[l] = result{known: true}
				if arrs[l] == never {
					continue
				}
				// Not faster than with a leg less: never listed, so its
				// journey isn't needed; it is looked at again next time.
				if prev := best[l-1]; l > 1 && prev.found && prev.arr <= arrs[l] {
					best[l] = result{known: true, found: true, arr: arrs[l], dep: tau - 1}
					continue
				}
				dep, ok := s.latest(tau, arrs[l], l)
				if !ok {
					continue // can't happen: the forward scan found a way, as below
				}
				only := make([]bool, maxLegs+1)
				only[l] = true
				s.forward(dep, only)
				if legs := s.path(l); len(legs) > 0 {
					best[l] = result{known: true, found: true, arr: arrs[l], dep: dep, legs: legs}
				}
			}
		}

		pick := -1
		arr := never
		for l := 1; l <= maxLegs; l++ {
			r := best[l]
			if !r.found || r.arr >= arr {
				continue
			}
			arr = r.arr
			if pick < 0 || r.dep <= best[pick].dep {
				pick = l
			}
		}
		if pick < 0 {
			break
		}
		out = append(out, Journey{t.legs(ix, best[pick].legs)})
		tau = best[pick].dep + 1
	}
	// List them like the direct board, by scheduled departure, so a delayed
	// train keeps its place under its own day's heading.
	slices.SortStableFunc(out, func(a, b Journey) int { return a.Legs[0].Departure.Compare(b.Legs[0].Departure) })
	return out
}

// clampTime converts seconds since the index base to a scan time.
func clampTime(s int64) int32 {
	return int32(min(max(s, math.MinInt32+1), math.MaxInt32-1))
}

// never is an arrival that doesn't happen.
const never = int32(math.MaxInt32)

// noDeparture is a departure that doesn't happen.
const noDeparture = int32(math.MinInt32)

// label is what a search knows about a station in one via state with at
// most some number of legs. Forward: the earliest arrival, and how it was
// reached. Backward: the latest departure that still reaches the
// destination in time.
type label struct {
	time   int32
	exit   int32 // forward: connection got off from; -1 at the origin
	board  int32 // forward: connection got on at
	legs   int8  // forward: legs used to get here
	boardV int8  // forward: via state where boarded
}

// ride is what a search knows about being on a run in one via state: the
// fewest legs (forward: used so far; backward: still needed), and where
// that ride was boarded.
type ride struct {
	legs   int8
	boardV int8
	board  int32
}

const noLegs = int8(math.MaxInt8)

// leg is a ride found by a search: boarded at one connection, left after
// another, possibly of a later run of the same train.
type leg struct{ board, exit int32 }

// search holds the labels of one query. Labels are reset by bumping gen,
// so repeated scans don't clear whole arrays.
type search struct {
	ix                 *index
	origin, dest       uint32
	via                []uint32
	states             int // via states: 0..len(via)
	maxLegs            int
	change             int32
	gen                uint32
	stGen, rideGen     []uint32
	labels             []label // station × state × legs (0..maxLegs)
	rides              []ride  // run × state
	empty              label
	cur, after, nxRide []ride
}

func newSearch(ix *index, stations int, stops []uint32, maxLegs int, change time.Duration) *search {
	states := len(stops) - 1
	return &search{
		ix: ix, origin: stops[0], dest: stops[len(stops)-1], via: stops[1 : len(stops)-1],
		states: states, maxLegs: maxLegs, change: int32(change / time.Second),
		stGen: make([]uint32, stations), rideGen: make([]uint32, len(ix.runs)),
		labels: make([]label, stations*states*(maxLegs+1)),
		rides:  make([]ride, len(ix.runs)*states),
		cur:    make([]ride, states), after: make([]ride, states), nxRide: make([]ride, states),
	}
}

// reset forgets all labels; unset ones read as empty.
func (s *search) reset(empty label) {
	s.gen++
	s.empty = empty
}

// lab returns the label of station st in via state v with at most l legs.
func (s *search) lab(st uint32, v, l int) *label {
	block := s.labels[int(st)*s.states*(s.maxLegs+1) : int(st+1)*s.states*(s.maxLegs+1)]
	if s.stGen[st] != s.gen {
		s.stGen[st] = s.gen
		for i := range block {
			block[i] = s.empty
		}
	}
	return &block[v*(s.maxLegs+1)+l]
}

// ridesOf returns run r's rides by via state.
func (s *search) ridesOf(r uint32) []ride {
	block := s.rides[int(r)*s.states : int(r+1)*s.states]
	if s.rideGen[r] != s.gen {
		s.rideGen[r] = s.gen
		for i := range block {
			block[i] = ride{legs: noLegs}
		}
	}
	return block
}

// next is the via state after reaching station st in state v.
func (s *search) next(v int, st uint32) int {
	if v < len(s.via) && s.via[v] == st {
		return v + 1
	}
	return v
}

// forward scans from time tau for the earliest arrival at the destination
// with each number of legs, until those in need are known. It records how
// each station was reached, for path. Once the arrivals with the most legs
// are known, rides with that many legs are no longer followed, so looking
// on for a direct train, say, only follows trains from the origin.
func (s *search) forward(tau int32, need []bool) {
	s.reset(label{time: never, exit: -1, board: -1, legs: noLegs})
	*s.lab(s.origin, 0, 0) = label{time: tau, exit: -1, board: -1, legs: 0}
	conns := s.ix.conns
	i, _ := slices.BinarySearchFunc(conns, tau, func(c conn, t int32) int { return cmp.Compare(c.dep, t) })
	limit, settle := s.scope(need, tau)
	last := s.states - 1
	for ; i < len(conns); i++ {
		c := &conns[i]
		if c.dep >= settle {
			if limit, settle = s.scope(need, c.dep); limit == 0 {
				break
			}
		}
		onRun := s.rideGen[c.run] == s.gen
		atFrom := s.stGen[c.from] == s.gen && c.flags&noPickup == 0
		if !onRun && !atFrom {
			continue
		}
		cur := s.cur
		if onRun {
			copy(cur, s.ridesOf(c.run))
		} else {
			for v := range cur {
				cur[v] = ride{legs: noLegs}
			}
		}
		if atFrom {
			for v := range s.states {
				if l := s.boardLegs(c.from, v, c.dep, limit); l < cur[v].legs {
					cur[v] = ride{legs: l, boardV: int8(v), board: int32(i)}
				}
			}
		}
		after := s.after
		for v := range after {
			after[v] = ride{legs: noLegs}
		}
		anyRide := false
		for v, r := range cur {
			if r.legs > limit {
				continue
			}
			anyRide = true
			if w := s.next(v, c.to); r.legs < after[w].legs {
				after[w] = r
			}
		}
		if !anyRide {
			if onRun {
				copy(s.rides[int(c.run)*s.states:], after)
			}
			continue
		}
		copy(s.ridesOf(c.run), after)
		if c.flags&noDropOff == 0 {
			for v, r := range after {
				if r.legs == noLegs {
					continue
				}
				if s.improve(c.to, v, r, c.arr, int32(i)) && c.to == s.dest && v == last {
					limit, settle = s.scope(need, c.dep)
				}
			}
		}
		if c.flags&lastOfRun != 0 {
			for _, nx := range s.ix.runs[c.run].next {
				rs := s.ridesOf(nx)
				for v, r := range after {
					if r.legs < rs[v].legs {
						rs[v] = r
					}
				}
			}
		}
	}
}

// scope is what a forward scan at time dep still has to find: the most
// legs of a needed arrival that a later connection could still improve
// (0 if none), and the time that changes next, when an arrival found
// becomes final.
func (s *search) scope(need []bool, dep int32) (limit int8, settle int32) {
	settle = never
	for l, n := range need {
		if !n {
			continue
		}
		a := s.arrival(l)
		if a != never && dep >= a {
			continue
		}
		limit = int8(l)
		settle = min(settle, a)
	}
	return limit, settle
}

// arrival is the earliest arrival at the destination with at most l legs,
// or never.
func (s *search) arrival(l int) int32 {
	if s.stGen[s.dest] != s.gen {
		return never
	}
	return s.lab(s.dest, s.states-1, l).time
}

// boardLegs is how many legs a ride boarded at station st in via state v
// at time dep makes, at fewest and at most limit: one from the origin, or
// one more than a leg arriving at least the change time before.
func (s *search) boardLegs(st uint32, v int, dep int32, limit int8) int8 {
	if s.lab(st, v, 0).time <= dep {
		return 1
	}
	for l := 1; l < int(limit); l++ {
		if a := s.lab(st, v, l).time; a != never && int64(a)+int64(s.change) <= int64(dep) {
			return int8(l + 1)
		}
	}
	return noLegs
}

// improve records an arrival at station st in via state v by ride r,
// getting off after connection exit, for r's number of legs and more.
func (s *search) improve(st uint32, v int, r ride, arr, exit int32) bool {
	improved := false
	for l := int(r.legs); l <= s.maxLegs; l++ {
		lab := s.lab(st, v, l)
		if arr >= lab.time {
			break
		}
		*lab = label{time: arr, exit: exit, board: r.board, legs: r.legs, boardV: r.boardV}
		improved = true
	}
	return improved
}

// path is the legs of the journey the last forward scan found to the
// destination with at most maxLegs legs, in order.
func (s *search) path(maxLegs int) []leg {
	var out []leg
	lab := *s.lab(s.dest, s.states-1, maxLegs)
	for lab.exit >= 0 && lab.legs > 0 {
		out = append(out, leg{board: lab.board, exit: lab.exit})
		lab = *s.lab(s.ix.conns[lab.board].from, int(lab.boardV), int(lab.legs)-1)
	}
	slices.Reverse(out)
	return out
}

// latest scans back from time by for the latest departure from the origin,
// at or after tau, that reaches the destination by then with at most
// maxLegs legs.
func (s *search) latest(tau, by int32, maxLegs int) (int32, bool) {
	s.reset(label{time: noDeparture})
	conns := s.ix.conns
	i, _ := slices.BinarySearchFunc(conns, by+1, func(c conn, t int32) int { return cmp.Compare(c.dep, t) })
	last := s.states - 1
	for i--; i >= 0 && conns[i].dep >= tau; i-- {
		c := &conns[i]
		if c.arr > by {
			continue
		}
		// Staying on: the run's next connection, or after its last one, the
		// runs the train goes on as.
		stay := s.nxRide
		copy(stay, s.ridesOf(c.run))
		if c.flags&lastOfRun != 0 {
			for _, nx := range s.ix.runs[c.run].next {
				for v, r := range s.ridesOf(nx) {
					stay[v].legs = min(stay[v].legs, r.legs)
				}
			}
		}
		cur := s.cur
		anyRide := false
		for v := range s.states {
			w := s.next(v, c.to)
			legs := stay[w].legs
			if c.flags&noDropOff == 0 {
				if c.to == s.dest && w == last {
					legs = 1
				} else {
					for l := 1; l < min(int(legs), maxLegs); l++ {
						if d := s.lab(c.to, w, l).time; d != noDeparture && int64(d) >= int64(c.arr)+int64(s.change) {
							legs = int8(l + 1)
							break
						}
					}
				}
			}
			if int(legs) > maxLegs {
				legs = noLegs
			}
			cur[v] = ride{legs: legs}
			anyRide = anyRide || legs != noLegs
		}
		if !anyRide && s.rideGen[c.run] != s.gen {
			continue
		}
		copy(s.ridesOf(c.run), cur)
		if c.flags&noPickup != 0 {
			continue
		}
		if c.from == s.origin && cur[0].legs != noLegs {
			return c.dep, true
		}
		for v, r := range cur {
			if r.legs == noLegs {
				continue
			}
			for l := int(r.legs); l <= maxLegs; l++ {
				lab := s.lab(c.from, v, l)
				if c.dep <= lab.time {
					break
				}
				lab.time = c.dep
			}
		}
	}
	return 0, false
}

// legs turns the legs of a search into connections, one per trip: a train
// split into trips in the feed shows each part.
func (t *Timetable) legs(ix *index, legs []leg) []Connection {
	var out []Connection
	for _, l := range legs {
		b, e := ix.conns[l.board], ix.conns[l.exit]
		chain := runChain(ix, b.run, e.run)
		for k, r := range chain {
			run := ix.runs[r]
			stops := runTimes(run.trip, run.date, run.live)
			from, to := -1, -1
			if k == 0 {
				from = int(b.stop)
			}
			for i, st := range stops {
				if !st.ok {
					continue
				}
				if from < 0 {
					from = i
				}
				if k == len(chain)-1 && i > int(e.stop) {
					to = i
					break
				}
				if k < len(chain)-1 {
					to = i
				}
			}
			c := t.connection(run.trip, run.trip.StopTimes[from], run.trip.StopTimes[to], run.date)
			if run.live != nil {
				c.Live = &Live{Departure: stops[from].liveDep, Arrival: stops[to].liveArr}
			}
			out = append(out, c)
		}
	}
	return out
}

// runChain is the runs from run a to run b along the runs a train goes on
// as.
func runChain(ix *index, a, b uint32) []uint32 {
	if a == b {
		return []uint32{a}
	}
	for _, nx := range ix.runs[a].next {
		if rest := runChain(ix, nx, b); rest != nil {
			return append([]uint32{a}, rest...)
		}
	}
	return nil
}
