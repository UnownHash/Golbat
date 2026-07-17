package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

func TestGetAvailableGyms(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	now := int64(1_000_000)

	// gym with team + active raid boss
	fortLookupCache.Store("g1", FortLookup{
		FortType: GYM, TeamId: 1, AvailableSlots: 2,
		RaidLevel: 5, RaidPokemonId: 150, RaidPokemonForm: 0, RaidEndTimestamp: now + 100,
	})
	// gym with an active egg (no boss) and an EXPIRED raid on another
	fortLookupCache.Store("g2", FortLookup{
		FortType: GYM, TeamId: 2, AvailableSlots: 6,
		RaidLevel: 3, RaidPokemonId: 0, RaidEndTimestamp: now + 100,
	})
	fortLookupCache.Store("g3", FortLookup{
		FortType: GYM, TeamId: 1, AvailableSlots: 0,
		RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: now - 1, // expired -> excluded
	})
	// a pokestop must be ignored
	fortLookupCache.Store("s1", FortLookup{FortType: POKESTOP, LureId: 501})

	res := GetAvailableGyms(now)

	if len(res.Teams) != 3 { // (1,2),(2,6),(1,0)
		t.Fatalf("teams: %+v", res.Teams)
	}
	// raids: boss 150 lvl5, egg lvl3; expired 999 excluded
	var bosses, eggs int
	for _, r := range res.Raids {
		if r.PokemonId == 999 {
			t.Fatalf("expired raid leaked: %+v", r)
		}
		if r.PokemonId == 0 {
			eggs++
		} else {
			bosses++
		}
	}
	if bosses != 1 || eggs != 1 {
		t.Fatalf("raids: %+v", res.Raids)
	}
}
