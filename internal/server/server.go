// Package server serves the board page and the small JSON API it reads.
package server

import (
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
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
	Version string
	Log     *slog.Logger
	// Notify reports whether following a flight will reach Telegram.
	Notify bool
}

func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(web, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok "+s.Version+"\n")
	})
	mux.HandleFunc("GET /api/airports", s.airports)
	mux.HandleFunc("GET /api/board", s.board)
	mux.HandleFunc("GET /api/tracked", s.listTracked)
	mux.HandleFunc("POST /api/tracked", s.addTracked)
	mux.HandleFunc("DELETE /api/tracked/{id}", s.removeTracked)
	return mux
}

func (s *Server) airports(w http.ResponseWriter, r *http.Request) {
	list, err := s.Board.Airports(r.Context())
	if err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	s.json(w, r, list)
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	airport := strings.ToUpper(r.URL.Query().Get("airport"))
	if len(airport) != 3 {
		s.fail(w, http.StatusBadRequest, errors.New("airport must be an IATA code like BCN"))
		return
	}
	dir, ok := aena.ParseDirection(r.URL.Query().Get("dir"))
	if !ok {
		dir = aena.Departures
	}
	short := r.URL.Query().Get("range") != "full"

	flights, at, err := s.Board.Board(r.Context(), airport, dir, short)
	if err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.json(w, r, map[string]any{
		"airport":   airport,
		"direction": dir,
		"updated":   at.Unix(),
		"flights":   flights,
	})
}

func (s *Server) listTracked(w http.ResponseWriter, r *http.Request) {
	s.json(w, r, map[string]any{"notify": s.Notify, "tracked": s.Watch.List()})
}

func (s *Server) addTracked(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Airport   string `json:"airport"`
		Direction string `json:"direction"`
		Date      string `json:"date"`
		Number    string `json:"number"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	dir, ok := aena.ParseDirection(in.Direction)
	if !ok || len(in.Airport) != 3 || in.Number == "" {
		s.fail(w, http.StatusBadRequest, errors.New("airport, direction, date and number are required"))
		return
	}
	if _, err := time.Parse("02/01/2006", in.Date); err != nil {
		s.fail(w, http.StatusBadRequest, errors.New("date must be dd/mm/yyyy"))
		return
	}
	t, err := s.Watch.Add(r.Context(), in.Airport, dir, in.Date, in.Number)
	if err != nil {
		s.fail(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.json(w, r, t)
}

func (s *Server) removeTracked(w http.ResponseWriter, r *http.Request) {
	if err := s.Watch.Remove(r.PathValue("id")); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// json gzips when it can: a busy airport's board is ~600 KB of JSON and ~50 KB compressed.
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

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	if code >= 500 {
		s.Log.Warn("request failed", "code", code, "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
