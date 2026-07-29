package geo

import (
	"math"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// CompiledFence is a geofence feature preprocessed for fast point-in-polygon
// matching. orb's planar.RingContains recomputes ring.Bound() — an O(vertices)
// sweep — on EVERY containment test; on a busy instance that recomputation
// alone was ~22% of total CPU (measured), inside entity-lock critical
// sections. Compiling caches each ring's bound once at load, and resolves the
// area name once instead of two Properties map lookups per match.
type CompiledFence struct {
	Area     AreaName
	polygons []compiledPolygon
}

type compiledPolygon struct {
	rings  []orb.Ring
	bounds []orb.Bound // cached ring.Bound(), same index as rings
}

// CompileFence preprocesses a geojson feature (Polygon or MultiPolygon).
// Returns nil for other geometry types.
func CompileFence(f *geojson.Feature) *CompiledFence {
	var mp orb.MultiPolygon
	switch g := f.Geometry.(type) {
	case orb.Polygon:
		mp = orb.MultiPolygon{g}
	case orb.MultiPolygon:
		mp = g
	default:
		return nil
	}

	name := f.Properties.MustString("name", "unknown")
	parent := f.Properties.MustString("parent", name)
	cf := &CompiledFence{Area: AreaName{Parent: parent, Name: name}}
	for _, poly := range mp {
		cp := compiledPolygon{
			rings:  poly,
			bounds: make([]orb.Bound, len(poly)),
		}
		for i, ring := range poly {
			cp.bounds[i] = ring.Bound()
		}
		cf.polygons = append(cf.polygons, cp)
	}
	return cf
}

// Contains reports whether the point is inside the fence (boundary counts as
// inside). Semantics match orb's planar.MultiPolygonContains exactly — see
// TestCompiledFenceMatchesPlanar — with the ring bounds served from cache.
func (cf *CompiledFence) Contains(p orb.Point) bool {
	for _, poly := range cf.polygons {
		if poly.contains(p) {
			return true
		}
	}
	return false
}

func (cp *compiledPolygon) contains(p orb.Point) bool {
	if len(cp.rings) == 0 || !ringContainsCached(cp.rings[0], cp.bounds[0], p) {
		return false
	}
	// Any hole ring containing the point excludes it.
	for i := 1; i < len(cp.rings); i++ {
		if ringContainsCached(cp.rings[i], cp.bounds[i], p) {
			return false
		}
	}
	return true
}

// ringContainsCached is planar.RingContains with the ring's bound supplied by
// the caller instead of recomputed per call.
func ringContainsCached(r orb.Ring, bound orb.Bound, point orb.Point) bool {
	if !bound.Contains(point) {
		return false
	}

	c, on := rayIntersect(point, r[0], r[len(r)-1])
	if on {
		return true
	}

	for i := 0; i < len(r)-1; i++ {
		inter, on := rayIntersect(point, r[i], r[i+1])
		if on {
			return true
		}
		if inter {
			c = !c
		}
	}

	return c
}

// rayIntersect is copied verbatim from github.com/paulmach/orb/planar
// (contains.go, MIT License, Copyright (c) 2017 Paul Mach) so that
// ringContainsCached is bit-for-bit equivalent to planar.RingContains.
// Original implementation: http://rosettacode.org/wiki/Ray-casting_algorithm#Go
func rayIntersect(p, s, e orb.Point) (intersects, on bool) {
	if s[0] > e[0] {
		s, e = e, s
	}

	switch p[0] {
	case s[0]:
		if p[1] == s[1] {
			// p == start
			return false, true
		} else if s[0] == e[0] {
			// vertical segment (s -> e)
			// return true if within the line, check to see if start or end is greater.
			if s[1] > e[1] && s[1] >= p[1] && p[1] >= e[1] {
				return false, true
			}

			if e[1] > s[1] && e[1] >= p[1] && p[1] >= s[1] {
				return false, true
			}
		}

		// Move the y coordinate to deal with degenerate case
		p[0] = math.Nextafter(p[0], math.Inf(1))
	case e[0]:
		if p[1] == e[1] {
			// matching the end point
			return false, true
		}

		p[0] = math.Nextafter(p[0], math.Inf(1))
	}

	if p[0] < s[0] || p[0] > e[0] {
		return false, false
	}

	if s[1] > e[1] {
		if p[1] > s[1] {
			return false, false
		} else if p[1] < e[1] {
			return true, false
		}
	} else {
		if p[1] > e[1] {
			return false, false
		} else if p[1] < s[1] {
			return true, false
		}
	}

	rs := (p[1] - s[1]) / (p[0] - s[0])
	ds := (e[1] - s[1]) / (e[0] - s[0])

	if rs == ds {
		return false, true
	}

	return rs <= ds, false
}
