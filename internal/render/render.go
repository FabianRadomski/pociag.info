// Package render draws departure boards as terminal text or HTML.
//
// Both formats share one layout: cells are measured on their visible text and
// styled afterwards, so ANSI escapes and HTML spans never break alignment.
package render

import (
	"fmt"
	"html"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"pociag.info/internal/timetable"
)

type Lang string

const (
	PL Lang = "pl"
	EN Lang = "en"
)

type Format int

const (
	FormatText Format = iota
	FormatANSI
	FormatHTML
)

// Board is everything shown on one page.
type Board struct {
	Lang        Lang
	From, To    string
	Now         time.Time
	LiveAt      time.Time // zero when there is no realtime data
	Connections []timetable.Connection

	// Via stations, in order; when set, Journeys are shown instead of
	// Connections.
	Via      []string
	Journeys []timetable.Journey
}

// Text writes the board for a terminal, with ANSI colours if color is set.
func Text(w io.Writer, b Board, color bool) error {
	f := FormatText
	if color {
		f = FormatANSI
	}
	_, err := io.WriteString(w, board(b, f))
	return err
}

// HTML writes the board as a standalone page.
func HTML(w io.Writer, b Board) error {
	return page(w, b.Lang, b.From+" → "+b.To+" · pociag.info", board(b, FormatHTML))
}

// Notice writes a short message (an error or help) with the credits footer.
func Notice(w io.Writer, f Format, lang Lang, msg string) error {
	body := style(f, "", msg) + "\n\n" + style(f, styleDim, For(lang).Credits) + "\n"
	if f == FormatHTML {
		return page(w, lang, "pociag.info", body)
	}
	_, err := io.WriteString(w, body)
	return err
}

type styleName string

const (
	styleBold      styleName = "bold"
	styleDim       styleName = "dim"
	styleLateSmall styleName = "late-small"
	styleLate      styleName = "late"
)

var ansiCodes = map[styleName]string{
	styleBold:      "1",
	styleDim:       "2",
	styleLateSmall: "33",
	styleLate:      "31",
}

// style escapes s for the format and applies a style to it.
func style(f Format, st styleName, s string) string {
	switch f {
	case FormatHTML:
		s = html.EscapeString(s)
		if st != "" && s != "" {
			s = `<span class="` + string(st) + `">` + s + `</span>`
		}
	case FormatANSI:
		if st != "" && s != "" {
			s = "\x1b[" + ansiCodes[st] + "m" + s + "\x1b[0m"
		}
	}
	return s
}

// segment is a piece of a cell with one style.
type segment struct {
	text  string
	style styleName
}

type cell []segment

func (c cell) width() int {
	n := 0
	for _, s := range c {
		n += utf8.RuneCountInString(s.text)
	}
	return n
}

func (c cell) render(f Format) string {
	var sb strings.Builder
	for _, s := range c {
		sb.WriteString(style(f, s.style, s.text))
	}
	return sb.String()
}

func plain(s string) cell { return cell{{text: s}} }

// row is either a table row or a line such as a day separator, which starts
// at column lineCol and runs to the end.
type row struct {
	cells   []cell
	line    cell
	lineCol int
}

func board(b Board, f Format) string {
	s := For(b.Lang)
	var out strings.Builder
	now := b.Now.In(warsaw)

	title := b.From + " → " + b.To
	if len(b.Via) > 0 {
		title += ", " + s.Via + " " + strings.Join(b.Via, ", ")
	}
	out.WriteString(style(f, styleBold, title) + "\n")
	out.WriteString(s.longDate(now) + ", " + now.Format("15:04") + "\n\n")

	switch {
	case len(b.Via) > 0 && len(b.Journeys) == 0:
		out.WriteString(style(f, "", s.NoJourneys) + "\n")
	case len(b.Via) > 0:
		out.WriteString(table(journeyRows(b.Journeys, now, s), f))
	case len(b.Connections) == 0:
		out.WriteString(style(f, "", s.NoConnections) + "\n")
	default:
		rows := []row{headerRow(s)}
		day := dateOf(now)
		for _, c := range b.Connections {
			dep := c.Departure.In(warsaw)
			if d := dateOf(dep); d != day {
				day = d
				rows = append(rows, row{line: plain("── " + s.shortDate(dep) + " ──")})
			}
			rows = append(rows, row{cells: connectionCells(c, s)})
		}
		out.WriteString(table(rows, f))
	}

	out.WriteString("\n")
	if b.LiveAt.IsZero() {
		out.WriteString(style(f, styleDim, s.NoLive) + "\n")
	} else {
		out.WriteString(style(f, styleDim, s.LiveAsOf+" "+b.LiveAt.In(warsaw).Format("15:04")) + "\n")
	}
	out.WriteString(style(f, styleDim, s.Credits) + "\n")
	return out.String()
}

func headerRow(s Strings) row {
	return row{cells: []cell{
		{{s.Departs, styleBold}}, {{s.Arrives, styleBold}}, {{s.Train, styleBold}},
		{{s.Towards, styleBold}}, {{s.Platform, styleBold}},
	}}
}

// journeyRows lists each journey's legs with a change line between them.
// A blank line sets off journeys with changes; through trains read like the
// direct board.
func journeyRows(journeys []timetable.Journey, now time.Time, s Strings) []row {
	rows := []row{headerRow(s)}
	day := dateOf(now)
	for i, j := range journeys {
		if i > 0 && (len(j.Legs) > 1 || len(journeys[i-1].Legs) > 1) {
			rows = append(rows, row{line: cell{}})
		}
		dep := j.Legs[0].Departure.In(warsaw)
		if d := dateOf(dep); d != day {
			day = d
			rows = append(rows, row{line: plain("── " + s.shortDate(dep) + " ──")})
		}
		for k, c := range j.Legs {
			if k > 0 {
				prev := j.Legs[k-1]
				wait := int(max(c.Leaves().Sub(prev.Arrives()), 0).Round(time.Minute) / time.Minute)
				rows = append(rows, row{line: cell{{fmt.Sprintf(s.ChangeAt, prev.To, wait), styleDim}}, lineCol: 1})
			}
			rows = append(rows, row{cells: connectionCells(c, s)})
		}
	}
	return rows
}

func connectionCells(c timetable.Connection, s Strings) []cell {
	dep := cell{{text: c.Departure.In(warsaw).Format("15:04")}}
	arr := cell{{text: c.Arrival.In(warsaw).Format("15:04")}}
	if l := c.Live; l != nil {
		if l.Cancelled {
			arr = cell{{s.Cancelled, styleLate}}
		} else {
			dep = append(dep, delay(l.Departure.Time, c.Departure)...)
			arr = append(arr, delay(l.Arrival.Time, c.Arrival)...)
		}
	}
	train := strings.Join(nonEmpty(c.Category, c.Number, c.Name), " ")
	return []cell{dep, arr, plain(train), plain(c.Destination), plain(c.DeparturePlatform)}
}

// delay renders " +N" for a late estimate; nothing when on time or unknown.
func delay(estimate, scheduled time.Time) cell {
	if estimate.IsZero() {
		return nil
	}
	m := int(estimate.Sub(scheduled).Round(time.Minute) / time.Minute)
	switch {
	case m <= 0:
		return nil
	case m <= 5:
		return cell{{" ", ""}, {fmt.Sprintf("+%d", m), styleLateSmall}}
	default:
		return cell{{" ", ""}, {fmt.Sprintf("+%d", m), styleLate}}
	}
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// table pads every column to its widest cell, two spaces apart.
func table(rows []row, f Format) string {
	var widths []int
	for _, r := range rows {
		for i, c := range r.cells {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], c.width())
		}
	}
	var out strings.Builder
	for _, r := range rows {
		if r.cells == nil {
			indent := 0
			for _, w := range widths[:min(r.lineCol, len(widths))] {
				indent += w + 2
			}
			if r.line.width() > 0 {
				out.WriteString(strings.Repeat(" ", indent))
			}
			out.WriteString(r.line.render(f) + "\n")
			continue
		}
		var line strings.Builder
		pending := 0 // padding is only written before visible text, so lines never end in spaces
		for i, c := range r.cells {
			if c.width() > 0 {
				line.WriteString(strings.Repeat(" ", pending))
				line.WriteString(c.render(f))
				pending = 0
			}
			pending += widths[i] - c.width() + 2
		}
		out.WriteString(line.String() + "\n")
	}
	return out.String()
}

type date struct {
	y int
	m time.Month
	d int
}

func dateOf(t time.Time) date {
	y, m, d := t.Date()
	return date{y, m, d}
}

func page(w io.Writer, lang Lang, title, pre string) error {
	_, err := fmt.Fprintf(w, `<!doctype html>
<html lang="%s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { --bg: #fafafa; --fg: #1d1d1d; --dim: #6b6b6b; --warn: #a15c00; --bad: #c4211a; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #111; --fg: #ddd; --dim: #888; --warn: #e5b33b; --bad: #ff6b5e; }
}
body { margin: 0; padding: 16px; background: var(--bg); color: var(--fg); }
pre { margin: 0; font: 15px/1.4 ui-monospace, "DejaVu Sans Mono", Menlo, Consolas, monospace; overflow-x: auto; }
.bold { font-weight: bold; }
.dim { color: var(--dim); }
.late-small { color: var(--warn); }
.late { color: var(--bad); }
</style>
</head>
<body>
<pre>%s</pre>
</body>
</html>
`, lang, html.EscapeString(title), pre)
	return err
}
