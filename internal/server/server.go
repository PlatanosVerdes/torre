// Package server serves the board page and the small JSON API it reads.
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/limit"
	"github.com/PlatanosVerdes/torre/internal/places"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

//go:embed web
var web embed.FS

type Board interface {
	Board(ctx context.Context, airport string, dir aena.Direction, short bool) ([]aena.Flight, time.Time, error)
	Airports(ctx context.Context) ([]aena.Airport, error)
}

type Server struct {
	Board   Board
	Watch   *watch.Watcher
	Bot     *telegram.Bot
	Version string
	Log     *slog.Logger

	// OwnerChat receives what is followed from a browser outside Telegram. It must stay empty once
	// the page is public, or anyone could fill the owner's chat.
	OwnerChat string
	// ClientIPHeader names the header the proxy in front writes the visitor's address into
	// (CF-Connecting-IP behind Cloudflare). Empty means the TCP peer is the client.
	ClientIPHeader string
	// BotName is shown when a browser visitor needs to open the bot to follow a flight.
	BotName string

	api, full, follow *limit.Limiter
}

// initDataAge is how long a Mini App launch stays valid. Telegram re-signs on every open.
const initDataAge = 24 * time.Hour

func (s *Server) Handler() http.Handler {
	s.api = limit.New(120, 40)
	s.full = limit.New(6, 2)
	s.follow = limit.New(20, 5)

	static, _ := fs.Sub(web, "web")
	files := http.FileServerFS(static)
	version, index := page(static)
	mux := http.NewServeMux()
	mux.Handle("GET /", s.cacheStatic(files))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		// Never cached, so it always names the current /static/<version>/ below.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
	mux.HandleFunc("GET /static/{version}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		// Another version's URL gets a 404 rather than this version's file, which a cache would
		// then keep under the wrong name for a year.
		if r.PathValue("version") != version {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + r.PathValue("path")
		files.ServeHTTP(w, r2)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok "+s.Version+"\n")
	})
	mux.HandleFunc("GET /api/airports", s.limited(s.api, s.airports))
	mux.HandleFunc("GET /api/board", s.limited(s.api, s.board))
	mux.HandleFunc("GET /api/places", s.limited(s.api, s.places))
	mux.HandleFunc("GET /api/tracked", s.limited(s.api, s.listTracked))
	mux.HandleFunc("POST /api/tracked", s.limited(s.follow, s.addTracked))
	mux.HandleFunc("DELETE /api/tracked/{id}", s.limited(s.follow, s.removeTracked))
	return secure(mux)
}

// secure sets the headers a public page is expected to send. The page loads nothing from other
// origins; Telegram's web client embeds it in a frame, hence the one frame-ancestors entry.
func secure(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval'",
		"style-src 'self'",
		"img-src 'self' data: blob:",
		"connect-src 'self'",
		"worker-src 'self' blob:",
		"frame-ancestors 'self' https://web.telegram.org",
		"base-uri 'none'",
		"form-action 'none'",
		"object-src 'none'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// Location for the page itself only: it picks the nearest airport on the phone and never sends it.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self), payment=(), usb=()")
		corp := "same-origin"
		if r.URL.Path == "/icon.svg" {
			// The home server's dashboards on other platanosverdes.com hosts show it as torre's icon.
			corp = "same-site"
		}
		h.Set("Cross-Origin-Resource-Policy", corp)
		next.ServeHTTP(w, r)
	})
}

// page returns the assets' version, a hash of every embedded file, and index.html pointing at
// /static/<version>/. Telegram's in-app browser keeps scripts it has seen even when told to
// revalidate, and an ES module's imports are cached by URL, so a release that kept the URLs could
// run a new page on old components. A URL per version cannot be stale.
func page(static fs.FS) (string, []byte) {
	h := sha256.New()
	_ = fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(raw)
		return nil
	})
	version := hex.EncodeToString(h.Sum(nil))[:12]
	index, _ := fs.ReadFile(static, "index.html")
	index = bytes.ReplaceAll(index, []byte(`href="app.css"`), []byte(`href="/static/`+version+`/app.css"`))
	index = bytes.ReplaceAll(index, []byte(`src="js/main.js"`), []byte(`src="/static/`+version+`/js/main.js"`))
	return version, index
}

func (s *Server) cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The scanner is ~1 MB and versioned by its path, so phones keep it; the rest is tiny.
		if strings.HasPrefix(r.URL.Path, "/vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limited(l *limit.Limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "30")
			s.fail(w, http.StatusTooManyRequests, errors.New("too many requests, try again in a moment"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		h(w, r)
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.ClientIPHeader != "" {
		if v := strings.TrimSpace(strings.Split(r.Header.Get(s.ClientIPHeader), ",")[0]); v != "" {
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// chat is who is asking: the Telegram user who opened the Mini App, else the owner on a private
// deployment, else nobody.
func (s *Server) chat(r *http.Request) (string, error) {
	if raw := r.Header.Get("X-Telegram-Init-Data"); raw != "" {
		u, err := s.Bot.User(raw, initDataAge, time.Now())
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(u.ID, 10), nil
	}
	if s.OwnerChat != "" {
		return s.OwnerChat, nil
	}
	return "", errors.New("open Torre from Telegram to follow flights")
}

func (s *Server) airports(w http.ResponseWriter, r *http.Request) {
	list, err := s.Board.Airports(r.Context())
	if err != nil {
		s.upstream(w, err)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	s.json(w, r, list)
}

// places is every airport in the world with scheduled flights, for picking the far end of a route.
func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "max-age=86400")
	s.json(w, r, places.All())
}

// known refuses codes AENA does not run, so a visitor cannot make the Pi ask AENA for 17,000
// made-up boards. If the list itself is unavailable, the board request says so on its own.
func (s *Server) known(ctx context.Context, airport string) bool {
	list, err := s.Board.Airports(ctx)
	if err != nil {
		return true
	}
	for _, a := range list {
		if a.IATA == airport {
			return true
		}
	}
	return false
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	airport := strings.ToUpper(r.URL.Query().Get("airport"))
	if len(airport) != 3 || !s.known(r.Context(), airport) {
		s.fail(w, http.StatusBadRequest, errors.New("not an AENA airport"))
		return
	}
	dir, ok := aena.ParseDirection(r.URL.Query().Get("dir"))
	if !ok {
		dir = aena.Departures
	}
	short := r.URL.Query().Get("range") != "full"
	if !short && !s.full.Allow(s.clientIP(r)) {
		s.fail(w, http.StatusTooManyRequests, errors.New("too many two-week boards, try again in a moment"))
		return
	}

	flights, at, err := s.Board.Board(r.Context(), airport, dir, short)
	if err != nil {
		s.upstream(w, err)
		return
	}
	s.json(w, r, map[string]any{
		"airport":   airport,
		"direction": dir,
		"updated":   at.Unix(),
		"flights":   flights,
	})
}

// view is what a chat sees of its own followed flights; the chat id stays on the server.
type view struct {
	ID        string         `json:"id"`
	Airport   string         `json:"airport"`
	Direction aena.Direction `json:"direction"`
	Date      string         `json:"date"`
	Number    string         `json:"number"`
	Last      *aena.Flight   `json:"last,omitempty"`
}

func (s *Server) listTracked(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"canFollow": false, "notify": s.Bot.Enabled(), "bot": s.BotName, "tracked": []view{}}
	chat, err := s.chat(r)
	if err == nil {
		list := []view{}
		for _, t := range s.Watch.List(chat) {
			list = append(list, view{t.ID, t.Airport, t.Direction, t.Date, t.Number, t.Last})
		}
		out["canFollow"], out["tracked"] = true, list
	}
	s.json(w, r, out)
}

func (s *Server) addTracked(w http.ResponseWriter, r *http.Request) {
	chat, err := s.chat(r)
	if err != nil {
		s.fail(w, http.StatusUnauthorized, err)
		return
	}
	var in struct {
		Airport   string `json:"airport"`
		Direction string `json:"direction"`
		Date      string `json:"date"`
		Number    string `json:"number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	dir, ok := aena.ParseDirection(in.Direction)
	in.Airport = strings.ToUpper(in.Airport)
	if !ok || len(in.Airport) != 3 || in.Number == "" || len(in.Number) > 10 {
		s.fail(w, http.StatusBadRequest, errors.New("airport, direction, date and number are required"))
		return
	}
	if !s.known(r.Context(), in.Airport) {
		s.fail(w, http.StatusBadRequest, errors.New("not an AENA airport"))
		return
	}
	t, err := s.Watch.Add(r.Context(), chat, in.Airport, dir, in.Date, in.Number)
	switch {
	case errors.Is(err, telegram.ErrBlocked):
		s.fail(w, http.StatusConflict, errors.New("open the bot and press Start first, so it can message you"))
	case errors.Is(err, watch.ErrNotFound):
		s.fail(w, http.StatusNotFound, err)
	case errors.Is(err, watch.ErrTooMany):
		s.fail(w, http.StatusTooManyRequests, err)
	case err != nil:
		s.fail(w, http.StatusUnprocessableEntity, err)
	default:
		s.json(w, r, view{t.ID, t.Airport, t.Direction, t.Date, t.Number, t.Last})
	}
}

func (s *Server) removeTracked(w http.ResponseWriter, r *http.Request) {
	chat, err := s.chat(r)
	if err != nil {
		s.fail(w, http.StatusUnauthorized, err)
		return
	}
	if err := s.Watch.Remove(chat, r.PathValue("id")); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, r, map[string]bool{"ok": true})
}

// json gzips when it can: a busy airport's board is ~600 KB of JSON and ~40 KB compressed.
func (s *Server) json(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json")
	var out io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		out = gz
	}
	if err := json.NewEncoder(out).Encode(v); err != nil {
		s.Log.Warn("write failed", "err", err)
	}
}

// upstream reports an AENA failure without passing on what AENA answered, which is for the log.
func (s *Server) upstream(w http.ResponseWriter, err error) {
	s.Log.Warn("aena failed", "err", err)
	s.fail(w, http.StatusBadGateway, errors.New("AENA is not answering right now, try again in a minute"))
}

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	if code >= 500 {
		s.Log.Warn("request failed", "code", code, "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
