package decoder

import (
	"time"

	"golbat/geo"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonScan3 struct {
	Min        geo.Location           `json:"min"`
	Max        geo.Location           `json:"max"`
	Limit      int                    `json:"limit"`
	DnfFilters []ApiPokemonDnfFilter3 `json:"filters"`
}

func (r ApiPokemonScan3) GetMin() geo.Location {
	return r.Min
}

func (r ApiPokemonScan3) GetMax() geo.Location {
	return r.Max
}

func (r ApiPokemonScan3) GetLimit() int {
	return r.Limit
}

type ApiPokemonDnfFilter3 struct {
	Pokemon []ApiPokemonDnfId     `json:"pokemon"`
	Iv      *ApiPokemonDnfMinMax8 `json:"iv"`
	AtkIv   *ApiPokemonDnfMinMax8 `json:"atk_iv"`
	DefIv   *ApiPokemonDnfMinMax8 `json:"def_iv"`
	StaIv   *ApiPokemonDnfMinMax8 `json:"sta_iv"`
	Level   *ApiPokemonDnfMinMax8 `json:"level"`
	Cp      *ApiPokemonDnfMinMax  `json:"cp"`
	Gender  []int8                `json:"gender"`
	Size    *ApiPokemonDnfMinMax8 `json:"size"`
	Little  *ApiPokemonDnfMinMax  `json:"pvp_little"`
	Great   *ApiPokemonDnfMinMax  `json:"pvp_great"`
	Ultra   *ApiPokemonDnfMinMax  `json:"pvp_ultra"`
}

type PokemonScan3Result struct {
	Pokemon  []*ApiPokemonResult `json:"pokemon"`
	Examined int                 `json:"examined"`
	Skipped  int                 `json:"skipped"`
	Total    int                 `json:"total"`
}

func internalGetPokemonInArea3(retrieveParameters ApiPokemonScan3) ([]uint64, int, int, int) {
	dnfFilters := make(map[dnfFilterLookup][]ApiPokemonDnfFilter3)

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

	isPokemonDnfMatch := func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter *ApiPokemonDnfFilter3) bool {
		if filter.Iv != nil && (pokemonLookup.Iv < filter.Iv.Min || pokemonLookup.Iv > filter.Iv.Max) ||
			filter.StaIv != nil && (pokemonLookup.Sta < filter.StaIv.Min || pokemonLookup.Sta > filter.StaIv.Max) ||
			filter.AtkIv != nil && (pokemonLookup.Atk < filter.AtkIv.Min || pokemonLookup.Atk > filter.AtkIv.Max) ||
			filter.DefIv != nil && (pokemonLookup.Def < filter.DefIv.Min || pokemonLookup.Def > filter.DefIv.Max) ||
			filter.Level != nil && (pokemonLookup.Level < filter.Level.Min || pokemonLookup.Level > filter.Level.Max) ||
			filter.Cp != nil && (pokemonLookup.Cp < filter.Cp.Min || pokemonLookup.Cp > filter.Cp.Max) ||
			(len(filter.Gender) > 0 && !contains(filter.Gender, pokemonLookup.Gender)) ||
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

	return internalGetPokemonInArea[ApiPokemonDnfFilter3](retrieveParameters, dnfFilters, isPokemonDnfMatch)
}

func GetPokemonInArea3(retrieveParameters ApiPokemonScan3) *PokemonScan3Result {
	returnKeys, examined, skipped, total := internalGetPokemonInArea3(retrieveParameters)
	results := make([]*ApiPokemonResult, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		if pokemonCacheEntry := getPokemonFromCache(key); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			if pokemon.ExpireTimestamp.ValueOrZero() < startUnix {
				examined--
				continue
			}

			apiPokemon := buildApiPokemonResult(&pokemon)

			results = append(results, &apiPokemon)
		}
	}

	log.Infof("GetPokemonInAreaV3 - result buffer time %s, %d added", time.Since(start), len(results))

	return &PokemonScan3Result{
		Pokemon:  results,
		Examined: examined,
		Skipped:  skipped,
		Total:    total,
	}
}

func GrpcGetPokemonInArea3(retrieveParameters *pb.PokemonScanRequestV3) ([]*pb.PokemonDetails, int, int, int) {
	// Build consistent api request

	apiRequest := ApiPokemonScan3{
		Min: geo.Location{
			Latitude:  float64(retrieveParameters.MinLat),
			Longitude: float64(retrieveParameters.MinLon),
		},
		Max: geo.Location{
			Latitude:  float64(retrieveParameters.MaxLat),
			Longitude: float64(retrieveParameters.MaxLon),
		},
		Limit: int(retrieveParameters.Limit),
	}
	var dnfFilters []ApiPokemonDnfFilter3

	for _, filter := range retrieveParameters.Filters {
		dnfFilter := ApiPokemonDnfFilter3{
			Pokemon: func() []ApiPokemonDnfId {
				var pokemonRes []ApiPokemonDnfId
				for _, pokemon := range filter.Pokemon {
					pokemonRes = append(pokemonRes, ApiPokemonDnfId{
						Pokemon: func() int16 {
							if pokemon.Id == nil {
								return 0
							}
							return int16(*pokemon.Id)
						}(),
						Form: func() *int16 {
							if pokemon.Form != nil {
								form := int16(*pokemon.Form)
								return &form
							}
							return nil
						}(),
					})
				}

				return pokemonRes
			}(),
			Iv:    convertToMinMax8(filter.Iv),
			AtkIv: convertToMinMax8(filter.AtkIv),
			DefIv: convertToMinMax8(filter.DefIv),
			StaIv: convertToMinMax8(filter.StaIv),
			Level: convertToMinMax8(filter.Level),
			Cp:    convertToMinMax16(filter.Cp),
			Size:  convertToMinMax8(filter.Size),
			Gender: func() []int8 {
				var genders []int8
				for _, gender := range filter.Gender {
					genders = append(genders, int8(gender))
				}
				return genders
			}(),
			Little: convertToMinMax16(filter.PvpLittleRanking),
			Great:  convertToMinMax16(filter.PvpGreatRanking),
			Ultra:  convertToMinMax16(filter.PvpUltraRanking),
		}

		dnfFilters = append(dnfFilters, dnfFilter)
	}
	apiRequest.DnfFilters = dnfFilters

	returnKeys, examined, skipped, total := internalGetPokemonInArea3(apiRequest)
	results := make([]*pb.PokemonDetails, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		if pokemonCacheEntry := getPokemonFromCache(key); pokemonCacheEntry != nil {
			pokemon := pokemonCacheEntry.Value()

			if pokemon.ExpireTimestamp.ValueOrZero() < startUnix {
				continue
			}

			apiPokemon := pb.PokemonDetails{
				Id:         pokemon.Id,
				PokestopId: pokemon.PokestopId.Ptr(),
				SpawnId:    pokemon.SpawnId.Ptr(),
				Lat:        pokemon.Lat,
				Lon:        pokemon.Lon,
			}

			results = append(results, &apiPokemon)
		}
	}

	log.Infof("GetPokemonInAreaV3 - result buffer time %s, %d added", time.Since(start), len(results))

	return results, examined, skipped, total
}
