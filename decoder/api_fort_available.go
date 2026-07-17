package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiAvailableForts is the whole-instance availability snapshot for every fort
// type, served by GET /api/fort/available. One fortLookupCache range produces
// all three sections — on large instances this replaces three full-cache
// walks (one per per-type endpoint) with one.
type ApiAvailableForts struct {
	Pokestops *ApiAvailablePokestops `json:"pokestops" doc:"Pokestop availability (same shape as /api/pokestop/available)"`
	Gyms      *ApiAvailableGyms      `json:"gyms" doc:"Gym availability (same shape as /api/gym/available)"`
	Stations  *ApiAvailableStations  `json:"stations" doc:"Station availability (same shape as /api/station/available)"`
}

// GetAvailableForts builds all three availability aggregates in a single
// fortLookupCache range, dispatching each fort to its type's accumulator.
func GetAvailableForts(now int64) *ApiAvailableForts {
	start := time.Now()
	g, p, s := newGymAvailAcc(), newPokestopAvailAcc(), newStationAvailAcc()
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		switch fl.FortType {
		case GYM:
			g.ingest(&fl, now)
		case POKESTOP:
			p.ingest(&fl, now)
		case STATION:
			s.ingest(&fl, now)
		}
		return true
	})
	// result() is pure — the combined builder emits ONE log line, not one per
	// per-type finalizer.
	res := &ApiAvailableForts{
		Pokestops: p.result(),
		Gyms:      g.result(),
		Stations:  s.result(),
	}
	verifyQuestAggregate(p.rewards) // same pokestop cross-check the per-type build runs
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-forts", time.Since(start).Seconds())
	}
	log.Infof("available-forts built in %s: one pass over %d gyms / %d pokestops / %d stations",
		time.Since(start), g.forts, p.forts, s.forts)
	return res
}
