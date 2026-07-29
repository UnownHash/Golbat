package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

// handlePokemonEviction must clean up when the pokemon is truly gone from
// the cache, and must leave everything alone when a save re-cached it after
// the eviction fired (the eviction/re-add race that blinds scans).
func TestHandlePokemonEvictionSkipsRecachedPokemon(t *testing.T) {
	const id = uint64(920001)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 3.5, Lon: 4.5, PokemonId: 25}}

	updatePokemonLookup(p, false, nil)
	pokemonCache.Set(id, p, time.Minute) // re-cached: eviction must be a no-op
	defer pokemonCache.Delete(id)

	handlePokemonEviction(p)

	if _, ok := pokemonLookupCache.Load(id); !ok {
		t.Error("eviction removed the lookup entry of a re-cached pokemon")
	}
	pokemonLookupCache.Delete(id)
}

func TestHandlePokemonEvictionCleansUncachedPokemon(t *testing.T) {
	const id = uint64(920002)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 3.5, Lon: 4.5, PokemonId: 25}}

	updatePokemonLookup(p, false, nil)
	pokemonCache.Delete(id) // ensure not cached

	handlePokemonEviction(p)

	if _, ok := pokemonLookupCache.Load(id); ok {
		t.Error("eviction left the lookup entry of an evicted pokemon")
	}
}

// updatePokemonLookup must report whether an entry existed, so the save
// path can restore the tree point an eviction removed mid-update.
func TestUpdatePokemonLookupReportsExisted(t *testing.T) {
	const id = uint64(920003)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 1, Lon: 1, PokemonId: 1, Form: null.IntFrom(0)}}
	defer pokemonLookupCache.Delete(id)

	if existed := updatePokemonLookup(p, false, nil); existed {
		t.Error("first updatePokemonLookup reported existed=true")
	}
	if existed := updatePokemonLookup(p, false, nil); !existed {
		t.Error("second updatePokemonLookup reported existed=false")
	}
}

// deferFortEviction must not touch lookup/tree state when the entry is
// already gone (deleted fort) or owned by a converted counterpart.
func TestDeferFortEvictionGuards(t *testing.T) {
	const id = "fort-race-1"

	// Absent lookup: no-op (and no panic / unpaired enqueue).
	fortLookupCache.Delete(id)
	deferFortEviction(POKESTOP, id, 8.5, 9.5)

	// Type mismatch (pokestop→gym conversion): the gym owns the entry now.
	fortLookupCache.Store(id, FortLookup{FortType: GYM, Lat: 8.5, Lon: 9.5})
	defer fortLookupCache.Delete(id)

	deferFortEviction(POKESTOP, id, 8.5, 9.5)
	if fl, ok := fortLookupCache.Load(id); !ok || fl.FortType != GYM {
		t.Error("stale pokestop eviction removed the live gym's lookup entry")
	}

	// Matching type: cleanup proceeds.
	deferFortEviction(GYM, id, 8.5, 9.5)
	if _, ok := fortLookupCache.Load(id); ok {
		t.Error("matching-type eviction did not remove the lookup entry")
	}
}
