package limit

import (
	"testing"
	"time"
)

func TestBurstThenRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(60, 3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if !l.Allow("a") {
			t.Fatalf("request %d of the burst refused", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("past the burst was allowed")
	}
	if !l.Allow("b") {
		t.Fatal("one client's burst blocked another")
	}
	now = now.Add(time.Second)
	if !l.Allow("a") {
		t.Fatal("no refill after a second at 60/min")
	}
}

func TestSweepKeepsMemoryBounded(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(60, 1)
	l.now = func() time.Time { return now }
	for i := range maxKeys {
		l.Allow(string(rune(i)))
	}
	now = now.Add(time.Minute)
	l.Allow("new")
	if len(l.buckets) > 1 {
		t.Errorf("%d buckets after a sweep, want only the new one", len(l.buckets))
	}
}
