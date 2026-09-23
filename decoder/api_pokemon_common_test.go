package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
)

// seedScannedPokemon places a live pokemon in the rtree and lookup cache for
// the duration of one test. Call pokemonTreeSnapshot.Store(nil) after seeding
// to make the next scan see the fresh tree.
func seedScannedPokemon(t *testing.T, id uint64, lat, lon float64, pokemonId int16, form uint16, gender uint8, iv float32) {
	t.Helper()

	p := &Pokemon{PokemonData: PokemonData{
		Id:        Uint64Str(id),
		Lat:       lat,
		Lon:       lon,
		PokemonId: pokemonId,
		Form:      null.ValueFrom(form),
		Gender:    null.ValueFrom(gender),
		Iv:        null.ValueFrom(iv),
	}}
	p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + 600))

	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(id, p, time.Minute)
	t.Cleanup(func() {
		pokemonCache.Delete(id)
		pokemonLookupCache.Delete(id)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
}

func matchedKeys(keys []uint64) map[uint64]struct{} {
	found := make(map[uint64]struct{}, len(keys))
	for _, key := range keys {
		found[key] = struct{}{}
	}
	return found
}

// A species-restricted clause must not shadow a generic clause for that
// species: Diadem's "Bulbasaur + female + 100%" combined with "all 100%" has
// to return a male 100% Bulbasaur via the generic clause.
func TestPokemonDnfGenericClauseNotShadowedBySpeciesClause(t *testing.T) {
	withScanLimits(t)

	const (
		maleBulbasaur   = uint64(940101)
		femaleBulbasaur = uint64(940102)
		malePidgey      = uint64(940103)
		lat, lon        = 21.25, 43.75
	)

	seedScannedPokemon(t, maleBulbasaur, lat, lon, 1, 0, 1, 100)
	seedScannedPokemon(t, femaleBulbasaur, lat, lon, 1, 0, 2, 100)
	seedScannedPokemon(t, malePidgey, lat, lon, 16, 0, 1, 100)
	pokemonTreeSnapshot.Store(nil)

	params := ApiPokemonScan3{
		Min: ApiLatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max: ApiLatLon{Lat: lat + 0.01, Lon: lon + 0.01},
		DnfFilters: []ApiPokemonDnfFilter3{
			{
				Pokemon: []ApiPokemonDnfId{{Pokemon: 1}},
				Iv:      &ApiPokemonDnfMinMax{Min: 100, Max: 100},
				Gender:  []int8{2},
			},
			{Iv: &ApiPokemonDnfMinMax{Min: 100, Max: 100}},
		},
	}

	keys, _, _, _ := internalGetPokemonInArea3(params)
	found := matchedKeys(keys)
	if _, ok := found[maleBulbasaur]; !ok {
		t.Error("male Bulbasaur must match the generic 100% clause; the species clause shadowed it")
	}
	if _, ok := found[femaleBulbasaur]; !ok {
		t.Error("female Bulbasaur must match the species clause")
	}
	if _, ok := found[malePidgey]; !ok {
		t.Error("Pidgey must match the generic 100% clause")
	}
}

// A clause with pokemon_id 0 matches any species but still pins the form;
// that lookup key has to be consulted as well.
func TestPokemonDnfAnyPokemonFormClauseMatches(t *testing.T) {
	withScanLimits(t)

	const (
		formZero = uint64(940201)
		formOne  = uint64(940202)
		lat, lon = 22.25, 44.75
	)

	seedScannedPokemon(t, formZero, lat, lon, 1, 0, 1, 100)
	seedScannedPokemon(t, formOne, lat, lon, 1, 1, 1, 100)
	pokemonTreeSnapshot.Store(nil)

	params := ApiPokemonScan3{
		Min: ApiLatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max: ApiLatLon{Lat: lat + 0.01, Lon: lon + 0.01},
		DnfFilters: []ApiPokemonDnfFilter3{{
			Pokemon: []ApiPokemonDnfId{{Pokemon: 0, Form: ptr(int16(0))}},
			Iv:      &ApiPokemonDnfMinMax{Min: 100, Max: 100},
		}},
	}

	keys, _, _, _ := internalGetPokemonInArea3(params)
	found := matchedKeys(keys)
	if _, ok := found[formZero]; !ok {
		t.Error("form 0 pokemon must match a pokemon_id 0 / form 0 clause")
	}
	if _, ok := found[formOne]; ok {
		t.Error("form 1 pokemon must not match a pokemon_id 0 / form 0 clause")
	}
}

func TestPokemonScanLimitReached(t *testing.T) {
	previousLimit := config.Config.Tuning.MaxPokemonResults
	config.Config.Tuning.MaxPokemonResults = 100
	t.Cleanup(func() {
		config.Config.Tuning.MaxPokemonResults = previousLimit
	})

	tests := []struct {
		name         string
		requestLimit int
		resultCount  int
		want         bool
	}{
		{name: "below requested limit", requestLimit: 10, resultCount: 9, want: false},
		{name: "at requested limit", requestLimit: 10, resultCount: 10, want: true},
		{name: "above requested limit", requestLimit: 10, resultCount: 11, want: true},
		{name: "below server default", requestLimit: 0, resultCount: 99, want: false},
		{name: "at server default", requestLimit: 0, resultCount: 100, want: true},
		{name: "requested limit capped by server", requestLimit: 200, resultCount: 100, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ApiPokemonScan3{Limit: tt.requestLimit}
			if got := pokemonScanLimitReached(req, tt.resultCount); got != tt.want {
				t.Errorf("pokemonScanLimitReached() = %v, want %v", got, tt.want)
			}
		})
	}
}
