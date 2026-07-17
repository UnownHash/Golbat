package decoder

import (
	log "github.com/sirupsen/logrus"
)

// ApiGymRaidAvailable is one distinct active raid option on resident gyms.
// PokemonId 0 means an egg (no boss yet). ReactMap derives its e/r/boss keys.
type ApiGymRaidAvailable struct {
	RaidLevel int8  `json:"raid_level" doc:"Raid level/tier"`
	PokemonId int16 `json:"pokemon_id" doc:"Raid boss pokemon id; 0 = egg (unhatched)"`
	Form      int16 `json:"form" doc:"Raid boss form id, else 0"`
}

// ApiAvailableGyms is the whole-instance gym filter snapshot. Only raids are
// dynamic — team/slot filter keys are generated statically by the consumer, so
// they are not aggregated here.
type ApiAvailableGyms struct {
	Raids []ApiGymRaidAvailable `json:"raids" doc:"Distinct active raid levels/bosses/eggs on resident gyms"`
}

// GetAvailableGyms reads the maintained raid index (no fort scan).
func GetAvailableGyms(now int64) *ApiAvailableGyms {
	res := &ApiAvailableGyms{Raids: readRaids(now)}
	log.Infof("available-gyms: %d raid options (maintained)", len(res.Raids))
	return res
}
