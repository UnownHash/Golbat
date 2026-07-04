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
	PokemonId int16 `json:"id" doc:"Pokedex id"`
	Form      int16 `json:"form" doc:"Form id"`
	Count     int   `json:"count" doc:"Number currently in the cache"`
}

func GetAvailablePokemon() []*ApiPokemonAvailableResult {
	var available []*ApiPokemonAvailableResult
	pokemonFormCount.Range(func(key pokemonFormKey, count int64) bool {
		if count > 0 {
			available = append(available, &ApiPokemonAvailableResult{
				PokemonId: key.pokemonId,
				Form:      key.form,
				Count:     int(count),
			})
		}
		return true
	})
	return available
}

// Pokemon search

type ApiPokemonSearch struct {
	Min       ApiLatLon `json:"min" required:"false" doc:"Lower-left (minimum lat/lon) corner of the bounding box to search. Omit together with max for a center-only search with a small default radius."`
	Max       ApiLatLon `json:"max" required:"false" doc:"Upper-right (maximum lat/lon) corner of the bounding box to search. Omit together with min for a center-only search with a small default radius."`
	Center    ApiLatLon `json:"center" required:"false" doc:"Center point used to order results by distance. Defaults to the zero coordinate."`
	Limit     int       `json:"limit" required:"false" doc:"Maximum number of results to return. 0 means use the configured maximum."`
	SearchIds []int16   `json:"searchIds" required:"false" doc:"Pokemon ids to match. A pokemon is returned only if its id is in this list."`
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

func SearchPokemon(request ApiPokemonSearch) ([]*ApiPokemonResult, error) {
	start := time.Now()
	results := make([]uint64, 0, request.Limit)
	pokemonMatched := 0

	if request.SearchIds == nil {
		return nil, fmt.Errorf("SearchPokemon - no search ids provided")
	}
	if haversine(request.Min.Location(), request.Max.Location()) > config.Config.Tuning.MaxPokemonDistance {
		return nil, fmt.Errorf("SearchPokemon - the distance between max and min points is greater than the configurable max distance")
	}

	pokemonTree2 := getPokemonTreeSnapshot()

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if request.Limit > 0 && request.Limit < maxPokemon {
		maxPokemon = request.Limit
	}
	pokemonSkipped := 0
	pokemonScanned := 0
	maxDistance := calculateHypotenuse(request.Max.Lon-request.Min.Lon, request.Max.Lat-request.Min.Lat) / 2
	if maxDistance == 0 {
		maxDistance = 10
	}

	pokemonTree2.Nearby(
		rtree.BoxDist[float64, uint64]([2]float64{request.Center.Lon, request.Center.Lat}, [2]float64{request.Center.Lon, request.Center.Lat}, nil),
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
				results = append(results, pokemonId)
				pokemonMatched++

				if pokemonMatched > maxPokemon {
					log.Infof("SearchPokemon - result would exceed maximum size (%d), stopping scan", maxPokemon)
					return false
				}
			}

			return true
		},
	)

	log.Infof("SearchPokemon - scanned %d pokemon, total time %s, %d returned", pokemonScanned, time.Since(start), len(results))

	apiResults := make([]*ApiPokemonResult, 0, len(results))

	for _, encounterId := range results {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(encounterId, "API.Pokemon")
		if pokemon != nil {
			apiPokemon := buildApiPokemonResult(pokemon)
			apiResults = append(apiResults, &apiPokemon)
			unlock()
		}
	}

	return apiResults, nil
}

// Get one result

func GetOnePokemon(pokemonId uint64) *ApiPokemonResult {
	item, unlock, _ := peekPokemonRecordReadOnly(pokemonId, "API.PokemonById")
	if item != nil {
		apiPokemon := buildApiPokemonResult(item)
		defer unlock()
		return &apiPokemon
	}
	return nil
}
