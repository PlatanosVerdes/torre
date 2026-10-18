// Package telegram sends messages through the Bot API and checks Mini App launch data. It never
// reads updates: the services bot has a single reader, wallapop-catalogue, and Telegram answers a
// second one with 409.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const api = "https://api.telegram.org/bot"

type Bot struct {
	Token  string
	HTTP   *http.Client
	APIURL string
}

func New(token string) *Bot {
	return &Bot{Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}, APIURL: api}
}

func (b *Bot) Enabled() bool { return b != nil && b.Token != "" }

// ErrBlocked means the user never pressed Start or blocked the bot: Telegram lets a bot write
// only to people who wrote to it first.
var ErrBlocked = errors.New("the user has not started the bot")

// Send posts an HTML message to chat. With open set it carries a button that opens that URL as a
// Mini App, which Telegram only allows in private chats.
func (b *Bot) Send(ctx context.Context, chat, text, open string) error {
	if !b.Enabled() || chat == "" {
		return nil
	}
	form := url.Values{
		"chat_id":                  {chat},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}
	if open != "" {
		keys, _ := json.Marshal(map[string]any{
			"inline_keyboard": [][]map[string]any{{{"text": "Open in Torre", "web_app": map[string]string{"url": open}}}},
		})
		form.Set("reply_markup", string(keys))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.APIURL+b.Token+"/sendMessage",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.HTTP.Do(req)
	if err != nil {
		// *url.Error prints the full URL, and the token is in it.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return ErrBlocked
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram sendMessage answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
