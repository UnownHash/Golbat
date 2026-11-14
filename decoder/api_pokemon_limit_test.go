package decoder

import "testing"

// The scan stops once it has collected exactly the effective limit, so a
// response never carries limit+1 pokemon.
func TestPokemonScanReturnsAtMostLimit(t *testing.T) {
	seeds := make([]scanSeed, 5)
	for i := range seeds {
		seeds[i] = scanSeed{id: uint64(950601 + i), pokemonId: 1, iv: 50}
	}
	request := seedScanPokemon(t, 36.25, 56.75, seeds)

	req := request([]ApiPokemonDnfFilter3{{}})
	req.Limit = 3
	if got := scanKeys(t, req); len(got) != 3 {
		t.Errorf("limit 3: got %d pokemon, want 3", len(got))
	}
	if !pokemonScanLimitReached(req, 3) {
		t.Error("limit 3 with 3 results: limit_reached false, want true")
	}

	req.Limit = 0
	if got := scanKeys(t, req); len(got) != 5 {
		t.Errorf("no limit: got %d pokemon, want all 5", len(got))
	}
}
