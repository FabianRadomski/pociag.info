package render

import (
	"bytes"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"pociag.info/internal/realtime"
	"pociag.info/internal/timetable"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, warsaw)
	if err != nil {
		panic(err)
	}
	return t
}

func predicted(s string) realtime.Event { return realtime.Event{Time: at(s)} }

func sampleBoard(lang Lang) Board {
	return Board{
		Lang:   lang,
		From:   "Wrocław Główny",
		To:     "Kraków Główny",
		Now:    at("2026-10-22 20:27:00"),
		LiveAt: at("2026-10-22 20:26:57"),
		Connections: []timetable.Connection{
			{
				Category: "IC", Number: "73004/5", Name: "ODRA",
				Departure: at("2026-10-22 20:53:00"), Arrival: at("2026-10-22 23:43:00"),
				DeparturePlatform: "3", Destination: "Przemyśl Główny",
				Live: &timetable.Live{Departure: predicted("2026-10-22 20:57:00"), Arrival: predicted("2026-10-22 23:43:00")},
			},
			{
				Category: "TLK", Number: "63100/1", Name: "PLANTY",
				Departure: at("2026-10-22 21:30:00"), Arrival: at("2026-10-23 01:10:00"),
				DeparturePlatform: "2", Destination: "Kraków Główny",
				Live: &timetable.Live{Cancelled: true},
			},
			{
				Category: "IC", Number: "73010/1", Name: "URSA",
				Departure: at("2026-10-23 03:10:00"), Arrival: at("2026-10-23 06:33:00"),
				DeparturePlatform: "3", Destination: "Przemyśl Główny",
				Live: &timetable.Live{Departure: predicted("2026-10-23 03:10:00"), Arrival: predicted("2026-10-23 06:42:00")},
			},
			{
				Category: "BUS", Number: "4000", Bus: true,
				Departure: at("2026-10-23 05:00:00"), Arrival: at("2026-10-23 08:00:00"),
				Destination: "Kraków Główny",
			},
		},
	}
}

func text(t *testing.T, b Board, color bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Text(&buf, b, color); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func assertText(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestText_Polish(t *testing.T) {
	got := text(t, sampleBoard(PL), false)

	assertText(t, got, `Wrocław Główny → Kraków Główny
czwartek, 22.10.2026, 20:27

Odjazd    Przyjazd  Pociąg              Kierunek         Peron
20:53 +4  23:43     IC 73004/5 ODRA     Przemyśl Główny  3
21:30     odwołany  TLK 63100/1 PLANTY  Kraków Główny    2
── piątek, 23.10 ──
03:10     06:33 +9  IC 73010/1 URSA     Przemyśl Główny  3
05:00     08:00     BUS 4000            Kraków Główny

Opóźnienia: stan na 20:26
Dane: PKP PLK S.A. · GTFS: Mikołaj Kuranowski
`)
}

func TestText_English(t *testing.T) {
	got := text(t, sampleBoard(EN), false)

	assertText(t, got, `Wrocław Główny → Kraków Główny
Thursday, 22.10.2026, 20:27

Departs   Arrives    Train               Towards          Platform
20:53 +4  23:43      IC 73004/5 ODRA     Przemyśl Główny  3
21:30     cancelled  TLK 63100/1 PLANTY  Kraków Główny    2
── Friday, 23.10 ──
03:10     06:33 +9   IC 73010/1 URSA     Przemyśl Główny  3
05:00     08:00      BUS 4000            Kraków Główny

Live data as of 20:26
Data: PKP PLK S.A. · GTFS: Mikołaj Kuranowski
`)
}

func TestText_NoLiveDataAndNoConnections(t *testing.T) {
	b := sampleBoard(PL)
	b.LiveAt = time.Time{}
	b.Connections = nil

	assertText(t, text(t, b, false), `Wrocław Główny → Kraków Główny
czwartek, 22.10.2026, 20:27

Brak bezpośrednich połączeń w najbliższych dniach.

Brak danych na żywo
Dane: PKP PLK S.A. · GTFS: Mikołaj Kuranowski
`)
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestText_ColorKeepsAlignment(t *testing.T) {
	b := sampleBoard(PL)
	plain := text(t, b, false)
	colored := text(t, b, true)

	if !strings.Contains(colored, "\x1b[33m+4\x1b[0m") {
		t.Error("small delay (+4) not yellow")
	}
	if !strings.Contains(colored, "\x1b[31m+9\x1b[0m") {
		t.Error("big delay (+9) not red")
	}
	if !strings.Contains(colored, "\x1b[31modwołany\x1b[0m") {
		t.Error("cancellation not red")
	}
	if stripped := ansi.ReplaceAllString(colored, ""); stripped != plain {
		t.Errorf("colored output without escapes differs from plain\n--- stripped ---\n%s\n--- plain ---\n%s", stripped, plain)
	}
}

func TestHTML_SameBoardInAPage(t *testing.T) {
	b := sampleBoard(PL)
	b.Connections[0].Destination = "<script>alert(1)</script>"
	var buf bytes.Buffer
	if err := HTML(&buf, b); err != nil {
		t.Fatal(err)
	}
	page := buf.String()

	for _, want := range []string{
		"<!doctype html>",
		`<html lang="pl">`,
		"<title>Wrocław Główny → Kraków Główny · pociag.info</title>",
		`<span class="late-small">+4</span>`,
		`<span class="late">+9</span>`,
		"&lt;script&gt;",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>") {
		t.Error("destination not escaped")
	}

	pre := regexp.MustCompile(`(?s)<pre>(.*)</pre>`).FindStringSubmatch(page)
	if pre == nil {
		t.Fatal("no <pre> block")
	}
	content := html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(pre[1], ""))
	if want := text(t, b, false); content != want {
		t.Errorf("<pre> text differs from the text board\n--- pre ---\n%s\n--- text ---\n%s", content, want)
	}
}

func TestNotice(t *testing.T) {
	var buf bytes.Buffer
	if err := Notice(&buf, FormatText, EN, "Station not found: „Xyz”"); err != nil {
		t.Fatal(err)
	}
	assertText(t, buf.String(), "Station not found: „Xyz”\n\nData: PKP PLK S.A. · GTFS: Mikołaj Kuranowski\n")

	buf.Reset()
	if err := Notice(&buf, FormatHTML, PL, "a < b"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "a &lt; b") || !strings.Contains(buf.String(), "<!doctype html>") {
		t.Errorf("HTML notice = %s", buf.String())
	}
}

func TestStringsExistInBothLanguages(t *testing.T) {
	for _, lang := range []Lang{PL, EN} {
		s := For(lang)
		for name, v := range map[string]string{
			"Departs": s.Departs, "Arrives": s.Arrives, "Train": s.Train, "Towards": s.Towards,
			"Platform": s.Platform, "Cancelled": s.Cancelled, "NoConnections": s.NoConnections,
			"LiveAsOf": s.LiveAsOf, "NoLive": s.NoLive, "Credits": s.Credits,
			"StationNotFound": s.StationNotFound, "NeedTwoStations": s.NeedTwoStations,
			"SameStation": s.SameStation, "Loading": s.Loading, "Help": s.Help,
		} {
			if v == "" {
				t.Errorf("%v: %s is empty", lang, name)
			}
		}
	}
}

func viaBoard() Board {
	return Board{
		Lang: EN,
		From: "Wrocław Główny",
		To:   "Gdańsk Główny",
		Via:  []string{"Poznań Główny", "Bydgoszcz Główna"},
		Now:  at("2026-10-22 20:27:00"),
		Journeys: []timetable.Journey{
			{Legs: []timetable.Connection{
				{
					Category: "IC", Number: "1234", Name: "ODRA",
					Departure: at("2026-10-22 21:12:00"), Arrival: at("2026-10-22 22:58:00"),
					DeparturePlatform: "3", Destination: "Szczecin Główny",
					From: "Wrocław Główny", To: "Poznań Główny",
					Live: &timetable.Live{Departure: predicted("2026-10-22 21:16:00"), Arrival: predicted("2026-10-22 23:02:00")},
				},
				{
					Category: "IC", Number: "5432", Name: "KASZUBY",
					Departure: at("2026-10-22 23:15:00"), Arrival: at("2026-10-23 01:40:00"),
					DeparturePlatform: "1", Destination: "Gdynia Główna",
					From: "Poznań Główny", To: "Gdańsk Główny",
				},
			}},
			{Legs: []timetable.Connection{
				{
					Category: "TLK", Number: "61100", Departure: at("2026-10-23 05:00:00"), Arrival: at("2026-10-23 07:00:00"),
					DeparturePlatform: "2", Destination: "Poznań Główny",
					From: "Wrocław Główny", To: "Poznań Główny",
				},
				{
					Category: "KW", Number: "77000", Departure: at("2026-10-23 07:10:00"), Arrival: at("2026-10-23 09:00:00"),
					Destination: "Bydgoszcz Główna",
					From:        "Poznań Główny", To: "Bydgoszcz Główna",
				},
				{
					Category: "IC", Number: "38100", Departure: at("2026-10-23 09:20:00"), Arrival: at("2026-10-23 11:00:00"),
					DeparturePlatform: "4", Destination: "Gdańsk Główny",
					From: "Bydgoszcz Główna", To: "Gdańsk Główny",
				},
			}},
		},
	}
}

func TestText_Via(t *testing.T) {
	got := text(t, viaBoard(), false)

	assertText(t, got, `Wrocław Główny → Gdańsk Główny, via Poznań Główny, Bydgoszcz Główna
Thursday, 22.10.2026, 20:27

Departs   Arrives   Train            Towards           Platform
21:12 +4  22:58 +4  IC 1234 ODRA     Szczecin Główny   3
          change in Poznań Główny, 13 min
23:15     01:40     IC 5432 KASZUBY  Gdynia Główna     1

── Friday, 23.10 ──
05:00     07:00     TLK 61100        Poznań Główny     2
          change in Poznań Główny, 10 min
07:10     09:00     KW 77000         Bydgoszcz Główna
          change in Bydgoszcz Główna, 20 min
09:20     11:00     IC 38100         Gdańsk Główny     4

No live data
Data: PKP PLK S.A. · GTFS: Mikołaj Kuranowski
`)
}

func TestText_ViaANSI(t *testing.T) {
	got := text(t, viaBoard(), true)

	if !strings.Contains(got, "          \x1b[2mchange in Poznań Główny, 13 min\x1b[0m\n") {
		t.Errorf("change line not indented and dimmed:\n%q", got)
	}
}

func TestText_ViaNoJourneys(t *testing.T) {
	b := viaBoard()
	b.Journeys = nil
	got := text(t, b, false)

	if !strings.Contains(got, "\nNo connections via these stations in the next few days.\n") {
		t.Errorf("missing no-journeys message:\n%s", got)
	}
}
