// Package watch follows chosen flights for each chat and says what changed: time, gate, status, belt.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
)

// Tracked is one flight followed by one chat. Last is nil while AENA has not published it yet.
type Tracked struct {
	ID        string         `json:"id"`
	Chat      string         `json:"chat"`
	Airport   string         `json:"airport"`
	Direction aena.Direction `json:"direction"`
	Date      string         `json:"date"`
	Number    string         `json:"number"`
	Added     time.Time      `json:"added"`
	Last      *aena.Flight   `json:"last,omitempty"`
	// Notified is the state the chat was last told about. Changes are measured from it, not from
	// Last, so a time creeping a minute per check still adds up to a message.
	Notified *aena.Flight `json:"notified,omitempty"`
	FinalAt  *time.Time   `json:"finalAt,omitempty"`
}

// StopData is the callback data of a message's "Stop following" button. The chat is left out:
// the tap arrives from that chat, which is also what proves the flight is theirs.
func (t Tracked) StopData() string {
	return "u:" + strings.TrimPrefix(t.ID, t.Chat+"-")
}

type Boards interface {
	Board(ctx context.Context, airport string, dir aena.Direction, short bool) ([]aena.Flight, time.Time, error)
}

type Notifier interface {
	Send(ctx context.Context, chat, html string, keys telegram.Keys) error
}

var (
	ErrNotFound = errors.New("flight not found")
	ErrTooMany  = errors.New("too many followed flights")
)

const (
	// keepFinal is how long a landed or cancelled flight stays on the list before it is dropped.
	keepFinal = 6 * time.Hour
	// AENA publishes about two weeks ahead. A flight further out is kept as pending until it shows up.
	published = 12 * 24 * time.Hour
	maxAhead  = 330 * 24 * time.Hour
)

type Watcher struct {
	path   string
	boards Boards
	notify Notifier
	log    *slog.Logger
	now    func() time.Time
	zone   *time.Location

	// BaseURL, when set, adds an "Open in Torre" button to every message.
	BaseURL string
	// StopButton adds "Stop following" to every message. Only for a bot whose updates torre reads,
	// or the taps go nowhere.
	StopButton bool
	MaxChat    int
	MaxTotal   int

	mu      sync.Mutex
	tracked []Tracked
}

func New(dataDir string, boards Boards, notify Notifier, zone *time.Location, log *slog.Logger) (*Watcher, error) {
	w := &Watcher{
		path:     filepath.Join(dataDir, "tracked.json"),
		boards:   boards,
		notify:   notify,
		log:      log,
		now:      time.Now,
		zone:     zone,
		MaxChat:  10,
		MaxTotal: 5000,
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

// List returns one chat's flights, or everyone's when chat is empty.
func (w *Watcher) List(chat string) []Tracked {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []Tracked{}
	for _, t := range w.tracked {
		if chat == "" || t.Chat == chat {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return sortKey(out[i]) < sortKey(out[j]) })
	return out
}

func sortKey(t Tracked) string {
	key := t.Date
	if d, err := time.Parse("02/01/2006", t.Date); err == nil {
		key = d.Format("2006-01-02")
	}
	if t.Last != nil {
		key += t.Last.Scheduled
	}
	return key
}

// Add starts following a flight and sends its current state, so the chat confirms it took.
func (w *Watcher) Add(ctx context.Context, chat, airport string, dir aena.Direction, date, number string) (Tracked, error) {
	airport, number = strings.ToUpper(airport), aena.Canonical(number)
	day, err := time.ParseInLocation("02/01/2006", date, w.zone)
	if err != nil {
		return Tracked{}, errors.New("date must be dd/mm/yyyy")
	}
	now := w.now()

	f, err := w.find(ctx, airport, dir, date, number)
	if err != nil {
		return Tracked{}, err
	}
	if f == nil && (day.Sub(now) < published || day.Sub(now) > maxAhead) {
		return Tracked{}, fmt.Errorf("%w: %s is not on AENA's %s board for %s", ErrNotFound, number, airport, date)
	}
	if f != nil {
		number = f.Number
	}
	t := Tracked{
		ID:      strings.Join([]string{chat, airport, string(dir), strings.ReplaceAll(date, "/", ""), aena.Canonical(number)}, "-"),
		Chat:    chat,
		Airport: airport, Direction: dir, Date: date, Number: number,
		Added: now, Last: f, Notified: f,
	}

	w.mu.Lock()
	mine := 0
	for _, o := range w.tracked {
		if o.ID == t.ID {
			w.mu.Unlock()
			return o, nil
		}
		if o.Chat == chat {
			mine++
		}
	}
	if mine >= w.MaxChat || len(w.tracked) >= w.MaxTotal {
		w.mu.Unlock()
		return Tracked{}, fmt.Errorf("%w: up to %d at a time", ErrTooMany, w.MaxChat)
	}
	w.tracked = append(w.tracked, t)
	err = w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		return Tracked{}, err
	}

	if err := w.notify.Send(ctx, chat, "Following "+Describe(t), w.keys(t)); err != nil {
		// Following without being able to write is pointless, and the page says why.
		if errors.Is(err, telegram.ErrBlocked) {
			_ = w.Remove(chat, t.ID)
			return Tracked{}, err
		}
		w.log.Warn("notify failed", "err", err)
	}
	return t, nil
}

func (w *Watcher) Remove(chat, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, t := range w.tracked {
		if t.ID == id && (chat == "" || t.Chat == chat) {
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
	for _, t := range w.List("") {
		f, err := w.find(ctx, t.Airport, t.Direction, t.Date, t.Number)
		if err != nil {
			w.log.Warn("board unavailable", "airport", t.Airport, "err", err)
			continue
		}
		now := w.now()

		if f == nil {
			// AENA drops a flight a couple of hours after it is done.
			if t.FinalAt != nil || w.past(t.Date, now) {
				_ = w.Remove("", t.ID)
			}
			continue
		}

		told := t.Notified
		if told == nil {
			told = t.Last
		}
		var msg string
		if told == nil {
			msg = "Now on AENA's board: " + Describe(Tracked{Number: f.Number, Last: f})
		} else if changes := Diff(*told, *f); len(changes) > 0 {
			msg = Headline(*f) + "\n" + strings.Join(changes, "\n")
		}
		if msg != "" {
			t.Last = f
			if err := w.notify.Send(ctx, t.Chat, msg, w.keys(t)); err != nil {
				if errors.Is(err, telegram.ErrBlocked) {
					// They blocked the bot; there is nobody left to tell.
					_ = w.Remove("", t.ID)
					continue
				}
				// The old state stays, so the change is sent again next round.
				w.log.Warn("notify failed", "flight", t.ID, "err", err)
				continue
			}
			t.Notified = f
		}

		t.Last = f
		if f.Final && t.FinalAt == nil {
			t.FinalAt = &now
		}
		if t.FinalAt != nil && now.Sub(*t.FinalAt) > keepFinal {
			_ = w.Remove("", t.ID)
			continue
		}
		w.update(t)
	}
}

func (w *Watcher) keys(t Tracked) telegram.Keys {
	k := telegram.Keys{Open: w.open(t)}
	if w.StopButton {
		k.Stop = t.StopData()
	}
	return k
}

func (w *Watcher) open(t Tracked) string {
	if w.BaseURL == "" {
		return ""
	}
	q := url.Values{"a": {t.Airport}, "d": {string(t.Direction)}, "f": {t.Number}}
	if t.Last != nil {
		q.Set("f", t.Last.ID)
	}
	return strings.TrimRight(w.BaseURL, "/") + "/?" + q.Encode()
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
	d, err := time.ParseInLocation("02/01/2006", date, w.zone)
	if err == nil && d.Sub(w.now()) > published+48*time.Hour {
		// Not even the two-week board reaches that far, so there is nothing to ask AENA yet.
		return nil, nil
	}
	short := err != nil || d.Sub(w.now()) < 36*time.Hour
	flights, _, err := w.boards.Board(ctx, airport, dir, short)
	if err != nil {
		return nil, err
	}
	return Find(flights, date, number), nil
}

func Find(flights []aena.Flight, date, number string) *aena.Flight {
	number = aena.Canonical(number)
	for i := range flights {
		f := &flights[i]
		if f.Date != date {
			continue
		}
		if aena.Canonical(f.Number) == number {
			return f
		}
		for _, c := range f.Codeshare {
			if aena.Canonical(c) == number {
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
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// minTimeChange is the smallest move of the expected time worth a message. AENA's estimates
// wobble by a minute (FR3070: 15:06, 15:07, 15:06 within six minutes).
const minTimeChange = 5

// Diff lists what a traveller would want to hear about, in the order they would act on it.
func Diff(old, cur aena.Flight) []string {
	var out []string
	if old.Status != cur.Status {
		out = append(out, "Status: <b>"+esc(cur.StatusLabel)+"</b>")
	}
	if moved := cur.DelayMin - old.DelayMin; moved >= minTimeChange || moved <= -minTimeChange || old.EstimatedDate != cur.EstimatedDate {
		line := fmt.Sprintf("Time: %s → <b>%s</b>", Time(old), Time(cur))
		if cur.DelayMin != 0 {
			line += fmt.Sprintf(" (%+d min)", cur.DelayMin)
		}
		out = append(out, line)
	}
	if old.Gate != cur.Gate {
		out = append(out, fmt.Sprintf("Gate: %s → <b>%s</b>", dash(old.Gate), dash(cur.Gate)))
	}
	if old.Terminal != cur.Terminal {
		out = append(out, fmt.Sprintf("Terminal: %s → <b>%s</b>", dash(old.Terminal), dash(cur.Terminal)))
	}
	if old.Belt != cur.Belt && cur.Belt != "" {
		out = append(out, "Baggage belt: <b>"+esc(cur.Belt)+"</b>")
	}
	if old.Counters != cur.Counters && cur.Counters != "" && cur.Direction == aena.Departures {
		out = append(out, "Check-in desks: "+esc(cur.Counters))
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

func Describe(t Tracked) string {
	f := t.Last
	if f == nil {
		return fmt.Sprintf("✈️ <b>%s</b> %s · %s\nAENA publishes flights about two weeks ahead; you will get a message when it shows up.",
			esc(t.Number), esc(t.Airport), esc(t.Date[:5]))
	}
	lines := []string{Headline(*f), "Status: <b>" + esc(f.StatusLabel) + "</b>"}
	verb := "Departs"
	if f.Direction == aena.Arrivals {
		verb = "Arrives"
	}
	when := verb + " " + f.Scheduled
	if f.Estimated != "" {
		when = fmt.Sprintf("%s %s, now expected <b>%s</b> (%+d min)", verb, f.Scheduled, Time(*f), f.DelayMin)
	}
	lines = append(lines, when)
	if f.Terminal != "" {
		lines = append(lines, "Terminal "+esc(f.Terminal))
	}
	if f.Gate != "" {
		lines = append(lines, "Gate <b>"+esc(f.Gate)+"</b>")
	}
	if f.Belt != "" {
		lines = append(lines, "Belt "+esc(f.Belt))
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
