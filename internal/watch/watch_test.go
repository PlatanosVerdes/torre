package watch

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
)

type fakeBoards struct {
	flights []aena.Flight
	calls   int
}

func (b *fakeBoards) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	b.calls++
	return b.flights, time.Now(), nil
}

type sent struct {
	chat, text string
	keys       telegram.Keys
}

type inbox struct {
	msgs    []sent
	blocked map[string]bool
}

func (i *inbox) Send(_ context.Context, chat, text string, keys telegram.Keys) error {
	if i.blocked[chat] {
		return telegram.ErrBlocked
	}
	i.msgs = append(i.msgs, sent{chat, text, keys})
	return nil
}

var madrid, _ = time.LoadLocation("Europe/Madrid")

var morning = time.Date(2026, 10, 18, 10, 0, 0, 0, madrid)

func flight() aena.Flight {
	return aena.Flight{
		ID: "18/10/2026-FR2495", Number: "FR2495", Codeshare: []string{"XX1"}, Direction: aena.Departures,
		Airport: "BCN", Other: "PMI", Date: "18/10/2026", Scheduled: "12:20", Status: "INI",
		StatusLabel: "On time", Terminal: "T2",
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
	w := newWatcher(t, b, n, morning)
	w.BaseURL = "https://torre.example"

	if _, err := w.Add(context.Background(), "42", "bcn", aena.Departures, "18/10/2026", "fr 02495"); err != nil {
		t.Fatal(err)
	}
	if len(n.msgs) != 1 || !strings.HasPrefix(n.msgs[0].text, "Following") || n.msgs[0].chat != "42" {
		t.Fatalf("no confirmation sent to the chat: %+v", n.msgs)
	}
	if !strings.Contains(n.msgs[0].keys.Open, "f=18%2F10%2F2026-FR2495") {
		t.Errorf("open button points to %q", n.msgs[0].keys.Open)
	}
	if n.msgs[0].keys.Stop != "" {
		t.Error("a stop button without StopButton set")
	}

	w.Check(context.Background())
	if len(n.msgs) != 1 {
		t.Fatalf("an unchanged flight sent %d messages", len(n.msgs)-1)
	}

	f := flight()
	f.Status, f.StatusLabel, f.Estimated, f.DelayMin, f.Gate = "RET", "Delayed", "12:55", 35, "R12"
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Fatalf("got %d messages, want one for the change", len(n.msgs))
	}
	for _, want := range []string{"Delayed", "12:20 → <b>12:55</b> (+35 min)", "Gate: — → <b>R12</b>"} {
		if !strings.Contains(n.msgs[1].text, want) {
			t.Errorf("message lacks %q:\n%s", want, n.msgs[1].text)
		}
	}

	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Error("the same change was sent twice")
	}
}

func TestEachChatSeesOnlyItsFlights(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	a, _ := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495")
	if _, err := w.Add(context.Background(), "2", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}
	if len(w.List("1")) != 1 || len(w.List("2")) != 1 || len(w.List("")) != 2 {
		t.Fatalf("lists: %d %d %d", len(w.List("1")), len(w.List("2")), len(w.List("")))
	}
	_ = w.Remove("2", a.ID)
	if len(w.List("1")) != 1 {
		t.Error("another chat removed a flight it does not own")
	}

	f := flight()
	f.Gate = "R12"
	b.flights = []aena.Flight{f}
	n.msgs = nil
	w.Check(context.Background())
	if len(n.msgs) != 2 || n.msgs[0].chat == n.msgs[1].chat {
		t.Errorf("a change should reach both chats once: %+v", n.msgs)
	}
}

func TestFollowByCodeshareNumberOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	for _, number := range []string{"XX1", "FR2495"} {
		tr, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", number)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Number != "FR2495" {
			t.Errorf("followed %s, want the operating flight FR2495", tr.Number)
		}
	}
	if len(w.List("1")) != 1 || len(n.msgs) != 1 {
		t.Errorf("following by codeshare and by operating number made %d entries, %d messages", len(w.List("1")), len(n.msgs))
	}
}

func TestPerChatLimit(t *testing.T) {
	var flights []aena.Flight
	for i := range 3 {
		f := flight()
		f.Number = "FR10" + string(rune('0'+i))
		flights = append(flights, f)
	}
	w := newWatcher(t, &fakeBoards{flights: flights}, &inbox{}, morning)
	w.MaxChat = 2
	for i, f := range flights {
		_, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", f.Number)
		if (i < 2) != (err == nil) {
			t.Errorf("flight %d: %v", i, err)
		}
	}
}

func TestUnknownFlightIsRefused(t *testing.T) {
	w := newWatcher(t, &fakeBoards{}, &inbox{}, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR1"); err == nil {
		t.Error("a flight that is not on the board was accepted")
	}
}

func TestFarFlightWaitsUntilPublished(t *testing.T) {
	b := &fakeBoards{}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	tr, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "20/11/2026", "FR2495")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Last != nil || b.calls != 0 {
		t.Errorf("a flight a month out asked AENA %d times", b.calls)
	}
	if !strings.Contains(n.msgs[0].text, "two weeks ahead") {
		t.Errorf("confirmation: %s", n.msgs[0].text)
	}

	f := flight()
	f.Date, f.ID = "20/11/2026", "20/11/2026-FR2495"
	b.flights = []aena.Flight{f}
	w.now = func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, madrid) }
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.HasPrefix(n.msgs[1].text, "Now on AENA") {
		t.Fatalf("no message when it appeared: %+v", n.msgs)
	}
	if w.List("1")[0].Last == nil {
		t.Error("the published flight was not stored")
	}
}

func TestBlockedBotIsNotFollowed(t *testing.T) {
	n := &inbox{blocked: map[string]bool{"9": true}}
	w := newWatcher(t, &fakeBoards{flights: []aena.Flight{flight()}}, n, morning)
	if _, err := w.Add(context.Background(), "9", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != telegram.ErrBlocked {
		t.Errorf("got %v, want ErrBlocked", err)
	}
	if len(w.List("9")) != 0 {
		t.Error("kept a flight nobody can be told about")
	}
}

func TestFinishedFlightsAreDroppedLater(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	w := newWatcher(t, b, &inbox{}, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}

	f := flight()
	f.Status, f.StatusLabel, f.Final = "BOR", "Departed", true
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(w.List("")) != 1 {
		t.Fatal("a flight that just left was dropped at once")
	}

	w.now = func() time.Time { return morning.Add(keepFinal + time.Minute) }
	w.Check(context.Background())
	if len(w.List("")) != 0 {
		t.Error("a finished flight was kept forever")
	}
}

func TestTrackedSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, _ := New(dir, b, &inbox{}, madrid, log)
	w.now = func() time.Time { return morning }
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}
	again, err := New(dir, b, &inbox{}, madrid, log)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.List("1")) != 1 {
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

func TestOneMinuteWobbleIsNotNews(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}
	for _, est := range []struct {
		time  string
		delay int
	}{{"12:21", 1}, {"12:20", 0}, {"12:22", 2}, {"12:24", 4}} {
		f := flight()
		f.Estimated, f.DelayMin = est.time, est.delay
		b.flights = []aena.Flight{f}
		w.Check(context.Background())
	}
	if len(n.msgs) != 1 {
		t.Fatalf("a time within four minutes of the last message sent %d messages", len(n.msgs)-1)
	}

	f := flight()
	f.Estimated, f.DelayMin = "12:26", 6
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "12:20 → <b>12:26</b>") {
		t.Fatalf("minutes that added up to six were not sent from the last told time: %+v", n.msgs)
	}
}

func TestStopButton(t *testing.T) {
	n := &inbox{}
	w := newWatcher(t, &fakeBoards{flights: []aena.Flight{flight()}}, n, morning)
	w.StopButton = true
	tr, err := w.Add(context.Background(), "42", "BCN", aena.Departures, "18/10/2026", "FR2495")
	if err != nil {
		t.Fatal(err)
	}
	if got := n.msgs[0].keys.Stop; got != "u:BCN-S-18102026-FR2495" || len(got) > 64 {
		t.Errorf("stop data %q", got)
	}
	if "42-"+strings.TrimPrefix(tr.StopData(), "u:") != tr.ID {
		t.Error("the stop data does not lead back to the flight's id")
	}
}

// twoBoards answers the departures at BCN and the arrivals at PMI separately.
type twoBoards struct{ dep, arr []aena.Flight }

func (b *twoBoards) Board(_ context.Context, airport string, dir aena.Direction, _ bool) ([]aena.Flight, time.Time, error) {
	if dir == aena.Arrivals {
		return b.arr, time.Now(), nil
	}
	return b.dep, time.Now(), nil
}

func TestTheWatchFollowsTheFlightToItsArrival(t *testing.T) {
	arrival := aena.Flight{
		ID: "18/10/2026-FR2495", Number: "FR2495", Direction: aena.Arrivals, Airport: "PMI", Other: "BCN",
		Date: "18/10/2026", Scheduled: "13:25", Status: "FLY", StatusLabel: "In flight",
	}
	b := &twoBoards{dep: []aena.Flight{flight()}, arr: []aena.Flight{arrival}}
	n := &inbox{}
	w := newWatcher(t, (*fakeBoards)(nil), n, morning)
	w.boards = b
	w.Spanish = func(code string) bool { return code == "PMI" || code == "BCN" }
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}

	gone := flight()
	gone.Status, gone.StatusLabel, gone.Final = "BOR", "Departed", true
	b.dep = []aena.Flight{gone}
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "Arrives at PMI <b>13:25</b>") {
		t.Fatalf("no hand-over message: %+v", n.msgs)
	}
	tr := w.List("1")[0]
	if tr.Airport != "PMI" || tr.Direction != aena.Arrivals {
		t.Fatalf("still watching %s/%s", tr.Airport, tr.Direction)
	}

	landed := arrival
	landed.Status, landed.StatusLabel, landed.Belt = "IBK", "Baggage on belt", "08"
	b.arr = []aena.Flight{landed}
	w.Check(context.Background())
	last := n.msgs[len(n.msgs)-1].text
	if !strings.Contains(last, "Baggage on belt") || !strings.Contains(last, "Baggage belt: <b>08</b>") {
		t.Errorf("arrival change not sent: %s", last)
	}
}

func TestNoArrivalWatchOutsideSpain(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	w.Spanish = func(string) bool { return false }
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495"); err != nil {
		t.Fatal(err)
	}
	gone := flight()
	gone.Status, gone.StatusLabel, gone.Final = "BOR", "Departed", true
	b.flights = []aena.Flight{gone}
	w.Check(context.Background())
	if w.List("1")[0].Direction != aena.Departures || !strings.Contains(n.msgs[1].text, "Departed") {
		t.Errorf("a flight to a non-AENA airport changed boards: %+v", n.msgs)
	}
}
