package adsbdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const vy3919 = `{"response":{"flightroute":{"callsign":"VY3919","airline":{"name":"Vueling Airlines","iata":"VY"},
	"origin":{"iata_code":"PMI"},"destination":{"iata_code":"BCN"}}}}`

func TestLookupReadsTheRouteAndCachesIt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v0/callsign/VY3919" || !strings.Contains(r.UserAgent(), "github.com/PlatanosVerdes/torre") {
			t.Errorf("asked %s as %q", r.URL.Path, r.UserAgent())
		}
		_, _ = io.WriteString(w, vy3919)
	}))
	defer srv.Close()
	c := New(srv.URL)
	for range 2 {
		r, ok := c.Lookup(context.Background(), "vy3919")
		if !ok || r != (Route{"Vueling Airlines", "PMI", "BCN"}) {
			t.Fatalf("got %+v %v", r, ok)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("asked adsbdb %d times, want once", calls.Load())
	}
}

func TestUnknownIsCachedBriefly(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"response":"unknown callsign"}`)
	}))
	defer srv.Close()
	c := New(srv.URL)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Lookup(context.Background(), "XX9999")
	if _, ok := c.Lookup(context.Background(), "XX9999"); ok || calls.Load() != 1 {
		t.Fatalf("ok %v after %d calls", ok, calls.Load())
	}
	now = now.Add(unknownTTL + time.Second)
	c.Lookup(context.Background(), "XX9999")
	if calls.Load() != 2 {
		t.Errorf("an old unknown was not asked again")
	}
}

func TestTooManyRequestsIsUnknownAndBacksOff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New(srv.URL)
	if _, ok := c.Lookup(context.Background(), "VY3919"); ok {
		t.Fatal("a 429 read as a route")
	}
	if _, ok := c.Lookup(context.Background(), "FR2495"); ok || calls.Load() != 1 {
		t.Errorf("asked again %d times while blocked", calls.Load()-1)
	}
}
