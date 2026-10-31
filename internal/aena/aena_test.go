package aena

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var madrid, _ = time.LoadLocation("Europe/Madrid")

func at(s string) time.Time {
	t, _ := time.ParseInLocation("02/01/2006 15:04", s, madrid)
	return t
}

func row(number, iata, codes, sched, est, status string) raw {
	return raw{
		Number: number, Airport: "BCN", Date: "18/10/2026", Scheduled: sched + ":00",
		EstimatedDate: "18/10/2026", Estimated: est + ":00", Other: "PMI", OtherCity: "PALMA DE MALLORCA",
		Status: status, Codes: codes, Terminal: "T1", Aircraft: "A32A", Gate: "B43", Gate2: "null",
		CounterFrom: "501", CounterTo: "514", Belt: "null", Belt2: "null", AirlineIATA: iata, AirlineName: iata,
	}
}

func seriesFor(own map[string][]int) *Series {
	s := &Series{own: map[string]map[int]bool{}}
	for airline, list := range own {
		for _, n := range list {
			s.add(airline, n)
		}
	}
	return s
}

func TestCodesharesFoldIntoTheOperatingFlight(t *testing.T) {
	// The PMI–BCN flight at 22:25 on 18/10/2026: AENA lists the Vueling row first and nothing
	// in the rows says who flies it. Iberia flies alone under other series, never 51xx.
	rows := []raw{
		row("3919", "VY", "VY,VLG,VY,VLG,VLG,VLG", "22:25", "22:25", "SCH"),
		row("3755", "QR", "QR,QTR,QR,QTR,QTR,QTR", "22:25", "22:25", "SCH"),
		row("5408", "LL", "LL,LVL,LL,LVL,LVL,LVL", "22:25", "22:25", "SCH"),
		row("5139", "IB", "IB,IBE,,IB,IB,IBE", "22:25", "22:25", "SCH"),
	}
	series := seriesFor(map[string][]int{"VY": {39}, "IB": {4, 31}, "QR": {1}})
	got := Normalize(rows, Departures, madrid, at("18/10/2026 12:00"), series)
	if len(got) != 1 {
		t.Fatalf("got %d flights, want the four rows folded into one", len(got))
	}
	if f := got[0]; f.Number != "VY3919" || len(f.Codeshare) != 3 {
		t.Errorf("operating %s with %v; want VY3919 with the other three", f.Number, f.Codeshare)
	}

	// Melilla: Air Nostrum flies as Iberia, and Vueling sells a seat on it.
	rows = []raw{row("5663", "VY", "VY,VLG,,VY,VLG,VLG", "07:00", "07:00", "SCH"), row("2263", "IB", "IB,IBE,IB,IBE,IBE,IBE", "07:00", "07:00", "SCH")}
	series = seriesFor(map[string][]int{"VY": {39}, "IB": {22}})
	if f := Normalize(rows, Departures, madrid, at("18/10/2026 06:00"), series)[0]; f.Number != "IB2263" {
		t.Errorf("Melilla flight read as %s", f.Number)
	}
}

func TestUnknownSeriesKeepsAENAsFirstRow(t *testing.T) {
	rows := []raw{row("3906", "VY", "", "09:05", "09:05", "SCH"), row("5126", "IB", "", "09:05", "09:05", "SCH")}
	if f := Normalize(rows, Departures, madrid, at("18/10/2026 06:00"), NewSeries())[0]; f.Number != "VY3906" && f.Number != "IB5126" {
		t.Errorf("got %s", f.Number)
	}
	if f := Normalize(rows, Departures, madrid, at("18/10/2026 06:00"), nil)[0]; f.Number != "VY3906" {
		t.Errorf("without series the first row should stay, got %s", f.Number)
	}
}

func TestSeriesLearnFromSoloFlights(t *testing.T) {
	s := seriesFor(nil)
	rows := []raw{row("2495", "FR", "", "12:20", "12:20", "SCH")}
	Normalize(rows, Departures, madrid, at("18/10/2026 06:00"), s)
	if !s.Owns("FR", "2401") || s.Owns("FR", "2595") {
		t.Error("a solo FR2495 did not teach series 24 (and only 24)")
	}
	shared := []raw{row("3919", "VY", "", "22:25", "22:25", "SCH"), row("5139", "IB", "", "22:25", "22:25", "SCH")}
	Normalize(shared, Departures, madrid, at("18/10/2026 06:00"), s)
	if s.Owns("IB", "5139") {
		t.Error("a codeshare taught Iberia a series it only sells")
	}
}

func TestIberiaSuffixRowIsNotACodeshare(t *testing.T) {
	a := row("0404", "IB", "IB,IBE,IB,IBE,IBE,IBE", "09:15", "09:47", "BOR")
	b := row("0404S", "IB", "IB,IBE,IB,IBE,IBE,IBE", "09:15", "09:47", "BOR")
	b.CounterFrom, b.CounterTo = "906", "906"
	got := Normalize([]raw{a, b}, Departures, madrid, at("18/10/2026 12:00"), nil)
	for _, f := range got {
		for _, c := range f.Codeshare {
			if c == "IB0404S" {
				t.Errorf("IB0404S listed as a codeshare of %s", f.Number)
			}
		}
	}
}

func TestFinishedBeforeItsTimeIsNotTrusted(t *testing.T) {
	r := row("3070", "FR", "FR,RYR,,FR,RYR,RYR", "13:25", "14:20", "BOR")
	f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 10:35"), nil)[0]
	if f.Final {
		t.Error("a flight four hours from its new time was taken as finished")
	}
	if f.StatusLabel != "Delayed *" || !f.Doubtful {
		t.Errorf("label %q", f.StatusLabel)
	}

	f = Normalize([]raw{r}, Departures, madrid, at("18/10/2026 14:30"), nil)[0]
	if !f.Final {
		t.Error("finished after its time should be final")
	}
}

func TestDelayAcrossMidnight(t *testing.T) {
	r := row("6378", "FR", "FR,RYR,,FR,RYR,RYR", "23:55", "00:40", "RET")
	r.EstimatedDate = "19/10/2026"
	f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 20:00"), nil)[0]
	if f.DelayMin != 45 || f.EstimatedDate != "19/10/2026" {
		t.Errorf("delay %d on %q, want 45 on 19/10/2026", f.DelayMin, f.EstimatedDate)
	}
}

func TestNullsAreEmpty(t *testing.T) {
	r := row("2495", "FR", "FR,RYR,,FR,RYR,RYR", "12:20", "12:20", "INI")
	r.Gate, r.Terminal = "null", "T2"
	f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 10:00"), nil)[0]
	if f.Gate != "" || f.Estimated != "" || f.StatusLabel != "On time" {
		t.Errorf("gate %q estimated %q label %q", f.Gate, f.Estimated, f.StatusLabel)
	}
}

func TestConcurrentCallersShareOneRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, time.Minute)
	done := make(chan struct{})
	for range 5 {
		go func() {
			_, _, _ = c.Board(context.Background(), "BCN", Departures, true)
			done <- struct{}{}
		}()
	}
	for range 5 {
		<-done
	}
	_, _, _ = c.Board(context.Background(), "bcn", Departures, true)
	if n := hits.Load(); n != 1 {
		t.Errorf("AENA was asked %d times, want 1", n)
	}
}

func TestHTMLInsteadOfJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>`))
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL, time.Minute).Board(context.Background(), "BCN", Departures, true); err == nil {
		t.Error("an HTML page was accepted as a board")
	}
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"FR2495":   "FR2495",
		"FR 02495": "FR2495",
		"IB0404":   "IB404",
		"IB 404":   "IB404",
		"IB0404S":  "IB404S",
		"U2 8010":  "U28010",
		"VLG3906":  "VLG3906",
		"vy3906":   "VY3906",
		"6E0021":   "6E21",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCacheStaysBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := New(srv.URL, time.Minute)
	for i := range maxShort + 10 {
		code := string(rune('A'+i/26%26)) + string(rune('A'+i%26)) + "X"
		_, _, _ = c.Board(context.Background(), code, Departures, true)
	}
	for i := range maxFull + 3 {
		_, _, _ = c.Board(context.Background(), string(rune('A'+i))+"YY", Arrivals, false)
	}
	if n := len(c.boards); n > maxShort+maxFull {
		t.Errorf("%d boards cached, cap is %d", n, maxShort+maxFull)
	}
}

func TestFailuresAreNotRetriedOnEveryRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(srv.URL, time.Minute)
	for range 5 {
		if _, _, err := c.Board(context.Background(), "BCN", Departures, true); err == nil {
			t.Fatal("a failing AENA returned no error")
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("a down AENA was asked %d times in a row", n)
	}
}

func TestFarEndInEnglish(t *testing.T) {
	r := row("2495", "FR", "FR,RYR,,FR,RYR,RYR", "12:20", "12:20", "INI")
	f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 10:00"), nil)[0]
	if f.OtherCity != "Palma de Mallorca" || f.OtherLocal != "PALMA DE MALLORCA" {
		t.Errorf("city %q, local %q", f.OtherCity, f.OtherLocal)
	}
	r.Other, r.OtherCity = "ZZZ", "SITIO RARO"
	if f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 10:00"), nil)[0]; f.OtherCity != "SITIO RARO" {
		t.Errorf("an unknown code lost AENA's name: %q", f.OtherCity)
	}
}

func TestSameTimeOnAnotherDayIsUnchanged(t *testing.T) {
	r := row("6985", "FR", "FR,RYR,,FR,RYR,RYR", "20:35", "20:35", "BOR")
	r.Date, r.EstimatedDate = "17/10/2026", "18/10/2026"
	f := Normalize([]raw{r}, Departures, madrid, at("18/10/2026 13:30"), nil)[0]
	if f.DelayMin != 0 || f.Estimated != "" || f.Doubtful || !f.Final {
		t.Errorf("yesterday's departed flight read as %+v", f)
	}
}

func TestICAONumbers(t *testing.T) {
	rows := []raw{
		row("3906", "VY", "VY,VLG,,VY,VLG,VLG", "09:05", "09:40", "BOR"),
		row("5126", "IB", "IB,IBE,IB,IBE,IBE,IBE", "09:05", "09:40", "BOR"),
	}
	rows[0].AirlineICAO, rows[1].AirlineICAO = "VLG", "IBE"
	f := Normalize(rows, Departures, madrid, at("18/10/2026 12:00"), seriesFor(map[string][]int{"VY": {39}}))[0]
	if len(f.ICAO) != 2 || f.ICAO[0] != "VLG3906" || f.ICAO[1] != "IBE5126" {
		t.Errorf("icao = %v", f.ICAO)
	}
}

func TestZoneBeforeTheGate(t *testing.T) {
	// BCN names the gate area in puertaSegunda while puertaPrimera is still empty (EC1030, 26/10/2026).
	cases := []struct {
		gate, gate2 string
		dir         Direction
		wantGate    string
		wantZone    string
	}{
		{"null", "RSU", Departures, "", "R/S/U"},
		{"null", "A", Departures, "", "A"},
		{"R13", "null", Departures, "R13", ""},
		{"S12", "S14", Departures, "S12/S14", ""},
		{"null", "A", Arrivals, "/A", ""},
	}
	for _, c := range cases {
		r := row("1030", "EC", "", "08:55", "08:55", "SCH")
		r.Gate, r.Gate2 = c.gate, c.gate2
		f := Normalize([]raw{r}, c.dir, madrid, at("18/10/2026 06:00"), nil)[0]
		if f.Gate != c.wantGate || f.Zone != c.wantZone {
			t.Errorf("%s+%s %s: gate %q zone %q, want %q %q", c.gate, c.gate2, c.dir, f.Gate, f.Zone, c.wantGate, c.wantZone)
		}
	}
}
