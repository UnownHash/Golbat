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
	Filters ApiStatusFilters `json:"filters" doc:"DNF filter fields this build supports beyond the base set. A consumer sends a filter only when its flag is true; a flag that is absent from the response is unsupported."`
}

// ApiStatusFilters advertises the optional DNF filter fields a build
// supports. Every field is true on the build that defines it; the point of
// the block is that a consumer talking to an older Golbat sees the key
// missing (or false) and does not send the filter. New filters get a new
// field here rather than a flag on an availability response.
type ApiStatusFilters struct {
	ShowcaseFocus   bool `json:"showcase_focus" doc:"contest_focus Buddy selectors are honoured by pokestop scans before the result cap"`
	BattleAvailable bool `json:"battle_available" doc:"battle_available (the station is_battle_available flag) is honoured by station scans"`
	UpdatedAfter    bool `json:"updated_after" doc:"updated_after (return only entities updated strictly after a unix time) is honoured by the pokemon v3 scan and every fort scan"`
}

func GetApiStatus() *ApiStatusResult {
	status := &ApiStatusResult{}
	status.Features.FortInMemory = config.Config.FortInMemory
	status.Limits.MaxPokemonResults = config.Config.Tuning.MaxPokemonResults
	status.Limits.MaxFortResults = config.Config.Tuning.MaxFortResults
	status.Filters = ApiStatusFilters{ShowcaseFocus: true, BattleAvailable: true, UpdatedAfter: true}
	return status
}
