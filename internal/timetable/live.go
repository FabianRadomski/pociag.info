package timetable

import (
	"cmp"
	"slices"
	"time"

	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// maxDelay is how late a train may run and still be found by Live.
const maxDelay = 6 * time.Hour

// Live is realtime information for a connection.
type Live struct {
	Departure, Arrival realtime.Event // zero Time when unknown
	Cancelled          bool
}

// Live is like Connections but merges realtime updates from snap and lists
// trains by when they will actually leave, so a late train scheduled before
// now still shows up. snap may be nil.
func (t *Timetable) Live(from, to string, now time.Time, n int, snap *realtime.Snapshot) []Connection {
	if n <= 0 {
		return nil
	}
	var out []Connection
	for _, c := range t.candidates(from, to, now.Add(-maxDelay)) {
		if u, ok := snap.Trip(c.TripID, c.ServiceDate); ok {
			c.Live = c.live(u)
		}
		if c.leaves().Before(now) {
			continue
		}
		out = append(out, c)
		if len(out) == n {
			break
		}
	}
	return out
}

// leaves is the best estimate of when the train leaves the origin.
func (c Connection) leaves() time.Time {
	if c.Live != nil && !c.Live.Cancelled && !c.Live.Departure.Time.IsZero() {
		return c.Live.Departure.Time
	}
	return c.Departure
}

func (c Connection) live(u *realtime.Trip) *Live {
	if u.Cancelled || skipped(u, c.origin.Sequence) || skipped(u, c.dest.Sequence) {
		return &Live{Cancelled: true}
	}
	return &Live{
		Departure: c.estimate(u, c.origin, true),
		Arrival:   c.estimate(u, c.dest, false),
	}
}

func skipped(u *realtime.Trip, seq uint32) bool {
	st, ok := u.Stop(seq)
	return ok && st.Skipped
}

// estimate returns the realtime departure or arrival at stop st: the update
// for that stop if there is one, otherwise the delay of the latest earlier
// update carried over (GTFS-Realtime propagation), marked as a prediction.
func (c Connection) estimate(u *realtime.Trip, st gtfs.StopTime, departure bool) realtime.Event {
	latest, ok := u.Latest(st.Sequence)
	if !ok {
		return realtime.Event{}
	}
	if latest.Sequence == st.Sequence {
		if ev := pick(latest, departure); !ev.Time.IsZero() {
			return ev
		}
	}

	// Delay of the latest update, preferring its departure.
	ref, refDeparture := latest.Departure, true
	if ref.Time.IsZero() {
		ref, refDeparture = latest.Arrival, false
	}
	refStop, ok := c.stopTime(latest.Sequence)
	if ref.Time.IsZero() || !ok {
		return realtime.Event{}
	}
	delay := ref.Time.Sub(c.ServiceDate.At(offset(refStop, refDeparture), warsaw))
	return realtime.Event{Time: c.ServiceDate.At(offset(st, departure), warsaw).Add(delay)}
}

func pick(st realtime.Stop, departure bool) realtime.Event {
	if departure {
		return st.Departure
	}
	return st.Arrival
}

// offset is the scheduled departure or arrival, falling back to the other
// when one is missing (first and last stops).
func offset(st gtfs.StopTime, departure bool) gtfs.Time {
	if departure && st.Departure != gtfs.NoTime || st.Arrival == gtfs.NoTime {
		return st.Departure
	}
	return st.Arrival
}

func (c Connection) stopTime(seq uint32) (gtfs.StopTime, bool) {
	sts := c.trip.StopTimes
	i, ok := slices.BinarySearchFunc(sts, seq, func(st gtfs.StopTime, seq uint32) int { return cmp.Compare(st.Sequence, seq) })
	if !ok {
		return gtfs.StopTime{}, false
	}
	return sts[i], true
}
