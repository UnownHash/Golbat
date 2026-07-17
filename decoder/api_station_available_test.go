package decoder

import (
	"testing"
)

func TestGetAvailableStations(t *testing.T) {
	initFortAvailability()
	now := int64(1_000_000)

	// station with two active battles (multi-battle path) + one expired
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 3, BattlePokemonId: 150, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 5, BattlePokemonId: 384, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 1, BattlePokemonId: 1, BattleEndTimestamp: now - 1}, // expired -> excluded
	}}, now)
	// station with only the top-battle projection (no StationBattles slice)
	observeStationBattles(&FortLookup{
		BattleLevel: 6, BattlePokemonId: 999, BattlePokemonForm: 0, BattleEndTimestamp: now + 100,
	}, now)
	// station with a level-0 battle -> excluded
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 0, BattlePokemonId: 5, BattleEndTimestamp: now + 100},
	}}, now)

	res := GetAvailableStations(now)
	// expect: (3,150),(5,384) from the slice, (6,999) from the scalar projection = 3
	// distinct; expired + level-0 excluded
	if len(res.Battles) != 3 {
		t.Fatalf("battles: %+v", res.Battles)
	}
	for _, b := range res.Battles {
		if b.BattleLevel == 0 || b.PokemonId == 1 {
			t.Fatalf("excluded battle leaked: %+v", b)
		}
	}
}
