package render

import (
	"time"
	_ "time/tzdata"
)

var warsaw = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Strings are the translatable texts. Messages ending in "%s" take a station
// name or query.
type Strings struct {
	Departs, Arrives, Train, Towards, Platform string
	Cancelled, NoConnections                   string
	LiveAsOf, NoLive, Credits                  string
	StationNotFound, NeedTwoStations           string
	SameStation, Loading, Help                 string

	weekdays [7]string // indexed by time.Weekday
}

const credits = "PKP PLK S.A. · GTFS: Mikołaj Kuranowski"

var strs = map[Lang]Strings{
	PL: {
		Departs: "Odjazd", Arrives: "Przyjazd", Train: "Pociąg", Towards: "Kierunek", Platform: "Peron",
		Cancelled:       "odwołany",
		NoConnections:   "Brak bezpośrednich połączeń w najbliższych dniach.",
		LiveAsOf:        "Opóźnienia: stan na",
		NoLive:          "Brak danych na żywo",
		Credits:         "Dane: " + credits,
		StationNotFound: "Nie znaleziono stacji „%s”.",
		NeedTwoStations: "Podaj stację początkową i docelową, np. curl pociag.info/Wroclaw/Krakow",
		SameStation:     "Stacja początkowa i docelowa to ta sama stacja: %s.",
		Loading:         "Wczytuję rozkład, spróbuj za chwilę.",
		Help: `pociag.info — najbliższe pociągi między dwiema stacjami

  curl pociag.info/Wroclaw/Krakow
  curl pociag.info/Warszawa/Gdansk?n=5
  curl pociag.info/Krakow-Plaszow/Katowice

Opcje:
  ?n=5       liczba pociągów (1–20)
  ?lang=en   po angielsku
  ?T         bez kolorów`,
		weekdays: [7]string{"niedziela", "poniedziałek", "wtorek", "środa", "czwartek", "piątek", "sobota"},
	},
	EN: {
		Departs: "Departs", Arrives: "Arrives", Train: "Train", Towards: "Towards", Platform: "Platform",
		Cancelled:       "cancelled",
		NoConnections:   "No direct connections in the next few days.",
		LiveAsOf:        "Live data as of",
		NoLive:          "No live data",
		Credits:         "Data: " + credits,
		StationNotFound: "Station not found: „%s”.",
		NeedTwoStations: "Give a start and a destination, e.g. curl pociag.info/Wroclaw/Krakow",
		SameStation:     "Start and destination are the same station: %s.",
		Loading:         "Loading the timetable, try again in a moment.",
		Help: `pociag.info — the next trains between two Polish stations

  curl pociag.info/Wroclaw/Krakow
  curl pociag.info/Warszawa/Gdansk?n=5
  curl pociag.info/Krakow-Plaszow/Katowice

Options:
  ?n=5       number of trains (1–20)
  ?lang=en   English
  ?T         no colours`,
		weekdays: [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	},
}

// For returns the texts for lang, falling back to Polish.
func For(lang Lang) Strings {
	if s, ok := strs[lang]; ok {
		return s
	}
	return strs[PL]
}

func (s Strings) longDate(t time.Time) string {
	return s.weekdays[t.Weekday()] + ", " + t.Format("02.01.2006")
}

func (s Strings) shortDate(t time.Time) string {
	return s.weekdays[t.Weekday()] + ", " + t.Format("02.01")
}
