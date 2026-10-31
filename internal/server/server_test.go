package server

import (
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/adsbdb"
	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type fakeBoard struct{}

func today() string { return time.Now().In(time.UTC).Format("02/01/2006") }

func (fakeBoard) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	return []aena.Flight{{
		ID: today() + "-FR2495", Number: "FR2495", Date: today(), Scheduled: "12:20",
		Airport: "BCN", Other: "PMI", Direction: aena.Departures, StatusLabel: "On time",
	}}, time.Now(), nil
}

func (fakeBoard) Airports(context.Context) ([]aena.Airport, error) {
	return []aena.Airport{{IATA: "BCN"}, {IATA: "PMI"}}, nil
}

type quiet struct{}

func (quiet) Send(context.Context, string, string, telegram.Keys) error { return nil }

const token = "123:secret"

func newServer(t *testing.T, owner string) *Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := watch.New(t.TempDir(), fakeBoard{}, quiet{}, time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Board: fakeBoard{}, Watch: w, Bot: telegram.New(token), Version: "test", Log: log, OwnerChat: owner}
}

func do(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func initData(userID int64) string {
	v := url.Values{
		"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)},
		"user":      {`{"id":` + strconv.FormatInt(userID, 10) + `,"first_name":"T"}`},
	}
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	m := hmac.New(sha256.New, []byte("WebAppData"))
	m.Write([]byte(token))
	m2 := hmac.New(sha256.New, m.Sum(nil))
	m2.Write([]byte(strings.Join(lines, "\n")))
	v.Set("hash", hex.EncodeToString(m2.Sum(nil)))
	return v.Encode()
}

func follow() string {
	return `{"airport":"BCN","direction":"S","date":"` + today() + `","number":"FR2495"}`
}

func TestPageHealthAndHeaders(t *testing.T) {
	h := newServer(t, "").Handler()
	rec := do(h, "GET", "/", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Torre</title>") {
		t.Errorf("/ answered %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff")
	}
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("page CORP %q, want same-origin", got)
	}
	if got := do(h, "GET", "/icon.svg", "").Header().Get("Cross-Origin-Resource-Policy"); got != "same-site" {
		t.Errorf("icon CORP %q, want same-site", got)
	}
	if rec := do(h, "GET", "/healthz", ""); rec.Body.String() != "ok test\n" {
		t.Errorf("/healthz answered %q", rec.Body.String())
	}
}

func TestBoardIsGzippedWhenAsked(t *testing.T) {
	rec := do(newServer(t, "").Handler(), "GET", "/api/board?airport=bcn&dir=S", "", "Accept-Encoding", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("not gzipped")
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Airport string        `json:"airport"`
		Flights []aena.Flight `json:"flights"`
	}
	if err := json.NewDecoder(gz).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Airport != "BCN" || len(body.Flights) != 1 {
		t.Errorf("got %+v", body)
	}
}

func TestOnlyAENAAirports(t *testing.T) {
	h := newServer(t, "").Handler()
	for _, a := range []string{"barcelona", "LHR", "XYZ"} {
		if rec := do(h, "GET", "/api/board?airport="+a, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d", a, rec.Code)
		}
	}
}

func TestPublicFollowNeedsTelegram(t *testing.T) {
	h := newServer(t, "").Handler()
	if rec := do(h, "POST", "/api/tracked", follow()); rec.Code != http.StatusUnauthorized {
		t.Errorf("a browser with no identity could follow: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/tracked", follow(), "X-Telegram-Init-Data", "user=%7B%22id%22%3A1%7D&hash=00"); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged init data could follow: %d", rec.Code)
	}
}

func TestMiniAppUsersAreSeparate(t *testing.T) {
	h := newServer(t, "").Handler()
	if rec := do(h, "POST", "/api/tracked", follow(), "X-Telegram-Init-Data", initData(1)); rec.Code != 200 {
		t.Fatalf("follow answered %d: %s", rec.Code, rec.Body)
	}

	var mine, theirs struct {
		CanFollow bool              `json:"canFollow"`
		Tracked   []json.RawMessage `json:"tracked"`
	}
	rec := do(h, "GET", "/api/tracked", "", "X-Telegram-Init-Data", initData(1))
	_ = json.Unmarshal(rec.Body.Bytes(), &mine)
	if strings.Contains(rec.Body.String(), `"chat"`) {
		t.Error("the chat id is sent to the page")
	}
	_ = json.Unmarshal(do(h, "GET", "/api/tracked", "", "X-Telegram-Init-Data", initData(2)).Body.Bytes(), &theirs)
	if len(mine.Tracked) != 1 || len(theirs.Tracked) != 0 || !theirs.CanFollow {
		t.Errorf("user 1 sees %d, user 2 sees %d", len(mine.Tracked), len(theirs.Tracked))
	}
}

func TestPrivateDeploymentFollowsForTheOwner(t *testing.T) {
	h := newServer(t, "99").Handler()
	if rec := do(h, "POST", "/api/tracked", follow()); rec.Code != 200 {
		t.Errorf("owner follow answered %d: %s", rec.Code, rec.Body)
	}
}

func TestRateLimited(t *testing.T) {
	h := newServer(t, "").Handler()
	var last int
	for range 60 {
		last = do(h, "GET", "/api/board?airport=BCN", "").Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("60 requests in a burst ended with %d", last)
	}
}

func TestClientIPHeaderSeparatesVisitors(t *testing.T) {
	s := newServer(t, "")
	s.ClientIPHeader = "CF-Connecting-IP"
	h := s.Handler()
	for range 60 {
		do(h, "GET", "/api/board?airport=BCN", "", "CF-Connecting-IP", "203.0.113.1")
	}
	if rec := do(h, "GET", "/api/board?airport=BCN", "", "CF-Connecting-IP", "203.0.113.2"); rec.Code != 200 {
		t.Errorf("a second visitor behind the same proxy was limited: %d", rec.Code)
	}
}

func TestAssetsAreVersioned(t *testing.T) {
	h := newServer(t, "").Handler()
	rec := do(h, "GET", "/", "")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("index cache-control %q", rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	i := strings.Index(body, `src="/static/`)
	if i < 0 {
		t.Fatalf("index does not load a versioned script:\n%s", body)
	}
	script := body[i+5 : i+5+strings.Index(body[i+5:], `"`)]
	got := do(h, "GET", script, "")
	if got.Code != 200 || !strings.Contains(got.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("%s answered %d, cache %q", script, got.Code, got.Header().Get("Cache-Control"))
	}
	if !strings.Contains(got.Body.String(), "TorreApp") {
		t.Errorf("%s is not main.js", script)
	}
	if rec := do(h, "GET", "/static/000000000000/js/main.js", ""); rec.Code != 404 {
		t.Errorf("another version's URL answered %d", rec.Code)
	}
}

type fakeRoutes map[string]adsbdb.Route

func (f fakeRoutes) Lookup(_ context.Context, callsign string) (adsbdb.Route, bool) {
	r, ok := f[callsign]
	return r, ok
}

func TestLocateFindsTheSpanishEnd(t *testing.T) {
	s := newServer(t, "")
	s.Routes = fakeRoutes{
		"FR8631": {Origin: "BCN", Destination: "TNG"},
		"FR3070": {Origin: "BCN", Destination: "PMI"},
		"BA478":  {Origin: "LHR", Destination: "PMI"},
		"BA1":    {Origin: "LHR", Destination: "JFK"},
	}
	h := s.Handler()
	cases := map[string]string{
		"fr8631":  `{"airport":"BCN","direction":"S"}`,
		"FR 3070": `{"airport":"BCN","direction":"S"}`,
		"BA0478":  `{"airport":"PMI","direction":"L"}`,
	}
	for number, want := range cases {
		rec := do(h, "GET", "/api/locate?number="+url.QueryEscape(number), "")
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != want {
			t.Errorf("%s: %d %s, want %s", number, rec.Code, got, want)
		}
	}
	for _, number := range []string{"BA1", "XX999", ""} {
		if rec := do(h, "GET", "/api/locate?number="+number, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%q: %d, want 404", number, rec.Code)
		}
	}
}
