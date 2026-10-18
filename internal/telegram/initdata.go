package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// ErrInitData covers every way a Mini App's launch data can be wrong; the caller only needs to refuse.
var ErrInitData = errors.New("telegram init data is missing, forged or expired")

// User checks the initData a Mini App was opened with and returns who opened it. The signature
// is an HMAC keyed by the bot token, so only Telegram could have produced it for this bot.
// See https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func (b *Bot) User(initData string, maxAge time.Duration, now time.Time) (User, error) {
	if b == nil || b.Token == "" || initData == "" {
		return User{}, ErrInitData
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return User{}, ErrInitData
	}
	got, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(got) == 0 {
		return User{}, ErrInitData
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}

	secret := mac([]byte("WebAppData"), []byte(b.Token))
	if !hmac.Equal(mac(secret, []byte(strings.Join(lines, "\n"))), got) {
		return User{}, ErrInitData
	}

	// A valid signature never expires on its own, so a leaked link would work forever without this.
	issued, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(issued, 0)) > maxAge {
		return User{}, ErrInitData
	}

	var u User
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil || u.ID == 0 {
		return User{}, ErrInitData
	}
	return u, nil
}

func mac(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
