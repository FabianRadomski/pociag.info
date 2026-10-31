package timetable

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	rt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"

	"pociag.info/internal/realtime"
)

// Synthetic feed for via journeys: stations Alfa, Beta, Gamma, Delta, all on
// 2026-10-22.
var viaFeed = fstest.MapFS{
	"agency.txt": {Data: []byte("agency_id,agency_name,agency_url,agency_timezone\n" +
		"KD,Koleje Dolnośląskie,https://x,Europe/Warsaw\n")},
	"routes.txt": {Data: []byte("route_id,agency_id,route_short_name,route_long_name,route_type\n" +
		"KD,KD,KD,KD,2\n")},
	"stops.txt": {Data: []byte("stop_id,stop_name,location_type,parent_station,platform_code\n" +
		"A,Alfa,1,,\nB,Beta,1,,\nC,Gamma,1,,\nD,Delta,1,,\n")},
	"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
		"AB0730,KD,S,Beta,KD,AB0730,\n" +
		"AB0800,KD,S,Beta,KD,AB0800,\n" +
		"AB0830,KD,S,Beta,KD,AB0830,\n" +
		"AB0900,KD,S,Beta,KD,AB0900,\n" +
		"BC0910,KD,S,Gamma,KD,BC0910,\n" +
		"BC0920,KD,S,Gamma,KD,BC0920,\n" +
		"BC1004,KD,S,Gamma,KD,BC1004,\n" +
		"BC1005,KD,S,Gamma,KD,BC1005,\n" +
		"THRU,KD,S,Gamma,KD,THRU,\n" +
		"CD1010,KD,S,Delta,KD,CD1010,\n" +
		"CD1140,KD,S,Delta,KD,CD1140,\n")},
	"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time\n" +
		"AB0730,0,A,07:30:00,07:30:00\nAB0730,1,B,08:50:00,08:50:00\n" +
		"AB0800,0,A,08:00:00,08:00:00\nAB0800,1,B,09:00:00,09:00:00\n" +
		"AB0830,0,A,08:30:00,08:30:00\nAB0830,1,B,09:40:00,09:40:00\n" +
		"AB0900,0,A,09:00:00,09:00:00\nAB0900,1,B,10:00:00,10:00:00\n" +
		// BC0920 leaves after BC0910 but overtakes it.
		"BC0910,0,B,09:10:00,09:10:00\nBC0910,1,C,11:00:00,11:00:00\n" +
		"BC0920,0,B,09:20:00,09:20:00\nBC0920,1,C,10:00:00,10:00:00\n" +
		"BC1004,0,B,10:04:00,10:04:00\nBC1004,1,C,10:30:00,10:30:00\n" +
		"BC1005,0,B,10:05:00,10:05:00\nBC1005,1,C,11:30:00,11:30:00\n" +
		"THRU,0,A,11:00:00,11:00:00\nTHRU,1,B,12:00:00,12:02:00\nTHRU,2,C,13:00:00,13:00:00\n" +
		"CD1010,0,C,10:10:00,10:10:00\nCD1010,1,D,10:40:00,10:40:00\n" +
		"CD1140,0,C,11:40:00,11:40:00\nCD1140,1,D,12:00:00,12:00:00\n")},
	"calendar_dates.txt": {Data: []byte("service_id,date,exception_type\nS,20261022,1\n")},
}

// journeySummary renders a journey as "dep train from-to arr" legs joined
// by " | ", with live estimates where known.
func journeySummary(j Journey) string {
	legs := make([]string, len(j.Legs))
	for i, c := range j.Legs {
		legs[i] = fmt.Sprintf("%s %s %s-%s %s",
			c.Leaves().In(warsaw).Format("15:04"), c.Number, c.From, c.To, c.Arrives().In(warsaw).Format("15:04"))
	}
	return strings.Join(legs, " | ")
}

func TestVia(t *testing.T) {
	tt := newTimetable(t, viaFeed)
	tests := []struct {
		name   string
		stops  []string
		now    string
		change time.Duration
		snap   *realtime.Snapshot
		want   []string
	}{
		{
			name:  "overtaking train preferred, exact change boundary, through train is one leg, beaten journey dropped",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			want: []string{
				"08:00 AB0800 Alfa-Beta 09:00 | 09:20 BC0920 Beta-Gamma 10:00",
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "shorter change catches an earlier train and beats a journey leaving earlier",
			stops: []string{"A", "B", "C"}, now: "08:15", change: 0,
			want: []string{
				"09:00 AB0900 Alfa-Beta 10:00 | 10:04 BC1004 Beta-Gamma 10:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "late first leg moves to a later connection",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			snap: snapshot(t, "AB0800", rt.TripDescriptor_SCHEDULED, upd(1, ev("09:20", false), nil)),
			want: []string{
				"07:30 AB0730 Alfa-Beta 08:50 | 09:20 BC0920 Beta-Gamma 10:00",
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "cancelled leg is skipped",
			stops: []string{"A", "B", "C"}, now: "00:00", change: 5 * time.Minute,
			snap: snapshot(t, "BC0920", rt.TripDescriptor_CANCELED),
			want: []string{
				"08:30 AB0830 Alfa-Beta 09:40 | 10:04 BC1004 Beta-Gamma 10:30",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30",
				"11:00 THRU Alfa-Gamma 13:00",
			},
		},
		{
			name:  "two via stations",
			stops: []string{"A", "B", "C", "D"}, now: "00:00", change: 5 * time.Minute,
			want: []string{
				"08:00 AB0800 Alfa-Beta 09:00 | 09:20 BC0920 Beta-Gamma 10:00 | 10:10 CD1010 Gamma-Delta 10:40",
				"09:00 AB0900 Alfa-Beta 10:00 | 10:05 BC1005 Beta-Gamma 11:30 | 11:40 CD1140 Gamma-Delta 12:00",
			},
		},
		{
			name:  "no onward train",
			stops: []string{"A", "C", "B"}, now: "00:00", change: 5 * time.Minute,
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tt.Via(tc.stops, at("2026-10-22", tc.now), 10, tc.change, tc.snap)
			g := make([]string, len(got))
			for i, j := range got {
				g[i] = journeySummary(j)
			}
			if strings.Join(g, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("journeys mismatch\n got:\n  %s\nwant:\n  %s",
					strings.Join(g, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestVia_LimitsCount(t *testing.T) {
	tt := newTimetable(t, viaFeed)
	got := tt.Via([]string{"A", "B", "C"}, at("2026-10-22", "00:00"), 2, 5*time.Minute, nil)
	if len(got) != 2 || got[0].Legs[0].Number != "AB0800" {
		t.Errorf("got %d journeys starting %v, want 2 starting AB0800", len(got), got)
	}
}
