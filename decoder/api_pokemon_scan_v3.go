package decoder

import (
	"time"

	"golbat/geo"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonScan3 struct {
	Min        ApiLatLon              `json:"min" doc:"Lower-left (minimum lat/lon) corner of the bounding box to scan."`
	Max        ApiLatLon              `json:"max" doc:"Upper-right (maximum lat/lon) corner of the bounding box to scan."`
	Limit      int                    `json:"limit" required:"false" doc:"Maximum number of results to return; 0 uses the server default."`
	DnfFilters []ApiPokemonDnfFilter3 `json:"filters" required:"false" doc:"List of filter clauses OR'd together; a pokemon matches if it satisfies any one clause."`
}

func (r ApiPokemonScan3) GetMin() geo.Location {
	return r.Min.Location()
}

func (r ApiPokemonScan3) GetMax() geo.Location {
	return r.Max.Location()
}

func (r ApiPokemonScan3) GetLimit() int {
	return r.Limit
}

type ApiPokemonDnfFilter3 struct {
	Pokemon []ApiPokemonDnfId    `json:"pokemon" required:"false" doc:"Pokemon/form ids this clause applies to; empty matches any pokemon. All other conditions in the clause are AND'd together."`
	Iv      *ApiPokemonDnfMinMax `json:"iv" required:"false" doc:"Inclusive IV percentage range; null means no IV constraint."`
	AtkIv   *ApiPokemonDnfMinMax `json:"atk_iv" required:"false" doc:"Inclusive attack IV range; null means no attack IV constraint."`
	DefIv   *ApiPokemonDnfMinMax `json:"def_iv" required:"false" doc:"Inclusive defense IV range; null means no defense IV constraint."`
	StaIv   *ApiPokemonDnfMinMax `json:"sta_iv" required:"false" doc:"Inclusive stamina IV range; null means no stamina IV constraint."`
	Level   *ApiPokemonDnfMinMax `json:"level" required:"false" doc:"Inclusive level range; null means no level constraint."`
	Cp      *ApiPokemonDnfMinMax `json:"cp" required:"false" doc:"Inclusive CP range; null means no CP constraint."`
	Gender  []int8               `json:"gender" required:"false" doc:"Explicit list of allowed gender values (unlike v2 which uses a min/max range); empty means no gender constraint."`
	Size    *ApiPokemonDnfMinMax `json:"size" required:"false" doc:"Inclusive size range; null means no size constraint."`
	Little  *ApiPokemonDnfMinMax `json:"pvp_little" required:"false" doc:"Inclusive Little League PVP rank range; null means no Little League constraint."`
	Great   *ApiPokemonDnfMinMax `json:"pvp_great" required:"false" doc:"Inclusive Great League PVP rank range; null means no Great League constraint."`
	Ultra   *ApiPokemonDnfMinMax `json:"pvp_ultra" required:"false" doc:"Inclusive Ultra League PVP rank range; null means no Ultra League constraint."`
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
		if filter.Iv != nil && (int16(pokemonLookup.Iv) < filter.Iv.Min || int16(pokemonLookup.Iv) > filter.Iv.Max) ||
			filter.StaIv != nil && (int16(pokemonLookup.Sta) < filter.StaIv.Min || int16(pokemonLookup.Sta) > filter.StaIv.Max) ||
			filter.AtkIv != nil && (int16(pokemonLookup.Atk) < filter.AtkIv.Min || int16(pokemonLookup.Atk) > filter.AtkIv.Max) ||
			filter.DefIv != nil && (int16(pokemonLookup.Def) < filter.DefIv.Min || int16(pokemonLookup.Def) > filter.DefIv.Max) ||
			filter.Level != nil && (int16(pokemonLookup.Level) < filter.Level.Min || int16(pokemonLookup.Level) > filter.Level.Max) ||
			filter.Cp != nil && (pokemonLookup.Cp < filter.Cp.Min || pokemonLookup.Cp > filter.Cp.Max) ||
			(len(filter.Gender) > 0 && !contains(filter.Gender, pokemonLookup.Gender)) ||
			filter.Size != nil && (int16(pokemonLookup.Size) < filter.Size.Min || int16(pokemonLookup.Size) > filter.Size.Max) {
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

func GrpcGetPokemonInArea3(retrieveParameters *pb.PokemonScanRequestV3) ([]*pb.PokemonDetails, int, int, int) {
	// Build consistent api request

	apiRequest := ApiPokemonScan3{
		Min: ApiLatLon{
			Lat: float64(retrieveParameters.MinLat),
			Lon: float64(retrieveParameters.MinLon),
		},
		Max: ApiLatLon{
			Lat: float64(retrieveParameters.MaxLat),
			Lon: float64(retrieveParameters.MaxLon),
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
			Iv:    convertToMinMax(filter.Iv),
			AtkIv: convertToMinMax(filter.AtkIv),
			DefIv: convertToMinMax(filter.DefIv),
			StaIv: convertToMinMax(filter.StaIv),
			Level: convertToMinMax(filter.Level),
			Cp:    convertToMinMax(filter.Cp),
			Size:  convertToMinMax(filter.Size),
			Gender: func() []int8 {
				var genders []int8
				for _, gender := range filter.Gender {
					genders = append(genders, int8(gender))
				}
				return genders
			}(),
			Little: convertToMinMax(filter.PvpLittleRanking),
			Great:  convertToMinMax(filter.PvpGreatRanking),
			Ultra:  convertToMinMax(filter.PvpUltraRanking),
		}

		dnfFilters = append(dnfFilters, dnfFilter)
	}
	apiRequest.DnfFilters = dnfFilters

	returnKeys, examined, skipped, total := internalGetPokemonInArea3(apiRequest)
	results := make([]*pb.PokemonDetails, 0, len(returnKeys))

	start := time.Now()
	startUnix := start.Unix()

	for _, key := range returnKeys {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(key, "API.ScanPokemon.v3.pokemon")
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

	log.Infof("GetPokemonInAreaV3 - result buffer time %s, %d added", time.Since(start), len(results))

	return results, examined, skipped, total
}
