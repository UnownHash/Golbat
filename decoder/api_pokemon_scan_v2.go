package decoder

import (
	"golbat/geo"
)

type ApiPokemonScan2 struct {
	Min        ApiLatLon             `json:"min" doc:"Lower-left (minimum lat/lon) corner of the bounding box to scan."`
	Max        ApiLatLon             `json:"max" doc:"Upper-right (maximum lat/lon) corner of the bounding box to scan."`
	Limit      int                   `json:"limit" required:"false" doc:"Maximum number of results to return; 0 uses the server default."`
	DnfFilters []ApiPokemonDnfFilter `json:"filters" required:"false" doc:"List of filter clauses OR'd together; a pokemon matches if it satisfies any one clause."`
}

func (r ApiPokemonScan2) GetMin() geo.Location {
	return r.Min.Location()
}

func (r ApiPokemonScan2) GetMax() geo.Location {
	return r.Max.Location()
}

func (r ApiPokemonScan2) GetLimit() int {
	return r.Limit
}

type ApiPokemonDnfFilter struct {
	Pokemon []ApiPokemonDnfId    `json:"pokemon" required:"false" doc:"Pokemon/form ids this clause applies to; empty matches any pokemon. All other conditions in the clause are AND'd together."`
	Iv      *ApiPokemonDnfMinMax `json:"iv" required:"false" doc:"Inclusive IV percentage range; null means no IV constraint."`
	AtkIv   *ApiPokemonDnfMinMax `json:"atk_iv" required:"false" doc:"Inclusive attack IV range; null means no attack IV constraint."`
	DefIv   *ApiPokemonDnfMinMax `json:"def_iv" required:"false" doc:"Inclusive defense IV range; null means no defense IV constraint."`
	StaIv   *ApiPokemonDnfMinMax `json:"sta_iv" required:"false" doc:"Inclusive stamina IV range; null means no stamina IV constraint."`
	Level   *ApiPokemonDnfMinMax `json:"level" required:"false" doc:"Inclusive level range; null means no level constraint."`
	Cp      *ApiPokemonDnfMinMax `json:"cp" required:"false" doc:"Inclusive CP range; null means no CP constraint."`
	Gender  *ApiPokemonDnfMinMax `json:"gender" required:"false" doc:"Inclusive gender value range; null means no gender constraint."`
	Size    *ApiPokemonDnfMinMax `json:"size" required:"false" doc:"Inclusive size range; null means no size constraint."`
	Little  *ApiPokemonDnfMinMax `json:"pvp_little" required:"false" doc:"Inclusive Little League PVP rank range; null means no Little League constraint."`
	Great   *ApiPokemonDnfMinMax `json:"pvp_great" required:"false" doc:"Inclusive Great League PVP rank range; null means no Great League constraint."`
	Ultra   *ApiPokemonDnfMinMax `json:"pvp_ultra" required:"false" doc:"Inclusive Ultra League PVP rank range; null means no Ultra League constraint."`
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
		if filter.Iv != nil && (int16(pokemonLookup.Iv) < filter.Iv.Min || int16(pokemonLookup.Iv) > filter.Iv.Max) ||
			filter.StaIv != nil && (int16(pokemonLookup.Sta) < filter.StaIv.Min || int16(pokemonLookup.Sta) > filter.StaIv.Max) ||
			filter.AtkIv != nil && (int16(pokemonLookup.Atk) < filter.AtkIv.Min || int16(pokemonLookup.Atk) > filter.AtkIv.Max) ||
			filter.DefIv != nil && (int16(pokemonLookup.Def) < filter.DefIv.Min || int16(pokemonLookup.Def) > filter.DefIv.Max) ||
			filter.Level != nil && (int16(pokemonLookup.Level) < filter.Level.Min || int16(pokemonLookup.Level) > filter.Level.Max) ||
			filter.Cp != nil && (pokemonLookup.Cp < filter.Cp.Min || pokemonLookup.Cp > filter.Cp.Max) ||
			filter.Gender != nil && (int16(pokemonLookup.Gender) < filter.Gender.Min || int16(pokemonLookup.Gender) > filter.Gender.Max) ||
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

	return internalGetPokemonInArea[ApiPokemonDnfFilter](retrieveParameters, dnfFilters, isPokemonDnfMatch)
}
