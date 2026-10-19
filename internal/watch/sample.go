package watch

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/telegram"
)

// SampleChat follows random flights so the watch can be judged on real traffic. Its messages go to
// a file, never to Telegram. A temporary aid like Collect: remove both together.
const SampleChat = "sample"

// SampleNotifier writes SampleChat's messages to path and passes every other chat's to next.
type SampleNotifier struct {
	Next Notifier
	Path string
	Now  func() time.Time

	mu sync.Mutex
}

func (n *SampleNotifier) Send(ctx context.Context, chat, html string, keys telegram.Keys) error {
	if chat != SampleChat {
		return n.Next.Send(ctx, chat, html, keys)
	}
	line, err := json.Marshal(struct {
		At   time.Time `json:"at"`
		Text string    `json:"text"`
	}{n.Now(), html})
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	file, err := os.OpenFile(n.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// Sample keeps size flights of the given airports followed by SampleChat, picked at random among
// those due within the next hours, and tops the sample up as flights finish.
func (w *Watcher) Sample(ctx context.Context, airports []string, size int, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if missing := size - len(w.List(SampleChat)); missing > 0 {
			w.sample(ctx, airports, missing)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// sampleFrom and sampleTo bound how soon a picked flight is due: late enough to see it change
// before it goes, soon enough to see it through within the day.
const (
	sampleFrom = 30 * time.Minute
	sampleTo   = 6 * time.Hour
)

func (w *Watcher) sample(ctx context.Context, airports []string, missing int) {
	now := w.now()
	var pool []aena.Flight
	for _, airport := range airports {
		for _, dir := range []aena.Direction{aena.Departures, aena.Arrivals} {
			flights, _, err := w.boards.Board(ctx, airport, dir, true)
			if err != nil {
				w.log.Warn("sample: board unavailable", "airport", airport, "dir", dir, "err", err)
				continue
			}
			for _, f := range flights {
				at, ok := w.expected(f)
				if ok && !f.Final && at.Sub(now) > sampleFrom && at.Sub(now) < sampleTo {
					pool = append(pool, f)
				}
			}
		}
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	before := len(w.List(SampleChat))
	for _, f := range pool {
		if len(w.List(SampleChat))-before >= missing {
			return
		}
		if _, err := w.Add(ctx, SampleChat, f.Airport, f.Direction, f.Date, f.Number, f.Scheduled); err != nil {
			w.log.Warn("sample: not followed", "flight", f.ID, "err", err)
			return
		}
	}
}

func (w *Watcher) capped(chat string) bool {
	return chat != SampleChat
}

// Sampled leaves SampleChat out, for counts of real people.
func Sampled(t Tracked) bool {
	return t.Chat == SampleChat
}
