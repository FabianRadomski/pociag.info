package timetable

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"pociag.info/internal/gtfs"
)

const (
	wroclawGlowny = "60103"
	krakowGlowny  = "80416"
)

func at(date, clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, warsaw)
	if err != nil {
		panic(err)
	}
	return t
}

// summary renders a connection as one comparable line, so failures read like
// a departure board instead of a struct dump.
func summary(c Connection) string {
	return fmt.Sprintf("%s %s %s %s -> %s | to %s | pl %s",
		c.Departure.In(warsaw).Format("01-02 15:04"),
		c.Category, c.Number, c.Name,
		c.Arrival.In(warsaw).Format("01-02 15:04"),
		c.Destination, c.DeparturePlatform)
}

func assertConnections(t *testing.T, got []Connection, want []string) {
	t.Helper()
	g := make([]string, len(got))
	for i, c := range got {
		g[i] = summary(c)
	}
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("connections mismatch\n got:\n  %s\nwant:\n  %s",
			strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

func newTimetable(t *testing.T, fsys fstest.MapFS) *Timetable {
	t.Helper()
	var feed *gtfs.Feed
	var err error
	if fsys == nil {
		// Real mkuran.pl GTFS (published 2026-10-22) trimmed to trains serving both
		// Wrocław Gł. and Kraków Gł.; expectations computed independently.
		feed, err = gtfs.Load(os.DirFS("../../testdata/gtfs/wro-krk"))
	} else {
		feed, err = gtfs.Load(fsys)
	}
	if err != nil {
		t.Fatalf("gtfs.Load: %v", err)
	}
	return New(feed)
}

func TestConnections_RealData_WroclawToKrakow(t *testing.T) {
	tt := newTimetable(t, nil)

	got := tt.Connections(wroclawGlowny, krakowGlowny, at("2026-10-22", "12:00"), 3)

	assertConnections(t, got, []string{
		"10-22 12:10 IC 8312/3 SZKUNER -> 10-22 15:19 | to Przemyśl Główny | pl 3",
		"10-22 13:10 IC 73000/1 GALICJA -> 10-22 16:07 | to Przemyśl Główny | pl 3",
		"10-22 14:10 IC 8310/1 KOSSAK -> 10-22 17:28 | to Przemyśl Główny | pl 3",
	})
}

func TestConnections_RealData_KrakowToWroclawOvernightArrival(t *testing.T) {
	tt := newTimetable(t, nil)

	got := tt.Connections(krakowGlowny, wroclawGlowny, at("2026-10-22", "21:00"), 1)

	assertConnections(t, got, []string{
		"10-22 21:35 IC 37010/1 URSA -> 10-23 00:48 | to Berlin Gesundbrunnen | pl 3",
	})
}

func TestConnections_RealData_SpansMidnightIntoNextServiceDay(t *testing.T) {
	tt := newTimetable(t, nil)

	// URSA leaves Wrocław at 03:10 as "27:10:00" of the previous service day.
	got := tt.Connections(wroclawGlowny, krakowGlowny, at("2026-10-22", "21:00"), 2)

	assertConnections(t, got, []string{
		"10-23 03:10 IC 73010/1 URSA -> 10-23 06:33 | to Przemyśl Główny | pl 3",
		"10-23 05:10 IC 6304/5 GROTTGER -> 10-23 08:15 | to Przemyśl Główny | pl 2",
	})
	if len(got) == 0 {
		return
	}
	if got[0].TripID != "PLK_IC_2026_322124420" || got[0].ServiceDate != (gtfs.Date{Year: 2026, Month: 10, Day: 22}) {
		t.Errorf("URSA keys = %s %v, want PLK_IC_2026_322124420 2026-10-22", got[0].TripID, got[0].ServiceDate)
	}
}

func TestStationName(t *testing.T) {
	tt := newTimetable(t, nil)

	if got := tt.StationName(krakowGlowny); got != "Kraków Główny" {
		t.Errorf("StationName(%s) = %q, want %q", krakowGlowny, got, "Kraków Główny")
	}
	if got := tt.StationName("nope"); got != "" {
		t.Errorf("StationName(nope) = %q, want empty", got)
	}
	if names := tt.StationNames(); names[wroclawGlowny] != "Wrocław Główny" {
		t.Errorf("StationNames()[%s] = %q", wroclawGlowny, names[wroclawGlowny])
	}
}

// Synthetic feed: stations A (with platform A_1), B, C.
var synthetic = fstest.MapFS{
	"agency.txt": {Data: []byte("agency_id,agency_name,agency_url,agency_timezone\n" +
		"IC,PKP Intercity,https://x,Europe/Warsaw\nKD,Koleje Dolnośląskie,https://x,Europe/Warsaw\n")},
	"routes.txt": {Data: []byte("route_id,agency_id,route_short_name,route_long_name,route_type\n" +
		"IC_IC,IC,IC,InterCity,2\nKD,KD,KD,KD,2\nKD_BUS,KD,ZKA KD,Bus,3\n")},
	"stops.txt": {Data: []byte("stop_id,stop_name,location_type,parent_station,platform_code\n" +
		"A,Alfa,1,,\nA_1,Alfa,0,A,1\nB,Beta,1,,\nC,Gamma,1,,\n")},
	"trips.txt": {Data: []byte("trip_id,route_id,service_id,trip_headsign,plk_category_code,plk_train_number,plk_train_name\n" +
		"MAIN,IC_IC,BOTH,Gamma,IC,1000,MAIN\n" +
		"KD2000,KD,ONLY22,Gamma,KD,2000,\n" +
		"NIGHT,IC_IC,ONLY22,Gamma,EN,3000,NIGHT\n" +
		"BUS,KD_BUS,ONLY22,Gamma,BUS,4000,\n")},
	"stop_times.txt": {Data: []byte("trip_id,stop_sequence,stop_id,arrival_time,departure_time,pickup_type,drop_off_type,platform,plk_category_code\n" +
		"MAIN,0,A_1,10:00:00,10:00:00,0,0,1,IC\n" +
		"MAIN,1,B,11:00:00,11:05:00,0,0,,IC\n" +
		"MAIN,2,C,12:00:00,12:00:00,0,0,,IC\n" +
		// Alighting only at A, boarding only at B.
		"KD2000,0,A,09:00:00,09:00:00,1,0,,KD\n" +
		"KD2000,1,B,09:30:00,09:31:00,0,1,,KD\n" +
		"KD2000,2,C,10:00:00,10:00:00,0,0,,KD\n" +
		"NIGHT,0,B,23:50:00,23:50:00,0,0,,EN\n" +
		"NIGHT,1,C,24:20:00,24:20:00,0,0,,EN\n" +
		"BUS,0,A,13:00:00,13:00:00,0,0,,\n" +
		"BUS,1,C,14:00:00,14:00:00,0,0,,\n")},
	"calendar_dates.txt": {Data: []byte("service_id,date,exception_type\n" +
		"BOTH,20261022,1\nBOTH,20261023,1\nONLY22,20261022,1\n")},
}

func TestConnections_Synthetic(t *testing.T) {
	tt := newTimetable(t, synthetic)

	tests := []struct {
		name     string
		from, to string
		after    time.Time
		n        int
		want     []string
	}{
		{
			name: "every service day yields a connection, sorted by departure",
			from: "A", to: "C", after: at("2026-10-22", "00:00"), n: 10,
			want: []string{
				"10-22 10:00 IC 1000 MAIN -> 10-22 12:00 | to Gamma | pl 1",
				"10-22 13:00 BUS 4000  -> 10-22 14:00 | to Gamma | pl ",
				"10-23 10:00 IC 1000 MAIN -> 10-23 12:00 | to Gamma | pl 1",
			},
		},
		{
			name: "n limits the result",
			from: "A", to: "C", after: at("2026-10-22", "00:00"), n: 1,
			want: []string{
				"10-22 10:00 IC 1000 MAIN -> 10-22 12:00 | to Gamma | pl 1",
			},
		},
		{
			name: "departure exactly at 'after' is included, earlier ones are not",
			from: "A", to: "C", after: at("2026-10-22", "13:00"), n: 10,
			want: []string{
				"10-22 13:00 BUS 4000  -> 10-22 14:00 | to Gamma | pl ",
				"10-23 10:00 IC 1000 MAIN -> 10-23 12:00 | to Gamma | pl 1",
			},
		},
		{
			name: "wrong direction yields nothing",
			from: "C", to: "A", after: at("2026-10-22", "00:00"), n: 10,
			want: []string{},
		},
		{
			name: "can't board where pickup is forbidden or alight where drop-off is",
			from: "A", to: "B", after: at("2026-10-22", "00:00"), n: 10,
			want: []string{
				"10-22 10:00 IC 1000 MAIN -> 10-22 11:00 | to Gamma | pl 1",
				"10-23 10:00 IC 1000 MAIN -> 10-23 11:00 | to Gamma | pl 1",
			},
		},
		{
			name: "boarding-only stop is a valid origin; times past 24:00 roll over",
			from: "B", to: "C", after: at("2026-10-22", "09:00"), n: 10,
			want: []string{
				"10-22 09:31 KD 2000  -> 10-22 10:00 | to Gamma | pl ",
				"10-22 11:05 IC 1000 MAIN -> 10-22 12:00 | to Gamma | pl ",
				"10-22 23:50 EN 3000 NIGHT -> 10-23 00:20 | to Gamma | pl ",
				"10-23 11:05 IC 1000 MAIN -> 10-23 12:00 | to Gamma | pl ",
			},
		},
		{
			name: "unknown station yields nothing",
			from: "A", to: "nope", after: at("2026-10-22", "00:00"), n: 10,
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tt.Connections(tc.from, tc.to, tc.after, tc.n)
			assertConnections(t, got, tc.want)
		})
	}
}

func TestConnections_BusAndCarrier(t *testing.T) {
	tt := newTimetable(t, synthetic)

	got := tt.Connections("A", "C", at("2026-10-22", "11:00"), 2)
	if len(got) != 2 {
		t.Fatalf("got %d connections, want 2", len(got))
	}
	bus, train := got[0], got[1]
	if !bus.Bus || bus.Carrier != "KD" {
		t.Errorf("bus = %+v; want Bus=true Carrier=KD", bus)
	}
	if train.Bus || train.Carrier != "IC" {
		t.Errorf("train = %+v; want Bus=false Carrier=IC", train)
	}
}
