// Package render draws departure boards as terminal text or HTML.
//
// Both formats share one layout: cells are measured on their visible text and
// styled afterwards, so ANSI escapes and HTML spans never break alignment.
package render

import (
	_ "embed"
	"fmt"
	"html"
	"io"
	"regexp"
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

// Text writes the board for a terminal, under the train, with ANSI colours if
// color is set.
func Text(w io.Writer, b Board, color bool) error {
	f := FormatText
	if color {
		f = FormatANSI
	}
	_, err := io.WriteString(w, train+"\n"+board(b, f, false))
	return err
}

// HTML writes the board under the train as a standalone page. It holds the
// board twice: the table, and a narrow layout that the page shows instead on
// small screens.
func HTML(w io.Writer, b Board) error {
	top := `<span class="train">` + style(FormatHTML, "", train) + "</span>\n"
	return page(w, b.Lang, b.From+" → "+b.To+" · pociag.info",
		`<pre class="wide">`+top+board(b, FormatHTML, false)+"</pre>\n"+
			`<pre class="narrow">`+top+board(b, FormatHTML, true)+"</pre>")
}

// train is the drawing shown above the help text.
//
//go:embed train.txt
var train string

// Help writes the help page: the train, a bold title and how to use the site
// from a terminal or, for HTML, from a browser.
func Help(w io.Writer, f Format, lang Lang) error {
	s := For(lang)
	if f == FormatHTML {
		link := func(text string) string {
			return exampleURL.ReplaceAllString(style(f, "", text), `<a href="${1}">${0}</a>`)
		}
		return htmlNotice(w, lang, s.HelpTitle, link(s.HelpWeb), link(option.ReplaceAllString(s.HelpWeb, "$1$2\n$1  $3")))
	}
	_, err := io.WriteString(w, style(f, "", train)+"\n"+style(f, styleBold, s.HelpTitle)+"\n\n"+style(f, "", s.Help)+footer(f, lang))
	return err
}

// exampleURL matches the example addresses in the browser help, capturing the
// path so they can be linked.
var exampleURL = regexp.MustCompile(`pociag\.info(/\S+)`)

// option matches a line of the options list: its indent, the option, and
// what it does, which the narrow layout moves under the option.
var option = regexp.MustCompile(`(?m)^( +)(\?\S+) +(.*)$`)

// Notice writes a short message, such as an error, with the credits footer.
func Notice(w io.Writer, f Format, lang Lang, msg string) error {
	if f == FormatHTML {
		return htmlNotice(w, lang, "", style(f, "", msg), style(f, "", msg))
	}
	_, err := io.WriteString(w, style(f, "", msg)+footer(f, lang))
	return err
}

// footer is the credits, a blank line below the text.
func footer(f Format, lang Lang) string {
	return "\n\n" + style(f, styleDim, For(lang).Credits) + "\n"
}

// htmlNotice writes a page with an already styled body and the credits,
// under the train and a title if there is one. Like the board, it holds the
// text twice: as in the terminal, and for small screens with the train and
// title centred and every line a block of its own, so that a line too long
// for the screen wraps and stays at its indent.
func htmlNotice(w io.Writer, lang Lang, title, wide, narrow string) error {
	f := FormatHTML
	credits := style(f, styleDim, For(lang).Credits)
	var top, narrowTop string
	if title != "" {
		t, b := style(f, "", train), style(f, styleBold, title)
		top = t + "\n" + b + "\n\n"
		narrowTop = `<span class="train">` + strings.TrimSuffix(t, "\n") + "</span>" + lines("") +
			`<span class="ln head">` + b + "</span>" + lines("")
	}
	return page(w, lang, "pociag.info",
		`<pre class="wide">`+top+wide+"\n\n"+credits+"\n</pre>\n"+
			`<pre class="narrow">`+narrowTop+lines(narrow+"\n\n"+dimLines(narrowCredits(For(lang).Credits)))+"</pre>")
}

// lines makes every line of HTML text a block, indented by its leading
// spaces, so that it wraps at that indent. Styled spans must not cross lines.
func lines(text string) string {
	var out strings.Builder
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimLeft(l, " ")
		if indent := len(l) - len(t); indent > 0 {
			fmt.Fprintf(&out, `<span class="ln" style="padding-left: %dch">%s</span>`, indent, t)
		} else {
			out.WriteString(`<span class="ln">` + t + "</span>")
		}
	}
	return out.String()
}

// dimLines dims every line of text on its own, for lines.
func dimLines(text string) string {
	ls := strings.Split(text, "\n")
	for i, l := range ls {
		ls[i] = style(FormatHTML, styleDim, l)
	}
	return strings.Join(ls, "\n")
}

// narrowCredits puts each source on a line of its own: "Dane:",
// "PKP PLK S.A.", "GTFS: …".
func narrowCredits(credits string) string {
	return strings.ReplaceAll(strings.Replace(credits, ": ", ":\n", 1), " · ", "\n")
}

type styleName string

const (
	styleBold      styleName = "bold"
	styleDim       styleName = "dim"
	styleLateSmall styleName = "late-small"
	styleLate      styleName = "late"
	styleDay       styleName = "day" // HTML only: centres the day separator on small screens
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
		if ansiCodes[st] != "" && s != "" {
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

// board lays the connections, or the journeys' legs, out as a table, or for
// narrow screens as two lines per train: platform and times, then the train
// and where it goes. A change line separates a journey's legs.
func board(b Board, f Format, narrow bool) string {
	s := For(b.Lang)
	var out strings.Builder
	now := b.Now.In(warsaw)

	title := b.From + " → " + b.To
	if len(b.Via) > 0 {
		title += ", " + s.Via + " " + strings.Join(b.Via, ", ")
	}
	head := style(f, styleBold, title) + "\n" + s.longDate(now) + ", " + now.Format("15:04") + "\n"
	if narrow && f == FormatHTML {
		head = `<span class="head">` + head + "</span>"
	}
	out.WriteString(head + "\n")

	journeys := b.Journeys
	if len(b.Via) == 0 {
		journeys = make([]timetable.Journey, len(b.Connections))
		for i, c := range b.Connections {
			journeys[i] = timetable.Journey{Legs: []timetable.Connection{c}}
		}
	}
	switch {
	case len(journeys) == 0 && len(b.Via) > 0:
		out.WriteString(style(f, "", s.NoJourneys) + "\n")
	case len(journeys) == 0:
		out.WriteString(style(f, "", s.NoConnections) + "\n")
	default:
		out.WriteString(table(journeyRows(journeys, now, s, narrow), f))
	}

	out.WriteString("\n")
	if b.LiveAt.IsZero() {
		out.WriteString(style(f, styleDim, s.NoLive) + "\n")
	} else {
		out.WriteString(style(f, styleDim, s.LiveAsOf+" "+b.LiveAt.In(warsaw).Format("15:04")) + "\n")
	}
	credits := s.Credits
	if narrow {
		credits = narrowCredits(credits)
	}
	out.WriteString(style(f, styleDim, credits) + "\n")
	return out.String()
}

// journeyRows lays out the journeys. On narrow screens every train is set
// off by a blank line; in the table only journeys with changes are, so
// direct trains read as a plain board.
func journeyRows(journeys []timetable.Journey, now time.Time, s Strings, narrow bool) []row {
	var rows []row
	if !narrow {
		rows = append(rows, row{cells: []cell{
			{{s.Departs, styleBold}}, {{s.Arrives, styleBold}}, {{s.Train, styleBold}},
			{{s.Towards, styleBold}}, {{s.Platform, styleBold}},
		}})
	}
	day := dateOf(now)
	for i, j := range journeys {
		if narrow && len(rows) > 0 || i > 0 && (len(j.Legs) > 1 || len(journeys[i-1].Legs) > 1) {
			rows = append(rows, row{})
		}
		// Journeys are ordered by departure, so heading each by the day it
		// leaves keeps the separators going forwards. A later leg on another
		// day is dated on its change line instead: a separator there would
		// put the next journey, which may leave before midnight, under it.
		legDay := day
		for k, c := range j.Legs {
			dep := c.Departure.In(warsaw)
			if k == 0 {
				if d := dateOf(dep); d != day {
					day, legDay = d, d
					sep := plain("── " + s.shortDate(dep) + " ──")
					if narrow {
						sep[0].style = styleDay
					}
					rows = append(rows, row{line: sep})
				}
			} else {
				prev := j.Legs[k-1]
				wait := int(max(c.Leaves().Sub(prev.Arrives()), 0).Round(time.Minute) / time.Minute)
				text := fmt.Sprintf(s.ChangeAt, prev.To, wait)
				if d := dateOf(dep); d != legDay {
					legDay = d
					text += " · " + s.shortDate(dep)
				}
				change := row{line: cell{{text, styleDim}}}
				if !narrow {
					change.lineCol = 1
				}
				rows = append(rows, change)
			}
			rows = append(rows, legRows(c, s, narrow)...)
		}
	}
	return rows
}

// legRows is one train: a table row, or two lines on narrow screens.
func legRows(c timetable.Connection, s Strings, narrow bool) []row {
	cells := connectionCells(c, s)
	if !narrow {
		return []row{{cells: cells}}
	}
	dep, arr, train, towards, platform := cells[0], cells[1], cells[2], cells[3], cells[4]
	if platform.width() > 0 {
		platform = plain(strings.ToLower(s.Platform) + " " + c.DeparturePlatform)
	}
	if towards.width() > 0 {
		train = append(train, segment{text: " · " + c.Destination})
	}
	return []row{
		{cells: []cell{platform, dep, append(cell{{text: "→ "}}, arr...)}},
		{line: train},
	}
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

// page writes a standalone HTML page around body.
func page(w io.Writer, lang Lang, title, body string) error {
	_, err := fmt.Fprintf(w, `<!doctype html>
<html lang="%s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { --bg: #fafafa; --fg: #1d1d1d; --dim: #6b6b6b; --warn: #a15c00; --bad: #c4211a; --btn: #f0f0f0; --line: #d0d0d0; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #111; --fg: #ddd; --dim: #888; --warn: #e5b33b; --bad: #ff6b5e; --btn: #1e1e1e; --line: #3a3a3a; }
}
body { margin: 0; padding: 16px; background: var(--bg); color: var(--fg); }
pre { margin: 0; font: 15px/1.4 ui-monospace, "DejaVu Sans Mono", Menlo, Consolas, monospace; overflow-x: auto; }
.narrow { display: none; white-space: pre-wrap; }
@media (max-width: 800px) {
  .wide { display: none; }
  .narrow { display: block; }
  /* The drawing is 33 characters, about 20em: shrink it rather than wrap it. */
  .narrow .train { display: block; width: fit-content; margin: 0 auto; white-space: pre; font-size: min(1em, calc((100vw - 32px) / 20)); }
  .narrow .head { display: block; text-align: center; }
  .narrow .day { display: inline-block; width: 100%%; text-align: center; }
  .narrow .ln { display: block; min-height: 1.4em; }
}
a { color: inherit; }
.bold { font-weight: bold; }
.dim { color: var(--dim); }
.late-small { color: var(--warn); }
.late { color: var(--bad); }
.repos { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
.repos a { display: inline-flex; align-items: center; gap: 6px; padding: 3px 10px; border: 1px solid var(--line); border-radius: 6px; background: var(--btn); font: 600 12px/20px system-ui, sans-serif; text-decoration: none; }
.repos svg { fill: currentColor; }
</style>
</head>
<body>
%s
<p class="repos">
%s
</p>
</body>
</html>
`, lang, html.EscapeString(title), body, repos)
	return err
}

// repos links to the code of the site and of the timetable feed it uses.
var repos = star("https://github.com/FabianRadomski/pociag.info", "pociag.info") + "\n" +
	star("https://github.com/MKuranowski/PolishTrainsGTFS", "PolishTrainsGTFS")

// star is a GitHub-style star button linking to a repository.
func star(url, name string) string {
	return `<a href="` + url + `"><svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">` +
		`<path d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.751.751 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25Zm0 2.445L6.615 5.5a.75.75 0 0 1-.564.41l-3.097.45 2.24 2.184a.75.75 0 0 1 .216.664l-.528 3.084 2.769-1.456a.75.75 0 0 1 .698 0l2.77 1.456-.53-3.084a.75.75 0 0 1 .216-.664l2.24-2.183-3.096-.45a.75.75 0 0 1-.564-.41L8 2.694Z"/>` +
		`</svg>` + name + `</a>`
}
