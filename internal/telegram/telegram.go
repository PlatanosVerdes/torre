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

// Keys are the buttons under a message: Open opens that URL as a Mini App (private chats only),
// Stop is the callback data of a "Stop following" button.
type Keys struct {
	Open string
	Stop string
}

func (k Keys) markup() string {
	var row []map[string]any
	if k.Open != "" {
		row = append(row, map[string]any{"text": "Open in Torre", "web_app": map[string]string{"url": k.Open}})
	}
	if k.Stop != "" {
		row = append(row, map[string]any{"text": "Stop following", "callback_data": k.Stop})
	}
	if row == nil {
		return ""
	}
	raw, _ := json.Marshal(map[string]any{"inline_keyboard": [][]map[string]any{row}})
	return string(raw)
}

// Send posts an HTML message to chat, with keys under it.
func (b *Bot) Send(ctx context.Context, chat, text string, keys Keys) error {
	if !b.Enabled() || chat == "" {
		return nil
	}
	form := url.Values{
		"chat_id":                  {chat},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}
	if m := keys.markup(); m != "" {
		form.Set("reply_markup", m)
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
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

// Callback is a tap on an inline button: its data, and the message the button was under.
type Callback struct {
	ID      string  `json:"id"`
	Data    string  `json:"data"`
	Message Message `json:"message"`
}

type Message struct {
	ID       int64     `json:"message_id"`
	Markup   *Markup   `json:"reply_markup"`
	Text     string    `json:"text"`
	Chat     Chat      `json:"chat"`
	Photo    []any     `json:"photo"`
	Document *struct{} `json:"document"`
}

type Markup struct {
	Keyboard [][]struct {
		WebApp *struct {
			URL string `json:"url"`
		} `json:"web_app"`
	} `json:"inline_keyboard"`
}

// OpenURL is the Mini App address of the message's "Open" button, if it has one.
func (m Message) OpenURL() string {
	if m.Markup == nil {
		return ""
	}
	for _, row := range m.Markup.Keyboard {
		for _, k := range row {
			if k.WebApp != nil {
				return k.WebApp.URL
			}
		}
	}
	return ""
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
		"allowed_updates": {`["message","callback_query"]`},
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

// Answer acknowledges a button tap with a short notice, which also stops its spinner.
func (b *Bot) Answer(ctx context.Context, callbackID, text string) error {
	return b.call(ctx, "answerCallbackQuery", url.Values{"callback_query_id": {callbackID}, "text": {text}}, nil, 20*time.Second)
}

// SetKeys replaces the buttons under a message already sent.
func (b *Bot) SetKeys(ctx context.Context, chat string, messageID int64, keys Keys) error {
	markup := keys.markup()
	if markup == "" {
		markup = `{"inline_keyboard":[]}`
	}
	return b.call(ctx, "editMessageReplyMarkup", url.Values{
		"chat_id":      {chat},
		"message_id":   {strconv.FormatInt(messageID, 10)},
		"reply_markup": {markup},
	}, nil, 20*time.Second)
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
