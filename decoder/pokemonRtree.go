package decoder

import (
	"context"
	"fmt"
	"golbat/geo"
	"sync"

	"github.com/jellydator/ttlcache/v3"
	"github.com/tidwall/rtree"
	"gopkg.in/guregu/null.v4"
)

type ApiFilter struct {
	Iv     []null.Float     `json:"iv"`
	AtkIv  []int            `json:"atk_iv"`
	DefIv  []int            `json:"def_iv"`
	StaIv  []int            `json:"sta_iv"`
	Level  []int            `json:"level"`
	Cp     []int            `json:"cp"`
	Gender int              `json:"gender"`
	Xxs    bool             `json:"xxs"`
	Xxl    bool             `json:"xxl"`
	Pvp    map[string][]int `json:"pvp"`
}

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

func GetPokemonInArea(min, max geo.Location, filters *map[string]ApiFilter) []*Pokemon {

	results := make([]*Pokemon, 0, 100)

	pokemonTreeMutex.Lock()
	defer pokemonTreeMutex.Unlock()

	pokemonTree.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
		func(min, max [2]float64, data string) bool {
			// println(data)
			if pokemon := pokemonCache.Get(data); pokemon != nil {
				pData := pokemon.Value()
				filter := (*filters)[fmt.Sprintf("%d-%d", pData.PokemonId, pData.Form.Int64)]

				if filter.Iv != nil {
					if pData.Iv.Float64 >= filter.Iv[0].Float64 && pData.Iv.Float64 <= filter.Iv[1].Float64 {
						results = append(results, &pData)
					}
				}
			}
			return true // always continue
		})

	return results
}
