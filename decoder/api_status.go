package decoder

import (
	"golbat/config"
)

// ApiStatusResult reports which optional Golbat features are enabled and the
// server's effective scan limits, so map consumers (e.g. Diadem) can detect
// capabilities and clamp their own request limits instead of probing gated
// endpoints. Contract consumers: ccev/diadem#174.
type ApiStatusResult struct {
	Features struct {
		FortInMemory bool `json:"fort_in_memory" doc:"Whether the in-memory fort index (and with it the /api/{gym,pokestop,station,fort}/scan and /available endpoints) is enabled. By-id and query endpoints do not depend on it: they read through to the database on a cache miss and work either way."`
	} `json:"features" doc:"Enabled optional features"`
	Limits struct {
		MaxPokemonResults int `json:"max_pokemon_results" doc:"Server cap on pokemon scan results per request (tuning.max_pokemon_results)"`
		MaxFortResults    int `json:"max_fort_results" doc:"Server cap on fort scan results per request (tuning.max_fort_results)"`
	} `json:"limits" doc:"Effective server-side result caps"`
}

func GetApiStatus() *ApiStatusResult {
	status := &ApiStatusResult{}
	status.Features.FortInMemory = config.Config.FortInMemory
	status.Limits.MaxPokemonResults = config.Config.Tuning.MaxPokemonResults
	status.Limits.MaxFortResults = config.Config.Tuning.MaxFortResults
	return status
}
