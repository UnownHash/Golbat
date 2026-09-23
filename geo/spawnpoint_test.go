package geo

import (
	"testing"

	"github.com/golang/geo/s2"
)

// Production spawnpoint rows: every id shifts into the level-20 S2 cell
// whose center is the stored location, to the metre.
func TestSpawnpointLocation(t *testing.T) {
	cases := []struct {
		id       int64
		lat, lon float64
	}{
		{8855336329721, 34.06451334604487, -117.39832236239404},
		{8855330996401, 34.02985747646264, -117.33249566359322},
		{8854983575577, 33.68415182259510, -117.15326745608779},
		{8848485935701, 34.04633813652718, -117.76743538334328},
		{8855359544681, 33.88185012906927, -117.59906200096601},
	}
	for _, c := range cases {
		got, ok := SpawnpointLocation(c.id)
		if !ok {
			t.Fatalf("id %d: not decodable", c.id)
		}
		d := s2.LatLngFromDegrees(got.Latitude, got.Longitude).Distance(s2.LatLngFromDegrees(c.lat, c.lon)).Radians() * 6371000
		if d > 0.5 {
			t.Errorf("id %d: derived %f,%f is %.2fm from stored %f,%f", c.id, got.Latitude, got.Longitude, d, c.lat, c.lon)
		}
	}
	for _, bad := range []int64{0, -1} {
		if _, ok := SpawnpointLocation(bad); ok {
			t.Errorf("id %d must not decode", bad)
		}
	}
}
