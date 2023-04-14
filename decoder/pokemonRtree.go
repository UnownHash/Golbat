package decoder

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/UnownHash/gohbem"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"golbat/config"
	"golbat/geo"
	"gopkg.in/guregu/null.v4"
	"math"
	"strconv"
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
	Iv         []int8               `json:"iv"`
	AtkIv      []int8               `json:"atk_iv"`
	DefIv      []int8               `json:"def_iv"`
	StaIv      []int8               `json:"sta_iv"`
	Level      []int8               `json:"level"`
	Cp         []int16              `json:"cp"`
	Gender     int8                 `json:"gender"`
	Xxs        bool                 `json:"xxs"`
	Xxl        bool                 `json:"xxl"`
	Additional *ApiAdditionalFilter `json:"additional"`
	Pvp        *ApiPvpFilter        `json:"pvp"`
}
type ApiPvpFilter struct {
	Little []int16 `json:"little"`
	Great  []int16 `json:"great"`
	Ultra  []int16 `json:"ultra"`
}
type ApiAdditionalFilter struct {
	IncludeEverything bool `json:"include_everything"`
	IncludeHundos     bool `json:"include_hundoiv"`
	IncludeNundos     bool `json:"include_zeroiv"`
}

type PokemonLookupCacheItem struct {
	PokemonLookup    *PokemonLookup
	PokemonPvpLookup *PokemonPvpLookup
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
	Size               int8
}

type PokemonPvpLookup struct {
	Little int16
	Great  int16
	Ultra  int16
}

var pokemonLookupCache map[uint64]PokemonLookupCacheItem

var pokemonTreeMutex sync.RWMutex
var pokemonTree rtree.RTreeG[uint64]

func watchPokemonCache() {
	pokemonLookupCache = make(map[uint64]PokemonLookupCacheItem)

	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[string, Pokemon]) {
		r := v.Value()
		removePokemonFromTree(&r)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})

}

func valueOrMinus1(n null.Int) int {
	if n.Valid {
		return int(n.Int64)
	}
	return -1
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)

	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	pokemonTreeMutex.Unlock()
}

func updatePokemonLookup(pokemon *Pokemon, changePvp bool, pvpResults map[string][]gohbem.PokemonEntry) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)

	pokemonTreeMutex.RLock()
	pokemonLookupCacheItem := pokemonLookupCache[pokemonId]
	pokemonTreeMutex.RUnlock()

	pokemonLookupCacheItem.PokemonLookup = &PokemonLookup{
		PokemonId:          pokemon.PokemonId,
		Form:               int16(pokemon.Form.ValueOrZero()),
		HasEncounterValues: pokemon.Move1.Valid,
		Atk:                int8(valueOrMinus1(pokemon.AtkIv)),
		Def:                int8(valueOrMinus1(pokemon.DefIv)),
		Sta:                int8(valueOrMinus1(pokemon.StaIv)),
		Level:              int8(valueOrMinus1(pokemon.Level)),
		Cp:                 int16(valueOrMinus1(pokemon.Cp)),
		Iv:                 int8(math.Round(pokemon.Iv.Float64)),
		Size:               int8(valueOrMinus1(pokemon.Size)),
	}

	if changePvp {
		pokemonLookupCacheItem.PokemonPvpLookup = calculatePokemonPvpLookup(pokemon, pvpResults)
	}

	pokemonTreeMutex.Lock()
	pokemonLookupCache[pokemonId] = pokemonLookupCacheItem
	pokemonTreeMutex.Unlock()
}

func calculatePokemonPvpLookup(pokemon *Pokemon, pvpResults map[string][]gohbem.PokemonEntry) *PokemonPvpLookup {
	if pvpResults == nil {
		return nil
	}

	pvpStore := make(map[string]int16)
	for key, value := range pvpResults {
		var best int16 = 4096 // worst possible rank
		// This code actually calculates best in a level cap, which is no longer strictly necessary
		// But will leave in this form to allow easy change to per-cap again later

		for _, levelCap := range config.Config.Pvp.LevelCaps {
			for _, entry := range value {
				// we don't exclude mega evolutions yet
				if (int(entry.Cap) == levelCap || (entry.Capped && int(entry.Cap) <= levelCap)) &&
					entry.Rank < best {
					best = entry.Rank
				}
			}
		}
		if best != 4096 {
			pvpStore[key] = best
		}
	}

	bestValue := func(leagueKey string) int16 {
		if value, ok := pvpStore[leagueKey]; ok {
			return value
		}
		return 4096
	}

	return &PokemonPvpLookup{
		Little: bestValue("little"),
		Great:  bestValue("great"),
		Ultra:  bestValue("ultra"),
	}
}

func removePokemonFromTree(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)
	pokemonTreeMutex.Lock()
	pokemonTree.Delete([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	delete(pokemonLookupCache, pokemonId)
	pokemonTreeMutex.Unlock()
}

func GetPokemonInArea(retrieveParameters ApiRetrieve) []*Pokemon {
	start := time.Now()

	min := retrieveParameters.Min
	max := retrieveParameters.Max
	filters := retrieveParameters.SpecificFilters
	globalFilter := retrieveParameters.GlobalFilter

	pokemonExamined := 0

	isPokemonMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter ApiFilter) bool {
		// start with filter true if we have any filter set (no filters no match)
		filterMatched := filter.Iv != nil || filter.StaIv != nil || filter.AtkIv != nil || filter.DefIv != nil || filter.Level != nil || filter.Cp != nil || filter.Gender != 0 || filter.Xxl || filter.Xxs
		pvpMatched := false // assume pvp match is true unless any filter matches
		additionalMatch := false

		if filterMatched {
			if filter.Iv != nil && (pokemonLookup.Iv < filter.Iv[0] || pokemonLookup.Iv > filter.Iv[1]) {
				filterMatched = false
			} else if filter.StaIv != nil && (pokemonLookup.Sta < filter.StaIv[0] || pokemonLookup.Sta > filter.StaIv[1]) {
				filterMatched = false
			} else if filter.AtkIv != nil && (pokemonLookup.Atk < filter.AtkIv[0] || pokemonLookup.Atk > filter.AtkIv[1]) {
				filterMatched = false
			} else if filter.DefIv != nil && (pokemonLookup.Def < filter.AtkIv[0] || pokemonLookup.Def > filter.AtkIv[1]) {
				filterMatched = false
			} else if filter.Level != nil && (pokemonLookup.Level < filter.Level[0] || pokemonLookup.Level > filter.Level[1]) {
				filterMatched = false
			} else if filter.Cp != nil && (pokemonLookup.Cp < filter.Cp[0] || pokemonLookup.Cp > filter.Cp[1]) {
				filterMatched = false
			} else if filter.Gender != 0 && pokemonLookup.Gender != filter.Gender {
				filterMatched = false
			} else if filter.Xxl && pokemonLookup.Size != 5 {
				filterMatched = false
			} else if filter.Xxs && pokemonLookup.Size != 1 {
				filterMatched = false
			}
		}

		if filter.Additional != nil {
			if filter.Additional.IncludeEverything {
				additionalMatch = true
			} else if filter.Additional.IncludeNundos && pokemonLookup.Sta == 0 && pokemonLookup.Atk == 0 && pokemonLookup.Def == 0 {
				additionalMatch = true
			} else if filter.Additional.IncludeHundos && pokemonLookup.Sta == 15 && pokemonLookup.Atk == 15 && pokemonLookup.Def == 15 {
				additionalMatch = true
			}
		}

		pvpFilter := filter.Pvp
		if pvpFilter != nil && pvpLookup != nil {
			if pvpFilter.Little != nil && (pvpLookup.Little >= pvpFilter.Little[0] && pvpLookup.Little <= pvpFilter.Little[1]) {
				pvpMatched = true
			}
			if pvpFilter.Great != nil && (pvpLookup.Great >= pvpFilter.Great[0] && pvpLookup.Great <= pvpFilter.Great[1]) {
				pvpMatched = true
			}
			if pvpFilter.Ultra != nil && (pvpLookup.Ultra >= pvpFilter.Ultra[0] && pvpLookup.Ultra <= pvpFilter.Ultra[1]) {
				pvpMatched = true
			}
		}

		return filterMatched || pvpMatched || additionalMatch
	}

	pokemonTreeMutex.RLock()

	var returnKeys []uint64

	pokemonTree.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
		func(min, max [2]float64, pokemonId uint64) bool {
			pokemonExamined++
			pokemonLookupItem, found := pokemonLookupCache[pokemonId]
			if !found {
				// Did not find cached result, something amiss?
				return true
			}

			pokemonLookup := pokemonLookupItem.PokemonLookup
			pvpLookup := pokemonLookupItem.PokemonPvpLookup

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
				returnKeys = append(returnKeys, pokemonId)
			}

			return true // always continue
		})

	pokemonTreeMutex.RUnlock()

	lockedTime := time.Since(start)

	results := make([]*Pokemon, 0, len(returnKeys))

	for _, key := range returnKeys {
		if pokemonCacheEntry := pokemonCache.Get(strconv.FormatUint(key, 10)); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			if ohbem != nil {
				// Add ohbem data
				pvp, err := ohbem.QueryPvPRank(int(pokemon.PokemonId),
					int(pokemon.Form.ValueOrZero()),
					int(pokemon.Costume.ValueOrZero()),
					int(pokemon.Gender.ValueOrZero()),
					int(pokemon.AtkIv.ValueOrZero()),
					int(pokemon.DefIv.ValueOrZero()),
					int(pokemon.StaIv.ValueOrZero()),
					float64(pokemon.Level.ValueOrZero()))

				if err == nil {
					pvpBytes, _ := json.Marshal(pvp)
					pokemon.Pvp = null.StringFrom(string(pvpBytes))
				}
			}

			results = append(results, &pokemon)
		}
	}

	log.Infof("GetPokemonInArea - total time %s (locked time %s), %d scanned, %d returned", time.Since(start), lockedTime, pokemonExamined, len(results))

	return results
}
