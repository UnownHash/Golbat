package decoder

import (
	"context"
	"github.com/jellydator/ttlcache/v3"
	"github.com/tidwall/rtree"
	"golbat/geo"
	"sync"
)

var pokemonTreeMutex sync.Mutex
var pokemonTree rtree.RTreeG[string]

func watchPokemonCache() {
	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[string, Pokemon]) {
		r := v.Value()
		removePokemonFromTree(&r)
	})
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemon.Id)
	pokemonTreeMutex.Unlock()
}

func removePokemonFromTree(pokemon *Pokemon) {
	pokemonTreeMutex.Lock()
	pokemonTree.Delete([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemon.Id)
	pokemonTreeMutex.Unlock()
}

func GetPokemonInArea(min, max geo.Location) []*Pokemon {

	results := make([]*Pokemon, 0, 100)

	pokemonTreeMutex.Lock()
	defer pokemonTreeMutex.Unlock()

	pokemonTree.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
		func(min, max [2]float64, data string) bool {
			// println(data)
			if pokemon := pokemonCache.Get(data); pokemon != nil {
				pData := pokemon.Value()
				results = append(results, &pData)
			}
			return true // always continue
		})

	return results
}
