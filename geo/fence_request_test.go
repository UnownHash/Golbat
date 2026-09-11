package geo

import (
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// TestCompileFenceToleratesNonStringProperties guards the request-body path:
// a caller controls the properties map, and orb's Properties.MustString panics
// when a key is present with a non-string value even if a default is supplied.
func TestCompileFenceToleratesNonStringProperties(t *testing.T) {
	f := geojson.NewFeature(orb.Polygon{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}})
	f.Properties["name"] = 123
	f.Properties["parent"] = true

	cf := CompileFence(f)
	if cf == nil {
		t.Fatal("polygon fence compiled to nil")
	}
	if cf.Area.Name != "unknown" || cf.Area.Parent != "unknown" {
		t.Fatalf("Area = %+v, want the defaults for non-string properties", cf.Area)
	}
	if !cf.Contains(orb.Point{1, 1}) {
		t.Fatal("compiled fence lost its geometry")
	}
}

// TestNormaliseFenceFromBytesRejectsNilGeometry: a Feature with a null or
// missing geometry parses, but every consumer dereferences fence.Geometry, so
// the parse boundary must reject it with an error (a 400) instead.
func TestNormaliseFenceFromBytesRejectsNilGeometry(t *testing.T) {
	for _, body := range []string{
		`{"type":"Feature","geometry":null,"properties":{}}`,
		`{"type":"Feature","properties":{}}`,
	} {
		f, err := NormaliseFenceFromBytes([]byte(body))
		if err == nil {
			t.Errorf("body %s: got fence %+v with nil error, want an error", body, f)
		}
	}
}

// TestCompileFenceFlattensGeometryCollection: a GeometryCollection is a fence
// when it holds polygons; its polygon members are matched and its other
// members ignored.
func TestCompileFenceFlattensGeometryCollection(t *testing.T) {
	f := geojson.NewFeature(orb.Collection{
		orb.Polygon{{{0, 0}, {2, 0}, {2, 2}, {0, 2}, {0, 0}}},
		orb.Point{50, 50},
		orb.MultiPolygon{{{{10, 10}, {12, 10}, {12, 12}, {10, 12}, {10, 10}}}},
	})
	cf := CompileFence(f)
	if cf == nil {
		t.Fatal("collection with polygons compiled to nil")
	}
	for _, c := range []struct {
		p    orb.Point
		want bool
	}{{orb.Point{1, 1}, true}, {orb.Point{11, 11}, true}, {orb.Point{50, 50}, false}, {orb.Point{5, 5}, false}} {
		if got := cf.Contains(c.p); got != c.want {
			t.Errorf("Contains(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if CompileFence(geojson.NewFeature(orb.Collection{orb.Point{1, 1}})) != nil {
		t.Error("collection without polygons should not compile")
	}
}

// TestNormaliseFenceFromBytesRejectsNonAreaGeometry: a fence must be an area.
// Point and line bodies used to reach the database and match nothing (or a
// stop at the exact coordinate); they are rejected at the parse boundary now.
func TestNormaliseFenceFromBytesRejectsNonAreaGeometry(t *testing.T) {
	for _, body := range []string{
		`{"type":"Point","coordinates":[1,2]}`,
		`{"type":"LineString","coordinates":[[0,0],[1,1]]}`,
		`{"type":"Feature","properties":{},"geometry":{"type":"Point","coordinates":[1,2]}}`,
		`{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[1,2]}]}`,
	} {
		if f, err := NormaliseFenceFromBytes([]byte(body)); err == nil {
			t.Errorf("body %s: got fence %T with nil error, want an error", body, f.Geometry)
		}
	}
}

// TestNormaliseFenceFromBytesAcceptsAreas: polygons, multipolygons, and
// collections holding polygons all normalise to a polygonal fence.
func TestNormaliseFenceFromBytesAcceptsAreas(t *testing.T) {
	for _, body := range []string{
		`{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}`,
		`{"type":"MultiPolygon","coordinates":[[[[0,0],[2,0],[2,2],[0,2],[0,0]]]]}`,
		`{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}}`,
		`{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[9,9]},{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}]}`,
		`{"fence":[{"lat":0,"lon":0},{"lat":0,"lon":2},{"lat":2,"lon":2},{"lat":2,"lon":0}]}`,
	} {
		f, err := NormaliseFenceFromBytes([]byte(body))
		if err != nil {
			t.Errorf("body %s: %v", body, err)
			continue
		}
		cf := CompileFence(f)
		if cf == nil || !cf.Contains(orb.Point{1, 1}) {
			t.Errorf("body %s: normalised fence does not contain (1,1)", body)
		}
	}
}
