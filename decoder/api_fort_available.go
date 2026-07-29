package decoder

import (
	log "github.com/sirupsen/logrus"
)

// ApiAvailableForts is the whole-instance availability snapshot for every fort
// type, served by GET /api/fort/available. Each section is read from its own
// maintained index (see decoder/fort_availability.go).
type ApiAvailableForts struct {
	Pokestops *ApiAvailablePokestops `json:"pokestops" doc:"Pokestop availability (same shape as /api/pokestop/available)"`
	Gyms      *ApiAvailableGyms      `json:"gyms" doc:"Gym availability (same shape as /api/gym/available)"`
	Stations  *ApiAvailableStations  `json:"stations" doc:"Station availability (same shape as /api/station/available)"`
}

// GetAvailableForts assembles all three availability sections from the
// maintained indexes — no fortLookupCache scan. It calls the internal
// build/read helpers directly (not the public GetAvailable{Pokestops,Gyms,
// Stations} wrappers), so it emits exactly one combined Info line instead of
// one per section plus its own.
func GetAvailableForts(now int64) *ApiAvailableForts {
	res := &ApiAvailableForts{
		Pokestops: buildAvailablePokestops(now),
		Gyms:      &ApiAvailableGyms{Raids: readRaids(now)},
		Stations:  &ApiAvailableStations{Battles: readBattles(now)},
	}
	log.Infof("available-forts: %d quest, %d raid, %d lure, %d invasion, %d showcase, %d battle options (maintained)",
		len(res.Pokestops.Quests), len(res.Gyms.Raids), len(res.Pokestops.Lures),
		len(res.Pokestops.Invasions), len(res.Pokestops.Showcases), len(res.Stations.Battles))
	return res
}
