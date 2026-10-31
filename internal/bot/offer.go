package bot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PlatanosVerdes/torre/internal/adsbdb"
	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/places"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type Routes interface {
	Lookup(ctx context.Context, callsign string) (adsbdb.Route, bool)
}

// flightText is a flight number typed on its own, optionally with an airport: "VY3919", "vy 3919", "VY3919 PMI".
var flightText = regexp.MustCompile(`^([A-Z][A-Z0-9]|[0-9][A-Z]|[A-Z]{3}) ?([0-9]{1,4}[A-Z]?)(?: +([A-Z]{3}))?$`)

// followData is a "Follow" button's callback: f:PMI:S:19102026:VY3919:2225. Buttons sent before
// the time was added have none.
var followData = regexp.MustCompile(`^f:([A-Z]{3}):([SL]):(\d{2})(\d{2})(\d{4}):([A-Z0-9]{3,8})(?::(\d{2})(\d{2}))?$`)

const declineData = "n"

type board struct {
	airport string
	dir     aena.Direction
}

func parseFlight(text string) (number, airport string, ok bool) {
	m := flightText.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(text)))
	if m == nil {
		return "", "", false
	}
	return aena.Canonical(m[1] + m[2]), m[3], true
}

// offer finds a typed flight on today's and tomorrow's AENA boards and offers to follow it.
func (b *Bot) offer(ctx context.Context, number, airport string) (string, telegram.Keys) {
	example := "PMI"
	var airline string
	var boards []board
	switch {
	case airport != "":
		if !b.spanish(airport) {
			return fmt.Sprintf("%s is not an AENA airport. Send the flight with its Spanish end, like <code>%s PMI</code>.", airport, number), telegram.Keys{}
		}
		example = airport
		boards = append(boards, board{airport, aena.Departures}, board{airport, aena.Arrivals})
	case b.Routes != nil:
		r, ok := b.Routes.Lookup(ctx, number)
		if !ok {
			break
		}
		airline = r.Airline
		if b.spanish(r.Origin) {
			example = r.Origin
			boards = append(boards, board{r.Origin, aena.Departures})
		} else if b.spanish(r.Destination) {
			example = r.Destination
			boards = append(boards, board{r.Destination, aena.Arrivals})
		}
	}

	now := b.clock().In(b.zone())
	days := []time.Time{now, now.AddDate(0, 0, 1)}
	var found []aena.Flight
	for _, at := range boards {
		flights, _, err := b.Boards.Board(ctx, at.airport, at.dir, true)
		if err != nil {
			b.Log.Warn("board unavailable", "airport", at.airport, "err", err)
			return "AENA is not answering right now, try again in a minute.", telegram.Keys{}
		}
		for _, d := range days {
			for _, f := range watch.FindAll(flights, d.Format("02/01/2006"), number) {
				found = append(found, *f)
			}
		}
		if len(found) > 0 {
			break
		}
	}
	if len(found) == 0 {
		where := ""
		if airport != "" {
			where = " at " + airport
		}
		return fmt.Sprintf("I couldn't find %s%s today or tomorrow. Send it with the airport, like <code>%s %s</code>.", number, where, number, example), telegram.Keys{}
	}

	f := found[0]
	// adsbdb writes the name better than AENA, but only for the number it was asked about.
	if airline == "" || aena.Canonical(f.Number) != number {
		airline = f.Airline
	}
	head := "<b>" + esc(f.Number) + "</b>"
	if airline != "" {
		head += " · " + esc(airline)
	}
	from, to := f.Airport, f.Other
	if f.Direction == aena.Arrivals {
		from, to = to, from
	}
	// A codeshare number can fly another route on another row (QR3652 into BCN from NAP and BIO).
	sameRoute := true
	for _, o := range found {
		sameRoute = sameRoute && o.Other == f.Other
	}
	lines := []string{head}
	if sameRoute {
		lines = append(lines, place(from)+" → "+place(to))
	}
	twice := map[string]bool{}
	seen := map[string]bool{}
	for _, o := range found {
		twice[o.Date] = seen[o.Date]
		seen[o.Date] = true
	}
	var keys telegram.Keys
	for _, f := range found {
		day := "today"
		if f.Date != days[0].Format("02/01/2006") {
			day = "tomorrow"
		}
		line := when(f) + " · " + day + " " + f.Date[:5] + " · " + esc(f.StatusLabel)
		if !sameRoute {
			line = route(f) + " · " + line
		}
		lines = append(lines, line)
		if len(found) == 1 {
			if where := gate(f); where != "" {
				lines = append(lines, where)
			}
		}
		text := "Follow this flight"
		if len(found) > 1 {
			text = "Follow " + day + " " + f.Date[:5]
			if twice[f.Date] {
				text += " " + f.Scheduled
			}
		}
		data := strings.Join([]string{"f", f.Airport, string(f.Direction), strings.ReplaceAll(f.Date, "/", ""), aena.Canonical(f.Number), strings.ReplaceAll(f.Scheduled, ":", "")}, ":")
		keys.Choices = append(keys.Choices, telegram.Choice{Text: text, Data: data})
	}
	keys.Choices = append(keys.Choices, telegram.Choice{Text: "No", Data: declineData})
	return strings.Join(lines, "\n"), keys
}

// follow answers a "Follow" or "No" button under an offer and reports whether the data was one.
func (b *Bot) follow(ctx context.Context, chat string, c *telegram.Callback) bool {
	if c.Data == declineData {
		_ = b.TG.Answer(ctx, c.ID, "")
		if err := b.TG.SetKeys(ctx, chat, c.Message.ID, telegram.Keys{}); err != nil {
			b.Log.Warn("edit keys failed", "err", err)
		}
		return true
	}
	m := followData.FindStringSubmatch(c.Data)
	if m == nil {
		return false
	}
	if !b.spanish(m[1]) {
		_ = b.TG.Answer(ctx, c.ID, "Not an AENA airport")
		return true
	}
	scheduled := ""
	if m[7] != "" {
		scheduled = m[7] + ":" + m[8]
	}
	_, err := b.Watch.Add(ctx, chat, m[1], aena.Direction(m[2]), m[3]+"/"+m[4]+"/"+m[5], m[6], scheduled)
	notice := "Following " + m[6]
	switch {
	case errors.Is(err, watch.ErrNotFound):
		notice = m[6] + " is no longer on AENA's board"
	case errors.Is(err, watch.ErrTooMany):
		notice = "You already follow as many flights as allowed"
	case errors.Is(err, telegram.ErrBlocked):
		notice = "Press Start first, so the bot can message you"
	case err != nil:
		b.Log.Warn("follow failed", "err", err)
		notice = "Could not follow it, try again"
	}
	_ = b.TG.Answer(ctx, c.ID, notice)
	if err == nil {
		if err := b.TG.SetKeys(ctx, chat, c.Message.ID, telegram.Keys{}); err != nil {
			b.Log.Warn("edit keys failed", "err", err)
		}
	}
	return true
}

func (b *Bot) spanish(code string) bool {
	return b.Spanish != nil && b.Spanish(code)
}

func (b *Bot) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *Bot) zone() *time.Location {
	if b.Zone != nil {
		return b.Zone
	}
	return time.UTC
}

func place(code string) string {
	if p, ok := places.Lookup(code); ok && p.City != "" {
		return esc(p.City) + " (" + esc(code) + ")"
	}
	return esc(code)
}

// when reads like watch.Describe's time line.
func when(f aena.Flight) string {
	verb := "Departs"
	if f.Direction == aena.Arrivals {
		verb = "Arrives"
	}
	if f.Estimated != "" {
		return fmt.Sprintf("%s %s, now expected <b>%s</b> (%+d min)", verb, f.Scheduled, watch.Time(f), f.DelayMin)
	}
	return verb + " " + f.Scheduled
}

func route(f aena.Flight) string {
	if f.Direction == aena.Arrivals {
		return place(f.Other) + " → " + place(f.Airport)
	}
	return place(f.Airport) + " → " + place(f.Other)
}

func gate(f aena.Flight) string {
	var parts []string
	if f.Terminal != "" {
		parts = append(parts, "Terminal "+esc(f.Terminal))
	}
	if f.Gate != "" {
		parts = append(parts, "Gate <b>"+esc(f.Gate)+"</b>")
	} else if f.Zone != "" {
		parts = append(parts, "Gate zone <b>"+esc(f.Zone)+"</b>")
	}
	return strings.Join(parts, " · ")
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
