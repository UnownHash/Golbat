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
