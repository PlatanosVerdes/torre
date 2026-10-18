package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/gonana/botstats"
	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/limit"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
	"path/filepath"
)

type reply struct{ chat, text, open string }

type fakeTG struct {
	sent     []reply
	answers  []string
	keysLeft []telegram.Keys
}

func (f *fakeTG) Updates(context.Context, int64) ([]telegram.Update, error) { return nil, nil }
func (f *fakeTG) Setup(context.Context, string, map[string]string) error    { return nil }
func (f *fakeTG) Send(_ context.Context, chat, text string, keys telegram.Keys) error {
	f.sent = append(f.sent, reply{chat, text, keys.Open})
	return nil
}
func (f *fakeTG) Answer(_ context.Context, _, text string) error {
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeTG) SetKeys(_ context.Context, _ string, _ int64, keys telegram.Keys) error {
	f.keysLeft = append(f.keysLeft, keys)
	return nil
}

type oneFlight struct{}

func (oneFlight) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	return []aena.Flight{{ID: "18/10/2026-FR2495", Number: "FR2495", Date: "18/10/2026", Airport: "BCN",
		Other: "PMI", Direction: aena.Departures, Scheduled: "12:20"}}, time.Now(), nil
}

func newBot(t *testing.T) (*Bot, *fakeTG) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := watch.New(t.TempDir(), oneFlight{}, &fakeTG{}, time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	tg := &fakeTG{}
	return &Bot{TG: tg, Watch: w, BaseURL: "https://torre.example", Log: log}, tg
}

func msg(text string) *telegram.Message {
	return &telegram.Message{Text: text, Chat: telegram.Chat{ID: 42, Type: "private"}}
}

func TestStartOpensTheApp(t *testing.T) {
	b, tg := newBot(t)
	b.Handle(context.Background(), msg("/start"))
	if len(tg.sent) != 1 || tg.sent[0].chat != "42" || tg.sent[0].open != "https://torre.example" {
		t.Fatalf("got %+v", tg.sent)
	}
}

func TestStartWithAFlightOpensIt(t *testing.T) {
	b, tg := newBot(t)
	b.Handle(context.Background(), msg("/start BCN_S_18102026_FR2495"))
	got := tg.sent[0]
	if !strings.Contains(got.text, "FR2495") || got.open != "https://torre.example/?a=BCN&d=S&f=18%2F10%2F2026-FR2495" {
		t.Errorf("got %+v", got)
	}
}

func TestBadStartParameterIsAPlainWelcome(t *testing.T) {
	b, tg := newBot(t)
	b.Handle(context.Background(), msg("/start <script>"))
	if strings.Contains(tg.sent[0].text, "<script>") || tg.sent[0].open != "https://torre.example" {
		t.Errorf("got %+v", tg.sent[0])
	}
}

func TestGroupsAreIgnored(t *testing.T) {
	b, tg := newBot(t)
	m := msg("/start")
	m.Chat.Type = "group"
	b.Handle(context.Background(), m)
	if len(tg.sent) != 0 {
		t.Error("answered in a group")
	}
}

func TestFlightsWhenNone(t *testing.T) {
	b, tg := newBot(t)
	b.Handle(context.Background(), msg("/flights@TorreFlightsBot"))
	if !strings.Contains(tg.sent[0].text, "not following") {
		t.Errorf("got %q", tg.sent[0].text)
	}
}

func TestPhotosPointToTheApp(t *testing.T) {
	b, tg := newBot(t)
	m := msg("")
	m.Photo = []any{map[string]any{}}
	b.Handle(context.Background(), m)
	if !strings.Contains(tg.sent[0].text, "boarding pass") {
		t.Errorf("got %q", tg.sent[0].text)
	}
}

func tap(chat int64, data string) *telegram.Callback {
	raw := `{"id":"c1","data":"` + data + `","message":{"message_id":9,"chat":{"id":` + strconv.FormatInt(chat, 10) + `,"type":"private"},
		"reply_markup":{"inline_keyboard":[[{"text":"Open in Torre","web_app":{"url":"https://torre.example/?f=x"}},{"text":"Stop following","callback_data":"` + data + `"}]]}}}`
	var c telegram.Callback
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		panic(err)
	}
	return &c
}

func TestStopFollowingFromTheMessage(t *testing.T) {
	b, tg := newBot(t)
	tr, err := b.Watch.Add(context.Background(), "42", "BCN", aena.Departures, "18/10/2026", "FR2495")
	if err != nil {
		t.Fatal(err)
	}

	// Someone else's chat with the same button data reaches nothing of 42's.
	b.Tap(context.Background(), tap(7, tr.StopData()))
	if len(b.Watch.List("42")) != 1 {
		t.Fatal("a tap from another chat removed the flight")
	}

	b.Tap(context.Background(), tap(42, tr.StopData()))
	if len(b.Watch.List("42")) != 0 {
		t.Fatal("the tap did not stop following")
	}
	if got := tg.answers[len(tg.answers)-1]; got != "Stopped following FR2495" {
		t.Errorf("answered %q", got)
	}
	if k := tg.keysLeft[len(tg.keysLeft)-1]; k.Stop != "" || k.Open != "https://torre.example/?f=x" {
		t.Errorf("buttons left %+v", k)
	}

	b.Tap(context.Background(), tap(42, tr.StopData()))
	if got := tg.answers[len(tg.answers)-1]; got != "You no longer follow this flight" {
		t.Errorf("second tap answered %q", got)
	}
}

func TestStatsCountWhoWritesAndWhatTheLimitTurnsAway(t *testing.T) {
	b, tg := newBot(t)
	rec, err := botstats.Open(filepath.Join(t.TempDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	b.Stats = rec
	b.perChat = limit.New(1, 2)

	m := msg("/help")
	m.Chat.FirstName, m.Chat.Username = "Ana", "ana"
	for i := 0; i < 4; i++ {
		b.Handle(context.Background(), m)
	}
	s := rec.Snapshot(botstats.About{})
	if s.Users.Total != 1 || s.People[0].Name != "Ana" || s.People[0].Username != "ana" || s.People[0].Messages != 4 {
		t.Errorf("people %+v", s.People)
	}
	if s.Traffic.Messages24h != 4 || s.Traffic.Rejected24h != 2 || len(tg.sent) != 2 {
		t.Errorf("traffic %+v with %d replies, want 4 messages, 2 turned away, 2 answered", s.Traffic, len(tg.sent))
	}
}
