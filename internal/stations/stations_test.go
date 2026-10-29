package stations

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// Real station list (location_type=1) from the mkuran.pl GTFS of 2026-10-16.
func loadIndex(t *testing.T) *Index {
	t.Helper()
	raw, err := os.ReadFile("testdata/stations-gtfs-2026-10-16.json")
	if err != nil {
		t.Fatal(err)
	}
	var names map[string]string
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	return NewIndex(names)
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Wrocław Główny", "wroclaw glowny"},
		{"ŁÓDŹ", "lodz"},
		{"Zażółć gęślą jaźń", "zazolc gesla jazn"},
		{"Krakow_Glowny", "krakow glowny"},
		{"krakow-glowny", "krakow glowny"},
		{"Krakow+Glowny", "krakow glowny"},
		{"Gdynia Wzgórze Św.Maksymiliana", "gdynia wzgorze sw maksymiliana"},
		{"  Bruck  A.D.Mur ", "bruck a d mur"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	ix := loadIndex(t)

	tests := []struct {
		name   string
		query  string
		wantID string
		want   string
	}{
		{"bare city picks Główny", "Wroclaw", "60103", "Wrocław Główny"},
		{"diacritics are optional", "wrocław", "60103", "Wrocław Główny"},
		{"case-insensitive", "KRAKOW", "80416", "Kraków Główny"},
		{"abbreviated suffix", "Kraków Gł", "80416", "Kraków Główny"},
		{"URL-style separators", "krakow-glowny", "80416", "Kraków Główny"},
		{"feminine/neuter main suffix", "Kielce", "63552", "Kielce Główne"},
		{"Zielona Góra: two-word city", "Zielona Gora", "27805", "Zielona Góra Główna"},
		{"Warsaw alias to Centralna", "Warszawa", "33605", "Warszawa Centralna"},
		{"explicit name beats alias", "warszawa glowna", "33472", "Warszawa Główna"},
		{"Łódź alias to Fabryczna", "Lodz", "46458", "Łódź Fabryczna"},
		{"Łódź other station", "lodz kaliska", "46706", "Łódź Kaliska"},
		{"exact city-named station", "Katowice", "73312", "Katowice"},
		{"exact beats longer names", "Bialystok", "24000", "Białystok"},
		{"district without city", "Plaszow", "79905", "Kraków Płaszów"},
		{"token prefixes", "krak plasz", "79905", "Kraków Płaszów"},
		{"punctuation in name", "Gdynia Wzgorze", "5926", "Gdynia Wzgórze Świętego Maksymiliana"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ix.Resolve(tc.query)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.query, err)
			}
			if got.ID != tc.wantID || got.Name != tc.want {
				t.Errorf("Resolve(%q) = {%s %q}, want {%s %q}", tc.query, got.ID, got.Name, tc.wantID, tc.want)
			}
		})
	}
}

func TestResolve_DuplicateNamesAreDeterministic(t *testing.T) {
	// PLK's own dictionary lists e.g. "Warszawa Praga" under two IDs.
	ix := NewIndex(map[string]string{"36129": "Warszawa Praga", "201092": "Warszawa Praga"})

	for range 20 {
		got, err := ix.Resolve("warszawa praga")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "201092" {
			t.Fatalf("Resolve(warszawa praga).ID = %s, want 201092 (lowest ID as string)", got.ID)
		}
	}
}

func TestResolve_SkipsPlaceholderEntries(t *testing.T) {
	// PLK's dictionary has pseudo-stations no train stops at.
	ix := NewIndex(map[string]string{
		"1000003": "KRAKÓW -",
		"281407":  "---Brak nazwy---",
		"80416":   "Kraków Główny",
	})

	for _, q := range []string{"Krakow", "brak nazwy"} {
		got, err := ix.Resolve(q)
		if q == "Krakow" && (err != nil || got.ID != "80416") {
			t.Errorf("Resolve(%q) = %+v, %v; want Kraków Główny", q, got, err)
		}
		if q == "brak nazwy" && !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %+v, %v; want ErrNotFound", q, got, err)
		}
	}
}

func TestResolve_NotFound(t *testing.T) {
	ix := loadIndex(t)

	for _, q := range []string{"", "   ", "Xyzzy", "Wroclaw Xyzzy", "---", "brak nazwy"} {
		if got, err := ix.Resolve(q); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %+v, %v; want ErrNotFound", q, got, err)
		}
	}
}
