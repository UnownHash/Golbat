package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

// TestGetAvailablePokestops seeds fortLookupCache + the quest-conditions map
// directly (initFortRtree pulls in pokestopCache/gymCache/stationCache wiring
// that isn't set up in this unit test, so we init only what this aggregate
// reads: fortLookupCache and the questConditionCount/questFortKeys pair via
// initQuestConditions).
func TestGetAvailablePokestops(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	initQuestConditions()
	now := int64(1_000_000)
	// quest reward + condition via the maintained map (the sole quest source)
	adjustQuestConditions([]questConditionKey{{RewardType: 2, ItemId: 1, Title: "catch_x", Target: 3}}, +1)
	// one fort: active lure, EXPIRED showcase (excluded), active grunt incident — all read in one range
	fortLookupCache.Store("s1", FortLookup{
		FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now + 100,
		ContestPokemonId: 1, ShowcaseExpiry: now - 1, // expired -> excluded
		Incidents: []FortLookupIncident{
			{DisplayType: 1, Character: 5, Confirmed: true, Slot1PokemonId: 41, ExpireTimestamp: now + 100},
		},
	})
	res := GetAvailablePokestops(now)
	if len(res.Lures) != 1 || res.Lures[0].LureId != 501 {
		t.Fatalf("lure: %+v", res.Lures)
	}
	if len(res.Showcases) != 0 {
		t.Fatalf("expired showcase should be excluded: %+v", res.Showcases)
	}
	if len(res.Quests) != 1 || res.Quests[0].RewardType != 2 {
		t.Fatalf("quest: %+v", res.Quests)
	}
	if len(res.Invasions) != 1 || res.Invasions[0].Character != 5 {
		t.Fatalf("invasion: %+v", res.Invasions)
	}
}
