package decoder

import (
	"slices"
	"testing"
)

// setPvp gives a seeded pokemon a PVP lookup.
func setPvp(t *testing.T, id uint64, pvp PokemonPvpLookup) {
	t.Helper()
	item, ok := pokemonLookupCache.Load(id)
	if !ok {
		t.Fatalf("pokemon %d not seeded", id)
	}
	item.PokemonPvpLookup, item.HasPvp = pvp, true
	pokemonLookupCache.Store(id, item)
}

// A league without a rank is stored as pvpNoRank; no PVP range may match it,
// whatever its bounds.
func TestPvpRangeExcludesNoRank(t *testing.T) {
	const ranked, unranked = uint64(960001), uint64(960002)
	request := seedScanPokemon(t, 33.25, 53.75, []scanSeed{{id: ranked, pokemonId: 1}, {id: unranked, pokemonId: 1}})
	setPvp(t, ranked, PokemonPvpLookup{Little: pvpNoRank, Great: 50, Ultra: pvpNoRank})
	setPvp(t, unranked, PokemonPvpLookup{Little: pvpNoRank, Great: pvpNoRank, Ultra: pvpNoRank})

	for name, r := range map[string]*ApiPokemonDnfMinMax{
		"min only (max unbounded)": {Min: 1, Max: 32767},
		"explicit large max":       {Min: 1, Max: 5000},
	} {
		v3 := request([]ApiPokemonDnfFilter3{{Great: r}})
		if got := scanKeys(t, v3); !slices.Equal(got, []uint64{ranked}) {
			t.Errorf("v3 %s: keys %v, want only %d", name, got, ranked)
		}
		v2 := ApiPokemonScan2{Min: v3.Min, Max: v3.Max, DnfFilters: []ApiPokemonDnfFilter{{Great: r}}}
		keys, _, _, _ := internalGetPokemonInArea2(v2)
		if !slices.Equal(keys, []uint64{ranked}) {
			t.Errorf("v2 %s: keys %v, want only %d", name, keys, ranked)
		}
	}
}
