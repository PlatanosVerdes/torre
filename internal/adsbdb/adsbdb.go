// Package adsbdb asks api.adsbdb.com which route a flight number flies, so the bot knows which
// AENA board to look at when someone types only the number.
package adsbdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "https://api.adsbdb.com"

const userAgent = "torre (https://github.com/PlatanosVerdes/torre)"

const (
	knownTTL   = 6 * time.Hour
	unknownTTL = 15 * time.Minute
	// adsbdb answers 429 past 512 requests a minute and then blocks the address for up to 5 minutes.
	blockedFor = 5 * time.Minute
	maxEntries = 5000
)

type Route struct {
	Airline     string
	Origin      string
	Destination string
}

// SpanishEnd is the end of the route on an AENA board: the origin's departures, else the
// destination's arrivals.
func (r Route) SpanishEnd(spanish func(string) bool) (airport string, departs, ok bool) {
	switch {
	case spanish(r.Origin):
		return r.Origin, true, true
	case spanish(r.Destination):
		return r.Destination, false, true
	}
	return "", false, false
}

type Client struct {
	BaseURL string
	HTTP    *http.Client

	now     func() time.Time
	mu      sync.Mutex
	blocked time.Time
	// adsbdb's licence forbids copying its routes into another database: keep this in memory only, never persist it.
	cache map[string]entry
}

type entry struct {
	route Route
	ok    bool
	until time.Time
}

func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 10 * time.Second}, now: time.Now, cache: map[string]entry{}}
}

// Lookup returns the route of an IATA or ICAO callsign. Any failure, a 429 included, is "unknown".
func (c *Client) Lookup(ctx context.Context, callsign string) (Route, bool) {
	callsign = strings.ToUpper(callsign)
	now := c.now()
	c.mu.Lock()
	if e, hit := c.cache[callsign]; hit && now.Before(e.until) {
		c.mu.Unlock()
		return e.route, e.ok
	}
	if now.Before(c.blocked) {
		c.mu.Unlock()
		return Route{}, false
	}
	c.mu.Unlock()

	r, ok, status := c.fetch(ctx, callsign)

	c.mu.Lock()
	defer c.mu.Unlock()
	if status == http.StatusTooManyRequests {
		c.blocked = now.Add(blockedFor)
		return Route{}, false
	}
	if len(c.cache) >= maxEntries {
		for k, e := range c.cache {
			if !now.Before(e.until) {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxEntries {
			c.cache = map[string]entry{}
		}
	}
	ttl := unknownTTL
	if ok {
		ttl = knownTTL
	}
	c.cache[callsign] = entry{route: r, ok: ok, until: now.Add(ttl)}
	return r, ok
}

func (c *Client) fetch(ctx context.Context, callsign string) (Route, bool, int) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v0/callsign/"+callsign, nil)
	if err != nil {
		return Route{}, false, 0
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Route{}, false, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Route{}, false, resp.StatusCode
	}
	var body struct {
		Response struct {
			FlightRoute *struct {
				Airline struct {
					Name string `json:"name"`
				} `json:"airline"`
				Origin struct {
					IATA string `json:"iata_code"`
				} `json:"origin"`
				Destination struct {
					IATA string `json:"iata_code"`
				} `json:"destination"`
			} `json:"flightroute"`
		} `json:"response"`
	}
	// An unknown callsign comes back as {"response":"unknown callsign"}, which fails here too.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil || body.Response.FlightRoute == nil {
		return Route{}, false, resp.StatusCode
	}
	fr := body.Response.FlightRoute
	r := Route{Airline: fr.Airline.Name, Origin: strings.ToUpper(fr.Origin.IATA), Destination: strings.ToUpper(fr.Destination.IATA)}
	return r, r.Origin != "" && r.Destination != "", resp.StatusCode
}
