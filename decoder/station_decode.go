package decoder

import (
	"encoding/json"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/util"
)

func (station *Station) updateFromStationProto(stationProto *pogo.StationProto, cellId uint64) *Station {
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
	if battleDetails := stationProto.BattleDetails; battleDetails != nil {
		station.SetBattleLevel(null.IntFrom(int64(battleDetails.BattleLevel)))
		station.SetBattleStart(null.IntFrom(battleDetails.BattleWindowStartMs / 1000))
		station.SetBattleEnd(null.IntFrom(battleDetails.BattleWindowEndMs / 1000))
		if pokemon := battleDetails.BattlePokemon; pokemon != nil {
			station.SetBattlePokemonId(null.IntFrom(int64(pokemon.PokemonId)))
			station.SetBattlePokemonMove1(null.IntFrom(int64(pokemon.Move1)))
			station.SetBattlePokemonMove2(null.IntFrom(int64(pokemon.Move2)))
			station.SetBattlePokemonForm(null.IntFrom(int64(pokemon.PokemonDisplay.Form)))
			station.SetBattlePokemonCostume(null.IntFrom(int64(pokemon.PokemonDisplay.Costume)))
			station.SetBattlePokemonGender(null.IntFrom(int64(pokemon.PokemonDisplay.Gender)))
			station.SetBattlePokemonAlignment(null.IntFrom(int64(pokemon.PokemonDisplay.Alignment)))
			station.SetBattlePokemonBreadMode(null.IntFrom(int64(pokemon.PokemonDisplay.BreadModeEnum)))
			station.SetBattlePokemonStamina(null.IntFrom(int64(pokemon.Stamina)))
			station.SetBattlePokemonCpMultiplier(null.FloatFrom(float64(pokemon.CpMultiplier)))
			if rewardPokemon := battleDetails.RewardPokemon; rewardPokemon != nil && pokemon.PokemonId != rewardPokemon.PokemonId {
				log.Infof("[DYNAMAX] Pokemon reward differs from battle: Battle %v - Reward %v", pokemon, rewardPokemon)
			}
		}
	}
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
