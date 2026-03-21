//go:build ignore

package decoder

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"golbat/geo"

	"github.com/golang/geo/s2"
	"github.com/paulmach/orb/geojson"
)

type KojiTestResponse struct {
	Data geojson.FeatureCollection `json:"data"`
}

func TestMatchStatsGeofenceWithCellVsRtree(t *testing.T) {
	data, err := os.ReadFile("../cache/geofences.txt")
	if err != nil {
		t.Fatalf("Failed to read geofences.txt: %v", err)
	}

	var response KojiTestResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("Failed to parse geofences.txt: %v", err)
	}

	fc := &response.Data
	t.Logf("Loaded %d features from geofences.txt", len(fc.Features))

	testStatsTree := geo.LoadRtree(fc)
	t.Logf("Built rtree")
	testS2Lookup := geo.BuildS2LookupFromFeatures(fc)
	t.Logf("Built S2 lookup with %d cells, size: %.2f MB", testS2Lookup.CellCount(), float64(testS2Lookup.SizeBytes())/(1024*1024))

	minLat, maxLat := 49.002043, 54.854478
	minLon, maxLon := 14.120178, 24.145783

	const numPoints = 50000
	rng := rand.New(rand.NewSource(42))

	var mismatches int
	var s2Hits int
	var rtreeHits int
	var rtreeTime time.Duration
	var lookupTime time.Duration

	for i := 0; i < numPoints; i++ {
		lat := minLat + rng.Float64()*(maxLat-minLat)
		lon := minLon + rng.Float64()*(maxLon-minLon)

		cellID := s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(geo.S2LookupLevel)

		start := time.Now()
		areasRtree := geo.MatchGeofencesRtree(testStatsTree, lat, lon)
		rtreeTime += time.Since(start)

		start = time.Now()
		areasS2 := testS2Lookup.Lookup(cellID)
		var areasWithCell []geo.AreaName
		if len(areasS2) > 0 {
			areasWithCell = areasS2
			s2Hits++
		} else {
			areasWithCell = geo.MatchGeofencesRtree(testStatsTree, lat, lon)
			rtreeHits++
		}
		lookupTime += time.Since(start)

		if !areasEqual(areasRtree, areasWithCell) {
			mismatches++
			if mismatches <= 10 {
				t.Logf("Mismatch at point %d (%.6f, %.6f): rtree=%v, withCell=%v",
					i, lat, lon, areasRtree, areasWithCell)
			}
		}
	}

	t.Logf("Results: %d points tested", numPoints)
	t.Logf("S2 lookup hits: %d (%.2f%%)", s2Hits, float64(s2Hits)/float64(numPoints)*100)
	t.Logf("Rtree fallback: %d (%.2f%%)", rtreeHits, float64(rtreeHits)/float64(numPoints)*100)
	t.Logf("Mismatches: %d (%.2f%%)", mismatches, float64(mismatches)/float64(numPoints)*100)

	// Mismatches should be 0 — edge cells are excluded from the S2 lookup,
	// so the rtree fallback always provides the complete result.
	if mismatches > 0 {
		t.Errorf("Expected 0 mismatches, got %d. Edge cells should be excluded from S2 lookup.", mismatches)
	}

	t.Logf("Timing: rtree only: %v (%.2f µs/call)", rtreeTime, float64(rtreeTime.Microseconds())/float64(numPoints))
	t.Logf("Timing: lookup+fallback: %v (%.2f µs/call)", lookupTime, float64(lookupTime.Microseconds())/float64(numPoints))
	t.Logf("Speedup: %.2fx", float64(rtreeTime)/float64(lookupTime))
}

func areasEqual(a, b []geo.AreaName) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}

	aCopy := make([]geo.AreaName, len(a))
	bCopy := make([]geo.AreaName, len(b))
	copy(aCopy, a)
	copy(bCopy, b)

	sort.Slice(aCopy, func(i, j int) bool {
		if aCopy[i].Parent != aCopy[j].Parent {
			return aCopy[i].Parent < aCopy[j].Parent
		}
		return aCopy[i].Name < aCopy[j].Name
	})
	sort.Slice(bCopy, func(i, j int) bool {
		if bCopy[i].Parent != bCopy[j].Parent {
			return bCopy[i].Parent < bCopy[j].Parent
		}
		return bCopy[i].Name < bCopy[j].Name
	})

	return reflect.DeepEqual(aCopy, bCopy)
}
