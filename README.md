# torre

Departures and arrivals for every AENA airport, on a page made for the phone, and a Telegram message
when a followed flight changes time, gate or status.

Runs on the Raspberry from [rpi-services](https://github.com/PlatanosVerdes/rpi-services) at
`https://torre.platanosverdes.com`, reachable only inside the tailnet. Go, no dependencies, CalVer tag
on every push to main.

## Where the data comes from

AENA's own board, the JSON behind [aena.es/es/infovuelos.html](https://www.aena.es/es/infovuelos.html):

```
GET https://www.aena.es/sites/Satellite?pagename=AENA_ConsultarVuelos&airport=BCN&flightType=S&dosDias=si
```

`flightType` is `S` (departures) or `L` (arrivals). `dosDias=si` is a two-day window, about 3 MB for
Barcelona; without it AENA sends two weeks and six times as much, so the page only asks for that when
"Ver más días" is pressed. No key, no login, no captcha.

It is **not a published API**. The URL, the fields and the status codes were read from the page's
JavaScript, and AENA can change any of them without notice. When that happens the board shows the
error AENA returned, and an HTML page where JSON was expected is reported as such rather than parsed.

It covers AENA airports only, which means Spain. A flight from London or Rome has no data here.

### What the service does with it

- **Codeshares fold into one flight.** AENA lists a plane once per marketing carrier: VY3906, IB5126,
  QR3619 and LL5395 are one Vueling flight. The operating row is the one whose `codigosCompania`
  leaves its third field empty. Searching or following by any of the numbers finds the same flight.
- **A 30-second cache per board**, shared by every phone and by the watcher, and concurrent requests
  for the same board wait on one call to AENA. Under 10 seconds is refused at start-up.
- **"Finished" before its time is not trusted.** AENA sometimes marks a departure `BOR` hours before
  its new time (FR3070 on 18/10/2026: finished at 10:35, leaving at 14:20). Taken at face value that
  hides the flight and stops the watch, so it shows as "Finalizado (dudoso)" until the time passes.

### Status codes

| Code | Shown as | Board |
| :--- | :--- | :--- |
| `SCH` | Programado | both |
| `INI`, `HOR` | En hora | both |
| `RET` | Retrasado | both |
| `EMB` | Embarcando | departures |
| `ULL` | Última llamada | departures |
| `CER` | Puerta cerrada | departures |
| `NPR` / `NPT` | Nueva puerta, retrasado / Nueva puerta | departures |
| `FLY` | En vuelo | arrivals |
| `FNL` | Aproximación | arrivals |
| `LND` | Aterrizado | arrivals |
| `IBK` | Equipaje en cinta | arrivals |
| `BOR` | Finalizado | both |
| `CAN` | Cancelado | both |

An unknown code is shown as the raw code rather than hidden.

## Following a flight

Tap a flight, then "Seguir". The service checks the board every `TORRE_CHECK_EVERY` and sends one
message per change: status, time (with the delay), gate, terminal, belt, check-in desks. It sends the
current state when you start following, so you know the chat is wired. A flight is dropped six hours
after it departs, lands or is cancelled, or two days after its date if AENA never lists it again.

Messages go through @PlatanosServicesBot. This service only **sends**: wallapop-catalogue is the
bot's single reader of updates, and Telegram answers a second reader with 409. Without
`TORRE_TELEGRAM_TOKEN` the page still works and following only shows the flight at the top.

## Privacy

Nothing about you leaves the Pi. AENA sees the Pi asking for an airport's whole board, never which
flight you looked at. The page loads no third-party script, font or analytics, and stores the last
airport in the phone's `localStorage`. The only file the service writes is the list of followed
flights.

## Configuration

| Variable | Default | |
| :--- | :--- | :--- |
| `TORRE_PORT` | `8000` | |
| `TORRE_DATA_DIR` | `./data` (`/data` in the image) | Holds `tracked.json` |
| `TORRE_CACHE` | `30s` | How long a board is reused. 10s minimum |
| `TORRE_CHECK_EVERY` | `2m` | How often followed flights are compared. 30s minimum |
| `TORRE_TELEGRAM_TOKEN` | | The services bot's token |
| `TORRE_TELEGRAM_CHAT` | | The chat to notify |
| `TORRE_LOG_JSON` | `1` in the image | JSON logs, which Vector collects |
| `TORRE_AENA_URL` | `https://www.aena.es` | For tests |

## API

The page's own, documented here so it can be used from a shortcut or `curl`.

| Request | Returns |
| :--- | :--- |
| `GET /api/board?airport=BCN&dir=S` | The board. `dir=L` for arrivals, `range=full` for two weeks |
| `GET /api/airports` | AENA's airports |
| `GET /api/tracked` | Followed flights, and whether Telegram is configured |
| `POST /api/tracked` | Follow `{"airport":"BCN","direction":"S","date":"18/10/2026","number":"FR2495"}` |
| `DELETE /api/tracked/{id}` | Stop following |
| `GET /healthz` | `ok <version>` |

The page also opens a flight directly: `/?a=BCN&d=S&f=FR2495`.

## Development

```bash
go test ./...
TORRE_PORT=8765 go run ./cmd/torre   # http://localhost:8765
```
