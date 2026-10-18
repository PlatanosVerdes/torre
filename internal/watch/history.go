package watch

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

// Seen is one status a followed flight reached and when torre saw it. AENA drops a flight soon
// after it is done, so these lines are the only record of how long a route really takes.
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
	At        time.Time      `json:"at"`
}

// History appends a Seen line to a JSON Lines file each time a followed flight changes status.
// It holds no chat, only what AENA's public board says.
type History struct {
	path string

	mu   sync.Mutex
	last map[string]string
}

// maxRemembered bounds the statuses kept to skip repeats; forgetting them only repeats a line.
const maxRemembered = 20000

func (h *History) Record(f aena.Flight, zone *time.Location, now time.Time) error {
	if h == nil {
		return nil
	}
	key := f.Airport + "|" + string(f.Direction) + "|" + f.ID
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last[key] == f.Status {
		return nil
	}
	if len(h.last) >= maxRemembered {
		h.last = map[string]string{}
	}
	s := Seen{
		Number: f.Number, Date: f.Date, Airport: f.Airport, Direction: f.Direction, Other: f.Other,
		Airline: f.Airline, Status: f.Status, At: now,
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
	h.last[key] = f.Status
	return file.Close()
}
