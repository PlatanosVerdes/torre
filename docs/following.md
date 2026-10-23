# Following flights and the bot

## Following a flight

Tap a flight, then "Follow". The service checks the board every `TORRE_CHECK_EVERY` and sends one
message per change: status, time with the delay, gate, terminal, belt, check-in desks.

A change is sent once it has stood for ten minutes. AENA's board flickers: a gate given, taken away
and given back within minutes, a delay revised twice in a quarter of an hour. Held for ten minutes,
the flicker sends nothing and the final value sends one message. What cannot wait goes at once:
boarding, final call, gate closed, departed, cancelled, diverted, landed, baggage on the belt, a
belt assigned, and a new gate within an hour of departure, when there is a walk to make. A gate taken
away is never sent on its own, since it tells nobody where to go. The time is only news once it
moves ten minutes from what the chat was last told, so a time creeping a minute per check still adds
up to a message.

It sends the current state when you start, so you know the chat is wired, and every message has an
"Open in Torre" button that opens the flight inside Telegram.

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
| `VY3919`, `vy 3919`, `VY3919 PMI` | The flight today and tomorrow, with **Follow this flight** (one button per date when both have it) and **No** |

A flight number typed on its own (airline code, up to four digits, an optional letter and an optional
airport) is looked up on one AENA board: with an airport, that airport's departures and then its
arrivals; without one, the route comes from [adsbdb](https://www.adsbdb.com) (free, no key) and the
board is the origin's departures when AENA runs it, else the destination's arrivals. adsbdb's route
data may not be copied into another database, so torre asks it live and keeps answers only in memory
for a few hours; when it does not know the number or is rate limiting, the bot asks for the airport.

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
