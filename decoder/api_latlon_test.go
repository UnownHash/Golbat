package decoder

import (
	"encoding/json"
	"testing"
)

func TestApiLatLon_AcceptsEitherSpelling(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"short lat/lon", `{"lat":1.5,"lon":-2.5}`},
		{"long latitude/longitude", `{"latitude":1.5,"longitude":-2.5}`},
		{"mixed prefers short", `{"lat":1.5,"lon":-2.5,"latitude":9,"longitude":9}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ll ApiLatLon
			if err := json.Unmarshal([]byte(c.body), &ll); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if ll.Lat != 1.5 || ll.Lon != -2.5 {
				t.Errorf("got %+v, want {Lat:1.5 Lon:-2.5}", ll)
			}
			loc := ll.Location()
			if loc.Latitude != 1.5 || loc.Longitude != -2.5 {
				t.Errorf("Location() = %+v, want {1.5,-2.5}", loc)
			}
		})
	}
}

func TestApiLatLon_ErrorsWhenIncomplete(t *testing.T) {
	for _, body := range []string{`{}`, `{"lat":1}`, `{"lon":2}`, `{"latitude":1}`} {
		var ll ApiLatLon
		if err := json.Unmarshal([]byte(body), &ll); err == nil {
			t.Errorf("body %s: expected error, got none (%+v)", body, ll)
		}
	}
}
