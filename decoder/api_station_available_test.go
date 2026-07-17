package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

func TestGetAvailableStations(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	now := int64(1_000_000)

	// station with two active battles (multi-battle path) + one expired
	fortLookupCache.Store("st1", FortLookup{FortType: STATION, StationBattles: []FortLookupStationBattle{
		{BattleLevel: 3, BattlePokemonId: 150, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 5, BattlePokemonId: 384, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 1, BattlePokemonId: 1, BattleEndTimestamp: now - 1}, // expired -> excluded
	}})
	// station with only the top-battle projection (no StationBattles slice)
	fortLookupCache.Store("st2", FortLookup{FortType: STATION,
		BattleLevel: 6, BattlePokemonId: 999, BattlePokemonForm: 0, BattleEndTimestamp: now + 100,
	})
	// station with a level-0 battle -> excluded
	fortLookupCache.Store("st3", FortLookup{FortType: STATION, StationBattles: []FortLookupStationBattle{
		{BattleLevel: 0, BattlePokemonId: 5, BattleEndTimestamp: now + 100},
	}})
	fortLookupCache.Store("g1", FortLookup{FortType: GYM, TeamId: 1}) // ignored

	res := GetAvailableStations(now)
	// expect: (3,150),(5,384) from st1, (6,999) from st2 = 3 distinct; expired + level-0 excluded
	if len(res.Battles) != 3 {
		t.Fatalf("battles: %+v", res.Battles)
	}
	for _, b := range res.Battles {
		if b.BattleLevel == 0 || b.PokemonId == 1 {
			t.Fatalf("excluded battle leaked: %+v", b)
		}
	}
}
