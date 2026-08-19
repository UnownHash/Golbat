package decoder

import (
	"fmt"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
	"github.com/tidwall/rtree"

	"golbat/config"
)

// TestBuddyShowcaseDnfRunsBeforeResultCap locks the endpoint correctness
// invariant: unrelated showcases must never consume max_fort_results before
// the exact structured-focus predicate runs.
func TestBuddyShowcaseDnfRunsBeforeResultCap(t *testing.T) {
	oldLookup := fortLookupCache
	oldSnapshot := fortTreeSnapshot.Load()
	oldMax := config.Config.Tuning.MaxFortResults
	fortTreeMutex.Lock()
	oldTree := fortTree
	fortTree = rtree.RTreeG[string]{}
	fortTreeMutex.Unlock()
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	fortTreeSnapshot.Store(nil)
	config.Config.Tuning.MaxFortResults = 1
	t.Cleanup(func() {
		config.Config.Tuning.MaxFortResults = oldMax
		fortLookupCache = oldLookup
		fortTreeMutex.Lock()
		fortTree = oldTree
		fortTreeMutex.Unlock()
		fortTreeSnapshot.Store(oldSnapshot)
	})

	now := time.Now().Unix()
	const count = 8
	fortTreeMutex.Lock()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("buddy-cap-%d", i)
		lon := -70.0 + float64(i)/1000
		lat := 40.0
		fortTree.Insert([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		fortLookupCache.Store(id, FortLookup{
			FortType:              POKESTOP,
			Lat:                   lat,
			Lon:                   lon,
			ShowcaseBuddyMinLevel: 2,
			ShowcaseExpiry:        now + 3600,
		})
	}
	fortTreeMutex.Unlock()
	fortTreeSnapshot.Store(nil)

	// Discover the snapshot's actual traversal order, then put the sole exact
	// match last. The production calls below reuse this same snapshot.
	snapshot := getFortTreeSnapshot()
	order := make([]string, 0, count)
	snapshot.Search([2]float64{-71, 39}, [2]float64{-69, 41}, func(_, _ [2]float64, id string) bool {
		order = append(order, id)
		return true
	})
	if len(order) != count {
		t.Fatalf("setup traversal found %d forts, want %d", len(order), count)
	}
	target := order[len(order)-1]
	lookup, ok := fortLookupCache.Load(target)
	if !ok {
		t.Fatalf("setup target %q missing from lookup", target)
	}
	lookup.ShowcaseBuddyMinLevel = 3
	fortLookupCache.Store(target, lookup)

	great := int8(3)
	filter := ApiFortDnfFilter{ContestFocus: []ApiFortDnfContestFocus{{Type: "buddy", MinLevel: &great}}}
	scan := ApiFortScan{
		Min:        ApiLatLon{Lat: 39, Lon: -71},
		Max:        ApiLatLon{Lat: 41, Lon: -69},
		Limit:      0,
		DnfFilters: []ApiFortDnfFilter{filter},
	}

	keys, examined, skipped, total := internalGetForts(POKESTOP, scan)
	if len(keys) != 1 || keys[0] != target {
		t.Fatalf("single-type scan returned %v, want only later exact match %q", keys, target)
	}
	if examined != count || skipped != 0 || total != count {
		t.Fatalf("single-type scan stats = examined:%d skipped:%d total:%d, want %d/0/%d", examined, skipped, total, count, count)
	}

	gyms, stops, stations, examined, skipped, total := internalGetFortsCombined(ApiFortCombinedScan{
		Min:       scan.Min,
		Max:       scan.Max,
		Limit:     0,
		Pokestops: &ApiFortTypeScanGroup{DnfFilters: []ApiFortDnfFilter{filter}},
	})
	if len(gyms) != 0 || len(stations) != 0 || len(stops) != 1 || stops[0] != target {
		t.Fatalf("combined scan returned gyms=%v stops=%v stations=%v, want only stop %q", gyms, stops, stations, target)
	}
	if examined != count || skipped != 0 || total != count {
		t.Fatalf("combined scan stats = examined:%d skipped:%d total:%d, want %d/0/%d", examined, skipped, total, count, count)
	}
}
