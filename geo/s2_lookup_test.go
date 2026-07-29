package geo

import (
	"testing"

	"github.com/golang/geo/s2"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

func fenceCollection(t testing.TB, name string, rings ...orb.Ring) *geojson.FeatureCollection {
	t.Helper()
	fc := geojson.NewFeatureCollection()
	for i, ring := range rings {
		f := geojson.NewFeature(orb.Polygon{ring})
		f.Properties["name"] = name
		if len(rings) > 1 {
			f.Properties["name"] = name + string(rune('A'+i))
		}
		fc.Append(f)
	}
	return fc
}

// square returns a ring around (latC, lonC) with the given half-size in
// degrees, wound counter-clockwise when ccw is true.
func square(latC, lonC, half float64, ccw bool) orb.Ring {
	// orb.Point is {lon, lat}
	r := orb.Ring{
		{lonC - half, latC - half},
		{lonC + half, latC - half},
		{lonC + half, latC + half},
		{lonC - half, latC + half},
		{lonC - half, latC - half},
	}
	if !ccw {
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
	}
	return r
}

func cell15(lat, lon float64) s2.CellID {
	return s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(S2LookupLevel)
}

// A clockwise-wound ring must produce the same lookup as its CCW mirror —
// without loop normalization S2 interprets CW as the ring's complement (the
// whole planet minus the fence) and the covering explodes.
func TestS2LookupWindingOrderIrrelevant(t *testing.T) {
	ccw := BuildS2LookupFromFeatures(fenceCollection(t, "test", square(50, 8, 0.5, true)))
	cw := BuildS2LookupFromFeatures(fenceCollection(t, "test", square(50, 8, 0.5, false)))

	if ccw.CellCount() == 0 {
		t.Fatal("CCW fence produced an empty lookup")
	}
	if ccw.CellCount() != cw.CellCount() || len(ccw.edgeCells) != len(cw.edgeCells) {
		t.Errorf("winding order changed the lookup: ccw %d/%d cells, cw %d/%d cells",
			ccw.CellCount(), len(ccw.edgeCells), cw.CellCount(), len(cw.edgeCells))
	}
	// Sanity ceiling: a 1°x1° fence must not cover the planet. A flat
	// level-15 covering of this square is ~140k cells; the coarse-interior
	// build should be far below even that.
	if total := ccw.CellCount() + len(ccw.edgeCells); total > 100_000 {
		t.Errorf("lookup suspiciously large (%d cells) — inverted loop?", total)
	}
}

func TestS2LookupInteriorEdgeAndOutside(t *testing.T) {
	l := BuildS2LookupFromFeatures(fenceCollection(t, "test", square(50, 8, 0.5, true)))

	if areas := l.Lookup(cell15(50, 8)); len(areas) != 1 || areas[0].Name != "test" {
		t.Errorf("deep-interior cell: got %v, want [test]", areas)
	}
	// On the fence boundary: must return nil (polygon fallback).
	if areas := l.Lookup(cell15(50, 8.5)); len(areas) != 0 {
		t.Errorf("boundary cell: got %v, want nil", areas)
	}
	if areas := l.Lookup(cell15(51.5, 8)); len(areas) != 0 {
		t.Errorf("outside cell: got %v, want nil", areas)
	}
}

// Overlapping fences may store their interiors at different levels; a cell
// inside both must return both areas via the parent walk.
func TestS2LookupOverlapUnion(t *testing.T) {
	fc := geojson.NewFeatureCollection()
	big := geojson.NewFeature(orb.Polygon{square(50, 8, 0.5, true)})
	big.Properties["name"] = "big"
	small := geojson.NewFeature(orb.Polygon{square(50, 8, 0.05, true)})
	small.Properties["name"] = "small"
	fc.Append(big)
	fc.Append(small)

	l := BuildS2LookupFromFeatures(fc)

	areas := l.Lookup(cell15(50, 8))
	names := map[string]bool{}
	for _, a := range areas {
		names[a.Name] = true
	}
	if !names["big"] || !names["small"] || len(areas) != 2 {
		t.Errorf("overlap union: got %v, want [big small]", areas)
	}

	// Inside big only.
	if areas := l.Lookup(cell15(50.3, 8)); len(areas) != 1 || areas[0].Name != "big" {
		t.Errorf("big-only cell: got %v, want [big]", areas)
	}
}

// Coarse-interior storage must actually engage for a large fence: interior
// cells stored at coarse levels means far fewer entries than a flat
// level-15 covering would produce.
func TestS2LookupUsesCoarseInterior(t *testing.T) {
	l := BuildS2LookupFromFeatures(fenceCollection(t, "test", square(50, 8, 0.5, true)))

	sawCoarse := false
	for id := range l.cells {
		if id.Level() < S2LookupLevel {
			sawCoarse = true
			break
		}
	}
	if !sawCoarse {
		t.Error("no coarse interior cells stored — covering is flat level-15")
	}
	// Flat level-15 interior for ~1°x1° at lat 50 would be ~100k+ cells.
	if l.CellCount() > 20_000 {
		t.Errorf("interior cell count %d suggests coarse storage is not working", l.CellCount())
	}
}

// The S2 fast path must agree with the polygon fallback on fences with
// hole rings: cells inside a hole are excluded, cells crossing the hole
// boundary fall back (nil), interior cells outside the hole match.
func TestS2LookupHonorsHoles(t *testing.T) {
	outer := square(50, 8, 0.5, true)
	hole := square(50, 8, 0.1, false) // opposite winding, as GeoJSON prescribes
	fc := geojson.NewFeatureCollection()
	f := geojson.NewFeature(orb.Polygon{outer, hole})
	f.Properties["name"] = "test"
	fc.Append(f)

	l := BuildS2LookupFromFeatures(fc)

	if areas := l.Lookup(cell15(50, 8)); len(areas) != 0 {
		t.Errorf("cell inside hole: got %v, want none", areas)
	}
	if areas := l.Lookup(cell15(50, 8.3)); len(areas) != 1 || areas[0].Name != "test" {
		t.Errorf("interior cell outside hole: got %v, want [test]", areas)
	}
	if areas := l.Lookup(cell15(51.5, 8)); len(areas) != 0 {
		t.Errorf("outside cell: got %v, want none", areas)
	}
}
