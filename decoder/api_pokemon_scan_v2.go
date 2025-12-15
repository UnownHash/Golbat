package decoder

import (
	"math"
	"time"

	"golbat/config"
	"golbat/geo"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonScan2 struct {
	Min        geo.Location          `json:"min"`
	Max        geo.Location          `json:"max"`
	Limit      int                   `json:"limit"`
	DnfFilters []ApiPokemonDnfFilter `json:"filters"`
}

type ApiPokemonDnfFilter struct {
	Pokemon []ApiPokemonDnfId     `json:"pokemon"`
	Iv      *ApiPokemonDnfMinMax8 `json:"iv"`
	AtkIv   *ApiPokemonDnfMinMax8 `json:"atk_iv"`
	DefIv   *ApiPokemonDnfMinMax8 `json:"def_iv"`
	StaIv   *ApiPokemonDnfMinMax8 `json:"sta_iv"`
	Level   *ApiPokemonDnfMinMax8 `json:"level"`
	Cp      *ApiPokemonDnfMinMax  `json:"cp"`
	Gender  *ApiPokemonDnfMinMax8 `json:"gender"`
	Size    *ApiPokemonDnfMinMax8 `json:"size"`
	Little  *ApiPokemonDnfMinMax  `json:"pvp_little"`
	Great   *ApiPokemonDnfMinMax  `json:"pvp_great"`
	Ultra   *ApiPokemonDnfMinMax  `json:"pvp_ultra"`
}

type ApiPokemonDnfId struct {
	Pokemon int16  `json:"id"`
	Form    *int16 `json:"form"`
}

type ApiPokemonDnfMinMax struct {
	Min int16 `json:"min"`
	Max int16 `json:"max"`
}

type ApiPokemonDnfMinMax8 struct {
	Min int8 `json:"min"`
	Max int8 `json:"max"`
}

func internalGetPokemonInArea2(retrieveParameters ApiPokemonScan2) []uint64 {
	type dnfFilterLookup struct {
		pokemon int16
		form    int16
	}

	dnfFilters := make(map[dnfFilterLookup][]ApiPokemonDnfFilter)

	for _, filter := range retrieveParameters.DnfFilters {
		if len(filter.Pokemon) > 0 {
			for _, keyString := range filter.Pokemon {
				pokemonId := keyString.Pokemon
				if pokemonId == 0 {
					pokemonId = -1
				}
				var formId int16 = -1
				if keyString.Form != nil {
					formId = *keyString.Form
				}
				key := dnfFilterLookup{
					pokemon: pokemonId,
					form:    formId,
				}
				dnfFilters[key] = append(dnfFilters[key], filter)
			}
		} else {
			key := dnfFilterLookup{
				pokemon: -1,
				form:    -1,
			}
			dnfFilters[key] = append(dnfFilters[key], filter)
		}
	}

	start := time.Now()

	minLocation := retrieveParameters.Min
	maxLocation := retrieveParameters.Max

	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxPokemon {
		maxPokemon = retrieveParameters.Limit
	}

	pokemonExamined := 0
	pokemonSkipped := 0

	isPokemonDnfMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter *ApiPokemonDnfFilter) bool {
		if filter.Iv != nil && (pokemonLookup.Iv < filter.Iv.Min || pokemonLookup.Iv > filter.Iv.Max) ||
			filter.StaIv != nil && (pokemonLookup.Sta < filter.StaIv.Min || pokemonLookup.Sta > filter.StaIv.Max) ||
			filter.AtkIv != nil && (pokemonLookup.Atk < filter.AtkIv.Min || pokemonLookup.Atk > filter.AtkIv.Max) ||
			filter.DefIv != nil && (pokemonLookup.Def < filter.DefIv.Min || pokemonLookup.Def > filter.DefIv.Max) ||
			filter.Level != nil && (pokemonLookup.Level < filter.Level.Min || pokemonLookup.Level > filter.Level.Max) ||
			filter.Cp != nil && (pokemonLookup.Cp < filter.Cp.Min || pokemonLookup.Cp > filter.Cp.Max) ||
			filter.Gender != nil && (pokemonLookup.Gender < filter.Gender.Min || pokemonLookup.Gender > filter.Gender.Max) ||
			filter.Size != nil && (pokemonLookup.Size < filter.Size.Min || pokemonLookup.Size > filter.Size.Max) {
			return false
		}

		if filter.Little != nil && (pvpLookup == nil || pvpLookup.Little < filter.Little.Min || pvpLookup.Little > filter.Little.Max) ||
			filter.Great != nil && (pvpLookup == nil || pvpLookup.Great < filter.Great.Min || pvpLookup.Great > filter.Great.Max) ||
			filter.Ultra != nil && (pvpLookup == nil || pvpLookup.Ultra < filter.Ultra.Min || pvpLookup.Ultra > filter.Ultra.Max) {
			return false
		}
		return true
	}

	pokemonTreeMutex.RLock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()

	lockedTime := time.Since(start)

	performScan := func() (returnKeys []uint64) {
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

		return
	}

	results := performScan()
	log.Infof("GetPokemonInAreaV2 - scan time %s (locked time %s), %d scanned, %d skipped, %d returned", time.Since(start), lockedTime, pokemonExamined, pokemonSkipped, len(results))

	return results
}

func GetPokemonInArea2(retrieveParameters ApiPokemonScan2) []*ApiPokemonResult {
	returnKeys := internalGetPokemonInArea2(retrieveParameters)
	results := make([]*ApiPokemonResult, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		if pokemonCacheEntry := getPokemonFromCache(key); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			if pokemon.ExpireTimestamp.ValueOrZero() < startUnix {
				continue
			}

			apiPokemon := buildApiPokemonResult(&pokemon)

			results = append(results, &apiPokemon)
		}
	}

	log.Infof("GetPokemonInAreaV2 - result buffer time %s, %d added", time.Since(start), len(results))

	return results
}

func GrpcGetPokemonInArea2(retrieveParameters *pb.PokemonScanRequest) []*pb.PokemonDetails {
	// Build consistent api request

	apiRequest := ApiPokemonScan2{
		Min: geo.Location{
			Latitude:  float64(retrieveParameters.GetMinLat()),
			Longitude: float64(retrieveParameters.GetMinLon()),
		},
		Max: geo.Location{
			Latitude:  float64(retrieveParameters.GetMaxLat()),
			Longitude: float64(retrieveParameters.GetMaxLon()),
		},
		Limit: int(retrieveParameters.GetLimit()),
	}
	var dnfFilters []ApiPokemonDnfFilter

	convertToMinMax8 := func(minmax *pb.RangeMinMax) *ApiPokemonDnfMinMax8 {
		if minmax == nil {
			return nil
		}
		var minV int8 = 0
		var maxV int8 = math.MaxInt8
		if minmax.HasMin() {
			minV = int8(minmax.GetMin())
		}
		if minmax.HasMax() {
			maxV = int8(minmax.GetMin())
		}

		return &ApiPokemonDnfMinMax8{
			Min: minV,
			Max: maxV,
		}
	}

	convertToMinMax16 := func(minmax *pb.RangeMinMax) *ApiPokemonDnfMinMax {
		if minmax == nil {
			return nil
		}
		var minV int16 = 0
		var maxV int16 = math.MaxInt16
		if minmax.HasMin() {
			minV = int16(minmax.GetMin())
		}
		if minmax.HasMax() {
			maxV = int16(minmax.GetMin())
		}

		return &ApiPokemonDnfMinMax{
			Min: minV,
			Max: maxV,
		}
	}

	for _, filter := range retrieveParameters.GetFilters() {
		dnfFilter := ApiPokemonDnfFilter{
			Pokemon: func() []ApiPokemonDnfId {
				var pokemonRes []ApiPokemonDnfId
				for _, pokemon := range filter.GetPokemon() {
					pokemonRes = append(pokemonRes, ApiPokemonDnfId{
						Pokemon: func() int16 {
							if !pokemon.HasId() {
								return 0
							}
							return int16(pokemon.GetId())
						}(),
						Form: func() *int16 {
							if pokemon.HasForm() {
								form := int16(pokemon.GetForm())
								return &form
							}
							return nil
						}(),
					})
				}

				return pokemonRes
			}(),
			Iv:     convertToMinMax8(filter.GetIv()),
			AtkIv:  convertToMinMax8(filter.GetAtkIv()),
			DefIv:  convertToMinMax8(filter.GetDefIv()),
			StaIv:  convertToMinMax8(filter.GetStaIv()),
			Level:  convertToMinMax8(filter.GetLevel()),
			Cp:     convertToMinMax16(filter.GetCp()),
			Size:   convertToMinMax8(filter.GetSize()),
			Gender: convertToMinMax8(filter.GetGender()),
			Little: convertToMinMax16(filter.GetPvpLittleRanking()),
			Great:  convertToMinMax16(filter.GetPvpGreatRanking()),
			Ultra:  convertToMinMax16(filter.GetPvpUltraRanking()),
		}

		dnfFilters = append(dnfFilters, dnfFilter)
	}
	apiRequest.DnfFilters = dnfFilters

	returnKeys := internalGetPokemonInArea2(apiRequest)
	results := make([]*pb.PokemonDetails, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		if pokemonCacheEntry := getPokemonFromCache(key); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			if pokemon.ExpireTimestamp.ValueOrZero() < startUnix {
				continue
			}

			apiPokemon := pb.PokemonDetails_builder{
				Id:         pokemon.Id,
				PokestopId: pokemon.PokestopId.Ptr(),
				SpawnId:    pokemon.SpawnId.Ptr(),
				Lat:        pokemon.Lat,
				Lon:        pokemon.Lon,
			}.Build()
			/* TODO: Add more fields to PokemonDetails */

			results = append(results, apiPokemon)
		}
	}

	log.Infof("GetPokemonInAreaV2 - result buffer time %s, %d added", time.Since(start), len(results))

	return results
}
