// Package bot answers torre's own Telegram bot: /start opens the page, /flights lists what the
// chat follows. Following and boarding passes happen in the page, where the user can see them.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PlatanosVerdes/gonana/botstats"
	"github.com/PlatanosVerdes/torre/internal/limit"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type Telegram interface {
	Updates(ctx context.Context, offset int64) ([]telegram.Update, error)
	Setup(ctx context.Context, open string, commands map[string]string) error
	Send(ctx context.Context, chat, html string, keys telegram.Keys) error
	Answer(ctx context.Context, callbackID, text string) error
	SetKeys(ctx context.Context, chat string, messageID int64, keys telegram.Keys) error
}

type Bot struct {
	TG      Telegram
	Watch   *watch.Watcher
	BaseURL string
	Log     *slog.Logger
	Stats   *botstats.Recorder

	perChat *limit.Limiter
}

var commands = map[string]string{
	"start":   "Open Torre",
	"flights": "The flights you follow",
	"help":    "What this bot does",
}

// The start parameter a "Follow in Telegram" link carries: BCN_S_18102026_FR2495.
var startParam = regexp.MustCompile(`^([A-Z]{3})_([SL])_(\d{2})(\d{2})(\d{4})_([A-Z0-9]{3,8})$`)

const help = `Torre shows departures and arrivals for every AENA airport in Spain and tells you when a flight you follow changes time, gate or status.

Tap <b>Open</b> to see a board, follow a flight, or add your boarding pass. The pass is read on your phone: your name and booking reference never leave it.

/flights lists what you follow.

Torre is open source: https://github.com/PlatanosVerdes/torre`

func (b *Bot) Run(ctx context.Context) {
	b.perChat = limit.New(20, 10)
	for {
		if err := b.TG.Setup(ctx, b.BaseURL, commands); err == nil {
			break
		} else {
			b.Log.Warn("bot setup failed, retrying", "err", err)
		}
		if !sleep(ctx, time.Minute) {
			return
		}
	}

	var offset int64
	for ctx.Err() == nil {
		updates, err := b.TG.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() == nil {
				b.Log.Warn("getUpdates failed", "err", err)
				sleep(ctx, 5*time.Second)
			}
			continue
		}
		for _, u := range updates {
			offset = u.ID + 1
			if u.Message != nil {
				b.Handle(ctx, u.Message)
			}
			if u.Callback != nil {
				b.Tap(ctx, u.Callback)
			}
		}
	}
}

// Handle answers one message. Groups are ignored: following is personal and Mini App buttons
// only work in private chats.
func (b *Bot) Handle(ctx context.Context, m *telegram.Message) {
	if m.Chat.Type != "private" {
		return
	}
	chat := strconv.FormatInt(m.Chat.ID, 10)
	if !b.allow(chat, m.Chat) {
		return
	}

	command, arg, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	command, _, _ = strings.Cut(command, "@")
	var text, open string
	switch {
	case command == "/start" && arg != "":
		text, open = b.startFlight(arg)
	case command == "/start":
		text, open = "Welcome to <b>Torre</b>.\n\n"+help, b.BaseURL
	case command == "/flights":
		text, open = b.flights(chat), b.BaseURL
	case command == "/help":
		text, open = help, b.BaseURL
	case len(m.Photo) > 0 || m.Document != nil:
		text, open = "Add boarding passes inside the app, with <b>Add boarding pass</b>. It is read on your phone, so your name and booking reference never reach the server.", b.BaseURL
	default:
		text, open = "Tap <b>Open</b> to search a flight, or send /help.", b.BaseURL
	}
	if err := b.TG.Send(ctx, chat, text, telegram.Keys{Open: open}); err != nil {
		b.Log.Warn("reply failed", "err", err)
	}
}

// Tap answers an inline button. "u:<flight>" stops following it; the chat the tap comes from is
// the owner, so a forwarded message cannot remove someone else's flight.
func (b *Bot) Tap(ctx context.Context, c *telegram.Callback) {
	chat := strconv.FormatInt(c.Message.Chat.ID, 10)
	if !b.allow(chat, c.Message.Chat) {
		_ = b.TG.Answer(ctx, c.ID, "Too many taps, try again in a moment")
		return
	}
	rest, ok := strings.CutPrefix(c.Data, "u:")
	if !ok {
		_ = b.TG.Answer(ctx, c.ID, "")
		return
	}

	id := chat + "-" + rest
	notice := "You no longer follow this flight"
	for _, t := range b.Watch.List(chat) {
		if t.ID == id {
			if err := b.Watch.Remove(chat, id); err != nil {
				b.Log.Warn("unfollow failed", "err", err)
				_ = b.TG.Answer(ctx, c.ID, "Could not stop following it, try again")
				return
			}
			notice = "Stopped following " + t.Number
		}
	}
	if err := b.TG.Answer(ctx, c.ID, notice); err != nil {
		b.Log.Warn("answer failed", "err", err)
	}
	// Keep "Open in Torre", drop the button that no longer does anything.
	if err := b.TG.SetKeys(ctx, chat, c.Message.ID, telegram.Keys{Open: c.Message.OpenURL()}); err != nil {
		b.Log.Warn("edit keys failed", "err", err)
	}
}

// Turned-away updates count too, so a flood shows in the stats.
func (b *Bot) allow(chat string, c telegram.Chat) bool {
	if b.Stats != nil {
		b.Stats.Message(chat, c.FirstName, c.Username)
	}
	if b.perChat != nil && !b.perChat.Allow(chat) {
		if b.Stats != nil {
			b.Stats.Rejected()
		}
		return false
	}
	return true
}

func (b *Bot) startFlight(arg string) (string, string) {
	m := startParam.FindStringSubmatch(arg)
	if m == nil {
		return "Welcome to <b>Torre</b>.\n\n" + help, b.BaseURL
	}
	date := m[3] + "/" + m[4] + "/" + m[5]
	q := url.Values{"a": {m[1]}, "d": {m[2]}, "f": {date + "-" + m[6]}}
	return fmt.Sprintf("✈️ <b>%s</b> · %s · %s\nOpen it to follow it and get a message when it changes.", m[6], m[1], date[:5]),
		strings.TrimRight(b.BaseURL, "/") + "/?" + q.Encode()
}

func (b *Bot) flights(chat string) string {
	list := b.Watch.List(chat)
	if len(list) == 0 {
		return "You are not following any flight. Open Torre, tap a flight and then <b>Follow</b>."
	}
	lines := []string{"You follow:"}
	for _, t := range list {
		if t.Last == nil {
			lines = append(lines, fmt.Sprintf("• <b>%s</b> %s · %s · waiting for AENA to publish it", t.Number, t.Airport, t.Date[:5]))
			continue
		}
		lines = append(lines, "• "+watch.Headline(*t.Last)+" · "+t.Last.StatusLabel+" "+watch.Time(*t.Last))
	}
	return strings.Join(lines, "\n")
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
