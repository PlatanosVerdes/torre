package watch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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

// later moves the watcher's clock forward by d.
func later(w *Watcher, d time.Duration) {
	at := w.now().Add(d)
	w.now = func() time.Time { return at }
}

func TestFollowNotifiesEachChangeOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	w.BaseURL = "https://torre.example"

	if _, err := w.Add(context.Background(), "42", "bcn", aena.Departures, "18/10/2026", "fr 02495", ""); err != nil {
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
	if len(n.msgs) != 1 {
		t.Fatalf("a change sent before it stood for %s", settle)
	}
	later(w, settle)
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
	a, _ := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", "")
	if _, err := w.Add(context.Background(), "2", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
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
	later(w, settle)
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
		tr, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", number, "")
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
		_, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", f.Number, "")
		if (i < 2) != (err == nil) {
			t.Errorf("flight %d: %v", i, err)
		}
	}
}

func TestUnknownFlightIsRefused(t *testing.T) {
	w := newWatcher(t, &fakeBoards{}, &inbox{}, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR1", ""); err == nil {
		t.Error("a flight that is not on the board was accepted")
	}
}

func TestFarFlightWaitsUntilPublished(t *testing.T) {
	b := &fakeBoards{}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	tr, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "20/11/2026", "FR2495", "")
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
	if _, err := w.Add(context.Background(), "9", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != telegram.ErrBlocked {
		t.Errorf("got %v, want ErrBlocked", err)
	}
	if len(w.List("9")) != 0 {
		t.Error("kept a flight nobody can be told about")
	}
}

func TestFinishedFlightsAreDroppedLater(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	w := newWatcher(t, b, &inbox{}, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
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
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
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
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
		t.Fatal(err)
	}
	for _, est := range []struct {
		time  string
		delay int
	}{{"12:21", 1}, {"12:20", 0}, {"12:26", 6}, {"12:29", 9}} {
		f := flight()
		f.Estimated, f.DelayMin = est.time, est.delay
		b.flights = []aena.Flight{f}
		w.Check(context.Background())
		later(w, settle)
		w.Check(context.Background())
	}
	if len(n.msgs) != 1 {
		t.Fatalf("a time within nine minutes of the last message sent %d messages", len(n.msgs)-1)
	}

	f := flight()
	f.Estimated, f.DelayMin = "12:32", 12
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	later(w, settle)
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "12:20 → <b>12:32</b>") {
		t.Fatalf("minutes that added up to twelve were not sent from the last told time: %+v", n.msgs)
	}
}

func TestStopButton(t *testing.T) {
	n := &inbox{}
	w := newWatcher(t, &fakeBoards{flights: []aena.Flight{flight()}}, n, morning)
	w.StopButton = true
	tr, err := w.Add(context.Background(), "42", "BCN", aena.Departures, "18/10/2026", "FR2495", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := n.msgs[0].keys.Stop; got != "u:BCN-S-18102026-FR2495-1220" || len(got) > 64 {
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
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
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
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
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

// VY3919, 18/10: AENA gave gate D84, took it away two minutes later and gave it back at 22:10.
func TestAGateThatComesBackIsNotNews(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"D84", "", "D84"} {
		f := flight()
		f.Gate = gate
		b.flights = []aena.Flight{f}
		w.Check(context.Background())
		later(w, 3*time.Minute)
	}
	later(w, settle)
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "Gate: — → <b>D84</b>") {
		t.Fatalf("want one message for D84 once it held: %+v", n.msgs)
	}
	f := flight()
	f.Gate = ""
	b.flights = []aena.Flight{f}
	later(w, settle)
	w.Check(context.Background())
	later(w, settle)
	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Errorf("a gate taken away was sent on its own: %+v", n.msgs[2:])
	}
}

func TestWhatCannotWaitGoesAtOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning) // 10:00, departure 12:20
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
		t.Fatal(err)
	}

	f := flight()
	f.Gate = "R12"
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 1 {
		t.Fatal("a gate two hours before departure went out without waiting")
	}

	later(w, 90*time.Minute) // 11:30, 50 minutes to go
	f.Gate = "R20"
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "<b>R20</b>") {
		t.Fatalf("a gate change within the hour waited: %+v", n.msgs)
	}

	f.Status, f.StatusLabel = "EMB", "Boarding"
	b.flights = []aena.Flight{f}
	w.Check(context.Background())
	if len(n.msgs) != 3 || !strings.Contains(n.msgs[2].text, "Boarding") {
		t.Fatalf("boarding waited: %+v", n.msgs)
	}
}

func TestAChangeThatMovesAgainWaitsAgain(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
		t.Fatal(err)
	}
	for _, est := range []struct {
		time  string
		delay int
	}{{"12:40", 20}, {"13:05", 45}} {
		f := flight()
		f.Status, f.StatusLabel, f.Estimated, f.DelayMin = "RET", "Delayed", est.time, est.delay
		b.flights = []aena.Flight{f}
		w.Check(context.Background())
		later(w, 6*time.Minute)
	}
	w.Check(context.Background())
	if len(n.msgs) != 1 {
		t.Fatal("a delay that moved again went out before the new one stood")
	}
	later(w, settle)
	w.Check(context.Background())
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].text, "<b>13:05</b>") {
		t.Fatalf("want the delay that held, 13:05: %+v", n.msgs)
	}
}

// VY3919, 18/10: BCN kept its pre-takeoff 00:05 after PMI said departed, and 01:08 came with FLY.
func TestArrivalTimeWaitsForTheDestinationToSeeTakeoff(t *testing.T) {
	arrival := aena.Flight{
		ID: "18/10/2026-FR2495", Number: "FR2495", Direction: aena.Arrivals, Airport: "PMI", Other: "BCN",
		Date: "18/10/2026", Scheduled: "13:25", Estimated: "14:05", DelayMin: 40, Status: "RET", StatusLabel: "Delayed",
	}
	b := &twoBoards{dep: []aena.Flight{flight()}, arr: []aena.Flight{arrival}}
	n := &inbox{}
	w := newWatcher(t, (*fakeBoards)(nil), n, morning)
	w.boards = b
	w.Spanish = func(code string) bool { return code == "PMI" || code == "BCN" }
	if _, err := w.Add(context.Background(), "1", "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
		t.Fatal(err)
	}

	gone := flight()
	gone.Status, gone.StatusLabel, gone.Final = "BOR", "Departed", true
	b.dep = []aena.Flight{gone}
	w.Check(context.Background())
	if len(n.msgs) != 2 || strings.Contains(n.msgs[1].text, "14:05") || !strings.Contains(n.msgs[1].text, "PMI has not seen it take off yet") {
		t.Fatalf("the destination's pre-takeoff time was sent: %+v", n.msgs)
	}

	later(w, 20*time.Minute)
	moved := arrival
	moved.Estimated, moved.DelayMin = "14:15", 50
	b.arr = []aena.Flight{moved}
	w.Check(context.Background())
	if len(n.msgs) != 2 {
		t.Fatalf("a time from before takeoff was sent: %s", n.msgs[len(n.msgs)-1].text)
	}

	flying := arrival
	flying.Estimated, flying.DelayMin, flying.Status, flying.StatusLabel = "15:08", 103, "FLY", "In flight"
	b.arr = []aena.Flight{flying}
	w.Check(context.Background())
	if len(n.msgs) != 3 || !strings.Contains(n.msgs[2].text, "Time: 13:25 → <b>15:08</b> (+103 min)") {
		t.Fatalf("takeoff did not send the arrival time at once: %+v", n.msgs)
	}
	if w.List("1")[0].AwaitTakeoff {
		t.Error("still waiting for takeoff after it was sent")
	}
}

func TestHistoryKeepsEachStatusOnce(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{flight()}}
	w := newWatcher(t, b, &inbox{}, morning)
	for _, chat := range []string{"1", "2"} {
		if _, err := w.Add(context.Background(), chat, "BCN", aena.Departures, "18/10/2026", "FR2495", ""); err != nil {
			t.Fatal(err)
		}
	}
	w.Check(context.Background())
	gone := flight()
	gone.Status, gone.Estimated, gone.DelayMin = "BOR", "12:41", 21
	b.flights = []aena.Flight{gone}
	w.Check(context.Background())
	w.Check(context.Background())

	raw, err := os.ReadFile(w.history.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per status, got %d:\n%s", len(lines), raw)
	}
	var s Seen
	if err := json.Unmarshal([]byte(lines[1]), &s); err != nil {
		t.Fatal(err)
	}
	if s.Status != "BOR" || s.Other != "PMI" || s.Expected.Sub(s.Scheduled) != 21*time.Minute || !s.At.Equal(morning) {
		t.Errorf("wrong line: %+v", s)
	}
	if strings.Contains(string(raw), `"1"`) {
		t.Error("a chat id reached the history")
	}
}

func TestCollectWritesEachChangeOnce(t *testing.T) {
	other := flight()
	other.ID, other.Number = "18/10/2026-VY3919", "VY3919"
	b := &fakeBoards{flights: []aena.Flight{flight(), other}}
	h := NewHistory(t.TempDir() + "/collect.jsonl")
	for range 2 {
		if err := collect(context.Background(), b, "BCN", aena.Departures, h, madrid); err != nil {
			t.Fatal(err)
		}
	}
	b.flights[1].Gate = "D84"
	if err := collect(context.Background(), b, "BCN", aena.Departures, h, madrid); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], `"gate":"D84"`) {
		t.Errorf("want two flights and one gate change:\n%s", raw)
	}
}

// arrivalAt is a BCN arrival of VY3011 from LPA, as AENA lists it.
func arrivalAt(date, scheduled, status string) aena.Flight {
	return aena.Flight{
		ID: date + "-VY3011-" + strings.ReplaceAll(scheduled, ":", ""), Number: "VY3011", Direction: aena.Arrivals,
		Airport: "BCN", Other: "LPA", Date: date, Scheduled: scheduled, Status: status, StatusLabel: aena.StatusLabel(status),
		Final: status == "CAN",
	}
}

// BCN on 20/10/2026: last night's VY3011 landing at 00:20 was cancelled; that afternoon's lands at 21:00.
func TestAFollowedRowIsNotMistakenForAnotherOfTheSameNumber(t *testing.T) {
	b := &fakeBoards{flights: []aena.Flight{arrivalAt("20/10/2026", "00:20", "CAN"), arrivalAt("20/10/2026", "21:00", "SCH")}}
	n := &inbox{}
	w := newWatcher(t, b, n, morning)
	tr, err := w.Add(context.Background(), "1", "BCN", aena.Arrivals, "20/10/2026", "VY3011", "21:00")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Last.Scheduled != "21:00" || strings.Contains(n.msgs[0].text, "Cancelled") {
		t.Fatalf("followed the other row: %+v", tr.Last)
	}
	w.Check(context.Background())
	if len(n.msgs) != 1 {
		t.Errorf("the cancelled row reached the chat: %s", n.msgs[len(n.msgs)-1].text)
	}
}

func TestLeavingOnPicksTheFlightThatLeavesThatDay(t *testing.T) {
	day := func(d string) time.Time { at, _ := time.ParseInLocation("02/01/2006", d, madrid); return at }
	board := []aena.Flight{
		arrivalAt("20/10/2026", "00:20", "CAN"),
		arrivalAt("20/10/2026", "21:00", "SCH"),
		arrivalAt("21/10/2026", "00:20", "SCH"),
	}
	cases := []struct {
		leaves string
		want   string
	}{
		{"19/10/2026", "20/10/2026 00:20"},
		{"20/10/2026", "20/10/2026 21:00"},
		{"21/10/2026", "21/10/2026 00:20"},
	}
	for _, c := range cases {
		f := LeavingOn(board, day(c.leaves), "VY3011")
		if f == nil || f.Date+" "+f.Scheduled != c.want {
			t.Errorf("leaving %s: got %+v, want %s", c.leaves, f, c.want)
		}
	}
	if f := LeavingOn(board, day("22/10/2026"), "VY3011"); f != nil {
		t.Errorf("found a flight that leaves on a day not on the board: %+v", f)
	}
}

func TestTheArrivalIsTheOneAfterTheDeparture(t *testing.T) {
	w := newWatcher(t, &fakeBoards{}, &inbox{}, morning)
	board := []aena.Flight{
		arrivalAt("20/10/2026", "00:20", "CAN"),
		arrivalAt("20/10/2026", "21:00", "SCH"),
		arrivalAt("21/10/2026", "00:20", "SCH"),
	}
	dep := aena.Flight{Number: "VY3011", Direction: aena.Departures, Airport: "LPA", Other: "BCN", Date: "20/10/2026", Scheduled: "21:20"}
	if f := w.arrivalAfter(board, dep); f == nil || f.Date != "21/10/2026" {
		t.Errorf("an evening departure lands after midnight, got %+v", f)
	}
	dep.Scheduled = "17:00"
	if f := w.arrivalAfter(board, dep); f == nil || f.Scheduled != "21:00" {
		t.Errorf("an afternoon departure lands that evening, got %+v", f)
	}
}

func TestHistoryDoesNotRepeatAfterARestart(t *testing.T) {
	path := t.TempDir() + "/history.jsonl"
	f := flight()
	if err := NewHistory(path).Record(f, madrid, morning); err != nil {
		t.Fatal(err)
	}
	if err := NewHistory(path).Record(f, madrid, morning.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if n := strings.Count(string(raw), "\n"); n != 1 {
		t.Errorf("want one line across the restart, got %d:\n%s", n, raw)
	}
}

func TestSampleFollowsFlightsDueSoonAndKeepsThemOffTelegram(t *testing.T) {
	due := func(number, at string) aena.Flight {
		f := flight()
		f.Number, f.Scheduled, f.ID, f.Codeshare = number, at, "18/10/2026-"+number+"-"+strings.ReplaceAll(at, ":", ""), nil
		return f
	}
	gone := due("FR0003", "11:00")
	gone.Final = true
	b := &fakeBoards{flights: []aena.Flight{due("FR0001", "10:10"), due("FR0002", "11:00"), gone, due("FR0004", "12:00"), due("FR0005", "17:00")}}
	real := &inbox{}
	path := t.TempDir() + "/sample.jsonl"
	w := newWatcher(t, b, real, morning)
	w.notify = &SampleNotifier{Next: real, Path: path, Now: func() time.Time { return morning }}
	w.MaxChat = 1

	w.sample(context.Background(), []string{"BCN"}, 5)
	var got []string
	for _, tr := range w.List(SampleChat) {
		got = append(got, tr.Number)
	}
	if strings.Join(got, ",") != "FR0002,FR0004" {
		t.Errorf("sampled %v, want only the flights due in 30 min to 6 h that are not done", got)
	}
	if len(real.msgs) != 0 {
		t.Errorf("a sample message reached Telegram: %+v", real.msgs)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "Following") != 2 {
		t.Errorf("sample messages not written:\n%s", raw)
	}

	w.sample(context.Background(), []string{"BCN"}, 5)
	if n := len(w.List(SampleChat)); n != 2 {
		t.Errorf("topping up followed a flight twice: %d", n)
	}
}

func TestZoneStandsInForTheGate(t *testing.T) {
	old := aena.Flight{Direction: aena.Departures, Status: "SCH"}
	cur := old
	cur.Zone = "R/S/U"
	if d := Diff(old, cur); len(d) != 1 || d[0] != "Gate: — → <b>R/S/U</b>" {
		t.Errorf("zone appearing: %v", d)
	}
	old = cur
	cur.Zone, cur.Gate = "", "R13"
	if d := Diff(old, cur); len(d) != 1 || d[0] != "Gate: R/S/U → <b>R13</b>" {
		t.Errorf("gate after zone: %v", d)
	}
}
