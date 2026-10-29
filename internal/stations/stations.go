// Package stations resolves loosely typed user input ("Wroclaw", "krakow-gl")
// to a station from the PLK dictionary.
package stations

import (
	"errors"
	"slices"
	"strings"
	"unicode"
)

// ErrNotFound is returned when no station matches a query.
var ErrNotFound = errors.New("station not found")

// Station is a PLK station.
type Station struct {
	ID   string
	Name string
}

// cityAliases maps a bare city name to its main station where that station
// isn't called "<city> Główny/Główna/Główne". Keys and values are normalized.
var cityAliases = map[string]string{
	"warszawa": "warszawa centralna",
	"lodz":     "lodz fabryczna",
}

var mainSuffixes = []string{"glowny", "glowna", "glowne"}

// Index looks stations up by name.
type Index struct {
	entries []entry // sorted by station ID, so ties resolve deterministically
}

type entry struct {
	station Station
	norm    string
	tokens  []string
}

// NewIndex builds an index from station ID → name.
func NewIndex(names map[string]string) *Index {
	ix := &Index{}
	for id, name := range names {
		// Skip placeholders like "---Brak nazwy---" and "KRAKÓW -" that no train stops at.
		if trimmed := strings.TrimSpace(name); strings.HasPrefix(trimmed, "-") || strings.HasSuffix(trimmed, "-") {
			continue
		}
		norm := normalize(name)
		if norm == "" {
			continue
		}
		ix.entries = append(ix.entries, entry{
			station: Station{ID: id, Name: name},
			norm:    norm,
			tokens:  strings.Fields(norm),
		})
	}
	slices.SortFunc(ix.entries, func(a, b entry) int { return strings.Compare(a.station.ID, b.station.ID) })
	return ix
}

// Match quality, best first.
const (
	rankAlias = iota
	rankExact
	rankMainStation // "<query> Główny"
	rankCityPrefix  // first query word starts the station name
	rankAnywhere    // query words appear somewhere, in order
)

// Resolve returns the station that best matches query.
func (ix *Index) Resolve(query string) (Station, error) {
	q := normalize(query)
	if q == "" {
		return Station{}, ErrNotFound
	}
	qTokens := strings.Fields(q)
	alias := cityAliases[q]

	var best *entry
	bestRank := rankAnywhere + 1
	for i := range ix.entries {
		e := &ix.entries[i]
		rank, ok := e.rank(q, qTokens, alias)
		if !ok {
			continue
		}
		if best == nil || rank < bestRank ||
			rank == bestRank && better(e, best) {
			best, bestRank = e, rank
		}
	}
	if best == nil {
		return Station{}, ErrNotFound
	}
	return best.station, nil
}

func (e *entry) rank(q string, qTokens []string, alias string) (int, bool) {
	switch {
	case alias != "" && e.norm == alias:
		return rankAlias, true
	case e.norm == q:
		return rankExact, true
	case len(e.tokens) == len(qTokens)+1 &&
		slices.Equal(e.tokens[:len(qTokens)], qTokens) &&
		slices.Contains(mainSuffixes, e.tokens[len(qTokens)]):
		return rankMainStation, true
	case !prefixesInOrder(qTokens, e.tokens):
		return 0, false
	case strings.HasPrefix(e.tokens[0], qTokens[0]):
		return rankCityPrefix, true
	default:
		return rankAnywhere, true
	}
}

// better breaks ties between equally ranked matches: shorter (more general)
// names first, then alphabetical; equal names keep the lower ID.
func better(a, b *entry) bool {
	if len(a.tokens) != len(b.tokens) {
		return len(a.tokens) < len(b.tokens)
	}
	return a.norm < b.norm
}

// prefixesInOrder reports whether every query word is a prefix of a distinct
// name word, preserving order ("krak plasz" ~ "krakow plaszow").
func prefixesInOrder(query, name []string) bool {
	j := 0
	for _, w := range name {
		if j < len(query) && strings.HasPrefix(w, query[j]) {
			j++
		}
	}
	return j == len(query)
}

var fold = map[rune]rune{
	'ą': 'a', 'ć': 'c', 'ę': 'e', 'ł': 'l', 'ń': 'n', 'ó': 'o', 'ś': 's', 'ź': 'z', 'ż': 'z',
	// Neighbouring countries show up in international routes.
	'á': 'a', 'ä': 'a', 'č': 'c', 'ď': 'd', 'é': 'e', 'ě': 'e', 'í': 'i', 'ň': 'n',
	'ö': 'o', 'ř': 'r', 'š': 's', 'ť': 't', 'ú': 'u', 'ů': 'u', 'ü': 'u', 'ý': 'y', 'ž': 'z',
}

// normalize lowercases, strips diacritics and collapses everything that isn't
// a letter or digit into single spaces.
func normalize(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if f, ok := fold[r]; ok {
			r = f
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}
