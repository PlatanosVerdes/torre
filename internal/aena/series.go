package aena

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// AENA lists a codeshare flight once per airline selling it and never says which one flies the
// plane. What does tell them apart is numbering: an airline flies its own planes under certain
// hundred-number series and sells seats on partners' planes under others. Iberia flies IB0404 and
// sells IB5139 on Vueling's VY3919; Qatar never flies alone from Spain at all. So a series an
// airline has been seen flying alone in, anywhere in Spain, marks the operating number of a group.
//
// series.json is the starting knowledge, learnt from every AENA airport's two-day boards with
// `go run ./cmd/genseries`. Each board fetched afterwards adds what it shows.

//go:embed series.json
var seedSeries []byte

// Series is, per airline code, the hundred-number series it has been seen flying alone in.
type Series struct {
	mu  sync.RWMutex
	own map[string]map[int]bool
}

func NewSeries() *Series {
	s := &Series{own: map[string]map[int]bool{}}
	var seed map[string][]int
	if err := json.Unmarshal(seedSeries, &seed); err == nil {
		for airline, list := range seed {
			for _, n := range list {
				s.add(airline, n)
			}
		}
	}
	return s
}

func (s *Series) add(airline string, series int) {
	if s.own[airline] == nil {
		s.own[airline] = map[int]bool{}
	}
	s.own[airline][series] = true
}

// Owns reports whether the airline flies its own planes under this flight number's series.
func (s *Series) Owns(airline, number string) bool {
	n, ok := seriesOf(number)
	if s == nil || !ok {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.own[airline][n]
}

// Learn records the series of every group on a board that only one airline sells.
func (s *Series) Learn(groups [][]member) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range groups {
		if !oneAirline(g) {
			continue
		}
		for _, m := range g {
			if n, ok := seriesOf(m.number); ok && m.airline != "" {
				s.add(m.airline, n)
			}
		}
	}
}

// Snapshot is the knowledge as genseries writes it.
func (s *Series) Snapshot() map[string][]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]int, len(s.own))
	for airline, set := range s.own {
		for n := range set {
			out[airline] = append(out[airline], n)
		}
	}
	return out
}

func seriesOf(number string) (int, bool) {
	digits := strings.TrimRight(number, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return n / 100, true
}

func oneAirline(g []member) bool {
	for _, m := range g[1:] {
		if m.airline != g[0].airline {
			return false
		}
	}
	return true
}
