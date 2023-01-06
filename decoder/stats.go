package decoder

import (
	log "github.com/sirupsen/logrus"
	"sync"
)

type areaStatsCount struct {
	tthBucket [12]int
	monsSeen  int
	monsIv    int
}

var pokemonStats = make(map[areaName]areaStatsCount)
var pokemonStatsLock sync.Mutex

func updatePokemonStats(old *Pokemon, new *Pokemon) {
	_ = old
	areas := matchGeofences(new.Lat, new.Lon)
	if len(areas) > 0 {
		pokemonStatsLock.Lock()
		defer pokemonStatsLock.Unlock()
		for i := 0; i < len(areas); i++ {
			area := areas[i]

			bucket := int64(-1)
			monsIvIncr := 0
			monsSeenIncr := 0

			if new.Cp.Valid && // an encounter has happened
				(old == nil || // this is first create
					!old.Cp.Valid && new.Cp.Valid) { // update is setting encounter details
				if new.ExpireTimestampVerified {
					tth := new.ExpireTimestamp.ValueOrZero() - new.Updated.ValueOrZero() // relies on Updated being set
					bucket = tth / (5 * 60)
					if bucket > 11 {
						bucket = 11
					}
					//areaStats.tthBucket[bucket]++
				}
				monsIvIncr++
			}

			if old == nil { // record being created
				monsSeenIncr++
			}

			// Update record if we have a new stat
			if monsSeenIncr > 0 || monsIvIncr > 0 || bucket >= 0 {
				areaStats := pokemonStats[area]
				if bucket >= 0 {
					areaStats.tthBucket[bucket]++
				}
				areaStats.monsIv += monsIvIncr
				areaStats.monsSeen += monsSeenIncr
				pokemonStats[area] = areaStats
			}
		}
	}
}

func logPokemonStats() {
	pokemonStatsLock.Lock()
	defer pokemonStatsLock.Unlock()
	log.Infof("---STATS---")
	for area, stats := range pokemonStats {
		log.Infof("STATS Pokemon stats for %+v %+v", area, stats)
	}
	pokemonStats = make(map[areaName]areaStatsCount) // clear stats
}
