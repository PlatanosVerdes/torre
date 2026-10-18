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
	IATA    string  `json:"iata"`
	Name    string  `json:"name"`
	City    string  `json:"city"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

var (
	once   sync.Once
	all    []Place
	byCode map[string]Place
)

func load() {
	var rows [][]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		panic("places.json: " + err.Error())
	}
	all = make([]Place, 0, len(rows))
	byCode = make(map[string]Place, len(rows))
	for _, r := range rows {
		var p Place
		if len(r) != 6 ||
			json.Unmarshal(r[0], &p.IATA) != nil || json.Unmarshal(r[1], &p.Name) != nil ||
			json.Unmarshal(r[2], &p.City) != nil || json.Unmarshal(r[3], &p.Country) != nil ||
			json.Unmarshal(r[4], &p.Lat) != nil || json.Unmarshal(r[5], &p.Lon) != nil {
			panic("places.json: a row is not [iata, name, city, country, lat, lon]")
		}
		all = append(all, p)
		byCode[p.IATA] = p
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
