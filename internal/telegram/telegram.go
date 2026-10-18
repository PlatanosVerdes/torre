// Package telegram sends messages through the Bot API. It never reads updates: the services bot
// it shares has a single reader, wallapop-catalogue, and Telegram answers a second one with 409.
package telegram

import (
	"context"
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
	Chat   string
	HTTP   *http.Client
	APIURL string
}

func New(token, chat string) *Bot {
	return &Bot{Token: token, Chat: chat, HTTP: &http.Client{Timeout: 20 * time.Second}, APIURL: api}
}

func (b *Bot) Enabled() bool { return b != nil && b.Token != "" && b.Chat != "" }

// Notify sends an HTML message. Without a token it does nothing, so the page works on its own.
func (b *Bot) Notify(ctx context.Context, text string) error {
	if !b.Enabled() {
		return nil
	}
	form := url.Values{
		"chat_id":                  {b.Chat},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
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
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram sendMessage answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
