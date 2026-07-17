package decoder

import (
	"time"
)

// ApiStationResult is the API representation of a station. Nullable database
// columns are represented as pointers (nil => JSON null) without omitempty so
// every key is always present.
type ApiStationResult struct {
	Id                        string                   `json:"id" doc:"Station ID"`
	Lat                       float64                  `json:"lat" doc:"Latitude of the station"`
	Lon                       float64                  `json:"lon" doc:"Longitude of the station"`
	Name                      string                   `json:"name" doc:"Name of the station"`
	CellId                    int64                    `json:"cell_id" doc:"S2 cell ID the station belongs to"`
	StartTime                 int64                    `json:"start_time" doc:"Unix timestamp when the station becomes active"`
	EndTime                   int64                    `json:"end_time" doc:"Unix timestamp when the station expires"`
	CooldownComplete          int64                    `json:"cooldown_complete" doc:"Unix timestamp when the station cooldown completes"`
	IsBattleAvailable         bool                     `json:"is_battle_available" doc:"Whether a battle is currently available at the station"`
	IsInactive                bool                     `json:"is_inactive" doc:"Whether the station is inactive"`
	Updated                   int64                    `json:"updated" doc:"Unix timestamp when the record was last updated"`
	BattleLevel               *int64                   `json:"battle_level" doc:"Level of the current (top) battle"`
	BattleStart               *int64                   `json:"battle_start" doc:"Unix timestamp when the current battle starts"`
	BattleEnd                 *int64                   `json:"battle_end" doc:"Unix timestamp when the current battle ends"`
	BattlePokemonId           *int64                   `json:"battle_pokemon_id" doc:"Pokedex ID of the battle pokemon"`
	BattlePokemonForm         *int64                   `json:"battle_pokemon_form" doc:"Form ID of the battle pokemon"`
	BattlePokemonCostume      *int64                   `json:"battle_pokemon_costume" doc:"Costume ID of the battle pokemon"`
	BattlePokemonGender       *int64                   `json:"battle_pokemon_gender" doc:"Gender of the battle pokemon"`
	BattlePokemonAlignment    *int64                   `json:"battle_pokemon_alignment" doc:"Alignment of the battle pokemon"`
	BattlePokemonBreadMode    *int64                   `json:"battle_pokemon_bread_mode" doc:"Bread mode of the battle pokemon"`
	BattlePokemonMove1        *int64                   `json:"battle_pokemon_move_1" doc:"First move ID of the battle pokemon"`
	BattlePokemonMove2        *int64                   `json:"battle_pokemon_move_2" doc:"Second move ID of the battle pokemon"`
	BattlePokemonStamina      *int64                   `json:"battle_pokemon_stamina" doc:"Stamina of the top battle pokemon"`
	BattlePokemonCpMultiplier *float64                 `json:"battle_pokemon_cp_multiplier" doc:"CP multiplier of the top battle pokemon"`
	TotalStationedPokemon     *int64                   `json:"total_stationed_pokemon" doc:"Total number of pokemon stationed"`
	TotalStationedGmax        *int64                   `json:"total_stationed_gmax" doc:"Total number of Gigantamax pokemon stationed"`
	StationedPokemon          *string                  `json:"stationed_pokemon" doc:"Serialized list of stationed pokemon"`
	Battles                   []ApiStationBattleResult `json:"battles,omitempty" doc:"Known battles at this station"`
}

// ApiStationBattleResult is one battle entry for a station.
type ApiStationBattleResult struct {
	BreadBattleSeed           int64    `json:"bread_battle_seed,omitempty" doc:"Bread battle seed"`
	BattleLevel               int16    `json:"battle_level" doc:"Battle level"`
	BattleStart               int64    `json:"battle_start" doc:"Unix timestamp when the battle starts"`
	BattleEnd                 int64    `json:"battle_end" doc:"Unix timestamp when the battle ends"`
	BattlePokemonId           *int64   `json:"battle_pokemon_id" doc:"Pokedex ID of the battle pokemon"`
	BattlePokemonForm         *int64   `json:"battle_pokemon_form" doc:"Form ID of the battle pokemon"`
	BattlePokemonCostume      *int64   `json:"battle_pokemon_costume" doc:"Costume ID of the battle pokemon"`
	BattlePokemonGender       *int64   `json:"battle_pokemon_gender" doc:"Gender of the battle pokemon"`
	BattlePokemonAlignment    *int64   `json:"battle_pokemon_alignment" doc:"Alignment of the battle pokemon"`
	BattlePokemonBreadMode    *int64   `json:"battle_pokemon_bread_mode" doc:"Bread mode of the battle pokemon"`
	BattlePokemonMove1        *int64   `json:"battle_pokemon_move_1" doc:"First move ID of the battle pokemon"`
	BattlePokemonMove2        *int64   `json:"battle_pokemon_move_2" doc:"Second move ID of the battle pokemon"`
	BattlePokemonStamina      *int64   `json:"battle_pokemon_stamina" doc:"Stamina of the battle pokemon"`
	BattlePokemonCpMultiplier *float64 `json:"battle_pokemon_cp_multiplier" doc:"CP multiplier of the battle pokemon"`
}

func BuildStationResult(station *Station) ApiStationResult {
	now := time.Now().Unix()
	battles := getKnownStationBattles(station.Id, now)

	result := ApiStationResult{
		Id:                    station.Id,
		Lat:                   station.Lat,
		Lon:                   station.Lon,
		Name:                  station.Name,
		CellId:                station.CellId,
		StartTime:             station.StartTime,
		EndTime:               station.EndTime,
		CooldownComplete:      station.CooldownComplete,
		IsBattleAvailable:     station.IsBattleAvailable,
		IsInactive:            station.IsInactive,
		Updated:               station.Updated,
		TotalStationedPokemon: station.TotalStationedPokemon.Ptr(),
		TotalStationedGmax:    station.TotalStationedGmax.Ptr(),
		StationedPokemon:      station.StationedPokemon.Ptr(),
		Battles:               buildApiStationBattleResults(battles),
	}
	applyTopStationBattleToApiStationResult(&result, battles)
	return result
}
