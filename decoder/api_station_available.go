package decoder

import (
	log "github.com/sirupsen/logrus"
)

// ApiStationBattleAvailable is one distinct active (battle_level, pokemon, form)
// option on resident stations. ReactMap derives its <id>-<form> and j<level> keys.
type ApiStationBattleAvailable struct {
	BattleLevel int8   `json:"battle_level" doc:"Max battle level"`
	PokemonId   *int16 `json:"pokemon_id" doc:"Battle pokemon id; null when the boss is not yet known"`
	Form        *int16 `json:"form" doc:"Battle pokemon form id (0 is a valid form); null when the boss is not yet known"`
}

// ApiAvailableStations is the whole-instance station filter snapshot served by
// GET /api/station/available.
type ApiAvailableStations struct {
	Battles []ApiStationBattleAvailable `json:"battles" doc:"Distinct active battle level/pokemon options on resident stations"`
}

// GetAvailableStations reads the maintained battle index (no fort scan).
func GetAvailableStations(now int64) *ApiAvailableStations {
	res := &ApiAvailableStations{Battles: readBattles(now)}
	log.Infof("available-stations: %d battle options (maintained)", len(res.Battles))
	return res
}
