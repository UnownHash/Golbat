package decoder

import (
	"context"
	"fmt"
	"github.com/Pupitar/ohbemgo"
	"github.com/jellydator/ttlcache/v3"
	"github.com/tidwall/rtree"
	"golbat/config"
	"golbat/geo"
	"gopkg.in/guregu/null.v4"
	"math"
	"sync"
	"time"
)

type ApiRetrieve struct {
	Min             geo.Location         `json:"min"`
	Max             geo.Location         `json:"max"`
	GlobalFilter    *ApiFilter           `json:"global"`
	SpecificFilters map[string]ApiFilter `json:"filters"`
}
type ApiFilter struct {
	Iv     []int8             `json:"iv"`
	AtkIv  []int8             `json:"atk_iv"`
	DefIv  []int8             `json:"def_iv"`
	StaIv  []int8             `json:"sta_iv"`
	Level  []int8             `json:"level"`
	Cp     []int16            `json:"cp"`
	Gender int                `json:"gender"`
	Xxs    bool               `json:"xxs"`
	Xxl    bool               `json:"xxl"`
	Pvp    map[string][]int16 `json:"pvp"`
}

type PokemonLookup struct {
	PokemonId          int16
	Form               int16
	HasEncounterValues bool
	Atk                int8
	Def                int8
	Sta                int8
	Level              int8
	Cp                 int16
	Gender             int8
	Xxs                bool
	Xxl                bool
	Iv                 int8
}

type PokemonPvpLookup struct {
	Pvp map[string]map[int16]int16
}

var pokemonLookupCache *ttlcache.Cache[string, *PokemonLookup]
var pokemonPvpLookupCache *ttlcache.Cache[string, *PokemonPvpLookup]

var pokemonTreeMutex sync.Mutex
var pokemonTree rtree.RTreeG[string]

func watchPokemonCache() {
	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[string, Pokemon]) {
		r := v.Value()
		removePokemonFromTree(&r)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})

	pokemonLookupCache = ttlcache.New[string, *PokemonLookup](
		ttlcache.WithTTL[string, *PokemonLookup](60*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, *PokemonLookup](), // Pokemon will last 60 mins from when we first see them not last see them
	)
	go pokemonLookupCache.Start()
	pokemonPvpLookupCache = ttlcache.New[string, *PokemonPvpLookup](
		ttlcache.WithTTL[string, *PokemonPvpLookup](60*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, *PokemonPvpLookup](), // Pokemon will last 60 mins from when we first see them not last see them
	)
	go pokemonPvpLookupCache.Start()
}

func valueOrMinus1(n null.Int) int {
	if n.Valid {
		return int(n.Int64)
	}
	return -1
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemon.Id)
	pokemonTreeMutex.Unlock()
}

func updatePokemonLookup(pokemon *Pokemon) {
	pokemonLookupCache.Set(pokemon.Id, &PokemonLookup{
		PokemonId:          pokemon.PokemonId,
		Form:               int16(pokemon.Form.ValueOrZero()),
		HasEncounterValues: pokemon.Move1.Valid,
		Atk:                int8(valueOrMinus1(pokemon.AtkIv)),
		Def:                int8(valueOrMinus1(pokemon.DefIv)),
		Sta:                int8(valueOrMinus1(pokemon.StaIv)),
		Level:              int8(valueOrMinus1(pokemon.Level)),
		Cp:                 int16(valueOrMinus1(pokemon.Cp)),
		Iv:                 int8(math.Round(pokemon.Iv.Float64)),
	}, pokemon.remainingDuration())
}

func updatePokemonPvpLookup(pokemon *Pokemon, pvpResults map[string][]ohbemgo.PokemonEntry) {
	if pvpResults == nil {
		pokemonPvpLookupCache.Delete(pokemon.Id)
		return
	}

	pvpStore := make(map[string]map[int16]int16)
	for key, value := range pvpResults {
		pvpStore[key] = make(map[int16]int16)

		for _, levelCap := range config.Config.Pvp.LevelCaps {
			var best int16 = 4096 // worst possible rank
			for _, entry := range value {
				// we don't exclude mega evolutions yet
				if (int(entry.Cap) == levelCap || (entry.Capped && int(entry.Cap) <= levelCap)) &&
					entry.Rank < best {
					best = entry.Rank
				}
			}
			if best != 4096 {
				pvpStore[key][int16(levelCap)] = best
			}
		}
	}

	pokemonPvpLookupCache.Set(pokemon.Id, &PokemonPvpLookup{
		Pvp: pvpStore,
	}, pokemon.remainingDuration())
}

func removePokemonFromTree(pokemon *Pokemon) {
	pokemonTreeMutex.Lock()
	pokemonTree.Delete([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemon.Id)
	pokemonTreeMutex.Unlock()
}

func GetPokemonInArea(retrieveParameters ApiRetrieve) []*Pokemon {
	min := retrieveParameters.Min
	max := retrieveParameters.Max
	filters := retrieveParameters.SpecificFilters
	globalFilter := retrieveParameters.GlobalFilter

	isPokemonMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter ApiFilter) bool {
		filterMatched := true // assume basic match is true unless any filter doesn't match
		pvpMatched := false   // assume pvp match is false unless any filter matches

		if filter.Iv != nil && (pokemonLookup.Iv < filter.Iv[0] || pokemonLookup.Iv > filter.Iv[1]) {
			filterMatched = false
		} else if filter.StaIv != nil && (pokemonLookup.Sta < filter.StaIv[0] || pokemonLookup.Sta > filter.StaIv[1]) {
			filterMatched = false
		} else if filter.AtkIv != nil && (pokemonLookup.Sta < filter.AtkIv[0] || pokemonLookup.Sta > filter.AtkIv[1]) {
			filterMatched = false
		} else if filter.DefIv != nil && (pokemonLookup.Def < filter.AtkIv[0] || pokemonLookup.Def > filter.AtkIv[1]) {
			filterMatched = false
		}

		if filter.Pvp != nil && pvpLookup != nil {

		pvpLoop:
			for key, value := range filter.Pvp {
				if rankings, found := pvpLookup.Pvp[key]; found == false {
					// Did not find this pvp league against the pokemon (try others)
					continue
				} else {

					for _, ranking := range rankings {
						if ranking >= value[0] && ranking <= value[1] {
							pvpMatched = true
							break pvpLoop
						}
					}
				}
			}
		}

		return filterMatched || pvpMatched
	}

	results := make([]*Pokemon, 0, 1000)

	pokemonTreeMutex.Lock()
	defer pokemonTreeMutex.Unlock()

	pokemonTree.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
		func(min, max [2]float64, data string) bool {
			pokemonLookupItem := pokemonLookupCache.Get(data)
			if pokemonLookupItem == nil {
				// Did not find cached result, something amiss?
				return true
			}

			pokemonLookup := pokemonLookupItem.Value()

			var pvpLookup *PokemonPvpLookup
			if pvpLookupItem := pokemonPvpLookupCache.Get(data); pvpLookupItem != nil {
				// Did not find cached result, something amiss?
				// Treat the PVP values like an 'or' - one of the matching leagues must be in the range
				pvpLookup = pvpLookupItem.Value()
			}

			globalFilterMatched := false
			if globalFilter != nil {
				globalFilterMatched = isPokemonMatch(pokemonLookup, pvpLookup, *globalFilter)
			}
			specificFilterMatched := false

			if !globalFilterMatched && filters != nil {
				formString := fmt.Sprintf("%d-%d", pokemonLookup.PokemonId, pokemonLookup.Form)
				filter, found := filters[formString]

				if found {
					specificFilterMatched = isPokemonMatch(pokemonLookup, pvpLookup, filter)
				}
			}

			if globalFilterMatched || specificFilterMatched {
				if pokemon := pokemonCache.Get(data); pokemon != nil {
					pData := pokemon.Value()
					results = append(results, &pData)
				}
			}

			return true // always continue
		})

	return results
}
