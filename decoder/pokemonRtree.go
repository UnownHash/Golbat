package decoder

import (
	"context"
	"encoding/json"
	"github.com/UnownHash/gohbem"
	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/vm"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"golbat/config"
	"golbat/geo"
	"gopkg.in/guregu/null.v4"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ApiPokemonRetrieve struct {
	Min             geo.Location                `json:"min"`
	Max             geo.Location                `json:"max"`
	Center          geo.Location                `json:"center"`
	Limit           int                         `json:"limit"`
	SearchIds       []int16                     `json:"searchIds"`
	GlobalFilter    *ApiPokemonFilter           `json:"global"`
	SpecificFilters map[string]ApiPokemonFilter `json:"filters"`
}
type ApiPokemonFilter struct {
	Iv         []int8                      `json:"iv"`
	AtkIv      []int8                      `json:"atk_iv"`
	DefIv      []int8                      `json:"def_iv"`
	StaIv      []int8                      `json:"sta_iv"`
	Level      []int8                      `json:"level"`
	Cp         []int16                     `json:"cp"`
	Gender     int8                        `json:"gender"`
	Xxs        bool                        `json:"xxs"`
	Xxl        bool                        `json:"xxl"`
	Additional *ApiPokemonAdditionalFilter `json:"additional"`
	Pvp        *ApiPvpFilter               `json:"pvp"`
	Expert     *string                     `json:"expert"`
}
type ApiPvpFilter struct {
	Little []int16 `json:"little"`
	Great  []int16 `json:"great"`
	Ultra  []int16 `json:"ultra"`
}
type ApiPokemonAdditionalFilter struct {
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

type Available struct {
	PokemonId int16 `json:"id"`
	Form      int16 `json:"form"`
	Count     int   `json:"count"`
}

var pokemonLookupCache map[uint64]PokemonLookupCacheItem
var pokemonTreeMutex sync.RWMutex
var pokemonTree rtree.RTreeG[uint64]

func initPokemonRtree() {
	pokemonLookupCache = make(map[uint64]PokemonLookupCacheItem)

	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[string, Pokemon]) {
		r := v.Value()
		log.Infof("PokemonRtree - Cache expiry - removing pokemon %s", r.Id)
		removePokemonFromTree(&r)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})

}

func pokemonRtreeUpdatePokemonOnGet(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)

	pokemonTreeMutex.RLock()
	_, inMap := pokemonLookupCache[pokemonId]
	pokemonTreeMutex.RUnlock()
	if !inMap {
		addPokemonToTree(pokemon)
		// this pokemon won't be available for pvp searches
		updatePokemonLookup(pokemon, false, nil)
	}
}

func valueOrMinus1(n null.Int) int {
	if n.Valid {
		return int(n.Int64)
	}
	return -1
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
		Gender:             int8(valueOrMinus1(pokemon.Gender)),
		Cp:                 int16(valueOrMinus1(pokemon.Cp)),
		Size:               int8(valueOrMinus1(pokemon.Size)),
		Iv: func() int8 {
			if pokemon.Iv.Valid {
				return int8(math.Round(pokemon.Iv.Float64))
			}
			return -1
		}(),
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

func addPokemonToTree(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)

	log.Infof("PokemonRtree - add %d, lat %f lon %f", pokemonId, pokemon.Lat, pokemon.Lon)

	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	pokemonTreeMutex.Unlock()
}

func removePokemonFromTree(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)
	pokemonTreeMutex.Lock()
	beforeLen := pokemonTree.Len()
	pokemonTree.Delete([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	afterLen := pokemonTree.Len()
	delete(pokemonLookupCache, pokemonId)
	pokemonTreeMutex.Unlock()
	unexpected := ""
	if beforeLen != afterLen+1 {
		unexpected = " UNEXPECTED"
	}
	log.Infof("PokemonRtree - removing %d, lat %f lon %f size %d->%d%s Map Len %d", pokemonId, pokemon.Lat, pokemon.Lon, beforeLen, afterLen, unexpected, len(pokemonLookupCache))
}

var filterTokenizer = regexp.MustCompile(
	`^\s*([()|&!,]|([ADSLXG]?|CP|LC|[GU]L)\s*([0-9]+(?:\.[0-9]*)?)(?:\s*-\s*([0-9]+(?:\.[0-9]*)?))?)`)
var emptyPvp = PokemonPvpLookup{Little: -1, Great: -1, Ultra: -1}

type filterEnv struct {
	pokemon *PokemonLookup
	pvp     *PokemonPvpLookup
}
type expertFilterCache map[string]*vm.Program

func compilePokemonFilter(cache expertFilterCache, expert string) *vm.Program {
	expert = strings.ToUpper(expert)
	if out, ok := cache[expert]; ok {
		return out
	}
	out := func() *vm.Program {
		var builder strings.Builder
		// we first transcode input filter into a compilable expr
		for i := 0; i < len(expert); {
			match := filterTokenizer.FindStringSubmatchIndex(expert[i:])
			if match == nil {
				log.Debugf("Failed to transcode Pokemon expert filter: %s", expert)
				return nil
			}
			i = match[1]
			if match[6] < 0 {
				switch s := expert[match[2]:match[3]]; s {
				case "(", ")", "!":
					builder.WriteString(s)
				case "|", ",":
					builder.WriteString("||")
				case "&":
					builder.WriteString("&&")
				}
				continue
			}
			var column string
			switch s := expert[match[4]:match[5]]; s {
			case "A":
				column = "pokemon.Atk"
			case "D":
				column = "pokemon.Def"
			case "S":
				column = "pokemon.Sta"
			case "L":
				column = "pokemon.Level"
			case "X":
				column = "pokemon.Size"
			case "CP":
				column = "pokemon.Cp"
			case "GL":
				column = "pvp.Great"
			case "UL":
				column = "pvp.Ultra"
			case "LC":
				column = "pvp.Little"
			}
			builder.WriteByte('(')
			builder.WriteString(column)
			if match[8] < 0 {
				builder.WriteString("==")
				builder.WriteString(expert[match[6]:match[7]])
			} else {
				builder.WriteString(">=")
				builder.WriteString(expert[match[6]:match[7]])
				builder.WriteString("&&")
				builder.WriteString(column)
				builder.WriteString("<=")
				builder.WriteString(expert[match[8]:match[9]])
			}
			builder.WriteByte(')')
		}
		if builder.Len() == 0 {
			builder.WriteString("true")
		}
		out, err := expr.Compile(builder.String(), expr.Env(filterEnv{}), expr.AsBool())
		if err != nil {
			log.Debugf("Malformed Pokemon expert filter: %s; Failed to compile %s: %s", expert, builder.String(), err)
		}
		return out
	}()
	cache[expert] = out
	return out
}

func GetPokemonInArea(retrieveParameters ApiPokemonRetrieve) []*Pokemon {
	start := time.Now()

	min := retrieveParameters.Min
	max := retrieveParameters.Max
	specificPokemonFilters := retrieveParameters.SpecificFilters
	globalFilter := retrieveParameters.GlobalFilter
	expertCache := make(expertFilterCache)

	pokemonExamined := 0
	pokemonSkipped := 0

	isPokemonMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter ApiPokemonFilter) bool {
		if filter.Expert != nil {
			compiled := compilePokemonFilter(expertCache, *filter.Expert)
			if compiled == nil {
				return false
			}
			env := filterEnv{pokemon: pokemonLookup, pvp: pvpLookup}
			if env.pvp == nil {
				env.pvp = &emptyPvp
			}
			output, err := expr.Run(compiled, env)
			if err != nil {
				log.Warnf("Failed to run expert filter %s on Pokemon %v: %s", *filter.Expert, env, err)
				return false
			}
			return output.(bool)
		}

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

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	pokemonMatched := 0
	pokemonTree.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
		func(min, max [2]float64, pokemonId uint64) bool {
			pokemonExamined++

			pokemonLookupItem, found := pokemonLookupCache[pokemonId]
			if !found {
				pokemonSkipped++
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

			if !globalFilterMatched && specificPokemonFilters != nil {
				var formString strings.Builder
				formString.WriteString(strconv.Itoa(int(pokemonLookup.PokemonId)))
				formString.WriteByte('-')
				formString.WriteString(strconv.Itoa(int(pokemonLookup.Form)))
				filter, found := specificPokemonFilters[formString.String()]

				if found {
					specificFilterMatched = isPokemonMatch(pokemonLookup, pvpLookup, filter)
				}
			}

			if globalFilterMatched || specificFilterMatched {
				returnKeys = append(returnKeys, pokemonId)
				pokemonMatched++
				if pokemonMatched > maxPokemon {
					log.Infof("GetPokemonInArea - result would exceed maximum size, stopping scan")
					return false
				}
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

	log.Infof("GetPokemonInArea - total time %s (locked time %s), %d scanned, %d skipped, %d returned", time.Since(start), lockedTime, pokemonExamined, pokemonSkipped, len(results))

	return results
}

func GetOnePokemon(pokemonId uint64) *Pokemon {
	if item := pokemonCache.Get(strconv.FormatUint(pokemonId, 10)); item != nil {
		pokemon := item.Value()
		return &pokemon
	}
	return nil
}

func GetAvailablePokemon() []*Available {
	type pokemonFormKey struct {
		pokemonId int16
		form      int16
	}

	pokemonTreeMutex.RLock()
	pkmnMap := make(map[pokemonFormKey]int)
	for _, pokemon := range pokemonLookupCache {
		pkmnMap[pokemonFormKey{pokemon.PokemonLookup.PokemonId, pokemon.PokemonLookup.Form}]++
	}
	pokemonTreeMutex.RUnlock()

	var available []*Available
	for key, count := range pkmnMap {

		pkmn := &Available{
			PokemonId: key.pokemonId,
			Form:      key.form,
			Count:     count,
		}
		available = append(available, pkmn)
	}

	return available
}

func SearchPokemon(request ApiPokemonRetrieve) []*Pokemon {
	start := time.Now()
	results := make([]*Pokemon, 0, request.Limit)
	pokemonMatched := 0

	pokemonTreeMutex.Lock()
	pokemonTree.Nearby(
		func(min, max [2]float64, data uint64, item bool) float64 {
			if item {
				if pokemonCacheEntry := pokemonCache.Get(strconv.FormatUint(data, 10)); pokemonCacheEntry != nil {
					pokemon := pokemonCacheEntry.Value()
					if request.SearchIds != nil && pokemonMatched < request.Limit {
						found := false
						for _, id := range request.SearchIds {
							if pokemon.PokemonId == id {
								found = true
								break
							}
						}
						if !found {
							return 0
						}
						results = append(results, &pokemon)
						pokemonMatched++
					}
				}
			}
			return 0
		},
		func(min, max [2]float64, data uint64, dist float64) bool {
			return true
		},
	)
	pokemonTreeMutex.Unlock()

	log.Infof("SearchPokemon - total time %s, %d returned", time.Since(start), len(results))
	return results
}
