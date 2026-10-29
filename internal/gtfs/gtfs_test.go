package gtfs

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// Real feed from mkuran.pl (published 2026-10-22), trimmed to the 311 trips
// serving both Wrocław Główny and Kraków Główny.
const sampleDir = "../../testdata/gtfs/wro-krk"

func loadSample(t *testing.T) *Feed {
	t.Helper()
	feed, err := Load(os.DirFS(sampleDir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return feed
}

// hm is a stop time of h hours and m minutes.
func hm(h, m int) Time { return Time(h*3600 + m*60) }

// stopTimeText is a StopTime with its Strs resolved, for comparisons.
type stopTimeText struct {
	StopID             string
	Sequence           int32
	Arrival, Departure Time
	NoPickup           bool
	NoDropOff          bool
	Platform, Track    string
	Category           string
}

func (f *Feed) text(st StopTime) stopTimeText {
	return stopTimeText{
		StopID: f.Str(st.StopID), Sequence: st.Sequence,
		Arrival: st.Arrival, Departure: st.Departure,
		NoPickup: st.NoPickup, NoDropOff: st.NoDropOff,
		Platform: f.Str(st.Platform), Track: f.Str(st.Track), Category: f.Str(st.Category),
	}
}

func TestLoad_Sample_Stations(t *testing.T) {
	feed := loadSample(t)

	st, ok := feed.Stations["60103"]
	if !ok || st.Name != "Wrocław Główny" {
		t.Fatalf("Stations[60103] = %+v, %v; want Wrocław Główny", st, ok)
	}
	if _, ok := feed.Stations["60103_RAIL_2_3"]; ok {
		t.Error("platform-level stop listed as a station")
	}

	stop, ok := feed.Stops["60103_RAIL_2_3"]
	if !ok || stop.StationID != "60103" {
		t.Errorf("Stops[60103_RAIL_2_3] = %+v, %v; want StationID 60103", stop, ok)
	}
	if got := feed.Stops["60103"].StationID; got != "60103" {
		t.Errorf("a station's own StationID = %q, want itself", got)
	}
}

func TestLoad_Sample_Trip(t *testing.T) {
	feed := loadSample(t)

	trip, ok := feed.Trips["PLK_IC_2026_305172747"]
	if !ok {
		t.Fatal("GROTTGER trip missing")
	}
	if trip.Headsign != "Przemyśl Główny" || trip.Number != "6304/5" || trip.Name != "GROTTGER" ||
		trip.Category != "IC" || trip.RouteID != "IC_IC" || trip.ServiceID != "PLK_38" {
		t.Errorf("trip = %+v", trip)
	}
	if len(trip.StopTimes) != 17 {
		t.Fatalf("len(StopTimes) = %d, want 17", len(trip.StopTimes))
	}
	first := feed.text(trip.StopTimes[0])
	want := stopTimeText{
		StopID: "60103_RAIL_2_3", Sequence: 1,
		Arrival: hm(5, 10), Departure: hm(5, 10),
		Platform: "2", Track: "3", Category: "IC",
	}
	if first != want {
		t.Errorf("StopTimes[0] = %+v\n                 want %+v", first, want)
	}
	if last := feed.text(trip.StopTimes[16]); last.StopID != "84400_RAIL_1_3" || last.Sequence != 17 {
		t.Errorf("StopTimes[16] = %+v, want 84400_RAIL_1_3 seq 17", last)
	}
}

func TestLoad_Sample_RoutesAndAgencies(t *testing.T) {
	feed := loadSample(t)

	r := feed.Routes["IC_TLK"]
	if r.AgencyID != "IC" || r.ShortName != "TLK" || r.Type != RouteTypeRail {
		t.Errorf("Routes[IC_TLK] = %+v", r)
	}
	if a := feed.Agencies["IC"]; a.Name != "PKP Intercity" {
		t.Errorf("Agencies[IC] = %+v", a)
	}
}

func TestLoad_Sample_TimesPastMidnight(t *testing.T) {
	feed := loadSample(t)

	// URSA's Wrocław stop: "26:59:00","27:10:00" on the previous service day.
	st := feed.Trips["PLK_IC_2026_322124420"].StopTimes[15]
	if st.Arrival != hm(26, 59) || st.Departure != hm(27, 10) {
		t.Errorf("arrival/departure = %v/%v, want 26h59m/27h10m", st.Arrival, st.Departure)
	}
}

func TestLoad_Sample_Services(t *testing.T) {
	feed := loadSample(t)

	for _, tc := range []struct {
		date Date
		want bool
	}{
		{Date{2026, 10, 21}, false},
		{Date{2026, 10, 22}, true},
		{Date{2026, 10, 24}, true},
		{Date{2026, 10, 25}, false},
	} {
		if got := feed.Runs("PLK_38", tc.date); got != tc.want {
			t.Errorf("Runs(PLK_38, %v) = %v, want %v", tc.date, got, tc.want)
		}
	}
	if feed.Runs("NO_SUCH_SERVICE", Date{2026, 10, 22}) {
		t.Error("unknown service reported as running")
	}
}

// minimal returns a tiny valid feed; callers override individual files.
func minimal(overrides map[string]string) fstest.MapFS {
	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\nX,Example,https://x,Europe/Warsaw\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\nR,X,R,Route,3\n",
		"stops.txt": "stop_id,stop_name,location_type,parent_station\n" +
			"S1,Alfa,1,\nS1_P,Alfa,0,S1\nS2,Beta,0,\n",
		// Columns deliberately in a non-standard order, without plk_* extras.
		"trips.txt": "trip_headsign,trip_id,service_id,route_id\nBeta,T,SV,R\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence,pickup_type,drop_off_type\n" +
			"T,25:30:00,25:31:00,S2,7,0,1\n" +
			"T,,24:00:00,S1_P,3,1,\n",
		"calendar_dates.txt": "service_id,date,exception_type\nSV,20261022,1\nSV,20261023,2\n",
	}
	for k, v := range overrides {
		files[k] = v
	}
	m := fstest.MapFS{}
	for k, v := range files {
		if v != "" {
			m[k] = &fstest.MapFile{Data: []byte(v)}
		}
	}
	return m
}

func TestLoad_Minimal(t *testing.T) {
	feed, err := Load(minimal(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	trip := feed.Trips["T"]
	if trip.Headsign != "Beta" || trip.RouteID != "R" || trip.Number != "" {
		t.Errorf("trip = %+v", trip)
	}
	if len(trip.StopTimes) != 2 || trip.StopTimes[0].Sequence != 3 || trip.StopTimes[1].Sequence != 7 {
		t.Fatalf("StopTimes not sorted by sequence: %+v", trip.StopTimes)
	}
	s1, s2 := trip.StopTimes[0], trip.StopTimes[1]
	if s1.Arrival != NoTime || s1.Departure != hm(24, 0) || !s1.NoPickup || s1.NoDropOff {
		t.Errorf("first stop = %+v; want no arrival, departure 24h, no pickup", s1)
	}
	if s2.Arrival != hm(25, 30) || !s2.NoDropOff || s2.NoPickup {
		t.Errorf("second stop = %+v; want arrival 25h30m, no drop-off", s2)
	}
	if feed.Str(s1.StopID) != "S1_P" || feed.Str(s2.StopID) != "S2" || s1.Platform != 0 || feed.Str(s1.Platform) != "" {
		t.Errorf("stop IDs %q, %q, platform %q; want S1_P, S2 and empty platform as the zero Str",
			feed.Str(s1.StopID), feed.Str(s2.StopID), feed.Str(s1.Platform))
	}
	if feed.Stops["S2"].StationID != "S2" {
		t.Errorf("parentless stop should be its own station: %+v", feed.Stops["S2"])
	}
	if feed.Routes["R"].Type != RouteTypeBus {
		t.Errorf("route type = %v, want bus", feed.Routes["R"].Type)
	}
	if !feed.Runs("SV", Date{2026, 10, 22}) || feed.Runs("SV", Date{2026, 10, 23}) {
		t.Error("exception_type 1 must add a date, 2 must not")
	}
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		wantInErr []string
	}{
		{"missing file", map[string]string{"stops.txt": ""}, []string{"stops.txt"}},
		{"missing column", map[string]string{"trips.txt": "route_id,service_id\nR,SV\n"}, []string{"trips.txt", "trip_id"}},
		{"bad time", map[string]string{"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT,ab:cd:ef,10:00:00,S2,1\n"}, []string{"stop_times.txt", "line 2", "ab:cd:ef"}},
		{"bad date", map[string]string{"calendar_dates.txt": "service_id,date,exception_type\nSV,2026-10-22,1\n"}, []string{"calendar_dates.txt", "2026-10-22"}},
		{"unknown trip in stop_times", map[string]string{"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nNOPE,10:00:00,10:00:00,S2,1\n"}, []string{"stop_times.txt", "NOPE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(minimal(tc.overrides))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			for _, s := range tc.wantInErr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not mention %q", err, s)
				}
			}
		})
	}
}

func TestLoad_FromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	src := minimal(nil)
	names, _ := fs.Glob(src, "*.txt")
	for _, name := range names {
		w, _ := zw.Create(name)
		w.Write(src[name].Data)
	}
	zw.Close()

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	feed, err := Load(zr)
	if err != nil {
		t.Fatalf("Load(zip): %v", err)
	}
	if len(feed.Trips) != 1 {
		t.Errorf("len(Trips) = %d, want 1", len(feed.Trips))
	}
}

func TestDate_At(t *testing.T) {
	warsaw, _ := time.LoadLocation("Europe/Warsaw")
	tests := []struct {
		name string
		date Date
		off  Time
		want string // RFC 3339
	}{
		{"ordinary day", Date{2026, 10, 22}, hm(5, 10), "2026-10-22T05:10:00+02:00"},
		{"past midnight rolls over", Date{2026, 10, 21}, hm(27, 10), "2026-10-22T03:10:00+02:00"},
		// GTFS measures from "noon minus 12h", so on DST days wall-clock
		// times after the switch come out right.
		{"autumn DST day, afternoon", Date{2026, 10, 25}, hm(15, 0), "2026-10-25T15:00:00+01:00"},
		{"spring DST day, afternoon", Date{2026, 3, 29}, hm(15, 0), "2026-03-29T15:00:00+02:00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.date.At(tc.off, warsaw)
			if got.Format(time.RFC3339) != tc.want {
				t.Errorf("At = %s, want %s", got.Format(time.RFC3339), tc.want)
			}
		})
	}
}

func TestDateOfAndAddDays(t *testing.T) {
	warsaw, _ := time.LoadLocation("Europe/Warsaw")
	// 23:30 UTC on the 21st is already the 22nd in Poland.
	d := DateOf(time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC).In(warsaw))
	if d != (Date{2026, 10, 22}) {
		t.Errorf("DateOf = %v, want 2026-10-22", d)
	}
	if got := (Date{2026, 10, 31}).AddDays(1); got != (Date{2026, 11, 1}) {
		t.Errorf("AddDays across month = %v, want 2026-11-01", got)
	}
	if got := (Date{2026, 10, 22}).String(); got != "2026-10-22" {
		t.Errorf("String = %q", got)
	}
}
