package decoder

import (
	"time"

	"github.com/guregu/null/v6"
)

type ApiStationResult struct {
	Id                     string             `json:"id"`
	Lat                    float64            `json:"lat"`
	Lon                    float64            `json:"lon"`
	Name                   string             `json:"name"`
	StartTime              int64              `json:"start_time"`
	EndTime                int64              `json:"end_time"`
	IsBattleAvailable      bool               `json:"is_battle_available"`
	Updated                int64              `json:"updated"`
	BattleLevel            null.Int           `json:"battle_level"`
	BattleStart            null.Int           `json:"battle_start"`
	BattleEnd              null.Int           `json:"battle_end"`
	BattlePokemonId        null.Int           `json:"battle_pokemon_id"`
	BattlePokemonForm      null.Int           `json:"battle_pokemon_form"`
	BattlePokemonCostume   null.Int           `json:"battle_pokemon_costume"`
	BattlePokemonGender    null.Int           `json:"battle_pokemon_gender"`
	BattlePokemonAlignment null.Int           `json:"battle_pokemon_alignment"`
	BattlePokemonBreadMode null.Int           `json:"battle_pokemon_bread_mode"`
	BattlePokemonMove1     null.Int           `json:"battle_pokemon_move_1"`
	BattlePokemonMove2     null.Int           `json:"battle_pokemon_move_2"`
	TotalStationedPokemon  null.Int           `json:"total_stationed_pokemon"`
	TotalStationedGmax     null.Int           `json:"total_stationed_gmax"`
	StationedPokemon       null.String        `json:"stationed_pokemon"`
	Battles                []ApiStationBattle `json:"battles,omitempty"`
}

func BuildStationResult(station *Station) ApiStationResult {
	now := time.Now().Unix()
	snapshot := collectStationBattleSnapshot(station, now)
	_, hasBattleCache := stationBattleCache.Load(station.Id)

	result := ApiStationResult{
		Id:                    station.Id,
		Lat:                   station.Lat,
		Lon:                   station.Lon,
		Name:                  station.Name,
		StartTime:             station.StartTime,
		EndTime:               station.EndTime,
		IsBattleAvailable:     station.IsBattleAvailable,
		Updated:               station.Updated,
		TotalStationedPokemon: station.TotalStationedPokemon,
		TotalStationedGmax:    station.TotalStationedGmax,
		StationedPokemon:      station.StationedPokemon,
		Battles:               buildApiStationBattlesFromSlice(snapshot.Battles),
	}
	if snapshot.Canonical != nil {
		result.BattleLevel = null.IntFrom(int64(snapshot.Canonical.BattleLevel))
		result.BattleStart = null.IntFrom(snapshot.Canonical.BattleStart)
		result.BattleEnd = null.IntFrom(snapshot.Canonical.BattleEnd)
		result.BattlePokemonId = snapshot.Canonical.BattlePokemonId
		result.BattlePokemonForm = snapshot.Canonical.BattlePokemonForm
		result.BattlePokemonCostume = snapshot.Canonical.BattlePokemonCostume
		result.BattlePokemonGender = snapshot.Canonical.BattlePokemonGender
		result.BattlePokemonAlignment = snapshot.Canonical.BattlePokemonAlignment
		result.BattlePokemonBreadMode = snapshot.Canonical.BattlePokemonBreadMode
		result.BattlePokemonMove1 = snapshot.Canonical.BattlePokemonMove1
		result.BattlePokemonMove2 = snapshot.Canonical.BattlePokemonMove2
	} else if !hasBattleCache {
		result.BattleLevel = station.BattleLevel
		result.BattleStart = station.BattleStart
		result.BattleEnd = station.BattleEnd
		result.BattlePokemonId = station.BattlePokemonId
		result.BattlePokemonForm = station.BattlePokemonForm
		result.BattlePokemonCostume = station.BattlePokemonCostume
		result.BattlePokemonGender = station.BattlePokemonGender
		result.BattlePokemonAlignment = station.BattlePokemonAlignment
		result.BattlePokemonBreadMode = station.BattlePokemonBreadMode
		result.BattlePokemonMove1 = station.BattlePokemonMove1
		result.BattlePokemonMove2 = station.BattlePokemonMove2
	}
	return result
}
