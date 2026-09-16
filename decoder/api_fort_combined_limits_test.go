package decoder

import (
	"fmt"
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
	"github.com/tidwall/rtree"
)

// swapFortIndex installs an empty fort tree and lookup cache for one test
// and restores the originals (and the tree snapshot) on cleanup.
func swapFortIndex(t *testing.T) {
	t.Helper()
	oldLookup := fortLookupCache
	oldSnapshot := fortTreeSnapshot.Load()
	fortTreeMutex.Lock()
	oldTree := fortTree
	fortTree = rtree.RTreeG[FortId]{}
	fortTreeMutex.Unlock()
	fortLookupCache = xsync.NewMap[FortId, FortLookup]()
	fortTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		fortLookupCache = oldLookup
		fortTreeMutex.Lock()
		fortTree = oldTree
		fortTreeMutex.Unlock()
		fortTreeSnapshot.Store(oldSnapshot)
	})
}

// plantCombinedForts swaps in a fresh fort index holding the given number of
// gyms, pokestops and stations inside one bounding box. Returns the box.
func plantCombinedForts(t *testing.T, gyms, pokestops, stations int) (minLoc, maxLoc ApiLatLon) {
	t.Helper()
	swapFortIndex(t)

	n := 0
	plant := func(count int, fortType FortType) {
		for i := 0; i < count; i++ {
			n++
			id := mustFortId(t, fmt.Sprintf("%032x.16", 0x9000+n))
			lat := 40.0 + float64(n)/1000
			lon := -70.0 + float64(fortType)/100
			fortTreeMutex.Lock()
			fortTree.Insert([2]float64{lon, lat}, [2]float64{lon, lat}, id)
			fortTreeMutex.Unlock()
			fortLookupCache.Store(id, FortLookup{FortType: fortType, Lat: lat, Lon: lon, StationEndTimestamp: 1 << 40})
		}
	}
	plant(gyms, GYM)
	plant(pokestops, POKESTOP)
	plant(stations, STATION)
	fortTreeSnapshot.Store(nil)
	return ApiLatLon{Lat: 39, Lon: -71}, ApiLatLon{Lat: 41, Lon: -69}
}

// A type stops accepting matches at its own limit while the walk keeps
// filling the others; examined is counted per type for every fort of that
// type whose lookup loaded, capped or not.
func TestCombinedScanPerTypeLimits(t *testing.T) {
	withScanLimits(t)
	minLoc, maxLoc := plantCombinedForts(t, 3, 4, 2)

	res := internalGetFortsCombined(ApiFortCombinedScan{
		Min: minLoc, Max: maxLoc,
		Gyms:      &ApiFortTypeScanGroup{Limit: 2},
		Pokestops: &ApiFortTypeScanGroup{Limit: 3},
		Stations:  &ApiFortTypeScanGroup{Limit: 5},
	})

	if got := len(res.gyms.keys); got != 2 {
		t.Errorf("gyms matched %d, want 2 (per-type limit)", got)
	}
	if got := res.gyms.stats(); got != (ApiFortTypeScanStats{Examined: 3, LimitReached: true}) {
		t.Errorf("gyms stats = %+v, want examined 3, limit_reached true", got)
	}
	if got := len(res.pokestops.keys); got != 3 {
		t.Errorf("pokestops matched %d, want 3", got)
	}
	if got := res.pokestops.stats(); got != (ApiFortTypeScanStats{Examined: 4, LimitReached: true}) {
		t.Errorf("pokestops stats = %+v, want examined 4, limit_reached true", got)
	}
	if got := len(res.stations.keys); got != 2 {
		t.Errorf("stations matched %d, want 2 (below its limit of 5)", got)
	}
	if got := res.stations.stats(); got != (ApiFortTypeScanStats{Examined: 2, LimitReached: false}) {
		t.Errorf("stations stats = %+v, want examined 2, limit_reached false", got)
	}
	// Stations never capped, so the walk visited every fort in the box.
	if res.examined != 9 || res.skipped != 0 || res.total != 9 {
		t.Errorf("top-level examined/skipped/total = %d/%d/%d, want 9/0/9", res.examined, res.skipped, res.total)
	}
	if res.overallCapReached {
		t.Error("overall cap (server default) must not be reported as reached")
	}
	if !res.anyLimitReached() {
		t.Error("top-level limit_reached must be true when any type hit its limit")
	}
}

// Once every requested type has hit its limit the walk ends; an omitted type
// is excluded and reports zero examined and limit_reached=false.
func TestCombinedScanStopsWhenAllRequestedTypesCap(t *testing.T) {
	withScanLimits(t)
	minLoc, maxLoc := plantCombinedForts(t, 3, 4, 2)

	res := internalGetFortsCombined(ApiFortCombinedScan{
		Min: minLoc, Max: maxLoc,
		Gyms:      &ApiFortTypeScanGroup{Limit: 1},
		Pokestops: &ApiFortTypeScanGroup{Limit: 1},
	})

	if len(res.gyms.keys) != 1 || len(res.pokestops.keys) != 1 {
		t.Errorf("matched gyms %d pokestops %d, want 1 and 1", len(res.gyms.keys), len(res.pokestops.keys))
	}
	if !res.gyms.stats().LimitReached || !res.pokestops.stats().LimitReached {
		t.Error("both requested types should report limit_reached")
	}
	if len(res.stations.keys) != 0 || res.stations.stats() != (ApiFortTypeScanStats{}) {
		t.Errorf("omitted stations must be excluded with zero stats, got keys %d stats %+v", len(res.stations.keys), res.stations.stats())
	}
	if res.examined >= 9 && res.gyms.stats().Examined+res.pokestops.stats().Examined == 7 {
		t.Error("walk should have ended once both requested types capped, before visiting every fort")
	}
	if !res.anyLimitReached() {
		t.Error("top-level limit_reached must be true")
	}
}

// The top-level limit is still an overall cap across types; hitting it ends
// the walk and is reported at the top level even when no type hit its own.
func TestCombinedScanOverallCapStillApplies(t *testing.T) {
	withScanLimits(t)
	minLoc, maxLoc := plantCombinedForts(t, 3, 4, 2)

	res := internalGetFortsCombined(ApiFortCombinedScan{
		Min: minLoc, Max: maxLoc, Limit: 3,
		Gyms: &ApiFortTypeScanGroup{}, Pokestops: &ApiFortTypeScanGroup{}, Stations: &ApiFortTypeScanGroup{},
	})

	if matched := len(res.gyms.keys) + len(res.pokestops.keys) + len(res.stations.keys); matched != 3 {
		t.Errorf("matched %d across types, want 3 (overall cap)", matched)
	}
	if !res.overallCapReached || !res.anyLimitReached() {
		t.Error("overall cap must be reported at the top level")
	}
	if res.gyms.stats().LimitReached || res.pokestops.stats().LimitReached || res.stations.stats().LimitReached {
		t.Error("no type hit its own (default) limit")
	}
}

// A bare probe (all groups omitted) requests every type at the server default.
func TestCombinedScanBareProbeRequestsEveryType(t *testing.T) {
	withScanLimits(t)
	minLoc, maxLoc := plantCombinedForts(t, 3, 4, 2)

	res := internalGetFortsCombined(ApiFortCombinedScan{Min: minLoc, Max: maxLoc})

	if len(res.gyms.keys) != 3 || len(res.pokestops.keys) != 4 || len(res.stations.keys) != 2 {
		t.Errorf("matched %d/%d/%d, want 3/4/2", len(res.gyms.keys), len(res.pokestops.keys), len(res.stations.keys))
	}
	if res.gyms.stats().Examined != 3 || res.pokestops.stats().Examined != 4 || res.stations.stats().Examined != 2 {
		t.Errorf("examined per type = %d/%d/%d, want 3/4/2", res.gyms.stats().Examined, res.pokestops.stats().Examined, res.stations.stats().Examined)
	}
	if res.anyLimitReached() {
		t.Error("nothing reached a limit")
	}
}
