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
	SameStation, Loading, HelpTitle            string
	Help, HelpWeb                              string // usage for curl and for browsers

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
		HelpTitle:       "pociag.info — sprawdź rozkład jazdy pociągów w terminalu i przeglądarce",
		Help: `  curl pociag.info/Wroclaw/Krakow
  curl pociag.info/Warszawa/Gdansk?n=5
  curl pociag.info/Krakow-Plaszow/Katowice

Opcje (łącz je przez &, np. ?n=5&lang=en):
  ?n=5       liczba pociągów, 1–20 (domyślnie 8)
  ?lang=en   język: pl lub en (domyślnie pl)
  ?T         czysty tekst bez kolorów (domyślnie z kolorami)`,
		HelpWeb: `Wpisz stacje w adresie strony:

  pociag.info/Wroclaw/Krakow
  pociag.info/Warszawa/Gdansk?n=5
  pociag.info/Kielce/Lublin?lang=en

Opcje (łącz je przez &, np. ?n=5&lang=en):
  ?n=5       liczba pociągów, 1–20 (domyślnie 8)
  ?lang=en   język: pl lub en (domyślnie język przeglądarki)`,
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
		HelpTitle:       "pociag.info — check train timetables in your terminal and browser",
		Help: `  curl pociag.info/Wroclaw/Krakow
  curl pociag.info/Warszawa/Gdansk?n=5
  curl pociag.info/Krakow-Plaszow/Katowice

Options (join them with &, e.g. ?n=5&lang=en):
  ?n=5       number of trains, 1–20 (default 8)
  ?lang=en   language: pl or en (default pl)
  ?T         plain text without colours (default: coloured)`,
		HelpWeb: `Put the stations in the address:

  pociag.info/Wroclaw/Krakow
  pociag.info/Warszawa/Gdansk?n=5
  pociag.info/Kielce/Lublin?lang=en

Options (join them with &, e.g. ?n=5&lang=pl):
  ?n=5       number of trains, 1–20 (default 8)
  ?lang=pl   language: pl or en (default: your browser's)`,
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
