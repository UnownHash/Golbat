package decoder

import (
	"time"

	"golbat/geo"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

type ApiPokemonCountResult struct {
	Count int `json:"count" doc:"Number of live (not yet despawned) pokemon inside the fence, or on the whole map when no fence was sent."`
}

// CountPokemon counts the live pokemon inside fence, or every live pokemon
// when fence is nil. The cache keeps pokemon for a grace period past despawn,
// so each candidate's expiry is checked under its record lock, the same test
// forEachLivePokemonResult applies to scan results. Unlike a scan there is no
// result cap: callers get an exact count without paying for the result JSON.
func CountPokemon(fence *geojson.Feature) int {
	minPoint := [2]float64{-180, -90}
	maxPoint := [2]float64{180, 90}

	var compiled *geo.CompiledFence
	if fence != nil {
		compiled = geo.CompileFence(fence)
		if compiled == nil {
			return 0
		}
		bound := fence.Geometry.Bound()
		minPoint = [2]float64{bound.Min.Lon(), bound.Min.Lat()}
		maxPoint = [2]float64{bound.Max.Lon(), bound.Max.Lat()}
	}

	var candidates []uint64
	// The shared snapshot can briefly hold duplicate points for one id.
	seen := make(map[uint64]struct{})
	getPokemonTreeSnapshot().Search(minPoint, maxPoint, func(point, _ [2]float64, pokemonId uint64) bool {
		if _, dup := seen[pokemonId]; dup {
			return true
		}
		if compiled != nil && !compiled.Contains(orb.Point{point[0], point[1]}) {
			return true
		}
		seen[pokemonId] = struct{}{}
		candidates = append(candidates, pokemonId)
		return true
	})

	nowUnix := time.Now().Unix()
	count := 0
	for _, pokemonId := range candidates {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(pokemonId, "API.CountPokemon")
		if pokemon == nil {
			continue
		}
		if int64OrZero(pokemon.ExpireTimestamp) > nowUnix {
			count++
		}
		unlock()
	}
	return count
}
