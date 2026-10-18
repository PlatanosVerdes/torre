// genseries learns which flight-number series each airline flies alone, from every AENA airport's
// two-day boards, and writes it as internal/aena/series.json (see internal/aena/series.go).
//
//	go run ./cmd/genseries > internal/aena/series.json
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	client := aena.New(aena.DefaultBaseURL, time.Minute)
	airports, err := client.Airports(ctx)
	if err != nil {
		return err
	}
	for _, a := range airports {
		for _, dir := range []aena.Direction{aena.Departures, aena.Arrivals} {
			if _, _, err := client.Board(ctx, a.IATA, dir, true); err != nil {
				fmt.Fprintf(os.Stderr, "%s %s: %v\n", a.IATA, dir, err)
			}
			// Ninety-odd requests in a row: spaced out, it is a visitor, not a burst.
			time.Sleep(500 * time.Millisecond)
		}
	}

	snap := client.Series.Snapshot()
	for _, list := range snap {
		sort.Ints(list)
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(snap)
}
