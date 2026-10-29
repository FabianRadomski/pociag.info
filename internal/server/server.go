// Package server serves departure boards over HTTP: text for terminals,
// HTML for browsers, wttr.in-style.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pociag.info/internal/feeds"
	"pociag.info/internal/realtime"
	"pociag.info/internal/render"
	"pociag.info/internal/stations"
)

const (
	defaultCount = 8
	maxCount     = 20
	// Older realtime data is worse than none: it would show stale delays.
	maxSnapshotAge = 30 * time.Minute
)

// Data supplies the current feeds; *feeds.Store implements it.
type Data interface {
	Static() *feeds.Static
	Snapshot() *realtime.Snapshot
}

type Server struct {
	Data Data
	Now  func() time.Time // nil means time.Now
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	if path == "favicon.ico" || path == "robots.txt" {
		http.NotFound(w, r)
		return
	}

	lang := language(r)
	format := format(r)
	str := render.For(lang)

	var parts []string
	if path != "" && path != ":help" {
		parts = strings.Split(path, "/")
	}
	switch len(parts) {
	case 0:
		s.notice(w, http.StatusOK, format, lang, str.Help)
		return
	case 2:
	default:
		s.notice(w, http.StatusBadRequest, format, lang, str.NeedTwoStations)
		return
	}

	static := s.Data.Static()
	if static == nil {
		// At startup and for a few seconds when a new timetable is parsed.
		w.Header().Set("Retry-After", "5")
		s.notice(w, http.StatusServiceUnavailable, format, lang, str.Loading)
		return
	}
	var resolved [2]stations.Station
	for i, q := range parts {
		st, err := static.Stations.Resolve(q)
		if errors.Is(err, stations.ErrNotFound) {
			s.notice(w, http.StatusNotFound, format, lang, fmt.Sprintf(str.StationNotFound, q))
			return
		}
		resolved[i] = st
	}
	from, to := resolved[0], resolved[1]
	if from.ID == to.ID {
		s.notice(w, http.StatusBadRequest, format, lang, fmt.Sprintf(str.SameStation, from.Name))
		return
	}

	now := s.now()
	snap := s.Data.Snapshot()
	if snap != nil && now.Sub(snap.Timestamp) > maxSnapshotAge {
		snap = nil
	}
	board := render.Board{
		Lang:        lang,
		From:        from.Name,
		To:          to.Name,
		Now:         now,
		Connections: static.Timetable.Live(from.ID, to.ID, now, count(r), snap),
	}
	if snap != nil {
		board.LiveAt = snap.Timestamp
	}

	setHeaders(w, format)
	w.WriteHeader(http.StatusOK)
	var err error
	if format == render.FormatHTML {
		err = render.HTML(w, board)
	} else {
		err = render.Text(w, board, format == render.FormatANSI)
	}
	if err != nil {
		slog.Debug("write board", "err", err)
	}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) notice(w http.ResponseWriter, status int, f render.Format, lang render.Lang, msg string) {
	setHeaders(w, f)
	w.WriteHeader(status)
	if err := render.Notice(w, f, lang, msg); err != nil {
		slog.Debug("write notice", "err", err)
	}
}

func setHeaders(w http.ResponseWriter, f render.Format) {
	if f == render.FormatHTML {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// terminalAgents are User-Agent substrings of command-line clients, which
// get text; everything else is treated as a browser.
var terminalAgents = []string{"curl", "wget", "httpie", "powershell", "libfetch", "aria2"}

func format(r *http.Request) render.Format {
	ua := strings.ToLower(r.UserAgent())
	for _, a := range terminalAgents {
		if strings.Contains(ua, a) {
			if r.URL.Query().Has("T") {
				return render.FormatText
			}
			return render.FormatANSI
		}
	}
	return render.FormatHTML
}

// language picks ?lang= if valid, else the first supported Accept-Language,
// else Polish.
func language(r *http.Request) render.Lang {
	switch l := render.Lang(r.URL.Query().Get("lang")); l {
	case render.PL, render.EN:
		return l
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		primary, _, _ := strings.Cut(strings.ToLower(tag), "-")
		switch l := render.Lang(primary); l {
		case render.PL, render.EN:
			return l
		}
	}
	return render.PL
}

func count(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("n"))
	if err != nil || n < 1 {
		return defaultCount
	}
	return min(n, maxCount)
}
