package telegram

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNothingIsSentWithoutAToken(t *testing.T) {
	if err := New("").Send(context.Background(), "1", "hola", ""); err != nil {
		t.Errorf("an unconfigured bot returned an error instead of staying quiet: %v", err)
	}
}

func TestErrorsDoNotCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	bot := New("secret-token")
	bot.APIURL = srv.URL + "/bot"
	err := bot.Send(context.Background(), "1", "hola", "")
	srv.Close()
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("answered error: %v", err)
	}

	err = bot.Send(context.Background(), "1", "hola", "")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("transport error: %v", err)
	}
}

func TestForbiddenMeansNotStarted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false,"description":"Forbidden: bot can't initiate conversation with a user"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	bot := New("t")
	bot.APIURL = srv.URL + "/bot"
	if err := bot.Send(context.Background(), "1", "hola", ""); !errors.Is(err, ErrBlocked) {
		t.Errorf("got %v, want ErrBlocked", err)
	}
}

func TestOpenButtonIsAMiniApp(t *testing.T) {
	var markup string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		markup = r.Form.Get("reply_markup")
	}))
	defer srv.Close()
	bot := New("t")
	bot.APIURL = srv.URL + "/bot"
	_ = bot.Send(context.Background(), "1", "hola", "https://torre.example/?f=FR1")
	if !strings.Contains(markup, `"web_app":{"url":"https://torre.example/?f=FR1"}`) {
		t.Errorf("markup %s", markup)
	}
}

// sign builds initData the way Telegram does, so the check is tested against the published algorithm.
func sign(token string, values url.Values) string {
	var lines []string
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}
	secret := mac([]byte("WebAppData"), []byte(token))
	values.Set("hash", hex.EncodeToString(mac(secret, []byte(strings.Join(lines, "\n")))))
	return values.Encode()
}

func TestInitData(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	bot := New("123:abc")
	fresh := func() url.Values {
		return url.Values{
			"auth_date": {strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)},
			"query_id":  {"AAH"},
			"user":      {`{"id":42,"first_name":"Ana","username":"ana"}`},
		}
	}

	u, err := bot.User(sign("123:abc", fresh()), 24*time.Hour, now)
	if err != nil || u.ID != 42 {
		t.Fatalf("valid data refused: %v %+v", err, u)
	}

	if _, err := bot.User(sign("other:token", fresh()), 24*time.Hour, now); err == nil {
		t.Error("data signed for another bot was accepted")
	}

	tampered, _ := url.ParseQuery(sign("123:abc", fresh()))
	tampered.Set("user", `{"id":7}`)
	if _, err := bot.User(tampered.Encode(), 24*time.Hour, now); err == nil {
		t.Error("a changed user id was accepted")
	}

	old := fresh()
	old.Set("auth_date", strconv.FormatInt(now.Add(-48*time.Hour).Unix(), 10))
	if _, err := bot.User(sign("123:abc", old), 24*time.Hour, now); err == nil {
		t.Error("two-day-old data was accepted")
	}

	if _, err := bot.User("", 24*time.Hour, now); err == nil {
		t.Error("empty data was accepted")
	}
}
