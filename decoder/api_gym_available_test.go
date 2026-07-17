package decoder

import (
	"testing"
)

func TestGetAvailableGyms(t *testing.T) {
	initFortAvailability()
	now := int64(1_000_000)

	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100}, now) // boss
	observeRaid(&FortLookup{RaidLevel: 3, RaidPokemonId: 0, RaidEndTimestamp: now + 100}, now)   // egg
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: now - 1}, now)   // expired -> ignored

	res := GetAvailableGyms(now)

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
