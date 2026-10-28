package decoder

import (
	"slices"
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

// These tests pin the documented pokemon filter semantics (api.md, "Filter
// semantics"): a pokemon is matched against the most specific clause group
// that exists for it — exact id+form, else id, else "everything else" — and a
// less specific group never applies to a pokemon that has a more specific
// one. #414/#416 read that as a bug and were reverted; see #417 and #134.

type scanSeed struct {
	id        uint64
	pokemonId int16
	form      uint16
	gender    uint8
	iv        float32
	size      uint8
}

// seedScanPokemon indexes live pokemon at one spot for the duration of the
// test and returns a request builder for a scan of that spot.
func seedScanPokemon(t *testing.T, lat, lon float64, seeds []scanSeed) func(filters []ApiPokemonDnfFilter3) ApiPokemonScan3 {
	t.Helper()
	withScanLimits(t)
	for _, s := range seeds {
		p := &Pokemon{PokemonData: PokemonData{
			Id:        Uint64Str(s.id),
			Lat:       lat,
			Lon:       lon,
			PokemonId: s.pokemonId,
			Form:      null.ValueFrom(s.form),
			Gender:    null.ValueFrom(s.gender),
			Iv:        null.ValueFrom(s.iv),
			Size:      null.ValueFrom(s.size),
		}}
		p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + 600))
		pokemonRtreePreloadInsert(p)
		t.Cleanup(func() {
			// Reverse pokemonRtreePreloadInsert the way eviction does.
			if item, ok := pokemonLookupCache.LoadAndDelete(s.id); ok && item.HasLookup {
				adjustPokemonFormCount(pokemonFormKey{item.PokemonLookup.PokemonId, item.PokemonLookup.Form}, -1)
			}
			pokemonTreeMutex.Lock()
			pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, s.id)
			pokemonTreeMutex.Unlock()
			pokemonTreeSnapshot.Store(nil)
		})
	}
	pokemonTreeSnapshot.Store(nil) // the next scan must see the fresh tree
	return func(filters []ApiPokemonDnfFilter3) ApiPokemonScan3 {
		return ApiPokemonScan3{
			Min:        ApiLatLon{Lat: lat - 0.01, Lon: lon - 0.01},
			Max:        ApiLatLon{Lat: lat + 0.01, Lon: lon + 0.01},
			DnfFilters: filters,
		}
	}
}

func scanKeys(t *testing.T, req ApiPokemonScan3) []uint64 {
	t.Helper()
	keys, _, _, _ := internalGetPokemonInArea3(req)
	slices.Sort(keys)
	return keys
}

func iv100() *ApiPokemonDnfMinMax { return &ApiPokemonDnfMinMax{Min: 100, Max: 100} }

func TestPokemonFilterEverythingElseExcludesSpeciesWithOwnClause(t *testing.T) {
	const maleBulbasaur, femaleBulbasaur, malePidgey = uint64(950101), uint64(950102), uint64(950103)
	request := seedScanPokemon(t, 31.25, 51.75, []scanSeed{
		{id: maleBulbasaur, pokemonId: 1, gender: 1, iv: 100},
		{id: femaleBulbasaur, pokemonId: 1, gender: 2, iv: 100},
		{id: malePidgey, pokemonId: 16, gender: 1, iv: 100},
	})
	bulbasaurFemale := ApiPokemonDnfFilter3{Pokemon: []ApiPokemonDnfId{{Pokemon: 1}}, Gender: []int8{2}, Iv: iv100()}
	everythingElse := ApiPokemonDnfFilter3{Iv: iv100()}

	got := scanKeys(t, request([]ApiPokemonDnfFilter3{bulbasaurFemale, everythingElse}))
	if want := []uint64{femaleBulbasaur, malePidgey}; !slices.Equal(got, want) {
		t.Errorf("Bulbasaur has its own clause, so 'everything else' must not apply to it: got %v, want %v", got, want)
	}

	// A shared clause that should also apply to Bulbasaur is listed under
	// Bulbasaur's key as well: the client owns that merge.
	bulbasaurPerfect := ApiPokemonDnfFilter3{Pokemon: []ApiPokemonDnfId{{Pokemon: 1}}, Iv: iv100()}
	got = scanKeys(t, request([]ApiPokemonDnfFilter3{bulbasaurFemale, bulbasaurPerfect, everythingElse}))
	if want := []uint64{maleBulbasaur, femaleBulbasaur, malePidgey}; !slices.Equal(got, want) {
		t.Errorf("shared clause listed under the species must apply there: got %v, want %v", got, want)
	}
}

func TestPokemonFilterExactFormShadowsSpecies(t *testing.T) {
	const maleForm0, maleForm1 = uint64(950201), uint64(950202)
	request := seedScanPokemon(t, 32.25, 52.75, []scanSeed{
		{id: maleForm0, pokemonId: 1, form: 0, gender: 1, iv: 100},
		{id: maleForm1, pokemonId: 1, form: 1, gender: 1, iv: 100},
	})
	form0 := int16(0)
	got := scanKeys(t, request([]ApiPokemonDnfFilter3{
		{Pokemon: []ApiPokemonDnfId{{Pokemon: 1, Form: &form0}}, Gender: []int8{2}},
		{Pokemon: []ApiPokemonDnfId{{Pokemon: 1}}, Iv: iv100()},
	}))
	if want := []uint64{maleForm1}; !slices.Equal(got, want) {
		t.Errorf("form 0 has an exact clause (female only); form 1 falls back to the species clause: got %v, want %v", got, want)
	}
}

func TestPokemonFilterNeverMatchingClauseHidesSpecies(t *testing.T) {
	const xxlBulbasaur, xxlPumpkaboo = uint64(950301), uint64(950302)
	request := seedScanPokemon(t, 33.25, 53.75, []scanSeed{
		{id: xxlBulbasaur, pokemonId: 1, size: 5, iv: 50},
		{id: xxlPumpkaboo, pokemonId: 710, size: 5, iv: 50},
	})
	got := scanKeys(t, request([]ApiPokemonDnfFilter3{
		{Size: &ApiPokemonDnfMinMax{Min: 5, Max: 5}},
		{Pokemon: []ApiPokemonDnfId{{Pokemon: 710}}, Iv: &ApiPokemonDnfMinMax{Min: 1, Max: 0}},
	}))
	if want := []uint64{xxlBulbasaur}; !slices.Equal(got, want) {
		t.Errorf("a clause that can never hold hides Pumpkaboo from 'everything else': got %v, want %v", got, want)
	}
}

func TestPokemonFilterFormWithoutSpeciesNeverMatches(t *testing.T) {
	const form0Bulbasaur = uint64(950401)
	request := seedScanPokemon(t, 34.25, 54.75, []scanSeed{{id: form0Bulbasaur, pokemonId: 1, form: 0, iv: 100}})
	form0 := int16(0)
	got := scanKeys(t, request([]ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 0, Form: &form0}}, Iv: iv100()}}))
	if len(got) != 0 {
		t.Errorf("id 0 with a form is not a key any pokemon has: got %v, want none", got)
	}
}

func TestPokemonFilterEmptyAndUnconditionalLists(t *testing.T) {
	const a, b = uint64(950501), uint64(950502)
	request := seedScanPokemon(t, 35.25, 55.75, []scanSeed{{id: a, pokemonId: 1, iv: 10}, {id: b, pokemonId: 16, iv: 90}})
	if got := scanKeys(t, request(nil)); len(got) != 0 {
		t.Errorf("an empty filters list matches nothing: got %v", got)
	}
	if got, want := scanKeys(t, request([]ApiPokemonDnfFilter3{{}})), []uint64{a, b}; !slices.Equal(got, want) {
		t.Errorf("one clause with no conditions matches everything: got %v, want %v", got, want)
	}
}
