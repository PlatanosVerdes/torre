package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

type fakeBoard struct{}

func (fakeBoard) Board(context.Context, string, aena.Direction, bool) ([]aena.Flight, time.Time, error) {
	return []aena.Flight{{ID: "18/10/2026-FR2495", Number: "FR2495", Date: "18/10/2026"}}, time.Now(), nil
}

func (fakeBoard) Airports(context.Context) ([]aena.Airport, error) {
	return []aena.Airport{{IATA: "BCN"}}, nil
}

func handler(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := watch.New(t.TempDir(), fakeBoard{}, nil, time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	return (&Server{Board: fakeBoard{}, Watch: w, Version: "test", Log: log}).Handler()
}

func get(t *testing.T, h http.Handler, path string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPageAndHealth(t *testing.T) {
	h := handler(t)
	if rec := get(t, h, "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Torre</title>") {
		t.Errorf("/ answered %d", rec.Code)
	}
	if rec := get(t, h, "/healthz"); rec.Body.String() != "ok test\n" {
		t.Errorf("/healthz answered %q", rec.Body.String())
	}
}

func TestBoardIsGzippedWhenAsked(t *testing.T) {
	rec := get(t, handler(t), "/api/board?airport=bcn&dir=S", "Accept-Encoding", "gzip")
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

func TestBadAirportIsRejected(t *testing.T) {
	if rec := get(t, handler(t), "/api/board?airport=barcelona"); rec.Code != http.StatusBadRequest {
		t.Errorf("answered %d", rec.Code)
	}
}
