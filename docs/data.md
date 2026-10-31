# Where the data comes from

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

## Choosing a route

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

## What the service does with it

- **Codeshares fold into one flight, under the airline that flies it.** AENA lists a plane once per
  airline selling it (VY3919, IB5139, QR3755 and LL5408 are one Vueling flight) and nothing in the
  rows says which one operates: `codigosCompania` looked like it did at Barcelona and points the
  other way at Palma. Numbering does tell: an airline flies its own planes under some hundred-number
  series and sells seats on partners' under others (Iberia flies IB04xx, sells IB51xx on Vueling;
  Qatar never flies alone from Spain). The operator is the one airline whose number falls in a
  series it has been seen flying alone in. That knowledge starts from `internal/aena/series.json`,
  learnt from every AENA airport with `go run ./cmd/genseries`, and grows with every board fetched.
  When no airline or several qualify, AENA's first row stays, which is a guess. Air Nostrum flies as
  Iberia (IB2263, with VY5663 sold on it), and the series get that right too.
  Searching or following by any of the numbers finds the same flight.
- **Flight numbers are compared without padding.** Boarding passes write `FR 02495`, AENA writes
  `IB0404`; both go through `aena.Canonical` (`FR2495`, `IB404`) before they are compared.
- **"Departed" before its time is not trusted.** AENA sometimes marks a departure `BOR` hours before
  its new time (FR3070 on 18/10/2026: departed at 10:35, leaving at 14:20). Taken at face value that
  hides the flight and stops the watch, so the time wins: it shows as "Delayed \*" (or "On time \*"),
  and the flight sheet says AENA lists it as departed, with a link to look the flight up elsewhere.
  No airline publishes a status API that could settle it, so the page does not pretend to.
- **A departure's gate zone comes before its gate.** Barcelona fills `puertaSegunda` with the zone
  letters while `puertaPrimera` is still empty (`RSU`, `WY`, `A`, `D`), often two days ahead. It shows
  as gate "R/S/U" until the gate arrives. `A` covers the A, B and C gates, `D` the D and E gates.
  `salaPrimera` is the arrivals baggage hall, never a departure zone.

## Status codes

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
