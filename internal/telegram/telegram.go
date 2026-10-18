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
	"strconv"
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

// pollSeconds is how long Telegram holds an empty getUpdates open.
const pollSeconds = 30

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	Text     string    `json:"text"`
	Chat     Chat      `json:"chat"`
	Photo    []any     `json:"photo"`
	Document *struct{} `json:"document"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Updates long-polls for new messages. Only a bot with a single reader may call it: Telegram
// answers a second reader of the same token with 409.
func (b *Bot) Updates(ctx context.Context, offset int64) ([]Update, error) {
	var out []Update
	err := b.call(ctx, "getUpdates", url.Values{
		"offset":          {strconv.FormatInt(offset, 10)},
		"timeout":         {strconv.Itoa(pollSeconds)},
		"allowed_updates": {`["message"]`},
	}, &out, (pollSeconds+10)*time.Second)
	return out, err
}

// Setup gives every private chat a menu button that opens the page, and the command list.
func (b *Bot) Setup(ctx context.Context, open string, commands map[string]string) error {
	button, _ := json.Marshal(map[string]any{"type": "web_app", "text": "Open", "web_app": map[string]string{"url": open}})
	if err := b.call(ctx, "setChatMenuButton", url.Values{"menu_button": {string(button)}}, nil, 20*time.Second); err != nil {
		return err
	}
	var list []map[string]string
	for _, name := range []string{"start", "flights", "help"} {
		if d, ok := commands[name]; ok {
			list = append(list, map[string]string{"command": name, "description": d})
		}
	}
	raw, _ := json.Marshal(list)
	return b.call(ctx, "setMyCommands", url.Values{"commands": {string(raw)}}, nil, 20*time.Second)
}

func (b *Bot) call(ctx context.Context, method string, form url.Values, result any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.APIURL+b.Token+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *b.HTTP
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	var body struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return fmt.Errorf("telegram %s answered %d", method, resp.StatusCode)
	}
	if !body.OK {
		return fmt.Errorf("telegram %s: %s", method, body.Description)
	}
	if result != nil {
		return json.Unmarshal(body.Result, result)
	}
	return nil
}
