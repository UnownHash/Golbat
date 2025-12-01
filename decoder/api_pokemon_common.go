package decoder

import (
	"math"
	"strconv"
	"time"

	"golbat/config"
	"golbat/geo"
	pb "golbat/grpc"

	"github.com/UnownHash/gohbem"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

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

type ApiPokemonResult struct {
	Id                      string      `json:"id"`
	PokestopId              null.String `json:"pokestop_id"`
	SpawnId                 null.Int    `json:"spawn_id"`
	Lat                     float64     `json:"lat"`
	Lon                     float64     `json:"lon"`
	Weight                  null.Float  `json:"weight"`
	Size                    null.Int    `json:"size"`
	Height                  null.Float  `json:"height"`
	ExpireTimestamp         null.Int    `json:"expire_timestamp"`
	Updated                 null.Int    `json:"updated"`
	PokemonId               int16       `json:"pokemon_id"`
	Move1                   null.Int    `json:"move_1"`
	Move2                   null.Int    `json:"move_2"`
	Gender                  null.Int    `json:"gender"`
	Cp                      null.Int    `json:"cp"`
	AtkIv                   null.Int    `json:"atk_iv"`
	DefIv                   null.Int    `json:"def_iv"`
	StaIv                   null.Int    `json:"sta_iv"`
	Iv                      null.Float  `json:"iv"`
	Form                    null.Int    `json:"form"`
	Level                   null.Int    `json:"level"`
	Weather                 null.Int    `json:"weather"`
	Costume                 null.Int    `json:"costume"`
	FirstSeenTimestamp      int64       `json:"first_seen_timestamp"`
	Changed                 int64       `json:"changed"`
	CellId                  null.Int    `json:"cell_id"`
	ExpireTimestampVerified bool        `json:"expire_timestamp_verified"`
	DisplayPokemonId        null.Int    `json:"display_pokemon_id"`
	IsDitto                 bool        `json:"is_ditto"`
	SeenType                null.String `json:"seen_type"`
	Shiny                   null.Bool   `json:"shiny"`
	Username                null.String `json:"username"`
	Capture1                null.Float  `json:"capture_1"`
	Capture2                null.Float  `json:"capture_2"`
	Capture3                null.Float  `json:"capture_3"`
	Pvp                     interface{} `json:"pvp"`
	IsEvent                 int8        `json:"is_event"`
}

func buildApiPokemonResult(pokemon *Pokemon) ApiPokemonResult {
	return ApiPokemonResult{
		Id:                      strconv.FormatUint(pokemon.Id, 10),
		PokestopId:              pokemon.PokestopId,
		SpawnId:                 pokemon.SpawnId,
		Lat:                     pokemon.Lat,
		Lon:                     pokemon.Lon,
		Weight:                  pokemon.Weight,
		Size:                    pokemon.Size,
		Height:                  pokemon.Height,
		ExpireTimestamp:         pokemon.ExpireTimestamp,
		Updated:                 pokemon.Updated,
		PokemonId:               pokemon.PokemonId,
		Move1:                   pokemon.Move1,
		Move2:                   pokemon.Move2,
		Gender:                  pokemon.Gender,
		Cp:                      pokemon.Cp,
		AtkIv:                   pokemon.AtkIv,
		DefIv:                   pokemon.DefIv,
		StaIv:                   pokemon.StaIv,
		Iv:                      pokemon.Iv,
		Form:                    pokemon.Form,
		Level:                   pokemon.Level,
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
}

func contains(s []int8, e int8) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func convertToMinMax8(minmax *pb.RangeMinMax) *ApiPokemonDnfMinMax8 {
	if minmax == nil {
		return nil
	}
	var minV int8 = 0
	var maxV int8 = math.MaxInt8
	if minmax.Min != nil {
		minV = int8(*minmax.Min)
	}
	if minmax.Max != nil {
		maxV = int8(*minmax.Max)
	}

	return &ApiPokemonDnfMinMax8{
		Min: minV,
		Max: maxV,
	}
}

func convertToMinMax16(minmax *pb.RangeMinMax) *ApiPokemonDnfMinMax {
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

	pokemonTreeMutex.RLock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()

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
