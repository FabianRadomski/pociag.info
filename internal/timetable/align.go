package timetable

import (
	"pociag.info/internal/gtfs"
	"pociag.info/internal/realtime"
)

// minFit is the share of a snapshot's trips that must fit the static feed
// for the snapshot to be used.
const minFit = 0.9

// alignment is the result of checking a snapshot against the timetable.
type alignment struct {
	snap       *realtime.Snapshot
	fit, known int
	usable     bool
}

// Match returns snap if it fits this timetable, or nil if it doesn't.
//
// GTFS-Realtime names stops only by stop_sequence, which is only stable
// within one version of the static feed. A snapshot made for another
// version puts live times at the wrong stops: on the board that shows wrong
// delays, and in a journey a change that can't be made. So a snapshot is
// used only if nearly all of its trips that the timetable knows, and that
// have stop updates, update only stops the trip has. (On 2026-10-31
// mkuran.pl's realtime feed counted stops from 1 and the static feed from
// 0; 98% of trips failed this.)
func (t *Timetable) Match(snap *realtime.Snapshot) *realtime.Snapshot {
	if usable, _, _ := t.Alignment(snap); usable {
		return snap
	}
	return nil
}

// Alignment reports whether snap fits this timetable (see Match), and how
// many of its trips fit out of those checked. A nil or empty snapshot fits.
func (t *Timetable) Alignment(snap *realtime.Snapshot) (usable bool, fit, known int) {
	if snap == nil {
		return true, 0, 0
	}
	if a := t.fit.Load(); a != nil && a.snap == snap {
		return a.usable, a.fit, a.known
	}
	a := &alignment{snap: snap}
	for id, u := range snap.All() {
		trip := t.feed.Trips[id]
		if trip == nil {
			continue
		}
		hasStops := false
		for range u.Sequences() {
			hasStops = true
			break
		}
		if !hasStops {
			continue
		}
		a.known++
		if fits(trip, u) {
			a.fit++
		}
	}
	a.usable = a.known == 0 || float64(a.fit) >= minFit*float64(a.known)
	t.fit.Store(a)
	return a.usable, a.fit, a.known
}

// fits reports whether every update of u is for a stop of trip.
func fits(trip *gtfs.Trip, u *realtime.Trip) bool {
	for seq := range u.Sequences() {
		if _, ok := stopTime(trip, seq); !ok {
			return false
		}
	}
	return true
}
