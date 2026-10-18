package places

import "testing"

func TestLookup(t *testing.T) {
	for code, name := range map[string]string{"LGW": "London Gatwick", "PMI": "Palma de Mallorca", "EDI": "Edinburgh"} {
		p, ok := Lookup(code)
		if !ok || p.Name != name {
			t.Errorf("%s: got %+v", code, p)
		}
	}
	if _, ok := Lookup("XXX"); ok {
		t.Error("an unknown code was found")
	}
	if n := len(All()); n < 3000 {
		t.Errorf("only %d places; the generator lost most of them", n)
	}
}

func TestCoordinates(t *testing.T) {
	p, _ := Lookup("PMI")
	if p.Lat < 39 || p.Lat > 40 || p.Lon < 2 || p.Lon > 3 {
		t.Errorf("PMI at %v,%v", p.Lat, p.Lon)
	}
}
