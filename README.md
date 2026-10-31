# *pociag.info — the right way to ~~check~~ `curl` train times!*

pociag.info is a service that allows you to check train times in Poland, with live delays — in your
terminal or your browser.

Data from [PKP PLK S.A.](https://www.plk-sa.pl), via Mikołaj Kuranowski's
[PolishTrainsGTFS](https://github.com/MKuranowski/PolishTrainsGTFS).

Inspired by [wttr.in](https://github.com/chubin/wttr.in) and [terminal.shop](https://www.terminal.shop/).

## Usage
```
$ curl 'pociag.info/Wroclaw/Krakow?lang=en'
Wrocław Główny → Kraków Główny
Thursday, 22.10.2026, 20:27

Departs   Arrives    Train               Towards          Platform
20:53 +4  23:43      IC 73004/5 ODRA     Przemyśl Główny  3
21:30     cancelled  TLK 63100/1 PLANTY  Kraków Główny    2
── Friday, 23.10 ──
03:10     06:33 +9   IC 73010/1 URSA     Przemyśl Główny  3
05:00     08:00      BUS 4000            Kraków Główny

Live data as of 20:26
Data: PKP PLK S.A. · GTFS: Mikołaj Kuranowski
```

Open the same URLs in a browser to get an HTML page: [pociag.info](https://pociag.info/)

Where no train goes all the way, or a change gets you there sooner, journeys
with changes are listed too (`?direct` shows only direct trains):
```
$ curl 'pociag.info/Opole/Torun?lang=en&n=2'
Opole Główne → Toruń Główny
Monday, 02.11.2026, 08:00

Departs  Arrives  Train                             Towards         Platform
08:10    08:50    IC 3802/3 CHEŁMOŃSKI 132 SAXONIA  Świnoujście     1
         change in Wrocław Główny, 7 min
08:57    10:29    IC 4510/1 HEWELIUSZ               Gdynia Główna   1
         change in Poznań Główny, 17 min
10:46    12:08    IC 7514/5 GOPŁO                   Olsztyn Główny  1

09:04    11:29    IC 3804/5 MALCZEWSKI              Kołobrzeg       1
         change in Poznań Główny, 20 min
11:49    13:55    R 75316/7                         Toruń Wschodni  2
```

Add via stations to the path to go through them: `pociag.info/Opole/Poznan/Torun`.


There are a few options that modify the output:
| Option       | Meaning                                                          |
|--------------|------------------------------------------------------------------|
| `?n=5`       | number of journeys, 1–20 (default 8)                             |
| `?change=15` | minimum time to change trains, in minutes, 0–60 (default 5)      |
| `?direct`    | only trains without changes                                      |
| `?lang=en`   | English; otherwise taken from `Accept-Language`, default Polish  |
| `?T`         | plain text without colours                                       |

`curl pociag.info` prints this help.


Station names are forgiving: Polish letters are optional, words are joined
with `-`, and a bare city name means its main station (`Krakow` → Kraków Główny,
`Warszawa` → Warszawa Centralna, `Lodz` → Łódź Fabryczna).

There is no public API — the text output is meant for people, and its format
may change.

## Running it yourself

You need Go 1.27.3 (`mise install` picks it up from `mise.toml`).

```sh
go run ./cmd/pociag -user-agent 'my-pociag (you@example.com)'
curl localhost:8080/Wroclaw/Krakow
```

| Flag              | Default                                             | Meaning                                  |
|-------------------|-----------------------------------------------------|------------------------------------------|
| `-addr`           | `:8080`                                             | listen address                           |
| `-static-url`     | `https://mkuran.pl/gtfs/polish_trains.zip`          | static GTFS zip                          |
| `-realtime-url`   | `https://mkuran.pl/gtfs/polish_trains/updates.pb`   | GTFS-Realtime trip updates               |
| `-static-every`   | `30m`                                               | how often to check the static feed       |
| `-realtime-every` | `1m`                                                | how often to check the realtime feed     |
| `-user-agent`     | `pociag.info/dev`                                   | User-Agent sent to the feed servers      |
| `-data-dir`       | `data`                                              | where the downloaded static feed is kept |

Both feeds are polled with conditional requests (`If-None-Match` /
`If-Modified-Since`), so an unchanged feed costs a `304`. Please keep it that
way and set `-user-agent` to something that identifies you if you point it at
mkuran.pl.

The last static feed that loaded is kept in `-data-dir`, so a restart serves
from it straight away. On a first start the server answers `503` with
`Retry-After` until the timetable has loaded.

## Development

```sh
go test ./...
```

The tests use a small Wrocław–Kraków sample in `testdata/gtfs/wro-krk`. To also
run them against a full feed, unpack `polish_trains.zip` and set:

```sh
POCIAG_FULL_GTFS=path/to/unpacked/feed POCIAG_FULL_RT=path/to/updates.pb go test ./...
```

## Acknowledgements

- **[Mikołaj Kuranowski](https://mkuran.pl)** — pociag.info is built on his
  GTFS and GTFS-Realtime feeds of all trains in Poland, generated by
  [PolishTrainsGTFS](https://github.com/MKuranowski/PolishTrainsGTFS). Thank
  you for the feeds and for allowing pociag.info to poll them.
- **[PKP Polskie Linie Kolejowe S.A.](https://www.plk-sa.pl)** — the source of
  the timetable and live running data.
- **[wttr.in](https://github.com/chubin/wttr.in)** by Igor Chubin — the
  inspiration for this tool
## License

TODO
