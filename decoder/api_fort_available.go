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

// GetAvailableForts builds the pokestop availability aggregate from a single
// fortLookupCache range. Gyms and stations are read from their maintained
// indexes (see decoder/fort_availability.go) instead of being scanned here;
// Tasks 3-4 migrate pokestops onto the same maintained-index pattern and drop
// this scan entirely.
func GetAvailableForts(now int64) *ApiAvailableForts {
	start := time.Now()
	p := newPokestopAvailAcc()
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType == POKESTOP {
			p.ingest(&fl, now)
		}
		return true
	})
	// result() is pure — the combined builder emits ONE log line, not one per
	// per-type finalizer.
	res := &ApiAvailableForts{
		Pokestops: p.result(),
		Gyms:      GetAvailableGyms(now),
		Stations:  GetAvailableStations(now),
	}
	verifyQuestAggregate(p.rewards) // same pokestop cross-check the per-type build runs
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-forts", time.Since(start).Seconds())
	}
	log.Infof("available-forts built in %s: one pass over %d pokestops (gyms/stations maintained)",
		time.Since(start), p.forts)
	return res
}
