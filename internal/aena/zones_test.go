package aena

import (
	"path/filepath"
	"testing"
	"time"
)

func departure(number, zone, gate string) Flight {
	return Flight{Number: number, Airport: "BCN", Direction: Departures, Date: "01/11/2026", Scheduled: "10:00", Zone: zone, Gate: gate}
}

// assign shows each flight in zone, then at its gate, as two boards a few hours apart would.
func assign(z *Zones, zone string, gates ...string) {
	for i, g := range gates {
		n := zone + string(rune('a'+i%26)) + string(rune('a'+i/26))
		z.Learn([]Flight{departure(n, zone, "")})
		z.Learn([]Flight{departure(n, "", g)})
	}
}

func TestZonesLearnWhichGatesAZoneLeadsTo(t *testing.T) {
	z := NewZones("")
	board := []Flight{departure("X1", "A", "")}
	z.Label(board)
	if board[0].Zone != "A" {
		t.Fatalf("nothing learnt yet, got %q", board[0].Zone)
	}

	assign(z, "A", "B26", "B31", "A01", "C71", "B28", "A08", "B40", "C73", "B51", "A20", "C74")
	assign(z, "W/Y", "W46", "W43", "Y12", "W40", "Y10", "W41", "Y14", "W42", "W44", "W45", "M11", "M12")
	board = []Flight{departure("X1", "A", ""), departure("X2", "W/Y", ""), departure("X3", "D", "")}
	z.Label(board)
	for i, want := range []string{"A/B/C", "W/Y", "D"} {
		if board[i].Zone != want {
			t.Errorf("zone %d: %q, want %q", i, board[i].Zone, want)
		}
	}
}

func TestZonesSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zones.json")
	z := NewZones(path)
	assign(z, "D", "D10", "E70", "D16", "E78", "D18", "E81", "D21", "E80", "D04", "E70")
	board := []Flight{departure("X1", "D", "")}
	NewZones(path).Label(board)
	if board[0].Zone != "D/E" {
		t.Errorf("after a restart: %q, want D/E", board[0].Zone)
	}
}

func TestZonesForgetFlightsThatNeverGetAGate(t *testing.T) {
	z := NewZones("")
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	z.now = func() time.Time { return now }
	z.Learn([]Flight{departure("X1", "A", "")})
	now = now.Add(zoneWaitLimit + time.Hour)
	z.Learn(nil)
	if len(z.waiting) != 0 {
		t.Errorf("%d flights still waiting", len(z.waiting))
	}
}

func TestGateLetter(t *testing.T) {
	for gate, want := range map[string]string{"B26": "B", "A211": "A", "S12/S14": "S", "12": "", "9A": ""} {
		if got := gateLetter(gate); got != want {
			t.Errorf("%s: %q, want %q", gate, got, want)
		}
	}
}
