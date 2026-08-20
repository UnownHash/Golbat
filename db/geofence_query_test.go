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
