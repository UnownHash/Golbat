package decoder

import (
	"time"

	"golbat/geo"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonScan2 struct {
	Min        geo.Location          `json:"min" doc:"Lower-left (minimum lat/lon) corner of the bounding box to scan."`
	Max        geo.Location          `json:"max" doc:"Upper-right (maximum lat/lon) corner of the bounding box to scan."`
	Limit      int                   `json:"limit" required:"false" doc:"Maximum number of results to return; 0 uses the server default."`
	DnfFilters []ApiPokemonDnfFilter `json:"filters" required:"false" doc:"List of filter clauses OR'd together; a pokemon matches if it satisfies any one clause."`
}

func (r ApiPokemonScan2) GetMin() geo.Location {
	return r.Min
}

func (r ApiPokemonScan2) GetMax() geo.Location {
	return r.Max
}

func (r ApiPokemonScan2) GetLimit() int {
	return r.Limit
}

type ApiPokemonDnfFilter struct {
	Pokemon []ApiPokemonDnfId     `json:"pokemon" required:"false" doc:"Pokemon/form ids this clause applies to; empty matches any pokemon. All other conditions in the clause are AND'd together."`
	Iv      *ApiPokemonDnfMinMax8 `json:"iv" required:"false" doc:"Inclusive IV percentage range; null means no IV constraint."`
	AtkIv   *ApiPokemonDnfMinMax8 `json:"atk_iv" required:"false" doc:"Inclusive attack IV range; null means no attack IV constraint."`
	DefIv   *ApiPokemonDnfMinMax8 `json:"def_iv" required:"false" doc:"Inclusive defense IV range; null means no defense IV constraint."`
	StaIv   *ApiPokemonDnfMinMax8 `json:"sta_iv" required:"false" doc:"Inclusive stamina IV range; null means no stamina IV constraint."`
	Level   *ApiPokemonDnfMinMax8 `json:"level" required:"false" doc:"Inclusive level range; null means no level constraint."`
	Cp      *ApiPokemonDnfMinMax  `json:"cp" required:"false" doc:"Inclusive CP range; null means no CP constraint."`
	Gender  *ApiPokemonDnfMinMax8 `json:"gender" required:"false" doc:"Inclusive gender value range; null means no gender constraint."`
	Size    *ApiPokemonDnfMinMax8 `json:"size" required:"false" doc:"Inclusive size range; null means no size constraint."`
	Little  *ApiPokemonDnfMinMax  `json:"pvp_little" required:"false" doc:"Inclusive Little League PVP rank range; null means no Little League constraint."`
	Great   *ApiPokemonDnfMinMax  `json:"pvp_great" required:"false" doc:"Inclusive Great League PVP rank range; null means no Great League constraint."`
	Ultra   *ApiPokemonDnfMinMax  `json:"pvp_ultra" required:"false" doc:"Inclusive Ultra League PVP rank range; null means no Ultra League constraint."`
}

func internalGetPokemonInArea2(retrieveParameters ApiPokemonScan2) ([]uint64, int, int, int) {
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

	return internalGetPokemonInArea[ApiPokemonDnfFilter](retrieveParameters, dnfFilters, isPokemonDnfMatch)
}

func GrpcGetPokemonInArea2(retrieveParameters *pb.PokemonScanRequest) []*pb.PokemonDetails {
	// Build consistent api request

	apiRequest := ApiPokemonScan2{
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
	var dnfFilters []ApiPokemonDnfFilter

	for _, filter := range retrieveParameters.Filters {
		dnfFilter := ApiPokemonDnfFilter{
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
			Iv:     convertToMinMax8(filter.Iv),
			AtkIv:  convertToMinMax8(filter.AtkIv),
			DefIv:  convertToMinMax8(filter.DefIv),
			StaIv:  convertToMinMax8(filter.StaIv),
			Level:  convertToMinMax8(filter.Level),
			Cp:     convertToMinMax16(filter.Cp),
			Size:   convertToMinMax8(filter.Size),
			Gender: convertToMinMax8(filter.Gender),
			Little: convertToMinMax16(filter.PvpLittleRanking),
			Great:  convertToMinMax16(filter.PvpGreatRanking),
			Ultra:  convertToMinMax16(filter.PvpUltraRanking),
		}

		dnfFilters = append(dnfFilters, dnfFilter)
	}
	apiRequest.DnfFilters = dnfFilters

	returnKeys, _, _, _ := internalGetPokemonInArea2(apiRequest)
	results := make([]*pb.PokemonDetails, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(key, "API.ScanPokemon.v2.pokemon")
		if pokemon != nil {
			if pokemon.ExpireTimestamp.ValueOrZero() > startUnix {
				apiPokemon := pb.PokemonDetails{
					Id:         uint64(pokemon.Id),
					PokestopId: pokemon.PokestopId.Ptr(),
					SpawnId:    pokemon.SpawnId.Ptr(),
					Lat:        pokemon.Lat,
					Lon:        pokemon.Lon,
				}
				results = append(results, &apiPokemon)
			}

			unlock()
		}
	}

	log.Infof("GetPokemonInAreaV2 - result buffer time %s, %d added", time.Since(start), len(results))

	return results
}
