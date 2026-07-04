package decoder

import (
	"math"
	"time"

	"golbat/config"
	"golbat/geo"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonDnfId struct {
	Pokemon int16  `json:"id" doc:"Pokedex id to match; 0 matches any pokemon. Required within a pokemon entry — a form without an id can never match."`
	Form    *int16 `json:"form" required:"false" doc:"Form id to match; null matches any form of the given id."`
}

// ApiPokemonDnfMinMax is an inclusive integer range used by the filter clauses.
// It is int16 internally (wide enough for CP and PVP ranks); the smaller fields
// like IV simply use the low end of that range.
type ApiPokemonDnfMinMax struct {
	Min int16 `json:"min" required:"false" doc:"Minimum value (inclusive). An omitted bound defaults to 0."`
	Max int16 `json:"max" required:"false" doc:"Maximum value (inclusive). An omitted bound defaults to 0, so a range with only min can never match — send both bounds."`
}

func contains(s []int8, e int8) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func convertToMinMax(minmax *pb.RangeMinMax) *ApiPokemonDnfMinMax {
	if minmax == nil {
		return nil
	}
	var minV int16 = 0
	var maxV int16 = math.MaxInt16
	if minmax.Min != nil {
		minV = int16(*minmax.Min)
	}
	if minmax.Max != nil {
		maxV = int16(*minmax.Max)
	}

	return &ApiPokemonDnfMinMax{
		Min: minV,
		Max: maxV,
	}
}

type dnfFilterLookup struct {
	pokemon int16
	form    int16
}

type PokemonScanRetrieveParameters interface {
	GetMin() geo.Location
	GetMax() geo.Location
	GetLimit() int
}

func internalGetPokemonInArea[F any](
	retrieveParameters PokemonScanRetrieveParameters,
	dnfFilters map[dnfFilterLookup][]F,
	isPokemonDnfMatch func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter *F) bool,
) ([]uint64, int, int, int) {
	start := time.Now()

	minLocation := retrieveParameters.GetMin()
	maxLocation := retrieveParameters.GetMax()

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.GetLimit() > 0 && retrieveParameters.GetLimit() < maxPokemon {
		maxPokemon = retrieveParameters.GetLimit()
	}

	pokemonExamined := 0
	pokemonSkipped := 0

	pokemonTree2 := getPokemonTreeSnapshot()

	lockedTime := time.Since(start)
	totalPokemon := pokemonTree2.Len()

	var returnKeys []uint64

	performScan := func() {
		pokemonMatched := 0
		pokemonTree2.Search([2]float64{minLocation.Longitude, minLocation.Latitude}, [2]float64{maxLocation.Longitude, maxLocation.Latitude},
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

				matched := false

				filters, found := dnfFilters[dnfFilterLookup{
					pokemon: pokemonLookup.PokemonId,
					form:    pokemonLookup.Form}]

				if !found {
					filters, found = dnfFilters[dnfFilterLookup{
						pokemon: pokemonLookup.PokemonId,
						form:    -1}]

					if !found {
						filters, found = dnfFilters[dnfFilterLookup{
							pokemon: -1,
							form:    -1}]

						if !found {
							return true
						}
					}
				}

				for x := 0; x < len(filters); x++ {
					if isPokemonDnfMatch(pokemonLookup, pvpLookup, &filters[x]) {
						matched = true
						break
					}
				}

				if matched {
					returnKeys = append(returnKeys, pokemonId)
					pokemonMatched++
					if pokemonMatched > maxPokemon {
						log.Infof("GetPokemonInArea - result would exceed maximum size (%d), stopping scan", maxPokemon)
						return false
					}
				}

				return true // always continue
			})

	}

	performScan()
	log.Infof("GetPokemonInArea - scan time %s (locked time %s), %d scanned, %d skipped, %d returned", time.Since(start), lockedTime, pokemonExamined, pokemonSkipped, len(returnKeys))

	return returnKeys, pokemonExamined, pokemonSkipped, totalPokemon
}
