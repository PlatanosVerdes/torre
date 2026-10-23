# API

The page's own, documented so it can be used from a shortcut or `curl`. Requests from the Mini App
carry `X-Telegram-Init-Data`.

| Request | Returns |
| :--- | :--- |
| `GET /api/board?airport=BCN&dir=S` | The board. `dir=L` for arrivals, `range=full` for two weeks |
| `GET /api/airports` | AENA's airports |
| `GET /api/places` | Every airport with scheduled flights, with coordinates (OurAirports) |
| `GET /api/tracked` | Your followed flights, and whether you can follow from here |
| `POST /api/tracked` | Follow `{"airport":"BCN","direction":"S","date":"18/10/2026","number":"FR2495"}` |
| `DELETE /api/tracked/{id}` | Stop following |
| `GET /healthz` | `ok <version>` |

The page opens a route with `/?from=BCN&to=PMI`, and a flight directly with
`/?from=BCN&f=FR2495`. The older `/?a=BCN&d=S` still works (`d=L` meaning arrivals, so `to`).
Inside Telegram the start parameter `BCN_S_18102026_FR2495` opens that flight.
