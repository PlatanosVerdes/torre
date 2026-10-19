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
	"reflect"
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
	// Scheduled tells apart the rows one number can have on a date. Until it is known, Date is the
	// day the flight leaves, which on an arrivals board can be the day before its row.
	Scheduled string       `json:"scheduled,omitempty"`
	Added     time.Time    `json:"added"`
	Last      *aena.Flight `json:"last,omitempty"`
	// Notified is the state the chat was last told about. Changes are measured from it, not from
	// Last, so a time creeping a minute per check still adds up to a message.
	Notified *aena.Flight `json:"notified,omitempty"`
	// Pending is a change not sent yet, held until it has stood for settle; Since is when it began.
	Pending *aena.Flight `json:"pending,omitempty"`
	Since   *time.Time   `json:"since,omitempty"`
	FinalAt *time.Time   `json:"finalAt,omitempty"`
	// AwaitTakeoff holds a departed flight's arrival news until the destination sees it airborne.
	AwaitTakeoff bool `json:"awaitTakeoff,omitempty"`
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
	// Spanish says whether AENA runs an airport. When it runs a departed flight's destination
	// too, the watch carries on at that arrivals board: landing, belt, baggage.
	Spanish func(code string) bool

	mu      sync.Mutex
	tracked []Tracked
	history *History
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
		history:  NewHistory(filepath.Join(dataDir, "history.jsonl")),
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
// scheduled is the row's time when the board showed it; without it, date is the day it leaves.
func (w *Watcher) Add(ctx context.Context, chat, airport string, dir aena.Direction, date, number, scheduled string) (Tracked, error) {
	airport, number = strings.ToUpper(airport), aena.Canonical(number)
	day, err := time.ParseInLocation("02/01/2006", date, w.zone)
	if err != nil {
		return Tracked{}, errors.New("date must be dd/mm/yyyy")
	}
	now := w.now()

	f, err := w.find(ctx, airport, dir, date, number, scheduled)
	if err != nil {
		return Tracked{}, err
	}
	if f == nil && (day.Sub(now) < published || day.Sub(now) > maxAhead) {
		return Tracked{}, fmt.Errorf("%w: %s is not on AENA's %s board for %s", ErrNotFound, number, airport, date)
	}
	if f != nil {
		number, date, scheduled = f.Number, f.Date, f.Scheduled
	}
	id := []string{chat, airport, string(dir), strings.ReplaceAll(date, "/", ""), aena.Canonical(number)}
	if scheduled != "" {
		id = append(id, strings.ReplaceAll(scheduled, ":", ""))
	}
	t := Tracked{
		ID:      strings.Join(id, "-"),
		Chat:    chat,
		Airport: airport, Direction: dir, Date: date, Number: number, Scheduled: scheduled,
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

// Check compares every tracked flight with the board and notifies each change that holds, or at
// once when it cannot wait. The file is written once per pass, and only when something changed.
func (w *Watcher) Check(ctx context.Context) {
	dirty := false
	defer func() {
		if dirty {
			w.mu.Lock()
			defer w.mu.Unlock()
			if err := w.saveLocked(); err != nil {
				w.log.Warn("save failed", "err", err)
			}
		}
	}()
	for _, t := range w.List("") {
		f, err := w.find(ctx, t.Airport, t.Direction, t.Date, t.Number, t.Scheduled)
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

		w.record(*f, now)
		t.Date, t.Scheduled = f.Date, f.Scheduled

		if next, msg := w.landing(ctx, t, f); next != nil {
			if err := w.notify.Send(ctx, t.Chat, msg, w.keys(*next)); err != nil {
				if errors.Is(err, telegram.ErrBlocked) {
					_ = w.Remove("", t.ID)
				} else {
					w.log.Warn("notify failed", "flight", t.ID, "err", err)
				}
				continue
			}
			w.update(*next)
			continue
		}

		if t.AwaitTakeoff && grounded[f.Status] {
			t.Last = f
			dirty = w.store(t) || dirty
			continue
		}

		told := t.Notified
		if told == nil {
			told = t.Last
		}
		var msg string
		if told == nil {
			msg = "Now on AENA's board: " + Describe(Tracked{Number: f.Number, Last: f})
		} else if changes := Diff(*told, *f); len(changes) == 0 {
			t.Pending, t.Since = nil, nil // back to what they were told, or nothing worth telling
			t.AwaitTakeoff = false
		} else if t.AwaitTakeoff || w.urgent(*told, *f, now) || w.settled(&t, *f, now) {
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
			t.Pending, t.Since = nil, nil
			t.AwaitTakeoff = false
		}

		t.Last = f
		if f.Final && t.FinalAt == nil {
			t.FinalAt = &now
		}
		if t.FinalAt != nil && now.Sub(*t.FinalAt) > keepFinal {
			_ = w.Remove("", t.ID)
			continue
		}
		dirty = w.store(t) || dirty
	}
}

// settle is how long a change must stand before it is worth a message: AENA takes a gate away
// and gives it back within minutes (VY3919: D84, none, D84 between 21:50 and 22:10).
const settle = 10 * time.Minute

// gateRush is how close to departure a new gate goes out at once: there is a walk to make.
const gateRush = time.Hour

// urgentStatus are the states a traveller acts on as soon as they appear.
var urgentStatus = map[string]bool{"EMB": true, "ULL": true, "CER": true, "BOR": true, "CAN": true, "DES": true, "DIV": true, "LND": true, "IBK": true}

func (w *Watcher) urgent(told, cur aena.Flight, now time.Time) bool {
	if cur.Status != told.Status && urgentStatus[cur.Status] {
		return true
	}
	if cur.Belt != told.Belt && cur.Belt != "" {
		return true
	}
	if cur.Direction == aena.Departures && cur.Gate != "" && cur.Gate != told.Gate {
		if at, ok := w.expected(cur); ok && at.Sub(now) < gateRush {
			return true
		}
	}
	return false
}

// settled holds a change as pending and reports when it has stood for settle. A change that moves
// again by something worth telling starts the wait over.
func (w *Watcher) settled(t *Tracked, cur aena.Flight, now time.Time) bool {
	if t.Pending == nil || len(Diff(*t.Pending, cur)) > 0 {
		f := cur
		t.Pending, t.Since = &f, &now
		return false
	}
	return now.Sub(*t.Since) >= settle
}

// expected is when the flight is due now, in the airport's zone.
func (w *Watcher) expected(f aena.Flight) (time.Time, bool) {
	date, clock := f.Date, f.Scheduled
	if f.Estimated != "" {
		clock = f.Estimated
	}
	if f.EstimatedDate != "" {
		date = f.EstimatedDate
	}
	at, err := time.ParseInLocation("02/01/2006 15:04", date+" "+clock, w.zone)
	return at, err == nil
}

// store puts t in the list without writing the file, and reports whether it changed.
func (w *Watcher) store(t Tracked) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.tracked {
		if w.tracked[i].ID == t.ID {
			if reflect.DeepEqual(w.tracked[i], t) {
				return false
			}
			w.tracked[i] = t
			return true
		}
	}
	return false
}

// grounded are the arrival states a destination keeps until it sees the flight take off. Its time
// is then a guess from the schedule (VY3919, 18/10: BCN said 00:05 until takeoff, then 01:08).
var grounded = map[string]bool{"": true, "SCH": true, "INI": true, "HOR": true, "RET": true}

// landing moves a departed flight's watch to its arrival when AENA also runs the destination,
// and returns the message that says so. It returns nil when there is nothing to move to.
func (w *Watcher) landing(ctx context.Context, t Tracked, f *aena.Flight) (*Tracked, string) {
	if !f.Final || t.Direction != aena.Departures || w.Spanish == nil || !w.Spanish(f.Other) {
		return nil, ""
	}
	flights, _, err := w.boards.Board(ctx, f.Other, aena.Arrivals, true)
	if err != nil {
		return nil, ""
	}
	arr := w.arrivalAfter(flights, *f)
	if arr == nil {
		return nil, ""
	}
	w.record(*arr, w.now())
	next := t
	next.Airport, next.Direction, next.Date, next.Scheduled = arr.Airport, aena.Arrivals, arr.Date, arr.Scheduled
	next.Last, next.Notified, next.FinalAt = arr, arr, nil
	msg := fmt.Sprintf("%s\nStatus: <b>Departed</b>\n", Headline(*f))
	if grounded[arr.Status] {
		// Measured from the schedule, so the time line that follows takeoff reads against it.
		told := *arr
		told.Estimated, told.EstimatedDate, told.DelayMin = "", "", 0
		next.Notified, next.AwaitTakeoff = &told, true
		msg += fmt.Sprintf("%s has not seen it take off yet; its arrival time will follow when it does.", esc(arr.Airport))
	} else {
		msg += fmt.Sprintf("Arrives at %s <b>%s</b>", esc(arr.Airport), Time(*arr))
		if arr.DelayMin != 0 {
			msg += fmt.Sprintf(" (%+d min)", arr.DelayMin)
		}
	}
	msg += "\nYou will hear about the landing and the baggage belt."
	return &next, msg
}

func (w *Watcher) record(f aena.Flight, now time.Time) {
	if err := w.history.Record(f, w.zone, now); err != nil {
		w.log.Warn("history not written", "flight", f.ID, "err", err)
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

func (w *Watcher) find(ctx context.Context, airport string, dir aena.Direction, date, number, scheduled string) (*aena.Flight, error) {
	d, err := time.ParseInLocation("02/01/2006", date, w.zone)
	if err == nil && d.Sub(w.now()) > published+48*time.Hour {
		// Not even the two-week board reaches that far, so there is nothing to ask AENA yet.
		return nil, nil
	}
	short := err != nil || d.AddDate(0, 0, 1).Sub(w.now()) < 36*time.Hour
	flights, _, err := w.boards.Board(ctx, airport, dir, short)
	if err != nil {
		return nil, err
	}
	if scheduled == "" && dir == aena.Arrivals {
		return LeavingOn(flights, d, number), nil
	}
	return Find(flights, date, number, scheduled), nil
}

// Find returns the flight with that number on that date, at that scheduled time when one is
// given, else the earliest.
func Find(flights []aena.Flight, date, number, scheduled string) *aena.Flight {
	for _, f := range FindAll(flights, date, number) {
		if scheduled == "" || f.Scheduled == scheduled {
			return f
		}
	}
	return nil
}

// FindAll returns every row of a number on a date, earliest first: one number can land twice on
// a date (VY3011 at BCN on 20/10: 00:20 from the night before, 21:00 from that afternoon).
func FindAll(flights []aena.Flight, date, number string) []*aena.Flight {
	number = aena.Canonical(number)
	var out []*aena.Flight
	for i := range flights {
		f := &flights[i]
		if f.Date == date && carries(*f, number) {
			out = append(out, f)
		}
	}
	return out
}

func carries(f aena.Flight, number string) bool {
	if aena.Canonical(f.Number) == number {
		return true
	}
	for _, c := range append(f.Codeshare, f.ICAO...) {
		if aena.Canonical(c) == number {
			return true
		}
	}
	return false
}

// earlyHours is when a row on the day a flight leaves is still the previous evening's flight.
const earlyHours = "06:00"

// LeavingOn finds on an arrivals board the flight that leaves on day, knowing only that: a
// boarding pass carries no time. It lands that day after the early hours, or in the early hours of
// the next; a row of the early hours of day itself is taken only when nothing else matches.
func LeavingOn(flights []aena.Flight, day time.Time, number string) *aena.Flight {
	today, tomorrow := day.Format("02/01/2006"), day.AddDate(0, 0, 1).Format("02/01/2006")
	for _, f := range FindAll(flights, today, number) {
		if f.Scheduled >= earlyHours {
			return f
		}
	}
	for _, f := range FindAll(flights, tomorrow, number) {
		if f.Scheduled < earlyHours {
			return f
		}
	}
	return Find(flights, today, number, "")
}

// arrivalAfter finds where a departed flight lands: the first row of its number scheduled after it
// left, on its day or the next.
func (w *Watcher) arrivalAfter(flights []aena.Flight, dep aena.Flight) *aena.Flight {
	left, err := time.ParseInLocation("02/01/2006 15:04", dep.Date+" "+dep.Scheduled, w.zone)
	if err != nil {
		return nil
	}
	for _, d := range []time.Time{left, left.AddDate(0, 0, 1)} {
		for _, f := range FindAll(flights, d.Format("02/01/2006"), dep.Number) {
			at, err := time.ParseInLocation("02/01/2006 15:04", f.Date+" "+f.Scheduled, w.zone)
			if err == nil && at.After(left) {
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
// wobble (FR3070: 15:06, 15:07, 15:06 within six minutes), and a few minutes change no plan.
const minTimeChange = 10

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
	// A gate taken away tells nobody where to go: wait for the next one.
	if old.Gate != cur.Gate && cur.Gate != "" {
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
