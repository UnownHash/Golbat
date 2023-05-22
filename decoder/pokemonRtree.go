package decoder

import (
	"context"
	"github.com/UnownHash/gohbem"
	"github.com/jellydator/ttlcache/v3"
	"github.com/puzpuzpuz/xsync/v2"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"golang.org/x/exp/slices"
	"golbat/config"
	"golbat/geo"
	"gopkg.in/guregu/null.v4"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ApiPokemonScan struct {
	Min             geo.Location                `json:"min"`
	Max             geo.Location                `json:"max"`
	Center          geo.Location                `json:"center"`
	Limit           int                         `json:"limit"`
	GlobalFilter    *ApiPokemonFilter           `json:"global"`
	SpecificFilters map[string]ApiPokemonFilter `json:"filters"`
}

type ApiPokemonSearch struct {
	Min       geo.Location `json:"min"`
	Max       geo.Location `json:"max"`
	Center    geo.Location `json:"center"`
	Limit     int          `json:"limit"`
	SearchIds []int16      `json:"searchIds"`
}

type ApiPokemonFilter struct {
	Iv         []int8                      `json:"iv"`
	AtkIv      []int8                      `json:"atk_iv"`
	DefIv      []int8                      `json:"def_iv"`
	StaIv      []int8                      `json:"sta_iv"`
	Level      []int8                      `json:"level"`
	Cp         []int16                     `json:"cp"`
	Gender     int8                        `json:"gender"`
	Additional *ApiPokemonAdditionalFilter `json:"additional"`
	Pvp        *ApiPvpFilter               `json:"pvp"`
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
	IncludeXxs        bool `json:"include_xxs"`
	IncludeXxl        bool `json:"include_xxl"`
}

type ApiPokemonResult struct {
	Id                      string                           `json:"id"`
	PokestopId              null.String                      `json:"pokestop_id"`
	SpawnId                 null.Int                         `json:"spawn_id"`
	Lat                     float64                          `json:"lat"`
	Lon                     float64                          `json:"lon"`
	Weight                  null.Float                       `json:"weight"`
	Size                    null.Int                         `json:"size"`
	Height                  null.Float                       `json:"height"`
	ExpireTimestamp         null.Int                         `json:"expire_timestamp"`
	Updated                 null.Int                         `json:"updated"`
	PokemonId               int16                            `json:"pokemon_id"`
	Move1                   null.Int                         `json:"move_1"`
	Move2                   null.Int                         `json:"move_2"`
	Gender                  null.Int                         `json:"gender"`
	Cp                      null.Int                         `json:"cp"`
	AtkIv                   null.Int                         `json:"atk_iv"`
	DefIv                   null.Int                         `json:"def_iv"`
	StaIv                   null.Int                         `json:"sta_iv"`
	Iv                      null.Float                       `json:"iv"`
	Form                    null.Int                         `json:"form"`
	Level                   null.Int                         `json:"level"`
	EncounterWeather        uint8                            `json:"encounter_weather"`
	Weather                 null.Int                         `json:"weather"`
	Costume                 null.Int                         `json:"costume"`
	FirstSeenTimestamp      int64                            `json:"first_seen_timestamp"`
	Changed                 int64                            `json:"changed"`
	CellId                  null.Int                         `json:"cell_id"`
	ExpireTimestampVerified bool                             `json:"expire_timestamp_verified"`
	DisplayPokemonId        null.Int                         `json:"display_pokemon_id"`
	IsDitto                 bool                             `json:"is_ditto"`
	SeenType                null.String                      `json:"seen_type"`
	Shiny                   null.Bool                        `json:"shiny"`
	Username                null.String                      `json:"username"`
	Capture1                null.Float                       `json:"capture_1"`
	Capture2                null.Float                       `json:"capture_2"`
	Capture3                null.Float                       `json:"capture_3"`
	Pvp                     map[string][]gohbem.PokemonEntry `json:"pvp"`
	IsEvent                 int8                             `json:"is_event"`
	Distance                float64                          `json:"distance,omitempty"`
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

var pokemonLookupCache *xsync.MapOf[uint64, PokemonLookupCacheItem]
var pokemonTreeMutex sync.RWMutex
var pokemonTree rtree.RTreeG[uint64]

func initPokemonRtree() {
	pokemonLookupCache = xsync.NewIntegerMapOf[uint64, PokemonLookupCacheItem]()

	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[string, Pokemon]) {
		r := v.Value()
		removePokemonFromTree(&r)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})

}

func pokemonRtreeUpdatePokemonOnGet(pokemon *Pokemon) {
	pokemonId, _ := strconv.ParseUint(pokemon.Id, 10, 64)

	_, inMap := pokemonLookupCache.Load(pokemonId)

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

	pokemonLookupCacheItem, _ := pokemonLookupCache.Load(pokemonId)

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
		Iv:                 int8(math.Round(pokemon.Iv.Float64)),
		Size:               int8(valueOrMinus1(pokemon.Size)),
	}

	if changePvp {
		pokemonLookupCacheItem.PokemonPvpLookup = calculatePokemonPvpLookup(pokemon, pvpResults)
	}

	pokemonLookupCache.Store(pokemonId, pokemonLookupCacheItem)
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
	pokemonTreeMutex.Unlock()
	pokemonLookupCache.Delete(pokemonId)

	if beforeLen != afterLen+1 {
		log.Infof("PokemonRtree - UNEXPECTED removing %d, lat %f lon %f size %d->%d Map Len %d", pokemonId, pokemon.Lat, pokemon.Lon, beforeLen, afterLen, pokemonLookupCache.Size())
	}
}

func GetPokemonInArea(retrieveParameters ApiPokemonScan) []*ApiPokemonResult {
	// Validate filters

	validateFilter := func(filter *ApiPokemonFilter) bool {

		if filter.StaIv != nil && len(filter.StaIv) != 2 {
			return false
		}
		if filter.AtkIv != nil && len(filter.AtkIv) != 2 {
			return false
		}
		if filter.DefIv != nil && len(filter.DefIv) != 2 {
			return false
		}
		if filter.Iv != nil && len(filter.Iv) != 2 {
			return false
		}
		if filter.Level != nil && len(filter.Level) != 2 {
			return false
		}
		if filter.Cp != nil && len(filter.Cp) != 2 {
			return false
		}

		if filter.Pvp != nil {
			if filter.Pvp.Little != nil && len(filter.Pvp.Little) != 2 {
				return false
			}
			if filter.Pvp.Great != nil && len(filter.Pvp.Great) != 2 {
				return false
			}
			if filter.Pvp.Ultra != nil && len(filter.Pvp.Ultra) != 2 {
				return false
			}
		}
		return true
	}

	if retrieveParameters.GlobalFilter != nil && !validateFilter(retrieveParameters.GlobalFilter) {
		log.Errorf("GetPokemonInArea - Invalid global filter")
		return nil
	}

	for _, filter := range retrieveParameters.SpecificFilters {
		if !validateFilter(&filter) {
			log.Errorf("GetPokemonInArea - Invalid specific filter")
			return nil
		}
	}

	start := time.Now()

	min := retrieveParameters.Min
	max := retrieveParameters.Max
	specificPokemonFilters := retrieveParameters.SpecificFilters
	globalFilter := retrieveParameters.GlobalFilter

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxPokemon {
		maxPokemon = retrieveParameters.Limit
	}

	pokemonExamined := 0
	pokemonSkipped := 0

	isPokemonMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter ApiPokemonFilter) bool {
		// start with filter true if we have any filter set (no filters no match)
		filterMatched := filter.Iv != nil || filter.StaIv != nil || filter.AtkIv != nil || filter.DefIv != nil || filter.Level != nil || filter.Cp != nil || filter.Gender != 0
		pvpMatched := false // assume pvp match is true unless any filter matches
		additionalMatch := false

		if filterMatched {
			if filter.Iv != nil && (pokemonLookup.Iv < filter.Iv[0] || pokemonLookup.Iv > filter.Iv[1]) {
				filterMatched = false
			} else if filter.StaIv != nil && (pokemonLookup.Sta < filter.StaIv[0] || pokemonLookup.Sta > filter.StaIv[1]) {
				filterMatched = false
			} else if filter.AtkIv != nil && (pokemonLookup.Atk < filter.AtkIv[0] || pokemonLookup.Atk > filter.AtkIv[1]) {
				filterMatched = false
			} else if filter.DefIv != nil && (pokemonLookup.Def < filter.DefIv[0] || pokemonLookup.Def > filter.DefIv[1]) {
				filterMatched = false
			} else if filter.Level != nil && (pokemonLookup.Level < filter.Level[0] || pokemonLookup.Level > filter.Level[1]) {
				filterMatched = false
			} else if filter.Cp != nil && (pokemonLookup.Cp < filter.Cp[0] || pokemonLookup.Cp > filter.Cp[1]) {
				filterMatched = false
			} else if filter.Gender != 0 && pokemonLookup.Gender != filter.Gender {
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
			} else if filter.Additional.IncludeXxs && pokemonLookup.Size == 1 {
				additionalMatch = true
			} else if filter.Additional.IncludeXxl && pokemonLookup.Size == 5 {
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
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()

	lockedTime := time.Since(start)

	performScan := func() (returnKeys []uint64) {
		pokemonMatched := 0
		pokemonTree2.Search([2]float64{min.Longitude, min.Latitude}, [2]float64{max.Longitude, max.Latitude},
			func(min, max [2]float64, pokemonId uint64) bool {
				pokemonExamined++

				pokemonLookupItem, found := pokemonLookupCache.Load(pokemonId)
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
						log.Infof("GetPokemonInArea - result would exceed maximum size (%d), stopping scan", maxPokemon)
						return false
					}
				}

				return true // always continue
			})

		return
	}

	returnKeys := performScan()

	results := make([]*ApiPokemonResult, 0, len(returnKeys))

	for _, key := range returnKeys {
		if pokemonCacheEntry := pokemonCache.Get(strconv.FormatUint(key, 10)); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			apiPokemon := ApiPokemonResult{
				Id:              pokemon.Id,
				PokestopId:      pokemon.PokestopId,
				SpawnId:         pokemon.SpawnId,
				Lat:             pokemon.Lat,
				Lon:             pokemon.Lon,
				Weight:          pokemon.Weight,
				Size:            pokemon.Size,
				Height:          pokemon.Height,
				ExpireTimestamp: pokemon.ExpireTimestamp,
				Updated:         pokemon.Updated,
				PokemonId:       pokemon.PokemonId,
				Move1:           pokemon.Move1,
				Move2:           pokemon.Move2,
				Gender:          pokemon.Gender,
				Cp:              pokemon.Cp,
				AtkIv:           pokemon.AtkIv,
				DefIv:           pokemon.DefIv,
				StaIv:           pokemon.StaIv,
				//not IvInactive
				Iv:                      pokemon.Iv,
				Form:                    pokemon.Form,
				Level:                   pokemon.Level,
				EncounterWeather:        pokemon.EncounterWeather, //? perhaps do not include
				Weather:                 pokemon.Weather,
				Costume:                 pokemon.Costume,
				FirstSeenTimestamp:      pokemon.FirstSeenTimestamp,
				Changed:                 pokemon.Changed,
				CellId:                  pokemon.CellId,
				ExpireTimestampVerified: pokemon.ExpireTimestampVerified,
				DisplayPokemonId:        pokemon.DisplayPokemonId,
				IsDitto:                 pokemon.IsDitto,
				SeenType:                pokemon.SeenType,
				Shiny:                   pokemon.Shiny,
				Username:                pokemon.Username,
				Pvp: func() map[string][]gohbem.PokemonEntry {
					if ohbem != nil {
						pvp, err := ohbem.QueryPvPRank(int(pokemon.PokemonId),
							int(pokemon.Form.ValueOrZero()),
							int(pokemon.Costume.ValueOrZero()),
							int(pokemon.Gender.ValueOrZero()),
							int(pokemon.AtkIv.ValueOrZero()),
							int(pokemon.DefIv.ValueOrZero()),
							int(pokemon.StaIv.ValueOrZero()),
							float64(pokemon.Level.ValueOrZero()))
						if err != nil {
							return nil
						}
						return pvp
					}
					return nil
				}(),
			}

			results = append(results, &apiPokemon)
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

	start := time.Now()

	pkmnMap := make(map[pokemonFormKey]int)
	pokemonLookupCache.Range(func(key uint64, pokemon PokemonLookupCacheItem) bool {
		pkmnMap[pokemonFormKey{pokemon.PokemonLookup.PokemonId, pokemon.PokemonLookup.Form}]++
		return true
	})

	var available []*Available
	for key, count := range pkmnMap {

		pkmn := &Available{
			PokemonId: key.pokemonId,
			Form:      key.form,
			Count:     count,
		}
		available = append(available, pkmn)
	}

	log.Infof("GetAvailablePokemon - total time %s (locked time --)", time.Since(start))

	return available
}

func SearchPokemon(request ApiPokemonSearch) []*Pokemon {
	start := time.Now()
	results := make([]*Pokemon, 0, request.Limit)
	pokemonMatched := 0

	if request.SearchIds == nil {
		return nil
	}

	pokemonTreeMutex.RLock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if request.Limit > 0 && request.Limit < maxPokemon {
		maxPokemon = request.Limit
	}
	pokemonSkipped := 0
	pokemonScanned := 0
	maxDistance := float64(1000) // This should come from the request?

	pokemonTree2.Nearby(
		rtree.BoxDist[float64, uint64]([2]float64{request.Center.Longitude, request.Center.Latitude}, [2]float64{request.Center.Longitude, request.Center.Latitude}, nil),
		func(min, max [2]float64, pokemonId uint64, dist float64) bool {
			pokemonLookupItem, inCache := pokemonLookupCache.Load(pokemonId)
			if !inCache {
				pokemonSkipped++
				// Did not find cached result, something amiss?
				return true
			}

			pokemonScanned++
			if dist > maxDistance {
				log.Infof("SearchPokemon - result would exceed maximum distance (%f), stopping scan", maxDistance)
				return false
			}

			found := slices.Contains(request.SearchIds, pokemonLookupItem.PokemonLookup.PokemonId)

			if found {
				if pokemonCacheEntry := pokemonCache.Get(strconv.FormatUint(pokemonId, 10)); pokemonCacheEntry != nil {
					pokemon := pokemonCacheEntry.Value()
					results = append(results, &pokemon)
					pokemonMatched++

					if pokemonMatched > maxPokemon {
						log.Infof("SearchPokemon - result would exceed maximum size (%d), stopping scan", maxPokemon)
						return false
					}
				}
			}

			return true
		},
	)

	log.Infof("SearchPokemon - scanned %d pokemon, total time %s, %d returned", pokemonScanned, time.Since(start), len(results))
	return results
}
