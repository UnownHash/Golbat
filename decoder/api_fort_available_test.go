package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

// TestGetAvailableForts locks that the single-pass combined builder produces
// the same aggregates as the three per-type builders over the same cache.
func TestGetAvailableForts(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	initQuestConditions()
	now := int64(1_000_000)

	fortLookupCache.Store("g1", FortLookup{
		FortType: GYM, TeamId: 1, AvailableSlots: 2,
		RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100,
	})
	fortLookupCache.Store("p1", FortLookup{
		FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now + 100,
		Incidents: []FortLookupIncident{{Character: 5, DisplayType: 1, ExpireTimestamp: now + 100}},
	})
	fortLookupCache.Store("s1", FortLookup{
		FortType: STATION,
		StationBattles: []FortLookupStationBattle{
			{BattleLevel: 5, BattlePokemonId: 150, BattleEndTimestamp: now + 100},
		},
	})

	combined := GetAvailableForts(now)
	if len(combined.Gyms.Teams) != 1 || len(combined.Gyms.Raids) != 1 {
		t.Fatalf("gyms: %+v", combined.Gyms)
	}
	if len(combined.Pokestops.Lures) != 1 || len(combined.Pokestops.Invasions) != 1 {
		t.Fatalf("pokestops: %+v", combined.Pokestops)
	}
	if len(combined.Stations.Battles) != 1 {
		t.Fatalf("stations: %+v", combined.Stations)
	}

	// parity with the per-type builders over the same cache
	perGym := GetAvailableGyms(now)
	perStop := GetAvailablePokestops(now)
	perStation := GetAvailableStations(now)
	if len(perGym.Teams) != len(combined.Gyms.Teams) ||
		len(perStop.Lures) != len(combined.Pokestops.Lures) ||
		len(perStation.Battles) != len(combined.Stations.Battles) {
		t.Fatal("combined diverges from per-type builders")
	}
}
