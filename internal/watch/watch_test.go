package watch

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

type fakeBoards struct{ flights []aena.Flight }

func (b *fakeBoards) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	return b.flights, time.Now(), nil
}

type inbox struct{ msgs []string }

func (i *inbox) Notify(_ context.Context, m string) error {
	i.msgs = append(i.msgs, m)
	return nil
}

var madrid, _ = time.LoadLocation("Europe/Madrid")

func flight() aena.Flight {
	return aena.Flight{
		ID: "18/10/2026-FR2495", Number: "FR2495", Codeshare: []string{"XX1"}, Direction: aena.Departures,
		Airport: "BCN", Other: "PMI", Date: "18/10/2026", Scheduled: "12:20", Status: "INI",
		StatusLabel: "En hora", Terminal: "T2",
	}
}

func newWatcher(t *testing.T, b *fakeBoards, n *inbox, now time.Time) *Watcher {
	t.Helper()
	w, err := New(t.TempDir(), b, n, madrid, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return now }
	return w
}

func TestFollowNotifiesEachChangeOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, time.Date(2026, 10, 18, 10, 0, 0, 0, madrid))

	if _, err := w.Add(context.Background(), "bcn", aena.Departures, "18/10/2026", "fr 2495"); err != nil {
		t.Fatal(err)
	}
	if len(n.msgs) != 1 || !strings.HasPrefix(n.msgs[0], "Siguiendo") {
		t.Fatalf("no confirmation sent: %v", n.msgs)
	}

	w.Check(context.Background())
	if len(n.msgs) != 1 {
		t.Fatalf("an unchanged flight sent %d messages", len(n.msgs)-1)
	}

	f := flight()
	f.Status, f.StatusLabel, f.Estimated, f.DelayMin, f.Gate = "RET", "Retrasado", "12:55", 35, "R12"
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Fatalf("got %d messages, want one for the change", len(n.msgs))
	}
	for _, want := range []string{"Retrasado", "12:20 → <b>12:55</b> (+35 min)", "Puerta: — → <b>R12</b>"} {
		if !strings.Contains(n.msgs[1], want) {
			t.Errorf("message lacks %q:\n%s", want, n.msgs[1])
		}
	}

	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Error("the same change was sent twice")
	}
}

func TestFollowByCodeshareNumber(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	w := newWatcher(t, b, &inbox{}, time.Date(2026, 10, 18, 10, 0, 0, 0, madrid))
	tr, err := w.Add(context.Background(), "BCN", aena.Departures, "18/10/2026", "XX1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Number != "FR2495" {
		t.Errorf("followed %s, want the operating flight FR2495", tr.Number)
	}
}

func TestUnknownFlightIsRefused(t *testing.T) {
	w := newWatcher(t, &fakeBoards{}, &inbox{}, time.Now())
	if _, err := w.Add(context.Background(), "BCN", aena.Departures, "18/10/2026", "FR1"); err == nil {
		t.Error("a flight that is not on the board was accepted")
	}
}

func TestFinishedFlightsAreDroppedLater(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, madrid)
	w := newWatcher(t, b, &inbox{}, now)
	if _, err := w.Add(context.Background(), "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}

	f := flight()
	f.Status, f.StatusLabel, f.Final = "BOR", "Finalizado", true
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(w.List()) != 1 {
		t.Fatal("a flight that just left was dropped at once")
	}

	w.now = func() time.Time { return now.Add(keepFinal + time.Minute) }
	w.Check(context.Background())
	if len(w.List()) != 0 {
		t.Error("a finished flight was kept forever")
	}
}

func TestTrackedSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, _ := New(dir, b, &inbox{}, madrid, log)
	if _, err := w.Add(context.Background(), "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}
	again, err := New(dir, b, &inbox{}, madrid, log)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.List()) != 1 {
		t.Error("the list was lost on restart")
	}
}

func TestNamesInMessagesAreEscaped(t *testing.T) {
	f := flight()
	f.Other = "<b>"
	if strings.Contains(Headline(f), "<b><b>") || !strings.Contains(Headline(f), "&lt;b&gt;") {
		t.Errorf("headline not escaped: %s", Headline(f))
	}
}

func TestSameFlightByTwoNumbersIsFollowedOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, time.Date(2026, 10, 18, 10, 0, 0, 0, madrid))
	for _, number := range []string{"XX1", "FR2495"} {
		if _, err := w.Add(context.Background(), "BCN", aena.Departures, "18/10/2026", number); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(w.List()); got != 1 {
		t.Errorf("following by codeshare and by operating number made %d entries", got)
	}
	if len(n.msgs) != 1 {
		t.Errorf("sent %d confirmations", len(n.msgs))
	}
}
