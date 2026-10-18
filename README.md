# torre

Departures and arrivals for every AENA airport on a page made for the phone, a Telegram message when
a followed flight changes time, gate or status, and a boarding pass reader that follows the flight
on it.

Runs on the Raspberry from [rpi-services](https://github.com/PlatanosVerdes/rpi-services). Go on the
server, [Lit](https://lit.dev) components on the page, CalVer tag on every push to main.

## Where the data comes from

AENA's own board, the JSON behind [aena.es/es/infovuelos.html](https://www.aena.es/es/infovuelos.html):

```
GET https://www.aena.es/sites/Satellite?pagename=AENA_ConsultarVuelos&airport=BCN&flightType=S&dosDias=si
```

`flightType` is `S` (departures) or `L` (arrivals). `dosDias=si` is a two-day window, about 3 MB for
Barcelona; without it AENA sends two weeks, six times as much, in about 16 seconds, so the page only
asks for that on "Show more days". No key, no login, no captcha.

It is **not a published API**. The URL, the fields and the status codes were read from the page's
JavaScript, and AENA can change any of them without notice. When it does, the page says AENA is not
answering and the log has what AENA sent; an HTML page where JSON was expected is reported as such
rather than parsed.

It covers AENA airports only, which means Spain. A flight between two other countries has no data.

### Choosing a route

The page asks where you fly **from** and **to**, not for a board. Since AENA only has boards for its
own airports, the route picks one: departures at `from` when it is a Spanish airport, narrowed to
`to` if set; otherwise arrivals at `to`, narrowed to `from`. "From BCN" is Barcelona's departures,
"To PMI" Palma's arrivals, "From London Heathrow to PMI" Palma's arrivals from LHR, and a route with
neither end in Spain says so.

Both fields search as you type, by code, name or city, without accents and forgiving typos. They
offer AENA's airports and every other airport with scheduled flights, from
[OurAirports](https://ourairports.com/data/) (public domain), embedded in the binary as
`internal/places/places.json` and regenerated with `go run ./cmd/genplaces`. That list is also what
names the far end of a flight in English ("London Gatwick" where AENA writes "LONDRES /GATWICK");
AENA's own name stays searchable, so "Londres" works too. Places the loaded board actually flies to
rank first: from Barcelona, "new york" offers JFK and Newark before Stewart.

A flight number in the search is matched exactly, never as a typo (FR2494 is another flight), on
every loaded day, and in either form: IATA (`FR2495`, `IB0404`) or ICAO (`RYR2495`, `IBE404`), which
is how AENA's app and many airline sites write it. If the board shown does not have it, the same
airport's other board is searched and shown below: a Barcelona → Palma flight typed while looking at
Palma's departures turns up in Palma's arrivals.

The flight search for words works the same way as the pickers. When nothing matches exactly it retries
tolerating a typo or two, and also compares names written as one word ("aireropa" finds Air Europa),
and the page says it is showing close matches.

### What the service does with it

- **Codeshares fold into one flight.** AENA lists a plane once per marketing carrier: VY3906, IB5126,
  QR3619 and LL5395 are one Vueling flight. The operating row is the one whose `codigosCompania`
  leaves its third field empty. Searching or following by any of the numbers finds the same flight.
- **Flight numbers are compared without padding.** Boarding passes write `FR 02495`, AENA writes
  `IB0404`; both go through `aena.Canonical` (`FR2495`, `IB404`) before they are compared.
- **"Departed" before its time is not trusted.** AENA sometimes marks a departure `BOR` hours before
  its new time (FR3070 on 18/10/2026: departed at 10:35, leaving at 14:20). Taken at face value that
  hides the flight and stops the watch, so the time wins: it shows as "Delayed \*" (or "On time \*"),
  and the flight sheet says AENA lists it as departed, with a link to look the flight up elsewhere.
  No airline publishes a status API that could settle it, so the page does not pretend to.

### Status codes

| Code | Shown as | Board |
| :--- | :--- | :--- |
| `SCH` | Scheduled | both |
| `INI`, `HOR` | On time | both |
| `RET` | Delayed | both |
| `EMB` | Boarding | departures |
| `ULL` | Final call | departures |
| `CER` | Gate closed | departures |
| `NPR` / `NPT` | New gate, delayed / New gate | departures |
| `FLY` | In flight | arrivals |
| `FNL` | Landing | arrivals |
| `LND` | Landed | arrivals |
| `IBK` | Baggage on belt | arrivals |
| `BOR` | Departed / Arrived | both |
| `CAN` | Cancelled | both |

An unknown code is shown as the raw code rather than hidden.

## Following a flight

Tap a flight, then "Follow". The service checks the board every `TORRE_CHECK_EVERY` and sends one
message per change: status, time with the delay, gate, terminal, belt, check-in desks. The time is
only news once it moves five minutes from what the chat was last told: AENA's estimates wobble by a
minute, and a time creeping a minute per check still adds up to a message. It sends the
current state when you start, so you know the chat is wired, and every message has an "Open in
Torre" button that opens the flight inside Telegram.

When a followed departure leaves for another AENA airport, the watch moves to that airport's arrivals
board, says so in one message ("Departed, arrives at PMI 13:47") and carries on with the landing, the
baggage belt and "Baggage on belt", the way AENA's own app follows a trip. A flight to an airport
AENA does not run ends at departure.

A flight further out than AENA publishes (about two weeks) is kept as pending and announced when it
appears. A flight is dropped six hours after it departs, lands or is cancelled, or two days after its
date if AENA never lists it again. Each chat can follow 10 at a time.

### The bot

[@TorreFlightsBot](https://t.me/TorreFlightsBot) is torre's own, so torre reads its messages
(`TORRE_BOT_UPDATES=1`, `internal/bot`):

| Message | Answer |
| :--- | :--- |
| `/start` | What Torre does, with a button that opens the page |
| `/start BCN_S_18102026_FR2495` | That flight, with a button that opens it. "Follow in Telegram" links from a browser land here |
| `/flights` | The flights the chat follows and their state |
| `/help` | What Torre does |
| A photo or file | Boarding passes go through the page, so the booking reference stays on the phone |
| **Stop following** under an alert | Stops following that flight and removes the button. Only from the chat that follows it |

On start it also sets the chat menu button to open the page and publishes the command list. Groups are
ignored, and each chat gets at most 20 answers a minute.

### Who is following

- **Inside Telegram** (the Mini App, opened from a bot button), Telegram signs the launch data with
  the bot's token and the page sends it with every request. The server checks that signature
  (`telegram.Bot.User`) and keys the flights by the user's id, so each person sees and changes only
  their own. Launch data older than 24 hours is refused.
- **In a browser**, there is no identity. With `TORRE_OWNER_CHAT` set (a private deployment) the
  browser follows for that chat; without it, the board is read-only and "Follow in Telegram" opens
  the bot instead.

The page reads Telegram's launch data from the URL fragment instead of loading
`telegram.org/js/telegram-web-app.js`, so it still loads nothing from another origin.

Telegram only lets a bot write to people who wrote to it first. If someone follows a flight without
having pressed Start, the follow is refused with that explanation rather than kept silently.

## Boarding passes

"Add boarding pass" takes a photo or screenshot of the barcode, or an Apple Wallet `.pkpass`.

The pass is decoded **on the phone** and only the flight is sent to the server. A boarding pass
barcode (IATA BCBP, Resolution 792) also holds the passenger's name and the booking reference, and
those two together open the booking at the airline, so neither leaves the page.

- **Images** are read with [zxing-wasm](https://github.com/Sec-ant/zxing-wasm) (zxing-cpp compiled to
  WebAssembly): Aztec, PDF417, QR and Data Matrix, which covers every airline's pass. It is ~1 MB, so
  it loads on the first pass rather than with the page, and it is served from the Pi.
- **Wallet passes** are a zip; the BCBP text is `pass.json`'s barcode message, unzipped in the page
  with `DecompressionStream`. No image decoding involved.
- **PDFs are not read.** Airlines draw the barcode in too many ways to turn a PDF into an image
  reliably in the browser; a screenshot of it works.

Each leg is matched to the AENA board that has it: departures at the origin when AENA runs it, else
arrivals at the destination. The pass carries the day of the year but not the year, so the nearest
date that is not in the past is taken.

## Public deployment

The page is meant to be public. What keeps it standing:

| Measure | Where |
| :--- | :--- |
| No open ports at home: the public hostname goes through a Cloudflare Tunnel, which also absorbs floods before they reach the Pi | rpi-services, once the page goes public |
| Per-address rate limits: 120 API requests a minute, 6 two-week boards a minute, 20 follows a minute | `internal/limit`, `server.Handler` |
| The visitor's address comes from `TORRE_CLIENT_IP_HEADER` (`CF-Connecting-IP` behind Cloudflare), so the limits apply per visitor, not per proxy | `server.clientIP` |
| Only AENA's 49 airports are accepted, so nobody can make the Pi ask AENA for made-up boards | `server.known` |
| AENA is asked at most once per board every `TORRE_CACHE`, shared by every visitor and the watcher; a failure is remembered for 10 seconds | `aena.Client.Board` |
| The cache holds at most 40 two-day and 4 two-week boards, and one two-week board decodes at a time (the heavy part, ~50 MiB) | `aena.Client` |
| 10 followed flights per chat, 5,000 in total | `watch.Watcher` |
| Request bodies capped at 8 KB, headers at 16 KB, read and write timeouts on the server | `main.go` |
| AENA's error text goes to the log, never to the visitor | `server.upstream` |
| Strict Content-Security-Policy (`default-src 'self'`, no inline script or style), `nosniff`, no referrer, no camera or location | `server.secure` |
| Non-root user, read-only filesystem, no capabilities, `no-new-privileges`, a process limit and a memory limit | `Dockerfile`, compose |

Memory: the usual two-day board keeps the process around 16 MiB; holding the two-week boards of BCN
and MAD at once peaked at 107 MiB.

## Privacy

- AENA sees the Pi asking for an airport's whole board, never which flight anyone looked at.
- Boarding passes are read on the phone; the server receives route, flight number and date.
- The server keeps the followed flights and the Telegram id of whoever follows each one, in
  `tracked.json` (mode 600), and deletes both when the flight is done.
- The page loads no third-party script, font or analytics, and stores the last airport in the
  phone's `localStorage`.

## Configuration

| Variable | Default | |
| :--- | :--- | :--- |
| `TORRE_PORT` | `8000` | |
| `TORRE_DATA_DIR` | `./data` (`/data` in the image) | Holds `tracked.json` |
| `TORRE_CACHE` | `30s` | How long a board is reused. 10s minimum |
| `TORRE_CHECK_EVERY` | `2m` | How often followed flights are compared. 30s minimum |
| `TORRE_TELEGRAM_TOKEN` | | The bot that sends messages and signs Mini App launches |
| `TORRE_OWNER_CHAT` | | Private deployments only: the chat a plain browser follows for. Leave empty when public |
| `TORRE_PUBLIC_URL` | | The page's address, for the "Open in Torre" button. No button when empty |
| `TORRE_BOT_NAME` | | The bot's username, for "Follow in Telegram" links from a browser |
| `TORRE_BOT_UPDATES` | | `1` makes torre answer its bot's messages. Only for a bot it has to itself: Telegram allows one reader per token |
| `TORRE_CLIENT_IP_HEADER` | | The header the proxy in front writes the visitor's address into |
| `TORRE_LOG_JSON` | `1` in the image | JSON logs, which Vector collects |
| `TORRE_AENA_URL` | `https://www.aena.es` | For tests |

## API

The page's own, documented so it can be used from a shortcut or `curl`. Requests from the Mini App
carry `X-Telegram-Init-Data`.

| Request | Returns |
| :--- | :--- |
| `GET /api/board?airport=BCN&dir=S` | The board. `dir=L` for arrivals, `range=full` for two weeks |
| `GET /api/airports` | AENA's airports |
| `GET /api/places` | Every airport with scheduled flights (OurAirports) |
| `GET /api/tracked` | Your followed flights, and whether you can follow from here |
| `POST /api/tracked` | Follow `{"airport":"BCN","direction":"S","date":"18/10/2026","number":"FR2495"}` |
| `DELETE /api/tracked/{id}` | Stop following |
| `GET /healthz` | `ok <version>` |

The page opens a route with `/?from=BCN&to=PMI`, and a flight directly with
`/?from=BCN&f=FR2495`. The older `/?a=BCN&d=S` still works (`d=L` meaning arrivals, so `to`).
Inside Telegram the start parameter `BCN_S_18102026_FR2495` opens that flight.

## Layout of the page

```
internal/server/web/
  index.html, app.css        the shell and the colour tokens, nothing else
  js/main.js                 builds the app
  js/api.js                  Api: the JSON API above
  js/telegram.js             TelegramWebApp: launch data, theme, start parameter
  js/boarding-pass.js        BoardingPass: barcode and Wallet reading, BCBP parsing
  js/format.js, styles.js    how a flight reads and looks, shared by every component
  js/components/             torre-app, airport-picker (from and to), flight-row, flight-details, pass-legs, torre-sheet
  vendor/                    Lit and zxing-wasm, pinned by version in the file name, with licences
```

Every file the page loads lives under `/static/<version>/`, where the version is a hash of the
embedded files, and only `index.html` (never cached) names it. Telegram's in-app browser keeps
scripts even when told to revalidate, and ES modules are cached by URL, so with fixed URLs a release
could run a new page on old components; with a URL per version it cannot.

Lit and zxing-wasm are vendored rather than loaded from a CDN, so the page has no third-party
origins and the Content-Security-Policy can say `'self'`. Lit is one ES module bundled from npm with
esbuild (`export * from 'lit'` plus the `repeat` and `classMap` directives); to update it, bundle the
new version the same way and change the file name.

## Development

```bash
go test ./...
TORRE_PORT=8765 TORRE_OWNER_CHAT=1 go run ./cmd/torre   # http://localhost:8765
```
