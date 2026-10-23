<p align="center">
  <img src="assets/bot-avatar.svg" width="96" alt="">
</p>

<h1 align="center">torre</h1>

<p align="center">
  <a href="https://github.com/PlatanosVerdes/torre/tags"><img src="https://img.shields.io/github/v/tag/PlatanosVerdes/torre?label=release&color=2f7d3a" alt="Latest release"></a>
  <a href="https://t.me/TorreFlightsBot"><img src="https://img.shields.io/badge/telegram-@TorreFlightsBot-26a5e4?logo=telegram&logoColor=white" alt="Telegram @TorreFlightsBot"></a>
</p>

Departures and arrivals for every AENA airport on a page made for the phone, a Telegram message when
a followed flight changes time, gate or status, and a boarding pass reader that follows the flight
on it.

Runs on the Raspberry from [rpi-services](https://github.com/PlatanosVerdes/rpi-services). Go on the
server, [Lit](https://lit.dev) components on the page, CalVer tag on every push to main. The data is
AENA's own board, which is not a published API, so it covers Spain only.

## Running it

```sh
TORRE_PORT=8765 TORRE_OWNER_CHAT=1 go run ./cmd/torre   # http://localhost:8765
```

The image listens on `8000` and keeps its state in `/data`. The settings that matter:

| Variable | What it does |
| :--- | :--- |
| `TORRE_TELEGRAM_TOKEN` | The bot that sends messages and signs Mini App launches |
| `TORRE_PUBLIC_URL` | The page's address, for the "Open in Torre" button |
| `TORRE_BOT_UPDATES` | `1` makes torre answer its bot's messages |
| `TORRE_OWNER_CHAT` | Private deployments only: the chat a plain browser follows for |

## Docs

| | |
| :--- | :--- |
| [Data](docs/data.md) | Where the flights come from, routes, search, codeshares and status codes |
| [Following flights](docs/following.md) | When an alert is sent, the bot's commands and who is following |
| [Boarding passes](docs/boarding-passes.md) | How a pass is read on the phone |
| [Deployment and privacy](docs/deployment.md) | What keeps the public page standing, and what is stored |
| [Configuration](docs/configuration.md) | Every environment variable |
| [API](docs/api.md) | The page's endpoints and URL parameters |
| [Page layout](docs/page-layout.md) | The web files, versioned URLs and vendored libraries |

## Development

```sh
go test ./...
```
