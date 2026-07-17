package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiGymTeamAvailable is one distinct (team, available-slots) pair present on
// resident gyms, with how many gyms carry it. ReactMap derives its t/g keys.
type ApiGymTeamAvailable struct {
	TeamId         int8 `json:"team_id" doc:"Controlling team id (0 = uncontested)"`
	AvailableSlots int8 `json:"available_slots" doc:"Open defender slots"`
	Count          int  `json:"count" doc:"Number of resident gyms with this team/slots"`
}

// ApiGymRaidAvailable is one distinct active raid option on resident gyms.
// PokemonId 0 means an egg (no boss yet). ReactMap derives its e/r/boss keys.
type ApiGymRaidAvailable struct {
	RaidLevel int8  `json:"raid_level" doc:"Raid level/tier"`
	PokemonId int16 `json:"pokemon_id" doc:"Raid boss pokemon id; 0 = egg (unhatched)"`
	Form      int16 `json:"form" doc:"Raid boss form id, else 0"`
	Count     int   `json:"count" doc:"Number of resident gyms with this raid option"`
}

// ApiAvailableGyms is the whole-instance gym filter snapshot served by
// GET /api/gym/available.
type ApiAvailableGyms struct {
	Teams []ApiGymTeamAvailable `json:"teams" doc:"Distinct team + available-slot pairs on resident gyms"`
	Raids []ApiGymRaidAvailable `json:"raids" doc:"Distinct active raid levels/bosses/eggs on resident gyms"`
}

// GetAvailableGyms builds the gym filter snapshot from a single fortLookupCache
// range over resident gyms — no maintained map (FortLookup carries every gym
// filter field). Teams are all-resident (no time filter); raids require an
// unexpired raid with level > 0.
// gymAvailAcc accumulates the gym availability aggregate; ingest assumes the
// fort is a GYM. Shared by the per-type and combined builders.
type gymAvailAcc struct {
	teams map[ApiGymTeamAvailable]int
	raids map[ApiGymRaidAvailable]int
	forts int
}

func newGymAvailAcc() *gymAvailAcc {
	return &gymAvailAcc{teams: map[ApiGymTeamAvailable]int{}, raids: map[ApiGymRaidAvailable]int{}}
}

func (a *gymAvailAcc) ingest(fl *FortLookup, now int64) {
	a.forts++
	a.teams[ApiGymTeamAvailable{TeamId: fl.TeamId, AvailableSlots: fl.AvailableSlots}]++
	if fl.RaidLevel > 0 && fl.RaidEndTimestamp > now {
		a.raids[ApiGymRaidAvailable{RaidLevel: fl.RaidLevel, PokemonId: fl.RaidPokemonId, Form: fl.RaidPokemonForm}]++
	}
}

func (a *gymAvailAcc) result(start time.Time) *ApiAvailableGyms {
	res := &ApiAvailableGyms{Teams: []ApiGymTeamAvailable{}, Raids: []ApiGymRaidAvailable{}}
	for k, n := range a.teams {
		k.Count = n
		res.Teams = append(res.Teams, k)
	}
	for k, n := range a.raids {
		k.Count = n
		res.Raids = append(res.Raids, k)
	}
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-gyms", time.Since(start).Seconds())
	}
	log.Infof("available-gyms built in %s: scanned %d gyms -> %d team/slot, %d raid options",
		time.Since(start), a.forts, len(res.Teams), len(res.Raids))
	return res
}

func GetAvailableGyms(now int64) *ApiAvailableGyms {
	start := time.Now()
	acc := newGymAvailAcc()
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType == GYM {
			acc.ingest(&fl, now)
		}
		return true
	})
	return acc.result(start)
}
