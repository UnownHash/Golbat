package decoder

import (
	"testing"
)

// TestGetAvailableForts locks that the combined builder assembles all three
// availability sections from the maintained maps, matching the per-type
// builders over the same maps.
func TestGetAvailableForts(t *testing.T) {
	initFortAvailability()
	initQuestConditions()
	now := int64(1_000_000)

	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100}, now)
	observePokestop(&FortLookup{LureId: 501, LureExpireTimestamp: now + 100}, now)
	observeInvasion(&FortLookupIncident{Character: 5, DisplayType: 1, ExpireTimestamp: now + 100}, now)
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 5, BattlePokemonId: 150, BattleEndTimestamp: now + 100},
	}}, now)

	combined := GetAvailableForts(now)
	if len(combined.Gyms.Raids) != 1 {
		t.Fatalf("gyms: %+v", combined.Gyms)
	}
	if len(combined.Pokestops.Lures) != 1 || len(combined.Pokestops.Invasions) != 1 {
		t.Fatalf("pokestops: %+v", combined.Pokestops)
	}
	if len(combined.Stations.Battles) != 1 {
		t.Fatalf("stations: %+v", combined.Stations)
	}

	// parity: combined sections equal the per-type reads over the same maps
	if len(GetAvailableGyms(now).Raids) != len(combined.Gyms.Raids) ||
		len(GetAvailableStations(now).Battles) != len(combined.Stations.Battles) {
		t.Fatal("combined diverges from per-type builders")
	}
}
