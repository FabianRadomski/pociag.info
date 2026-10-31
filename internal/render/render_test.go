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

	assertText(t, got, train+"\n"+`Wrocław Główny → Kraków Główny
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

	assertText(t, got, train+"\n"+`Wrocław Główny → Kraków Główny
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

	assertText(t, text(t, b, false), train+"\n"+`Wrocław Główny → Kraków Główny
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
		`<a href="https://github.com/FabianRadomski/pociag.info">`,
		`<a href="https://github.com/MKuranowski/PolishTrainsGTFS">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>") {
		t.Error("destination not escaped")
	}

	pre := regexp.MustCompile(`(?s)<pre class="wide">(.*?)</pre>`).FindStringSubmatch(page)
	if pre == nil {
		t.Fatal("no wide <pre> block")
	}
	content := html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(pre[1], ""))
	if want := text(t, b, false); content != want {
		t.Errorf("<pre> text differs from the text board\n--- pre ---\n%s\n--- text ---\n%s", content, want)
	}
}

func TestHTML_NarrowLayout(t *testing.T) {
	var buf bytes.Buffer
	if err := HTML(&buf, sampleBoard(PL)); err != nil {
		t.Fatal(err)
	}
	pre := regexp.MustCompile(`(?s)<pre class="narrow">(.*?)</pre>`).FindStringSubmatch(buf.String())
	if pre == nil {
		t.Fatal("no narrow <pre> block")
	}
	assertText(t, html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(pre[1], "")), train+"\n"+`Wrocław Główny → Kraków Główny
czwartek, 22.10.2026, 20:27

peron 3  20:53 +4  → 23:43
IC 73004/5 ODRA · Przemyśl Główny

peron 2  21:30     → odwołany
TLK 63100/1 PLANTY · Kraków Główny

── piątek, 23.10 ──
peron 3  03:10     → 06:33 +9
IC 73010/1 URSA · Przemyśl Główny

         05:00     → 08:00
BUS 4000 · Kraków Główny

Opóźnienia: stan na 20:26
Dane:
PKP PLK S.A.
GTFS: Mikołaj Kuranowski
`)
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

func TestHelp(t *testing.T) {
	var buf bytes.Buffer
	if err := Help(&buf, FormatANSI, EN); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(@)=(@)") || !strings.Contains(buf.String(), "\x1b[1mpociag.info — check train timetables") {
		t.Errorf("help = %q", buf.String())
	}

	buf.Reset()
	if err := Help(&buf, FormatHTML, EN); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "curl") || strings.Contains(buf.String(), "?T") {
		t.Errorf("browser help shows terminal usage: %s", buf.String())
	}
	for _, link := range []string{
		`<a href="/Wroclaw/Krakow">pociag.info/Wroclaw/Krakow</a>`,
		`<a href="/Warszawa/Gdansk?n=5">pociag.info/Warszawa/Gdansk?n=5</a>`,
		`<a href="/Kielce/Lublin?lang=en">pociag.info/Kielce/Lublin?lang=en</a>`,
	} {
		if !strings.Contains(buf.String(), link) {
			t.Errorf("browser help is missing %s", link)
		}
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
			"SameStation": s.SameStation, "Loading": s.Loading, "HelpTitle": s.HelpTitle, "Help": s.Help,
			"HelpWeb": s.HelpWeb,
		} {
			if v == "" {
				t.Errorf("%v: %s is empty", lang, name)
			}
		}
	}
}
