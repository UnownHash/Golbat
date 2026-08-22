package db

import (
	"strings"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// TestFenceContainsPredicateBindsTheFence is the regression test for geofence
// SQL injection. The predicate must be a constant with a bind placeholder: a
// geojson.Feature round-trips its whole properties map through MarshalJSON, so
// any fence content spliced into the statement text carries whatever quoting
// the caller sent.
func TestFenceContainsPredicateBindsTheFence(t *testing.T) {
	if !strings.Contains(FenceContainsPredicate, "?") {
		t.Fatalf("predicate has no bind placeholder: %s", FenceContainsPredicate)
	}
	if strings.Contains(FenceContainsPredicate, "'") {
		t.Fatalf("predicate still quotes a literal, so fence content could escape it: %s", FenceContainsPredicate)
	}
}

// TestFenceQueryArgsCarriesHostilePropertiesAsData feeds the payload that used
// to break out of the statement and checks it survives only as an argument.
func TestFenceQueryArgsCarriesHostilePropertiesAsData(t *testing.T) {
	const payload = `x' OR 1=1 -- `

	fence := geojson.NewFeature(orb.Polygon{{{0, 0}, {2, 0}, {2, 4}, {0, 4}, {0, 0}}})
	fence.Properties["name"] = payload

	args, err := FenceQueryArgs(fence)
	if err != nil {
		t.Fatalf("FenceQueryArgs: %v", err)
	}
	if len(args) != 5 {
		t.Fatalf("got %d args, want 4 bbox corners plus the fence", len(args))
	}

	fenceJSON, ok := args[4].(string)
	if !ok {
		t.Fatalf("fence arg is %T, want string", args[4])
	}
	if !strings.Contains(fenceJSON, payload) {
		t.Fatal("fence argument lost the properties payload, so this test proves nothing")
	}
	// The payload rides in the argument. The statement it is bound into is a
	// constant, so there is nothing for the quote to terminate.
	if strings.Contains(FenceContainsPredicate, payload) {
		t.Fatal("payload reached the statement text")
	}
}

// TestFenceQueryArgsBoundingBoxOrder locks the corner order every geofence
// query binds: min lat, min lon, max lat, max lon.
func TestFenceQueryArgsBoundingBoxOrder(t *testing.T) {
	// lon spans 0..2, lat spans 0..4, so a swapped pair is visible.
	fence := geojson.NewFeature(orb.Polygon{{{0, 0}, {2, 0}, {2, 4}, {0, 4}, {0, 0}}})

	args, err := FenceQueryArgs(fence)
	if err != nil {
		t.Fatalf("FenceQueryArgs: %v", err)
	}
	want := []float64{0, 0, 4, 2} // minLat, minLon, maxLat, maxLon
	for i, w := range want {
		got, ok := args[i].(float64)
		if !ok {
			t.Fatalf("arg %d is %T, want float64", i, args[i])
		}
		if got != w {
			t.Fatalf("arg %d = %v, want %v (order is minLat, minLon, maxLat, maxLon)", i, got, w)
		}
	}
}

// TestNewFenceMatcherFallsBackForNonPolygons locks the fallback that keeps
// exotic geometries working: containment moves to Go only for polygons, and
// anything else must still reach the SQL predicate.
func TestNewFenceMatcherFallsBackForNonPolygons(t *testing.T) {
	if _, ok := newFenceMatcher(geojson.NewFeature(orb.Point{1, 1})); ok {
		t.Fatal("a Point fence must fall back to SQL containment")
	}
	if _, ok := newFenceMatcher(geojson.NewFeature(orb.LineString{{0, 0}, {1, 1}})); ok {
		t.Fatal("a LineString fence must fall back to SQL containment")
	}
	if _, ok := newFenceMatcher(geojson.NewFeature(orb.Polygon{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}})); !ok {
		t.Fatal("a Polygon fence must be matched in Go")
	}
	if _, ok := newFenceMatcher(geojson.NewFeature(orb.MultiPolygon{
		{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}},
	})); !ok {
		t.Fatal("a MultiPolygon fence must be matched in Go")
	}
}

// TestFenceMatcherContains covers the lat/lon argument order, which is the easy
// thing to get backwards: the fence is in GeoJSON lon/lat order while the rows
// scan as lat, lon. The fence below is deliberately not square, so a swap shows.
func TestFenceMatcherContains(t *testing.T) {
	// lon spans 0..1, lat spans 0..8.
	fence := geojson.NewFeature(orb.Polygon{{{0, 0}, {1, 0}, {1, 8}, {0, 8}, {0, 0}}})
	m, ok := newFenceMatcher(fence)
	if !ok {
		t.Fatal("expected a polygon matcher")
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

// TestFenceBoundArgsOrder locks the corner order the bounding-box queries bind.
func TestFenceBoundArgsOrder(t *testing.T) {
	fence := geojson.NewFeature(orb.Polygon{{{0, 0}, {2, 0}, {2, 4}, {0, 4}, {0, 0}}})
	args := FenceBoundArgs(fence)
	want := []float64{0, 0, 4, 2} // minLat, minLon, maxLat, maxLon
	if len(args) != len(want) {
		t.Fatalf("got %d args, want %d", len(args), len(want))
	}
	for i, w := range want {
		if got := args[i].(float64); got != w {
			t.Fatalf("arg %d = %v, want %v (minLat, minLon, maxLat, maxLon)", i, got, w)
		}
	}
}
