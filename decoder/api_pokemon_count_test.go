package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// seedCountPokemon caches a pokemon at lat/lon that expires expireIn seconds
// from now (negative = already despawned but still inside the cache grace).
func seedCountPokemon(t *testing.T, id uint64, lat, lon float64, expireIn int64) {
	t.Helper()
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: lat, Lon: lon, PokemonId: 25}}
	p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + expireIn))

	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(id, p, time.Minute)
	pokemonTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		pokemonCache.Delete(id)
		pokemonLookupCache.Delete(id)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
}

func TestCountPokemonInFence(t *testing.T) {
	// A triangle, so the bounding-box corner at (lon 51, lat 41) is outside
	// the fence and proves the polygon test runs, not just the box search.
	fence := geojson.NewFeature(orb.Polygon{{{50, 40}, {52, 40}, {50, 42}, {50, 40}}})

	seedCountPokemon(t, 950001, 40.5, 50.5, 600) // live, inside
	seedCountPokemon(t, 950002, 40.6, 50.6, 600) // live, inside
	seedCountPokemon(t, 950003, 40.5, 50.4, -30) // despawned, inside
	seedCountPokemon(t, 950004, 41.9, 51.9, 600) // live, in the box but outside the triangle
	seedCountPokemon(t, 950005, 45.0, 55.0, 600) // live, far outside

	if got := CountPokemon(fence); got != 2 {
		t.Fatalf("CountPokemon(fence) = %d, want 2", got)
	}
}

func TestCountPokemonWholeMapSkipsExpired(t *testing.T) {
	before := CountPokemon(nil)
	seedCountPokemon(t, 950101, -33.5, 151.5, 600)
	seedCountPokemon(t, 950102, -33.6, 151.6, -30)

	if got := CountPokemon(nil); got != before+1 {
		t.Fatalf("CountPokemon(nil) = %d, want %d", got, before+1)
	}
}
