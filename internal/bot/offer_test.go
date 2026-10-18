package bot

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/adsbdb"
	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type boards struct {
	by    map[string][]aena.Flight
	asked []string
}

func (b *boards) Board(_ context.Context, airport string, dir aena.Direction, _ bool) ([]aena.Flight, time.Time, error) {
	b.asked = append(b.asked, airport+"/"+string(dir))
	return b.by[airport+"/"+string(dir)], time.Now(), nil
}

func day(offset int) string { return time.Now().UTC().AddDate(0, 0, offset).Format("02/01/2006") }

func vueling(date string) aena.Flight {
	return aena.Flight{ID: date + "-VY3919", Number: "VY3919", Codeshare: []string{"IB5139"}, Airline: "Vueling Airlines",
		Date: date, Airport: "PMI", Other: "BCN", Direction: aena.Departures, Scheduled: "23:18",
		StatusLabel: "On time", Terminal: "T1"}
}

// adsbdbServer answers VY3919 with PMI → BCN, and anything else as unknown or with status.
func adsbdbServer(t *testing.T, status int) *adsbdb.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.URL.Path != "/v0/callsign/VY3919" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"response":"unknown callsign"}`)
			return
		}
		_, _ = io.WriteString(w, `{"response":{"flightroute":{"airline":{"name":"Vueling Airlines"},"origin":{"iata_code":"PMI"},"destination":{"iata_code":"BCN"}}}}`)
	}))
	t.Cleanup(srv.Close)
	return adsbdb.New(srv.URL)
}

func offerBot(t *testing.T, by map[string][]aena.Flight, status int) (*Bot, *fakeTG, *boards) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bs := &boards{by: by}
	w, err := watch.New(t.TempDir(), bs, &fakeTG{}, time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	spanish := func(code string) bool { return code == "PMI" || code == "BCN" }
	w.Spanish = spanish
	tg := &fakeTG{}
	b := &Bot{TG: tg, Watch: w, BaseURL: "https://torre.example", Log: log,
		Routes: adsbdbServer(t, status), Boards: bs, Spanish: spanish, Zone: time.UTC}
	return b, tg, bs
}

func TestParseFlight(t *testing.T) {
	for in, want := range map[string]string{
		"VY3919": "VY3919 ", "vy 3919": "VY3919 ", "FR2495": "FR2495 ", "VY3919 PMI": "VY3919 PMI",
		"ib0404": "IB404 ", "VLG3919": "VLG3919 ", "U2 8001": "U28001 ", "6E12a bcn": "6E12A BCN",
	} {
		n, a, ok := parseFlight(in)
		if !ok || n+" "+a != want {
			t.Errorf("%q read as %q %q %v, want %q", in, n, a, ok, want)
		}
	}
	for _, in := range []string{"hello", "1234", "VY", "VY39190", "VY3919 PALMA", "/start VY3919", "VY3919 PMI BCN", "12 34", ""} {
		if n, a, ok := parseFlight(in); ok {
			t.Errorf("%q read as a flight: %q %q", in, n, a)
		}
	}
}

func TestOfferFoundThroughTheRouteAndTheBoard(t *testing.T) {
	f := vueling(day(0))
	f.Estimated, f.DelayMin, f.StatusLabel, f.Gate = "23:50", 32, "Delayed", "D84"
	b, tg, bs := offerBot(t, map[string][]aena.Flight{"PMI/S": {f}}, 0)
	b.Handle(context.Background(), msg("vy 3919"))

	got := tg.sent[0]
	want := "<b>VY3919</b> · Vueling Airlines\nPalma de Mallorca (PMI) → Barcelona (BCN)\n" +
		"Departs 23:18, now expected <b>23:50</b> (+32 min) · today " + day(0)[:5] + " · Delayed\nTerminal T1 · Gate <b>D84</b>"
	if got.text != want {
		t.Errorf("offer\n%s\nwant\n%s", got.text, want)
	}
	c := got.keys.Choices
	if len(c) != 2 || c[0].Text != "Follow this flight" || c[1].Text != "No" || c[1].Data != "n" {
		t.Fatalf("buttons %+v", c)
	}
	if c[0].Data != "f:PMI:S:"+strings.ReplaceAll(day(0), "/", "")+":VY3919" || len(c[0].Data) > 64 {
		t.Errorf("follow data %q", c[0].Data)
	}
	if strings.Join(bs.asked, ",") != "PMI/S" {
		t.Errorf("asked boards %v, want only PMI departures", bs.asked)
	}
}

func TestOfferWithTheAirportSkipsTheRoute(t *testing.T) {
	arr := vueling(day(0))
	arr.Number, arr.Airport, arr.Other, arr.Direction = "FR2495", "PMI", "STN", aena.Arrivals
	b, tg, bs := offerBot(t, map[string][]aena.Flight{"PMI/L": {arr}}, http.StatusInternalServerError)
	b.Handle(context.Background(), msg("FR2495 pmi"))
	if !strings.Contains(tg.sent[0].text, "London, Essex (STN) → Palma de Mallorca (PMI)\nArrives 23:18") {
		t.Errorf("offer %q", tg.sent[0].text)
	}
	if strings.Join(bs.asked, ",") != "PMI/S,PMI/L" {
		t.Errorf("asked boards %v, want PMI departures then arrivals", bs.asked)
	}
}

func TestOfferNotFoundAsksForTheAirport(t *testing.T) {
	b, tg, _ := offerBot(t, map[string][]aena.Flight{}, 0)
	b.Handle(context.Background(), msg("FR2495"))
	want := "I couldn't find FR2495 today or tomorrow. Send it with the airport, like <code>FR2495 PMI</code>."
	if tg.sent[0].text != want || len(tg.sent[0].keys.Choices) != 0 {
		t.Errorf("got %+v", tg.sent[0])
	}

	b.Handle(context.Background(), msg("VY3919"))
	if !strings.HasPrefix(tg.sent[1].text, "I couldn't find VY3919 today") {
		t.Errorf("route known but not on the board: %q", tg.sent[1].text)
	}
}

func TestTooManyRequestsAtAdsbdbIsNotFound(t *testing.T) {
	b, tg, bs := offerBot(t, map[string][]aena.Flight{"PMI/S": {vueling(day(0))}}, http.StatusTooManyRequests)
	b.Handle(context.Background(), msg("VY3919"))
	if !strings.HasPrefix(tg.sent[0].text, "I couldn't find VY3919") || len(bs.asked) != 0 {
		t.Errorf("got %q after asking %v", tg.sent[0].text, bs.asked)
	}
}

func TestOfferTodayAndTomorrow(t *testing.T) {
	b, tg, _ := offerBot(t, map[string][]aena.Flight{"PMI/S": {vueling(day(0)), vueling(day(1))}}, 0)
	b.Handle(context.Background(), msg("VY3919"))
	c := tg.sent[0].keys.Choices
	if len(c) != 3 || c[0].Text != "Follow today "+day(0)[:5] || c[1].Text != "Follow tomorrow "+day(1)[:5] || c[2].Text != "No" {
		t.Errorf("buttons %+v", c)
	}
	if !strings.Contains(tg.sent[0].text, "· tomorrow "+day(1)[:5]+" · On time") {
		t.Errorf("offer %q", tg.sent[0].text)
	}
}

func offerTap(data string) *telegram.Callback {
	c := tap(42, data)
	c.Message.Markup = nil
	return c
}

func TestFollowButtonAddsTheFlight(t *testing.T) {
	b, tg, _ := offerBot(t, map[string][]aena.Flight{"PMI/S": {vueling(day(0))}}, 0)
	b.Handle(context.Background(), msg("VY3919"))
	b.Tap(context.Background(), offerTap(tg.sent[0].keys.Choices[0].Data))

	list := b.Watch.List("42")
	if len(list) != 1 || list[0].Number != "VY3919" || list[0].Airport != "PMI" || list[0].Date != day(0) {
		t.Fatalf("followed %+v", list)
	}
	if got := tg.answers[len(tg.answers)-1]; got != "Following VY3919" {
		t.Errorf("answered %q", got)
	}
	if len(tg.keysLeft) != 1 || len(tg.keysLeft[0].Choices) != 0 || tg.keysLeft[0].Open != "" {
		t.Errorf("buttons left %+v", tg.keysLeft)
	}
}

func TestNoRemovesTheButtons(t *testing.T) {
	b, tg, _ := offerBot(t, map[string][]aena.Flight{"PMI/S": {vueling(day(0))}}, 0)
	b.Tap(context.Background(), offerTap("n"))
	if len(b.Watch.List("42")) != 0 || len(tg.answers) != 1 || len(tg.keysLeft) != 1 || len(tg.keysLeft[0].Choices) != 0 {
		t.Errorf("answers %v, buttons left %+v", tg.answers, tg.keysLeft)
	}
}
