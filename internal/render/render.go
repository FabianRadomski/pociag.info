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
	usage := style(f, "", s.Help)
	if f == FormatHTML {
		usage = exampleURL.ReplaceAllString(style(f, "", s.HelpWeb), `<a href="${1}">${0}</a>`)
	}
	return notice(w, f, lang, style(f, "", train)+"\n"+style(f, styleBold, s.HelpTitle)+"\n\n"+usage)
}

// exampleURL matches the example addresses in the browser help, capturing the
// path so they can be linked.
var exampleURL = regexp.MustCompile(`pociag\.info(/\S+)`)

// Notice writes a short message, such as an error, with the credits footer.
func Notice(w io.Writer, f Format, lang Lang, msg string) error {
	return notice(w, f, lang, style(f, "", msg))
}

// notice writes an already styled body with the credits footer.
func notice(w io.Writer, f Format, lang Lang, body string) error {
	body += "\n\n" + style(f, styleDim, For(lang).Credits) + "\n"
	if f == FormatHTML {
		return page(w, lang, "pociag.info", "<pre>"+body+"</pre>")
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

// row is either a table row or a full-width line such as a day separator.
type row struct {
	cells []cell
	line  cell
}

// board lays the connections out as a table, or for narrow screens as two
// lines per train: platform and times, then the train and where it goes.
func board(b Board, f Format, narrow bool) string {
	s := For(b.Lang)
	var out strings.Builder
	now := b.Now.In(warsaw)

	head := style(f, styleBold, b.From+" → "+b.To) + "\n" + s.longDate(now) + ", " + now.Format("15:04") + "\n"
	if narrow && f == FormatHTML {
		head = `<span class="head">` + head + "</span>"
	}
	out.WriteString(head + "\n")

	if len(b.Connections) == 0 {
		out.WriteString(style(f, "", s.NoConnections) + "\n")
	} else {
		var rows []row
		if !narrow {
			rows = append(rows, row{cells: []cell{
				{{s.Departs, styleBold}}, {{s.Arrives, styleBold}}, {{s.Train, styleBold}},
				{{s.Towards, styleBold}}, {{s.Platform, styleBold}},
			}})
		}
		day := dateOf(now)
		for _, c := range b.Connections {
			if narrow && len(rows) > 0 {
				rows = append(rows, row{})
			}
			dep := c.Departure.In(warsaw)
			if d := dateOf(dep); d != day {
				day = d
				sep := plain("── " + s.shortDate(dep) + " ──")
				if narrow {
					sep[0].style = styleDay
				}
				rows = append(rows, row{line: sep})
			}
			cells := connectionCells(c, s)
			if narrow {
				dep, arr, train, towards, platform := cells[0], cells[1], cells[2], cells[3], cells[4]
				if platform.width() > 0 {
					platform = plain(strings.ToLower(s.Platform) + " " + c.DeparturePlatform)
				}
				if towards.width() > 0 {
					train = append(train, segment{text: " · " + c.Destination})
				}
				rows = append(rows,
					row{cells: []cell{platform, dep, append(cell{{text: "→ "}}, arr...)}},
					row{line: train})
			} else {
				rows = append(rows, row{cells: cells})
			}
		}
		out.WriteString(table(rows, f))
	}

	out.WriteString("\n")
	if b.LiveAt.IsZero() {
		out.WriteString(style(f, styleDim, s.NoLive) + "\n")
	} else {
		out.WriteString(style(f, styleDim, s.LiveAsOf+" "+b.LiveAt.In(warsaw).Format("15:04")) + "\n")
	}
	credits := s.Credits
	if narrow {
		// One source per line: "Dane:", "PKP PLK S.A.", "GTFS: …".
		credits = strings.ReplaceAll(strings.Replace(credits, ": ", ":\n", 1), " · ", "\n")
	}
	out.WriteString(style(f, styleDim, credits) + "\n")
	return out.String()
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
  .narrow .train { display: block; width: fit-content; margin: 0 auto; }
  .narrow .head { display: block; text-align: center; }
  .narrow .day { display: inline-block; width: 100%%; text-align: center; }
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
