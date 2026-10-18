// Package aena reads the flight board behind aena.es/es/infovuelos.html. It is not a published
// API: the endpoint, its fields and its status codes were read from the page's own bundle.
package aena

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/places"
)

const DefaultBaseURL = "https://www.aena.es"

type Direction string

const (
	Departures Direction = "S"
	Arrivals   Direction = "L"
)

func ParseDirection(s string) (Direction, bool) {
	switch strings.ToUpper(s) {
	case "S", "SALIDAS", "DEPARTURES":
		return Departures, true
	case "L", "LLEGADAS", "ARRIVALS":
		return Arrivals, true
	}
	return "", false
}

// raw is one row as AENA sends it. Missing values arrive as the string "null".
type raw struct {
	Number        string `json:"numVuelo"`
	Airport       string `json:"iataAena"`
	Date          string `json:"fecha"`
	Scheduled     string `json:"horaProgramada"`
	EstimatedDate string `json:"fechaEstimada"`
	Estimated     string `json:"horaEstimada"`
	Other         string `json:"iataOtro"`
	OtherCity     string `json:"ciudadIataOtro"`
	Status        string `json:"estado"`
	Codes         string `json:"codigosCompania"`
	Terminal      string `json:"terminal"`
	Aircraft      string `json:"tipoAeronave"`
	Gate          string `json:"puertaPrimera"`
	Gate2         string `json:"puertaSegunda"`
	CounterFrom   string `json:"mostradorDesde"`
	CounterTo     string `json:"mostradorHasta"`
	Belt          string `json:"cintaPrimera"`
	Belt2         string `json:"cintaSegunda"`
	Hall          string `json:"salaPrimera"`
	AirlineIATA   string `json:"iataCompania"`
	AirlineICAO   string `json:"oaciCompania"`
	AirlineName   string `json:"nombreCompania"`
}

type Flight struct {
	ID        string    `json:"id"`
	Number    string    `json:"number"`
	Codeshare []string  `json:"codeshare,omitempty"`
	Airline   string    `json:"airline"`
	Direction Direction `json:"direction"`
	Airport   string    `json:"airport"`
	Other     string    `json:"other"`
	// OtherCity is the far end in English when the code is known ("London Gatwick"); OtherLocal is
	// what AENA calls it ("LONDRES /GATWICK"), kept so a Spanish search still finds it.
	OtherCity  string `json:"otherCity"`
	OtherLocal string `json:"otherLocal,omitempty"`
	Date       string `json:"date"`
	Scheduled  string `json:"scheduled"`
	Estimated  string `json:"estimated,omitempty"`
	// EstimatedDate differs from Date when the delay crosses midnight.
	EstimatedDate string `json:"estimatedDate,omitempty"`
	DelayMin      int    `json:"delayMin"`
	Status        string `json:"status"`
	StatusLabel   string `json:"statusLabel"`
	Final         bool   `json:"final"`
	// Doubtful marks a status AENA gave that its own times contradict.
	Doubtful bool `json:"doubtful,omitempty"`
	Terminal      string `json:"terminal,omitempty"`
	Gate          string `json:"gate,omitempty"`
	Counters      string `json:"counters,omitempty"`
	Belt          string `json:"belt,omitempty"`
	Aircraft      string `json:"aircraft,omitempty"`
	// Sort is the scheduled time as Unix seconds in the airport's zone, for ordering only.
	Sort int64 `json:"sort"`
}

type Airport struct {
	IATA string `json:"iata"`
	Name string `json:"name"`
	City string `json:"city"`
}

// statuses maps AENA's codes to what the board shows. Departures and arrivals share some codes
// (BOR is "finalizado" on both boards: gone from the gate, or passengers off the plane).
var statuses = map[string]string{
	"SCH": "Scheduled",
	"INI": "On time",
	"HOR": "On time",
	"RET": "Delayed",
	"EMB": "Boarding",
	"ULL": "Final call",
	"CER": "Gate closed",
	"BOR": "Departed",
	"NPR": "New gate, delayed",
	"NPT": "New gate",
	"CAN": "Cancelled",
	"DES": "Diverted",
	"DIV": "Diverted",
	"FLY": "In flight",
	"FNL": "Landing",
	"LND": "Landed",
	"IBK": "Baggage on belt",
}

var finals = map[string]bool{"BOR": true, "CAN": true, "DES": true, "DIV": true, "IBK": true}

func StatusLabel(code string) string {
	if l, ok := statuses[code]; ok {
		return l
	}
	return code
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Zone    *time.Location
	TTL     time.Duration

	mu       sync.Mutex
	boards   map[string]*entry
	airports []Airport
	airAt    time.Time
	// fullSlot lets one two-week board decode at a time: each one is 18 MB of JSON for BCN and
	// briefly costs ~50 MiB, so a burst of them is the one thing that could exhaust the container.
	fullSlot chan struct{}
}

// How many boards stay cached. A short board is ~0.5 MB once normalized, a full one ~3 MB.
const (
	maxShort = 40
	maxFull  = 4
	// A failed fetch is remembered this long, so a down AENA is not asked again on every request.
	failTTL = 10 * time.Second
)

type entry struct {
	short   bool
	at      time.Time
	flights []Flight
	err     error
	ready   chan struct{}
}

func New(baseURL string, ttl time.Duration) *Client {
	zone, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		zone = time.UTC
	}
	return &Client{
		BaseURL:  baseURL,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		Zone:     zone,
		TTL:      ttl,
		boards:   map[string]*entry{},
		fullSlot: make(chan struct{}, 1),
	}
}

// Board returns an airport's departures or arrivals. Short is AENA's two-day window (about 3 MB
// for BCN); the full one covers two weeks, is six times larger and takes AENA ~16s, so it is
// only asked for when a later date is needed. Concurrent callers for the same board share one request.
func (c *Client) Board(ctx context.Context, airport string, dir Direction, short bool) ([]Flight, time.Time, error) {
	airport = strings.ToUpper(airport)
	key := fmt.Sprintf("%s/%s/%t", airport, dir, short)

	c.mu.Lock()
	e := c.boards[key]
	if e != nil {
		select {
		case <-e.ready:
			if e.err == nil && time.Since(e.at) < c.TTL {
				c.mu.Unlock()
				return e.flights, e.at, nil
			}
			if e.err != nil && time.Since(e.at) < failTTL {
				c.mu.Unlock()
				return nil, e.at, e.err
			}
		default:
			c.mu.Unlock()
			return wait(ctx, e)
		}
	}
	e = &entry{ready: make(chan struct{}), short: short}
	c.boards[key] = e
	c.mu.Unlock()

	// Detached from the caller: an impatient phone must not cancel what others are waiting on.
	fetchCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	if !short {
		c.fullSlot <- struct{}{}
	}
	e.flights, e.err = c.fetch(fetchCtx, airport, dir, short)
	if !short {
		<-c.fullSlot
	}
	cancel()
	c.mu.Lock()
	e.at = time.Now()
	close(e.ready)
	c.evictLocked()
	c.mu.Unlock()
	if e.err != nil {
		return nil, e.at, e.err
	}
	return e.flights, e.at, nil
}

// evictLocked drops stale boards and, past the caps, the oldest ones of each size.
func (c *Client) evictLocked() {
	var short, full []string
	for k, e := range c.boards {
		select {
		case <-e.ready:
		default:
			continue
		}
		if time.Since(e.at) > 10*c.TTL {
			delete(c.boards, k)
			continue
		}
		if e.short {
			short = append(short, k)
		} else {
			full = append(full, k)
		}
	}
	for _, group := range []struct {
		keys []string
		max  int
	}{{short, maxShort}, {full, maxFull}} {
		if len(group.keys) <= group.max {
			continue
		}
		sort.Slice(group.keys, func(i, j int) bool { return c.boards[group.keys[i]].at.Before(c.boards[group.keys[j]].at) })
		for _, k := range group.keys[:len(group.keys)-group.max] {
			delete(c.boards, k)
		}
	}
}

func wait(ctx context.Context, e *entry) ([]Flight, time.Time, error) {
	select {
	case <-e.ready:
		return e.flights, e.at, e.err
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
}

func (c *Client) fetch(ctx context.Context, airport string, dir Direction, short bool) ([]Flight, error) {
	q := url.Values{
		"pagename":   {"AENA_ConsultarVuelos"},
		"airport":    {airport},
		"flightType": {string(dir)},
	}
	if short {
		q.Set("dosDias", "si")
	}
	var rows []raw
	if err := c.getJSON(ctx, "/sites/Satellite?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	return Normalize(rows, dir, c.Zone, time.Now()), nil
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", c.BaseURL+"/es/infovuelos.html")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("aena answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// An HTML error page with a 200 is how a changed endpoint shows up.
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		return fmt.Errorf("aena answered %q instead of JSON; the endpoint may have changed", ct)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Airports is the list of AENA airports, which changes about never.
func (c *Client) Airports(ctx context.Context) ([]Airport, error) {
	c.mu.Lock()
	if c.airports != nil && time.Since(c.airAt) < 24*time.Hour {
		list := c.airports
		c.mu.Unlock()
		return list, nil
	}
	c.mu.Unlock()

	var rows []struct {
		Name string `json:"nombre"`
		IATA string `json:"iata"`
		City string `json:"ciudad"`
	}
	if err := c.getJSON(ctx, "/sites/Satellite?pagename=AENA_InfoAeropuertos&l=es_ES", &rows); err != nil {
		return nil, err
	}
	list := make([]Airport, 0, len(rows))
	for _, r := range rows {
		// Heliports carry made-up codes like RJ2 and never have a board.
		if len(r.IATA) != 3 || strings.ContainsAny(r.IATA, "0123456789") {
			continue
		}
		list = append(list, Airport{IATA: r.IATA, Name: r.Name, City: r.City})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	c.mu.Lock()
	c.airports, c.airAt = list, time.Now()
	c.mu.Unlock()
	return list, nil
}

// Normalize turns rows into flights, folding codeshares into the operating flight: AENA lists
// one physical flight once per marketing carrier (VY3906, IB5126, QR3619 and LL5395 are one plane).
func Normalize(rows []raw, dir Direction, zone *time.Location, now time.Time) []Flight {
	type group struct {
		flight   Flight
		operated bool
	}
	groups := map[string]*group{}
	var order []string

	for _, r := range rows {
		f := toFlight(r, dir, zone, now)
		key := strings.Join([]string{f.Date, f.Scheduled, f.Other, f.Terminal, f.Aircraft, f.Gate, f.Counters, f.Belt}, "|")
		// Iberia publishes a second row with an S suffix for the same flight.
		if strings.HasSuffix(r.Number, "S") {
			key += "|" + strings.TrimSuffix(f.Number, "S")
		}
		operated := operates(r.Codes)

		g, ok := groups[key]
		if !ok {
			groups[key] = &group{flight: f, operated: operated}
			order = append(order, key)
			continue
		}
		if operated && !g.operated {
			f.Codeshare = append(append(f.Codeshare, g.flight.Number), g.flight.Codeshare...)
			g.flight, g.operated = f, true
		} else if f.Number != g.flight.Number && !strings.HasSuffix(f.Number, "S") {
			g.flight.Codeshare = append(g.flight.Codeshare, f.Number)
		}
	}

	out := make([]Flight, 0, len(order))
	for _, k := range order {
		f := groups[k].flight
		f.ID = f.Date + "-" + f.Number
		sort.Strings(f.Codeshare)
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Number < out[j].Number
	})
	return out
}

// operates reads codigosCompania. The operating carrier's row leaves the third field empty
// ("VY,VLG,,VY,VLG,VLG"); a marketing carrier's fills it ("IB,IBE,IB,IBE,IBE,IBE").
func operates(codes string) bool {
	parts := strings.Split(codes, ",")
	return len(parts) >= 3 && parts[2] == ""
}

func toFlight(r raw, dir Direction, zone *time.Location, now time.Time) Flight {
	f := Flight{
		Number:     clean(r.AirlineIATA) + clean(r.Number),
		Airline:    clean(r.AirlineName),
		Direction:  dir,
		Airport:    clean(r.Airport),
		Other:      clean(r.Other),
		OtherLocal: clean(r.OtherCity),
		Date:       clean(r.Date),
		Scheduled:  hhmm(r.Scheduled),
		Status:     clean(r.Status),
		Terminal:   clean(r.Terminal),
		Aircraft:   clean(r.Aircraft),
		Gate:       clean(r.Gate),
		Belt:       clean(r.Belt),
	}
	if f.Number == clean(r.Number) {
		f.Number = clean(r.AirlineICAO) + clean(r.Number)
	}
	if g2 := clean(r.Gate2); g2 != "" && g2 != f.Gate {
		f.Gate += "/" + g2
	}
	if b2 := clean(r.Belt2); b2 != "" && b2 != f.Belt {
		f.Belt += "/" + b2
	}
	if from, to := clean(r.CounterFrom), clean(r.CounterTo); from != "" {
		f.Counters = from
		if to != "" && to != from {
			f.Counters += "–" + to
		}
	}
	f.OtherCity = f.OtherLocal
	if p, ok := places.Lookup(f.Other); ok {
		f.OtherCity = p.Name
	}
	if f.Terminal == "N" {
		f.Terminal = ""
	}
	f.StatusLabel = StatusLabel(f.Status)
	if f.Status == "BOR" && dir == Arrivals {
		f.StatusLabel = "Arrived"
	}
	f.Final = finals[f.Status]

	sched, okS := parse(f.Date, f.Scheduled, zone)
	if okS {
		f.Sort = sched.Unix()
	}
	expected := sched
	estDate := clean(r.EstimatedDate)
	if estDate == "" {
		estDate = f.Date
	}
	est := hhmm(r.Estimated)
	// AENA moves the estimated date to today on yesterday's evening flights while keeping the
	// scheduled time (FR6985, 17/10 20:35, "estimated" 18/10 20:35). An exact 24-hour delay is
	// not a thing, so the same time on another day means unchanged.
	if est == f.Scheduled && estDate != f.Date {
		estDate = f.Date
	}
	if est != "" {
		if e, ok := parse(estDate, est, zone); ok && okS {
			f.DelayMin = int(e.Sub(sched).Minutes())
			expected = e
		}
		if est != f.Scheduled || estDate != f.Date {
			f.Estimated = est
			if estDate != f.Date {
				f.EstimatedDate = estDate
			}
		}
	}
	// AENA sometimes marks a flight finished hours before its new time (FR3070 on 18/10/2026:
	// BOR at 10:35 for a 14:20 departure). Trusting it would hide the flight and stop the watch, so
	// the time wins, and Doubtful lets the page say AENA claims otherwise.
	if f.Status == "BOR" && okS && expected.Sub(now) > 15*time.Minute {
		f.Final = false
		f.Doubtful = true
		f.StatusLabel = "On time *"
		if f.DelayMin >= 15 {
			f.StatusLabel = "Delayed *"
		}
	}
	return f
}

// Canonical writes a flight number one way: boarding passes pad it ("FR 02495", "IB0404" as
// "IB 404") and AENA keeps its own zeros, so both sides are compared through this.
func Canonical(number string) string {
	n := strings.ToUpper(strings.ReplaceAll(number, " ", ""))
	if len(n) < 3 {
		return n
	}
	carrier, rest := n[:2], n[2:]
	// A three-letter prefix is an ICAO code (VLG3906); IATA codes are two characters (FR, U2, 6E).
	if len(n) > 3 && isLetter(n[0]) && isLetter(n[1]) && isLetter(n[2]) {
		carrier, rest = n[:3], n[3:]
	}
	digits := strings.TrimRight(rest, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	suffix := rest[len(digits):]
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return n
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	return carrier + digits + suffix
}

func isLetter(b byte) bool { return b >= 'A' && b <= 'Z' }

func parse(date, hm string, zone *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("02/01/2006 15:04", date+" "+hm, zone)
	return t, err == nil
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "null" {
		return ""
	}
	return s
}

func hhmm(s string) string {
	s = clean(s)
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}
