package server

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"pociag.info/internal/feeds"
)

const (
	curlUA    = "curl/8.5.0"
	browserUA = "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0"
)

var warsaw, _ = time.LoadLocation("Europe/Warsaw")

// snapshotTime is when testdata/gtfs/wro-krk-updates.pb was captured.
var snapshotTime = time.Date(2026, 10, 22, 20, 27, 0, 0, warsaw)

func loadedStore(t *testing.T) *feeds.Store {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	paths, _ := filepath.Glob("../../testdata/gtfs/wro-krk/*.txt")
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create(filepath.Base(p))
		w.Write(data)
	}
	zw.Close()
	zipPath := filepath.Join(t.TempDir(), "static.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	var s feeds.Store
	if err := s.LoadStaticFile(zipPath); err != nil {
		t.Fatal(err)
	}
	pb, err := os.ReadFile("../../testdata/gtfs/wro-krk-updates.pb")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LoadRealtime(pb); err != nil {
		t.Fatal(err)
	}
	return &s
}

type response struct {
	status      int
	contentType string
	body        string
}

func get(t *testing.T, h http.Handler, target string, headers ...string) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("User-Agent", curlUA)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return response{rec.Code, rec.Header().Get("Content-Type"), string(body)}
}

func newServer(t *testing.T, now time.Time) *Server {
	return &Server{Data: loadedStore(t), Now: func() time.Time { return now }}
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body lacks %q\n%s", w, body)
		}
	}
}

var boardRow = regexp.MustCompile(`(?m)^\d\d:\d\d `)

func TestBoard_CurlGetsColouredText(t *testing.T) {
	r := get(t, newServer(t, snapshotTime), "/Wroclaw/Krakow")

	if r.status != http.StatusOK || r.contentType != "text/plain; charset=utf-8" {
		t.Fatalf("status %d, content-type %q", r.status, r.contentType)
	}
	mustContain(t, r.body,
		"(@)=(@)",
		"Wrocław Główny → Kraków Główny",
		"IC 73004/5 ODRA", // first train still to leave at 20:27, predicted +4
		"\x1b[",
		"Opóźnienia: stan na 20:26",
		"Dane: PKP PLK S.A. · GTFS: Mikołaj Kuranowski",
	)
}

func TestBoard_PlainTextWithT(t *testing.T) {
	r := get(t, newServer(t, snapshotTime), "/Wroclaw/Krakow?T")

	if strings.Contains(r.body, "\x1b") {
		t.Errorf("?T output contains escape sequences:\n%q", r.body)
	}
	mustContain(t, r.body, "20:53 +4")
}

func TestBoard_BrowserGetsHTML(t *testing.T) {
	r := get(t, newServer(t, snapshotTime), "/Wroclaw/Krakow", "User-Agent", browserUA)

	if r.status != http.StatusOK || r.contentType != "text/html; charset=utf-8" {
		t.Fatalf("status %d, content-type %q", r.status, r.contentType)
	}
	mustContain(t, r.body, "<!doctype html>", `<pre class="wide">`, `<pre class="narrow">`, "IC 73004/5 ODRA")
}

func TestBoard_Language(t *testing.T) {
	s := newServer(t, snapshotTime)

	mustContain(t, get(t, s, "/Wroclaw/Krakow?lang=en").body, "Departs", "Data: PKP PLK")
	mustContain(t, get(t, s, "/Wroclaw/Krakow", "User-Agent", browserUA, "Accept-Language", "en-US,en;q=0.9").body, "Departs")
	mustContain(t, get(t, s, "/Wroclaw/Krakow", "User-Agent", browserUA, "Accept-Language", "pl-PL,pl;q=0.9,en;q=0.8").body, "Odjazd")
	mustContain(t, get(t, s, "/Wroclaw/Krakow?lang=xx").body, "Odjazd")
}

func TestBoard_Count(t *testing.T) {
	// From 08:00 there are plenty of trains in both service days.
	s := newServer(t, time.Date(2026, 10, 22, 8, 0, 0, 0, warsaw))

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 8},
		{"?n=1", 1},
		{"?n=3", 3},
		{"?n=abc", 8},
		{"?n=0", 8},
		{"?n=100", 20},
	} {
		body := get(t, s, "/Wroclaw/Krakow"+tc.query+"&T").body
		if tc.query == "" {
			body = get(t, s, "/Wroclaw/Krakow?T").body
		}
		if got := len(boardRow.FindAllString(body, -1)); got != tc.want {
			t.Errorf("%q: %d rows, want %d", tc.query, got, tc.want)
		}
	}
}

func TestBoard_StationSpellings(t *testing.T) {
	s := newServer(t, snapshotTime)

	for _, path := range []string{
		"/wroclaw/krakow/",
		"/Wroc%C5%82aw%20G%C5%82%C3%B3wny/Krak%C3%B3w",
		"/Wroclaw-Glowny/Krakow_Glowny",
	} {
		r := get(t, s, path)
		if r.status != http.StatusOK {
			t.Errorf("%s: status %d\n%s", path, r.status, r.body)
			continue
		}
		mustContain(t, r.body, "Wrocław Główny → Kraków Główny")
	}
}

func TestErrors(t *testing.T) {
	s := newServer(t, snapshotTime)

	tests := []struct {
		path   string
		status int
		want   string
	}{
		{"/Xyzzy/Krakow", http.StatusNotFound, "Nie znaleziono stacji „Xyzzy”."},
		{"/Wroclaw/Xyzzy?lang=en", http.StatusNotFound, "Station not found: „Xyzzy”."},
		{"/Wroclaw", http.StatusBadRequest, "Podaj stację początkową i docelową"},
		{"/Wroclaw/Krakow/Katowice", http.StatusBadRequest, "Podaj stację początkową i docelową"},
		{"/Wroclaw/Wroclaw%20Glowny", http.StatusBadRequest, "ta sama stacja: Wrocław Główny"},
	}
	for _, tc := range tests {
		r := get(t, s, tc.path)
		if r.status != tc.status {
			t.Errorf("%s: status %d, want %d", tc.path, r.status, tc.status)
		}
		mustContain(t, r.body, tc.want, "Mikołaj Kuranowski")
	}
}

func TestHelp(t *testing.T) {
	s := newServer(t, snapshotTime)

	for _, path := range []string{"/", "/:help"} {
		r := get(t, s, path)
		if r.status != http.StatusOK {
			t.Errorf("%s: status %d", path, r.status)
		}
		mustContain(t, r.body, "(@)=(@)", "curl pociag.info/Wroclaw/Krakow", "?lang=en")
	}
	mustContain(t, get(t, s, "/?lang=en").body, "check train timetables")
	mustContain(t, get(t, s, "/", "User-Agent", browserUA).body, "<!doctype html>", "pociag.info/Wroclaw/Krakow")
}

func TestFavicon(t *testing.T) {
	if r := get(t, newServer(t, snapshotTime), "/favicon.ico"); r.status != http.StatusNotFound {
		t.Errorf("status %d, want 404", r.status)
	}
}

func TestNotLoadedYet(t *testing.T) {
	s := &Server{Data: &feeds.Store{}, Now: func() time.Time { return snapshotTime }}

	req := httptest.NewRequest(http.MethodGet, "/Wroclaw/Krakow", nil)
	req.Header.Set("User-Agent", curlUA)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	mustContain(t, rec.Body.String(), "Wczytuję rozkład")
}

func TestStaleRealtimeIsIgnored(t *testing.T) {
	r := get(t, newServer(t, snapshotTime.Add(31*time.Minute)), "/Wroclaw/Krakow?T")

	mustContain(t, r.body, "Brak danych na żywo")
	if strings.Contains(r.body, "Opóźnienia: stan na") {
		t.Error("stale snapshot still reported")
	}
}
