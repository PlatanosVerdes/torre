// genplaces turns OurAirports' airports.csv (public domain, https://ourairports.com/data/) into
// internal/places/places.json: every airport with an IATA code and scheduled flights.
//
//	go run ./cmd/genplaces > internal/places/places.json
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

const source = "https://davidmegginson.github.io/ourairports-data/airports.csv"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	resp, err := http.Get(source)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", source, resp.StatusCode)
	}

	r := csv.NewReader(resp.Body)
	header, err := r.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, need := range []string{"type", "name", "iso_country", "municipality", "scheduled_service", "iata_code"} {
		if _, ok := col[need]; !ok {
			return fmt.Errorf("column %q is missing; the CSV changed", need)
		}
	}

	// [iata, name, city, country]: positional to keep the embedded file small.
	var out [][4]string
	seen := map[string]bool{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		code := rec[col["iata_code"]]
		if len(code) != 3 || rec[col["scheduled_service"]] != "yes" || seen[code] {
			continue
		}
		switch rec[col["type"]] {
		case "large_airport", "medium_airport", "small_airport":
		default:
			continue
		}
		seen[code] = true
		// The municipality is kept whole ("Ingliston, Edinburgh"): it is only searched, never shown.
		out = append(out, [4]string{code, shorten(rec[col["name"]]), rec[col["municipality"]], rec[col["iso_country"]]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// shorten drops what every airport's name ends with: "London Gatwick Airport" is "London Gatwick".
func shorten(name string) string {
	for _, suffix := range []string{" International Airport", " Intl Airport", " Airport", " Airfield", " Aerodrome"} {
		if s, ok := strings.CutSuffix(name, suffix); ok && s != "" {
			return s
		}
	}
	return name
}
