# Public deployment and privacy

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
| Strict Content-Security-Policy (`default-src 'self'`, no inline script or style), `nosniff`, no referrer, no camera; location for the page itself only | `server.secure` |
| Non-root user, read-only filesystem, no capabilities, `no-new-privileges`, a process limit and a memory limit | `Dockerfile`, compose |

Memory: the usual two-day board keeps the process around 16 MiB; holding the two-week boards of BCN
and MAD at once peaked at 107 MiB.

## Privacy

- AENA sees the Pi asking for an airport's whole board, never which flight anyone looked at.
- Boarding passes are read on the phone; the server receives route, flight number and date.
- The server keeps the followed flights and the Telegram id of whoever follows each one, in
  `tracked.json` (mode 600), and deletes both when the flight is done.
- Each change of a followed flight's status, expected time, gate or belt is appended to
  `history.jsonl` (mode 600): flight, route, those values, and when torre saw it. No chat id; it is there to learn how long
  routes really take, since AENA drops a flight soon after it is done.
- The page loads no third-party script, font or analytics, and stores the last route in the
  phone's `localStorage`.
- There is no default airport. On a first visit with no route the page asks for the phone's
  location once, finds the nearest Spanish airport within 200 km **on the phone**, and starts there.
  The position is never sent anywhere; refusing, an error or being abroad leaves the route empty.
  The location button in the From field asks again at any time, and says why when it cannot.
  Airport coordinates come with the OurAirports list.
