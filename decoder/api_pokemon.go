package decoder

import (
	"fmt"
	"math"
	"slices"
	"time"

	"golbat/config"
	"golbat/geo"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
)

const earthRadiusKm = 6371

type ApiPokemonAvailableResult struct {
	PokemonId int16 `json:"id"`
	Form      int16 `json:"form"`
	Count     int   `json:"count"`
}

func GetAvailablePokemon() []*ApiPokemonAvailableResult {
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

	var available []*ApiPokemonAvailableResult
	for key, count := range pkmnMap {

		pkmn := &ApiPokemonAvailableResult{
			PokemonId: key.pokemonId,
			Form:      key.form,
			Count:     count,
		}
		available = append(available, pkmn)
	}

	log.Infof("GetAvailablePokemon - total time %s (locked time --)", time.Since(start))

	return available
}

// Pokemon search

type ApiPokemonSearch struct {
	Min       geo.Location `json:"min"`
	Max       geo.Location `json:"max"`
	Center    geo.Location `json:"center"`
	Limit     int          `json:"limit"`
	SearchIds []int16      `json:"searchIds"`
}

func calculateHypotenuse(a, b float64) float64 {
	return math.Sqrt(a*a + b*b)
}

func toRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

func haversine(start, end geo.Location) float64 {
	lat1Rad := toRadians(start.Latitude)
	lat2Rad := toRadians(end.Latitude)
	deltaLat := toRadians(end.Latitude - start.Latitude)
	deltaLon := toRadians(end.Longitude - start.Longitude)

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

func SearchPokemon(request ApiPokemonSearch) ([]*Pokemon, error) {
	start := time.Now()
	results := make([]*Pokemon, 0, request.Limit)
	pokemonMatched := 0

	if request.SearchIds == nil {
		return nil, fmt.Errorf("SearchPokemon - no search ids provided")
	}
	if haversine(request.Min, request.Max) > config.Config.Tuning.MaxPokemonDistance {
		return nil, fmt.Errorf("SearchPokemon - the distance between max and min points is greater than the configurable max distance")
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
	maxDistance := calculateHypotenuse(request.Max.Longitude-request.Min.Longitude, request.Max.Latitude-request.Min.Latitude) / 2
	if maxDistance == 0 {
		maxDistance = 10
	}
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
				if pokemonCacheEntry := getPokemonFromCache(pokemonId); pokemonCacheEntry != nil {
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
	return results, nil
}

// Get one result

func GetOnePokemon(pokemonId uint64) *ApiPokemonResult {
	if item := getPokemonFromCache(pokemonId); item != nil {
		pokemon := item.Value()
		apiPokemon := buildApiPokemonResult(&pokemon)
		return &apiPokemon
	}
	return nil
}
