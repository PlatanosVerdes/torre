// Package watch follows chosen flights and says what changed: time, gate, status, belt.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

type Tracked struct {
	ID        string         `json:"id"`
	Airport   string         `json:"airport"`
	Direction aena.Direction `json:"direction"`
	Date      string         `json:"date"`
	Number    string         `json:"number"`
	Added     time.Time      `json:"added"`
	Last      *aena.Flight   `json:"last,omitempty"`
	FinalAt   *time.Time     `json:"finalAt,omitempty"`
}

type Boards interface {
	Board(ctx context.Context, airport string, dir aena.Direction, short bool) ([]aena.Flight, time.Time, error)
}

type Notifier interface {
	Notify(ctx context.Context, html string) error
}

type Watcher struct {
	path   string
	boards Boards
	notify Notifier
	log    *slog.Logger
	now    func() time.Time
	zone   *time.Location

	mu      sync.Mutex
	tracked []Tracked
}

// keepFinal is how long a landed or cancelled flight stays on the list before it is dropped.
const keepFinal = 6 * time.Hour

func New(dataDir string, boards Boards, notify Notifier, zone *time.Location, log *slog.Logger) (*Watcher, error) {
	w := &Watcher{
		path:   filepath.Join(dataDir, "tracked.json"),
		boards: boards,
		notify: notify,
		log:    log,
		now:    time.Now,
		zone:   zone,
	}
	raw, err := os.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &w.tracked); err != nil {
		return nil, fmt.Errorf("%s: %w", w.path, err)
	}
	return w, nil
}

func (w *Watcher) List() []Tracked {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]Tracked{}, w.tracked...)
	sort.Slice(out, func(i, j int) bool {
		return sortKey(out[i]) < sortKey(out[j])
	})
	return out
}

func sortKey(t Tracked) int64 {
	if t.Last != nil {
		return t.Last.Sort
	}
	return t.Added.Unix()
}

// Add starts following a flight and sends its current state, so the chat confirms it took.
func (w *Watcher) Add(ctx context.Context, airport string, dir aena.Direction, date, number string) (Tracked, error) {
	airport, number = strings.ToUpper(airport), strings.ToUpper(strings.ReplaceAll(number, " ", ""))
	f, err := w.find(ctx, airport, dir, date, number)
	if err != nil {
		return Tracked{}, err
	}
	if f == nil {
		return Tracked{}, fmt.Errorf("%s no aparece en %s el %s", number, airport, date)
	}
	// Keyed by the operating flight, so following it by a codeshare number does not duplicate it.
	id := strings.Join([]string{airport, string(dir), strings.ReplaceAll(date, "/", ""), f.Number}, "-")

	w.mu.Lock()
	for _, t := range w.tracked {
		if t.ID == id {
			w.mu.Unlock()
			return t, nil
		}
	}
	w.mu.Unlock()

	t := Tracked{ID: id, Airport: airport, Direction: dir, Date: date, Number: f.Number, Added: w.now(), Last: f}

	w.mu.Lock()
	w.tracked = append(w.tracked, t)
	err = w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		return Tracked{}, err
	}
	if err := w.notify.Notify(ctx, "Siguiendo "+Describe(*f)); err != nil {
		w.log.Warn("notify failed", "err", err)
	}
	return t, nil
}

func (w *Watcher) Remove(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, t := range w.tracked {
		if t.ID == id {
			w.tracked = append(w.tracked[:i], w.tracked[i+1:]...)
			return w.saveLocked()
		}
	}
	return nil
}

func (w *Watcher) Run(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Check compares every tracked flight with the board and notifies each change.
func (w *Watcher) Check(ctx context.Context) {
	for _, t := range w.List() {
		f, err := w.find(ctx, t.Airport, t.Direction, t.Date, t.Number)
		if err != nil {
			w.log.Warn("board unavailable", "airport", t.Airport, "err", err)
			continue
		}
		now := w.now()

		if f == nil {
			// AENA drops a flight a couple of hours after it is done.
			if t.FinalAt != nil || w.past(t.Date, now) {
				_ = w.Remove(t.ID)
			}
			continue
		}

		if t.Last != nil {
			if changes := Diff(*t.Last, *f); len(changes) > 0 {
				msg := Headline(*f) + "\n" + strings.Join(changes, "\n")
				if err := w.notify.Notify(ctx, msg); err != nil {
					w.log.Warn("notify failed", "flight", t.ID, "err", err)
					// Keep the old state so the change is sent again next round.
					continue
				}
			}
		}
		t.Last = f
		if f.Final && t.FinalAt == nil {
			t.FinalAt = &now
		}
		if t.FinalAt != nil && now.Sub(*t.FinalAt) > keepFinal {
			_ = w.Remove(t.ID)
			continue
		}
		w.update(t)
	}
}

func (w *Watcher) update(t Tracked) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.tracked {
		if w.tracked[i].ID == t.ID {
			w.tracked[i] = t
			if err := w.saveLocked(); err != nil {
				w.log.Warn("save failed", "err", err)
			}
			return
		}
	}
}

// past is true once the flight's day ended more than a day ago, for flights AENA never listed again.
func (w *Watcher) past(date string, now time.Time) bool {
	d, err := time.ParseInLocation("02/01/2006", date, w.zone)
	return err == nil && now.Sub(d) > 48*time.Hour
}

func (w *Watcher) find(ctx context.Context, airport string, dir aena.Direction, date, number string) (*aena.Flight, error) {
	short := w.withinShort(date)
	flights, _, err := w.boards.Board(ctx, airport, dir, short)
	if err != nil {
		return nil, err
	}
	return Find(flights, date, number), nil
}

// withinShort says whether AENA's two-day board covers the date, so the six-times-larger
// fortnight board is only fetched for flights further out.
func (w *Watcher) withinShort(date string) bool {
	d, err := time.ParseInLocation("02/01/2006", date, w.zone)
	if err != nil {
		return true
	}
	return d.Sub(w.now()) < 36*time.Hour
}

func Find(flights []aena.Flight, date, number string) *aena.Flight {
	number = strings.ToUpper(strings.ReplaceAll(number, " ", ""))
	for i := range flights {
		f := &flights[i]
		if f.Date != date {
			continue
		}
		if f.Number == number {
			return f
		}
		for _, c := range f.Codeshare {
			if c == number {
				return f
			}
		}
	}
	return nil
}

func (w *Watcher) saveLocked() error {
	raw, err := json.MarshalIndent(w.tracked, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Diff lists what a traveller would want to hear about, in the order they would act on it.
func Diff(old, cur aena.Flight) []string {
	var out []string
	if old.Status != cur.Status {
		out = append(out, "Estado: <b>"+esc(cur.StatusLabel)+"</b>")
	}
	if Time(old) != Time(cur) {
		line := fmt.Sprintf("Hora: %s → <b>%s</b>", Time(old), Time(cur))
		if cur.DelayMin != 0 {
			line += fmt.Sprintf(" (%+d min)", cur.DelayMin)
		}
		out = append(out, line)
	}
	if old.Gate != cur.Gate {
		out = append(out, fmt.Sprintf("Puerta: %s → <b>%s</b>", dash(old.Gate), dash(cur.Gate)))
	}
	if old.Terminal != cur.Terminal {
		out = append(out, fmt.Sprintf("Terminal: %s → <b>%s</b>", dash(old.Terminal), dash(cur.Terminal)))
	}
	if old.Belt != cur.Belt && cur.Belt != "" {
		out = append(out, "Cinta de equipaje: <b>"+esc(cur.Belt)+"</b>")
	}
	if old.Counters != cur.Counters && cur.Counters != "" && cur.Direction == aena.Departures {
		out = append(out, "Mostradores: "+esc(cur.Counters))
	}
	return out
}

// Time is the time the flight is expected now, with the day when the delay crossed midnight.
func Time(f aena.Flight) string {
	t := f.Scheduled
	if f.Estimated != "" {
		t = f.Estimated
	}
	if f.EstimatedDate != "" {
		t += " (" + f.EstimatedDate[:5] + ")"
	}
	return t
}

func Headline(f aena.Flight) string {
	route := f.Airport + " → " + f.Other
	if f.Direction == aena.Arrivals {
		route = f.Other + " → " + f.Airport
	}
	return fmt.Sprintf("✈️ <b>%s</b> %s · %s", esc(f.Number), esc(route), esc(f.Date[:5]))
}

func Describe(f aena.Flight) string {
	lines := []string{Headline(f), "Estado: <b>" + esc(f.StatusLabel) + "</b>"}
	verb := "Sale"
	if f.Direction == aena.Arrivals {
		verb = "Llega"
	}
	when := verb + " " + f.Scheduled
	if f.Estimated != "" {
		when = fmt.Sprintf("%s %s, previsto <b>%s</b> (%+d min)", verb, f.Scheduled, Time(f), f.DelayMin)
	}
	lines = append(lines, when)
	if f.Terminal != "" {
		lines = append(lines, "Terminal "+esc(f.Terminal))
	}
	if f.Gate != "" {
		lines = append(lines, "Puerta <b>"+esc(f.Gate)+"</b>")
	}
	if f.Belt != "" {
		lines = append(lines, "Cinta "+esc(f.Belt))
	}
	return strings.Join(lines, "\n")
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return esc(s)
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
