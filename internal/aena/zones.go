package aena

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AENA names a departure's zone before its gate, but the zone is not always the gate's letter:
// BCN's "A" is the Schengen side of T1 and ends at A, B or C gates. Zones watches each flight go
// from a zone to a gate and learns, per airport and zone, which gate letters it really means.
type Zones struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	learned map[string]map[string]map[string]int // airport → zone → gate letter → flights
	waiting map[string]waiting                   // flight → the zone it was last seen in
}

type waiting struct {
	airport, zone string
	seen          time.Time
}

// A letter must show up this often to be part of a zone, and a zone needs this many flights
// before what it learnt replaces AENA's own letters.
const (
	zoneMinLetter = 3
	zoneMinShare  = 0.05
	zoneMinTotal  = 10
	// Past this many flights a zone's counts are halved, so a reshuffle of gates wins over time.
	zoneMaxTotal = 1000
	// A flight waiting this long for its gate is forgotten.
	zoneWaitLimit = 4 * 24 * time.Hour
)

// NewZones picks up what was learnt before from path; an empty path keeps it in memory only.
func NewZones(path string) *Zones {
	z := &Zones{path: path, now: time.Now, learned: map[string]map[string]map[string]int{}, waiting: map[string]waiting{}}
	if path == "" {
		return z
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &z.learned)
	}
	return z
}

// Learn notes departures in a zone and, once one of them gets its gate, the letter it got.
func (z *Zones) Learn(flights []Flight) {
	if z == nil {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	now := z.now()
	changed := false
	for _, f := range flights {
		if f.Direction != Departures {
			continue
		}
		key := f.Airport + "|" + f.Date + "|" + f.Number + "|" + f.Scheduled
		if f.Gate == "" && f.Zone != "" {
			z.waiting[key] = waiting{f.Airport, f.Zone, now}
			continue
		}
		w, ok := z.waiting[key]
		if !ok || f.Gate == "" {
			continue
		}
		delete(z.waiting, key)
		if letter := gateLetter(f.Gate); letter != "" {
			z.count(w.airport, w.zone, letter)
			changed = true
		}
	}
	for key, w := range z.waiting {
		if now.Sub(w.seen) > zoneWaitLimit {
			delete(z.waiting, key)
		}
	}
	if changed {
		z.save()
	}
}

func (z *Zones) count(airport, zone, letter string) {
	if z.learned[airport] == nil {
		z.learned[airport] = map[string]map[string]int{}
	}
	letters := z.learned[airport][zone]
	if letters == nil {
		letters = map[string]int{}
		z.learned[airport][zone] = letters
	}
	letters[letter]++
	total := 0
	for _, n := range letters {
		total += n
	}
	if total > zoneMaxTotal {
		for l, n := range letters {
			if letters[l] = n / 2; letters[l] == 0 {
				delete(letters, l)
			}
		}
	}
}

// Label rewrites each departure's zone as the gate letters it has led to ("A" → "A/B/C"). A zone
// with too little seen yet keeps AENA's letters.
func (z *Zones) Label(flights []Flight) {
	if z == nil {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	for i, f := range flights {
		if f.Zone == "" {
			continue
		}
		if label := z.label(f.Airport, f.Zone); label != "" {
			flights[i].Zone = label
		}
	}
}

func (z *Zones) label(airport, zone string) string {
	letters := z.learned[airport][zone]
	total := 0
	for _, n := range letters {
		total += n
	}
	if total < zoneMinTotal {
		return ""
	}
	var keep []string
	for l, n := range letters {
		if n >= zoneMinLetter && float64(n) >= zoneMinShare*float64(total) {
			keep = append(keep, l)
		}
	}
	sort.Strings(keep)
	return strings.Join(keep, "/")
}

func (z *Zones) save() {
	if z.path == "" {
		return
	}
	data, err := json.Marshal(z.learned)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(z.path), ".zones-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil || tmp.Close() != nil {
		os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), z.path) != nil {
		os.Remove(tmp.Name())
	}
}

// gateLetter is the letters a gate starts with ("B26" → "B", "A211" → "A"); bare numbers have none.
func gateLetter(gate string) string {
	gate, _, _ = strings.Cut(gate, "/")
	end := strings.IndexFunc(gate, func(r rune) bool { return r < 'A' || r > 'Z' })
	if end < 0 {
		return gate
	}
	return gate[:end]
}
