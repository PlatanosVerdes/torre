package watch

import (
	"context"
	"log/slog"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

// Collect records every flight on the given airports' boards, followed or not, to study how AENA
// moves times and states. A temporary aid to tune the watch: remove it once the data is in.
func Collect(ctx context.Context, boards Boards, airports []string, h *History, zone *time.Location, every time.Duration, log *slog.Logger) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		for _, airport := range airports {
			for _, dir := range []aena.Direction{aena.Departures, aena.Arrivals} {
				if err := collect(ctx, boards, airport, dir, h, zone); err != nil {
					log.Warn("collect failed", "airport", airport, "dir", dir, "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func collect(ctx context.Context, boards Boards, airport string, dir aena.Direction, h *History, zone *time.Location) error {
	flights, _, err := boards.Board(ctx, airport, dir, true)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, f := range flights {
		if err := h.Record(f, zone, now); err != nil {
			return err
		}
	}
	return nil
}
