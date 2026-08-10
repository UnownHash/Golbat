package decoder

import (
	"encoding/json"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/util"
)

func (station *Station) updateFromStationProto(stationProto *pogo.StationProto, cellId uint64) *Station {
	// [STATION-WINDOW-DEBUG] temporary: attribute duplicate max_battle webhooks.
	// station.StartTime/EndTime here are the PREVIOUS committed values (SetStartTime
	// below overwrites them), so this shows the raw proto window vs. what we held,
	// plus the single battle detail this proto carries. Correlate with the
	// [STATION-WEBHOOK-DEBUG] line for the same id to see whether a second window
	// comes from the proto or from something internal.
	protoStart := stationProto.StartTimeMs / 1000
	protoEnd := stationProto.EndTimeMs / 1000
	if bd := stationProto.BattleDetails; bd != nil {
		log.Infof("[STATION-WINDOW-DEBUG] %s protoWindow=%d-%d prevStationWindow=%d-%d battleDetail seed=%d level=%d bWindow=%d-%d",
			stationProto.Id, protoStart, protoEnd, station.StartTime, station.EndTime,
			bd.GetBreadBattleSeed(), bd.GetBattleLevel(), bd.GetBattleWindowStartMs()/1000, bd.GetBattleWindowEndMs()/1000)
	} else if station.StartTime != protoStart || station.EndTime != protoEnd {
		log.Infof("[STATION-WINDOW-DEBUG] %s protoWindow=%d-%d prevStationWindow=%d-%d (no battle detail, window CHANGED)",
			stationProto.Id, protoStart, protoEnd, station.StartTime, station.EndTime)
	}
	station.SetId(stationProto.Id)
	name := stationProto.Name
	// NOTE: Some names have more than 255 runes, which won't fit in our
	// varchar(255).
	if truncateStr, truncated := util.TruncateUTF8(stationProto.Name, 255); truncated {
		log.Debugf("truncating name for station id '%s'. Orig name: %s",
			stationProto.Id,
			stationProto.Name,
		)
		name = truncateStr
	}
	station.SetName(name)
	station.SetLat(stationProto.Lat)
	station.SetLon(stationProto.Lng)
	station.SetStartTime(stationProto.StartTimeMs / 1000)
	station.SetEndTime(stationProto.EndTimeMs / 1000)
	station.SetCooldownComplete(stationProto.CooldownCompleteMs)
	station.SetIsBattleAvailable(stationProto.IsBreadBattleAvailable)
	station.SetCellId(int64(cellId))
	return station
}

func (station *Station) updateFromGetStationedPokemonDetailsOutProto(stationProto *pogo.GetStationedPokemonDetailsOutProto) *Station {
	type stationedPokemonDetail struct {
		PokemonId             int    `json:"pokemon_id"`
		Form                  int    `json:"form"`
		Costume               int    `json:"costume"`
		Gender                int    `json:"gender"`
		Shiny                 bool   `json:"shiny,omitempty"`
		TempEvolution         int    `json:"temp_evolution,omitempty"`
		TempEvolutionFinishMs int64  `json:"temp_evolution_finish_ms,omitempty"`
		Alignment             int    `json:"alignment,omitempty"`
		Badge                 int    `json:"badge,omitempty"`
		Background            *int64 `json:"background,omitempty"`
		BreadMode             int    `json:"bread_mode"`
	}

	var stationedPokemon []stationedPokemonDetail
	stationedGmax := int64(0)
	for _, stationedPokemonDetails := range stationProto.StationedPokemons {
		pokemon := stationedPokemonDetails.Pokemon
		display := pokemon.PokemonDisplay
		stationedPokemon = append(stationedPokemon, stationedPokemonDetail{
			PokemonId:             int(pokemon.PokemonId),
			Form:                  int(display.Form),
			Costume:               int(display.Costume),
			Gender:                int(display.Gender),
			Shiny:                 display.Shiny,
			TempEvolution:         int(display.CurrentTempEvolution),
			TempEvolutionFinishMs: display.TemporaryEvolutionFinishMs,
			Alignment:             int(display.Alignment),
			Badge:                 int(display.PokemonBadge),
			Background:            util.ExtractBackgroundFromDisplay(display),
			BreadMode:             int(display.BreadModeEnum),
		})
		if display.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE || display.BreadModeEnum == pogo.BreadModeEnum_BREAD_DOUGH_MODE_2 {
			stationedGmax++
		}
	}
	jsonString, _ := json.Marshal(stationedPokemon)
	station.SetStationedPokemon(null.StringFrom(string(jsonString)))
	station.SetTotalStationedPokemon(null.IntFrom(int64(stationProto.TotalNumStationedPokemon)))
	station.SetTotalStationedGmax(null.IntFrom(stationedGmax))
	return station
}

func (station *Station) resetStationedPokemonFromStationDetailsNotFound() *Station {
	jsonString, _ := json.Marshal([]string{})
	station.SetStationedPokemon(null.StringFrom(string(jsonString)))
	station.SetTotalStationedPokemon(null.IntFrom(0))
	station.SetTotalStationedGmax(null.IntFrom(0))
	return station
}
