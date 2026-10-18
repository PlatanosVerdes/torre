package bot

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type reply struct{ chat, text, open string }

type fakeTG struct{ sent []reply }

func (f *fakeTG) Updates(context.Context, int64) ([]telegram.Update, error) { return nil, nil }
func (f *fakeTG) Setup(context.Context, string, map[string]string) error    { return nil }
func (f *fakeTG) Send(_ context.Context, chat, text, open string) error {
	f.sent = append(f.sent, reply{chat, text, open})
	return nil
}

type noBoards struct{}

func (noBoards) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	return nil, time.Now(), nil
}

func newBot(t *testing.T) (*Bot, *fakeTG) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := watch.New(t.TempDir(), noBoards{}, &fakeTG{}, time.UTC, log)
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
