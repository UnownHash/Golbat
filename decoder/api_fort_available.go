package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiAvailableForts is the whole-instance availability snapshot for every fort
// type, served by GET /api/fort/available. Each section is read from its own
// maintained index (see decoder/fort_availability.go) — no fortLookupCache scan.
type ApiAvailableForts struct {
	Pokestops *ApiAvailablePokestops `json:"pokestops" doc:"Pokestop availability (same shape as /api/pokestop/available)"`
	Gyms      *ApiAvailableGyms      `json:"gyms" doc:"Gym availability (same shape as /api/gym/available)"`
	Stations  *ApiAvailableStations  `json:"stations" doc:"Station availability (same shape as /api/station/available)"`
}

// GetAvailableForts assembles all three availability sections from the
// maintained indexes — no fortLookupCache scan.
func GetAvailableForts(now int64) *ApiAvailableForts {
	start := time.Now()
	res := &ApiAvailableForts{
		Pokestops: GetAvailablePokestops(now),
		Gyms:      GetAvailableGyms(now),
		Stations:  GetAvailableStations(now),
	}
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-forts", time.Since(start).Seconds())
	}
	log.Infof("available-forts built in %s (pokestops/gyms/stations maintained)", time.Since(start))
	return res
}
