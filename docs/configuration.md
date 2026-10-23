# Configuration

| Variable | Default | |
| :--- | :--- | :--- |
| `TORRE_PORT` | `8000` | |
| `TORRE_DATA_DIR` | `./data` (`/data` in the image) | Holds `tracked.json` and `history.jsonl` |
| `TORRE_CACHE` | `30s` | How long a board is reused. 10s minimum |
| `TORRE_CHECK_EVERY` | `2m` | How often followed flights are compared. 30s minimum |
| `TORRE_TELEGRAM_TOKEN` | | The bot that sends messages and signs Mini App launches |
| `TORRE_OWNER_CHAT` | | Private deployments only: the chat a plain browser follows for. Leave empty when public |
| `TORRE_PUBLIC_URL` | | The page's address, for the "Open in Torre" button. No button when empty |
| `TORRE_BOT_NAME` | | The bot's username, for "Follow in Telegram" links from a browser |
| `TORRE_BOT_UPDATES` | | `1` makes torre answer its bot's messages. Only for a bot it has to itself: Telegram allows one reader per token |
| `TORRE_COLLECT` | | Temporary, to tune the watch: airports (`PMI,BCN`) whose every flight change goes to `collect.jsonl` |
| `TORRE_SAMPLE` | `0` | Temporary, with `TORRE_COLLECT`: random flights of those airports kept followed by an internal chat, its messages written to `sample.jsonl` instead of Telegram |
| `TORRE_STATS_ADDR` | | With the bot on, an address such as `:9180` that exposes its stats at `/bot-stats`: who uses it, its traffic and the updates the rate limit turned away (`gonana/botstats`, kept in `bot-stats.json` in the data dir). It lists names: never publish it |
| `TORRE_CLIENT_IP_HEADER` | | The header the proxy in front writes the visitor's address into |
| `TORRE_LOG_JSON` | `1` in the image | JSON logs, which Vector collects |
| `TORRE_AENA_URL` | `https://www.aena.es` | For tests |
| `TORRE_ADSBDB_URL` | `https://api.adsbdb.com` | For tests |
