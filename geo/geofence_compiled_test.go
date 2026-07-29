package geo

import (
	"math/rand/v2"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

func testFeature(t testing.TB, geom orb.Geometry) *geojson.Feature {
	t.Helper()
	f := geojson.NewFeature(geom)
	f.Properties["name"] = "test"
	f.Properties["parent"] = "parent"
	return f
}

// A polygon with a hole, and a second disjoint polygon (as a MultiPolygon).
func testMultiPolygon() orb.MultiPolygon {
	outer := orb.Ring{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}
	hole := orb.Ring{{4, 4}, {6, 4}, {6, 6}, {4, 6}, {4, 4}}
	island := orb.Ring{{20, 20}, {22, 20}, {22, 22}, {20, 22}, {20, 20}}
	return orb.MultiPolygon{{outer, hole}, {island}}
}

func TestCompiledFenceBasicSemantics(t *testing.T) {
	cf := CompileFence(testFeature(t, testMultiPolygon()))
	if cf == nil {
		t.Fatal("CompileFence returned nil for MultiPolygon")
	}
	if cf.Area.Name != "test" || cf.Area.Parent != "parent" {
		t.Errorf("area = %+v", cf.Area)
	}

	cases := []struct {
		p    orb.Point
		want bool
		desc string
	}{
		{orb.Point{2, 2}, true, "inside outer"},
		{orb.Point{5, 5}, false, "inside hole"},
		{orb.Point{4, 4}, true, "on hole boundary (boundary counts as in => excluded ring contains => out per orb)"},
		{orb.Point{0, 0}, true, "on outer boundary"},
		{orb.Point{21, 21}, true, "inside island polygon"},
		{orb.Point{15, 15}, false, "between polygons"},
		{orb.Point{-1, 5}, false, "outside bbox"},
	}
	mp := testMultiPolygon()
	for _, c := range cases {
		// Whatever orb says is the contract; the case list documents it.
		want := planar.MultiPolygonContains(mp, c.p)
		if got := cf.Contains(c.p); got != want {
			t.Errorf("%s: Contains(%v) = %v, planar says %v", c.desc, c.p, got, want)
		}
	}
}

// Differential test: CompiledFence.Contains must agree with
// planar.MultiPolygonContains on every point, including boundary-adjacent
// ones — the compiled path only caches bounds, it must not change results.
func TestCompiledFenceMatchesPlanar(t *testing.T) {
	mp := testMultiPolygon()
	cf := CompileFence(testFeature(t, mp))

	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20000; i++ {
		var p orb.Point
		switch i % 3 {
		case 0: // anywhere in/around the shapes
			p = orb.Point{rng.Float64()*30 - 3, rng.Float64()*30 - 3}
		case 1: // dense around the hole boundary
			p = orb.Point{4 + rng.Float64()*2, 4 + rng.Float64()*2}
		case 2: // exactly on grid lines through vertices/edges
			p = orb.Point{float64(rng.IntN(12)), rng.Float64() * 12}
		}
		want := planar.MultiPolygonContains(mp, p)
		if got := cf.Contains(p); got != want {
			t.Fatalf("divergence at %v: compiled=%v planar=%v", p, got, want)
		}
	}
}

// bigRing builds an n-vertex ring approximating a circle — realistic city
// geofences have hundreds to thousands of vertices, and orb's per-call
// Bound() recompute scales with vertex count.
func bigRing(n int) orb.Ring {
	r := make(orb.Ring, 0, n+1)
	for i := 0; i < n; i++ {
		angle := float64(i) / float64(n) * 2 * 3.141592653589793
		r = append(r, orb.Point{10 * cosApprox(angle), 10 * sinApprox(angle)})
	}
	r = append(r, r[0])
	return r
}

func cosApprox(x float64) float64 { return sinApprox(x + 3.141592653589793/2) }
func sinApprox(x float64) float64 {
	// cheap sine; precision is irrelevant for benchmark geometry
	for x > 3.141592653589793 {
		x -= 2 * 3.141592653589793
	}
	for x < -3.141592653589793 {
		x += 2 * 3.141592653589793
	}
	return x - x*x*x/6 + x*x*x*x*x/120
}

func BenchmarkContainsPlanar1kVerts(b *testing.B) {
	mp := orb.MultiPolygon{{bigRing(1000)}}
	p := orb.Point{2, 2}
	for b.Loop() {
		planar.MultiPolygonContains(mp, p)
	}
}

func BenchmarkContainsCompiled1kVerts(b *testing.B) {
	f := geojson.NewFeature(orb.MultiPolygon{{bigRing(1000)}})
	f.Properties["name"] = "big"
	cf := CompileFence(f)
	p := orb.Point{2, 2}
	for b.Loop() {
		cf.Contains(p)
	}
}

func BenchmarkContainsPlanar(b *testing.B) {
	mp := testMultiPolygon()
	p := orb.Point{2, 2}
	for b.Loop() {
		planar.MultiPolygonContains(mp, p)
	}
}

func BenchmarkContainsCompiled(b *testing.B) {
	cf := CompileFence(testFeature(b, testMultiPolygon()))
	p := orb.Point{2, 2}
	for b.Loop() {
		cf.Contains(p)
	}
}
