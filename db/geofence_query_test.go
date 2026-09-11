package db

import (
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// TestNewFenceMatcherRejectsNonAreas: a fence is an area. Points and lines
// are errors, not fences that match nothing; polygons compile.
func TestNewFenceMatcherRejectsNonAreas(t *testing.T) {
	for _, g := range []orb.Geometry{orb.Point{1, 1}, orb.LineString{{0, 0}, {1, 1}}, nil} {
		if _, err := newFenceMatcher(geojson.NewFeature(g)); err == nil {
			t.Errorf("%T fence accepted, want an error", g)
		}
	}
	if _, err := newFenceMatcher(nil); err == nil {
		t.Error("nil feature accepted, want an error")
	}
	for _, g := range []orb.Geometry{
		orb.Polygon{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}},
		orb.MultiPolygon{{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}}},
	} {
		if _, err := newFenceMatcher(geojson.NewFeature(g)); err != nil {
			t.Errorf("%T fence rejected: %v", g, err)
		}
	}
}

// TestFenceMatcherContains covers the lat/lon argument order, which is the easy
// thing to get backwards: the fence is in GeoJSON lon/lat order while the rows
// scan as lat, lon. The fence below is deliberately not square, so a swap shows.
func TestFenceMatcherContains(t *testing.T) {
	// lon spans 0..1, lat spans 0..8.
	fence := geojson.NewFeature(orb.Polygon{{{0, 0}, {1, 0}, {1, 8}, {0, 8}, {0, 0}}})
	m, err := newFenceMatcher(fence)
	if err != nil {
		t.Fatalf("expected a polygon matcher: %v", err)
	}

	cases := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"inside", 4, 0.5, true},
		{"outside in lon", 4, 5, false},
		{"outside in lat", 9, 0.5, false},
		{"swapped lat/lon lands outside", 0.5, 4, false},
		{"on the boundary counts as inside", 0, 0.5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m.contains(c.lat, c.lon); got != c.want {
				t.Fatalf("contains(lat=%v, lon=%v) = %v, want %v", c.lat, c.lon, got, c.want)
			}
		})
	}
}

// TestFenceBoundArgsOrder locks the corner order the bounding-box query binds:
// min lat, min lon, max lat, max lon. Every corner is distinct, so a swapped
// min pair is as visible as a swapped max pair.
func TestFenceBoundArgsOrder(t *testing.T) {
	fence := geojson.NewFeature(orb.Polygon{{{10, 20}, {12, 20}, {12, 24}, {10, 24}, {10, 20}}})
	args := FenceBoundArgs(fence)
	want := []float64{20, 10, 24, 12} // minLat, minLon, maxLat, maxLon
	if len(args) != len(want) {
		t.Fatalf("got %d args, want %d", len(args), len(want))
	}
	for i, w := range want {
		if got := args[i].(float64); got != w {
			t.Fatalf("arg %d = %v, want %v (minLat, minLon, maxLat, maxLon)", i, got, w)
		}
	}
}
