// Package places knows every airport with scheduled flights, by IATA code, in English: AENA names
// the far end of a flight in Spanish ("LONDRES /GATWICK") and only knows the places it flies to.
// The data is OurAirports' (public domain), regenerated with `go run ./cmd/genplaces`.
package places

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed places.json
var raw []byte

type Place struct {
	IATA    string `json:"iata"`
	Name    string `json:"name"`
	City    string `json:"city"`
	Country string `json:"country"`
}

var (
	once   sync.Once
	all    []Place
	byCode map[string]Place
)

func load() {
	var rows [][4]string
	if err := json.Unmarshal(raw, &rows); err != nil {
		panic("places.json: " + err.Error())
	}
	all = make([]Place, len(rows))
	byCode = make(map[string]Place, len(rows))
	for i, r := range rows {
		all[i] = Place{IATA: r[0], Name: r[1], City: r[2], Country: r[3]}
		byCode[r[0]] = all[i]
	}
}

func All() []Place {
	once.Do(load)
	return all
}

func Lookup(code string) (Place, bool) {
	once.Do(load)
	p, ok := byCode[code]
	return p, ok
}
