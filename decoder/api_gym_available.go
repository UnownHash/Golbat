package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

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
// ApiAvailableGyms is the whole-instance gym filter snapshot. Only raids are
// dynamic — team/slot filter keys are generated statically by the consumer
// (every team/slot combination exists on a live instance), so they are not
// aggregated here.
type ApiAvailableGyms struct {
	Raids []ApiGymRaidAvailable `json:"raids" doc:"Distinct active raid levels/bosses/eggs on resident gyms"`
}

// GetAvailableGyms builds the gym filter snapshot from a single fortLookupCache
// range over resident gyms — no maintained map (FortLookup carries every gym
// filter field). Teams are all-resident (no time filter); raids require an
// unexpired raid with level > 0.
// gymAvailAcc accumulates the gym availability aggregate; ingest assumes the
// fort is a GYM. Shared by the per-type and combined builders.
type gymAvailAcc struct {
	raids map[ApiGymRaidAvailable]int
	forts int
}

func newGymAvailAcc() *gymAvailAcc {
	return &gymAvailAcc{raids: map[ApiGymRaidAvailable]int{}}
}

func (a *gymAvailAcc) ingest(fl *FortLookup, now int64) {
	a.forts++
	if fl.RaidLevel > 0 && fl.RaidEndTimestamp > now {
		a.raids[ApiGymRaidAvailable{RaidLevel: fl.RaidLevel, PokemonId: fl.RaidPokemonId, Form: fl.RaidPokemonForm}]++
	}
}

// result is a pure finalizer — no logging (the caller owns the log line so the
// combined builder doesn't emit a spurious per-type "built" entry).
func (a *gymAvailAcc) result() *ApiAvailableGyms {
	res := &ApiAvailableGyms{Raids: []ApiGymRaidAvailable{}}
	for k, n := range a.raids {
		k.Count = n
		res.Raids = append(res.Raids, k)
	}
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
	res := acc.result()
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-gyms", time.Since(start).Seconds())
	}
	log.Infof("available-gyms built in %s: scanned %d gyms -> %d raid options",
		time.Since(start), acc.forts, len(res.Raids))
	return res
}
