package watch

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

// Seen is one state a flight reached and when torre saw it. AENA drops a flight soon after it is
// done, so these lines are the only record of how long a route really takes.
type Seen struct {
	Number    string         `json:"number"`
	Date      string         `json:"date"`
	Airport   string         `json:"airport"`
	Direction aena.Direction `json:"direction"`
	Other     string         `json:"other"`
	Airline   string         `json:"airline,omitempty"`
	Status    string         `json:"status"`
	Scheduled time.Time      `json:"scheduled"`
	Expected  time.Time      `json:"expected"`
	Gate      string         `json:"gate,omitempty"`
	Belt      string         `json:"belt,omitempty"`
	At        time.Time      `json:"at"`
}

// History appends a Seen line to a JSON Lines file each time a flight's status, expected time,
// gate or belt changes. It holds no chat, only what AENA's public board says.
type History struct {
	path string

	mu   sync.Mutex
	last map[string]remembered
}

type remembered struct {
	state string
	day   time.Time
}

// maxRemembered bounds the states kept to skip repeats; past it, flights over two days old go.
const maxRemembered = 20000

func NewHistory(path string) *History {
	return &History{path: path, last: map[string]remembered{}}
}

func (h *History) Record(f aena.Flight, zone *time.Location, now time.Time) error {
	if h == nil {
		return nil
	}
	s := Seen{
		Number: f.Number, Date: f.Date, Airport: f.Airport, Direction: f.Direction, Other: f.Other,
		Airline: f.Airline, Status: f.Status, Gate: f.Gate, Belt: f.Belt, At: now,
	}
	s.Scheduled, _ = time.ParseInLocation("02/01/2006 15:04", f.Date+" "+f.Scheduled, zone)
	s.Expected = s.Scheduled
	if f.Estimated != "" {
		date := f.Date
		if f.EstimatedDate != "" {
			date = f.EstimatedDate
		}
		s.Expected, _ = time.ParseInLocation("02/01/2006 15:04", date+" "+f.Estimated, zone)
	}
	key := f.Airport + "|" + string(f.Direction) + "|" + f.ID
	state := s.Status + "|" + s.Expected.Format(time.RFC3339) + "|" + s.Gate + "|" + s.Belt

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last[key].state == state {
		return nil
	}
	if len(h.last) >= maxRemembered {
		for k, r := range h.last {
			if now.Sub(r.day) > 48*time.Hour {
				delete(h.last, k)
			}
		}
	}
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return err
	}
	h.last[key] = remembered{state, s.Scheduled}
	return file.Close()
}
