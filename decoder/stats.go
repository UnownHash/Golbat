package decoder

import (
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"sync"
	"time"
)

type areaStatsCount struct {
	tthBucket             [12]int
	monsSeen              int
	verifiedEnc           int
	unverifiedEnc         int
	verifiedEncSecTotal   int64
	monsIv                int
	mons100Iv             int
	timeToEncounterCount  int
	timeToEncounterSum    int64
	statsResetCount       int
	verifiedReEncounter   int
	verifiedReEncSecTotal int64
}

type pokemonTimings struct {
	first_wild      int64
	first_encounter int64
}

var pokemonCount = make(map[areaName]*areaPokemonCountDetail)

type areaPokemonCountDetail struct {
	hundos  map[int16]int
	nundos  map[int16]int
	shiny   map[int16]int
	count   map[int16]int
	ivCount map[int16]int
}

var pokemonTimingCache *ttlcache.Cache[string, pokemonTimings]

var pokemonStats = make(map[areaName]areaStatsCount)
var pokemonStatsLock sync.Mutex

func initLiveStats() {
	pokemonTimingCache = ttlcache.New[string, pokemonTimings](
		ttlcache.WithTTL[string, pokemonTimings](60 * time.Minute),
	)
	go pokemonTimingCache.Start()
}

func updatePokemonStats(old *Pokemon, new *Pokemon) {
	_ = old
	areas := matchGeofences(new.Lat, new.Lon)
	if len(areas) > 0 {
		pokemonStatsLock.Lock()
		defer pokemonStatsLock.Unlock()
		for i := 0; i < len(areas); i++ {
			area := areas[i]

			// Count stats

			if old == nil || old.Cp != new.Cp { // pokemon is new or cp has changed (eg encountered, or re-encountered)
				countStats := pokemonCount[area]

				if countStats == nil {
					countStats = &areaPokemonCountDetail{
						hundos:  make(map[int16]int),
						nundos:  make(map[int16]int),
						shiny:   make(map[int16]int),
						count:   make(map[int16]int),
						ivCount: make(map[int16]int),
					}
					pokemonCount[area] = countStats
				}

				if old == nil || old.PokemonId != new.PokemonId { // pokemon is new or type has changed
					countStats.count[new.PokemonId]++
				}
				if new.Cp.Valid {
					countStats.ivCount[new.PokemonId]++
					if new.Shiny.ValueOrZero() {
						countStats.shiny[new.PokemonId]++
					}
					if new.AtkIv.Valid && new.DefIv.Valid && new.StaIv.Valid {
						atk := new.AtkIv.ValueOrZero()
						def := new.DefIv.ValueOrZero()
						sta := new.StaIv.ValueOrZero()
						if atk == 15 && def == 15 && sta == 15 {
							countStats.hundos[new.PokemonId]++
						}
						if atk == 0 && def == 0 && sta == 0 {
							countStats.nundos[new.PokemonId]--
						}
					}
				}
			}

			// General stats

			bucket := int64(-1)
			monsIvIncr := 0
			monsSeenIncr := 0
			verifiedEncIncr := 0
			unverifiedEncIncr := 0
			verifiedEncSecTotalIncr := int64(0)
			timeToEncounter := int64(0)
			mons100Incr := 0
			statsResetCountIncr := 0
			verifiedReEncounterIncr := 0
			verifiedReEncSecTotalIncr := int64(0)

			if new.Cp.Valid && // an encounter has happened
				(old == nil || // this is first create
					!old.Cp.Valid && new.Cp.Valid) { // update is setting encounter details
				if new.ExpireTimestampVerified {
					tth := new.ExpireTimestamp.ValueOrZero() - new.Updated.ValueOrZero() // relies on Updated being set
					bucket = tth / (5 * 60)
					if bucket > 11 {
						bucket = 11
					}
					verifiedEncIncr = 1
					verifiedEncSecTotalIncr = tth

					pokemonTimingEntry := pokemonTimingCache.Get(new.Id)
					if pokemonTimingEntry != nil {
						pokemonTiming := pokemonTimingEntry.Value()
						if pokemonTiming.first_encounter > 0 {
							verifiedReEncounterIncr = 1
							verifiedReEncSecTotalIncr = tth
						}
					}

				} else {
					unverifiedEncIncr = 1
				}
				monsIvIncr++
				if new.StaIv.ValueOrZero() == 15 && new.AtkIv.ValueOrZero() == 15 && new.DefIv.ValueOrZero() == 15 {
					mons100Incr++
				}
			}

			currentSeenType := new.SeenType.ValueOrZero()
			oldSeenType := ""
			if old != nil {
				oldSeenType = old.SeenType.ValueOrZero()
			}

			if currentSeenType != oldSeenType {
				if (currentSeenType == SeenType_Wild || currentSeenType == SeenType_Encounter) &&
					(oldSeenType == "" || oldSeenType == SeenType_NearbyStop || oldSeenType == SeenType_Cell) {
					monsSeenIncr++
				}
				if currentSeenType == SeenType_Wild && (oldSeenType == "" || oldSeenType == SeenType_NearbyStop || oldSeenType == SeenType_Cell) {
					// transition to wild for the first time
					pokemonTimingCache.Set(new.Id,
						pokemonTimings{first_wild: new.Updated.ValueOrZero()},
						ttlcache.DefaultTTL)
				}
				if currentSeenType == SeenType_Encounter && oldSeenType == SeenType_Wild {
					// transition to encounter from wild
					pokemonTimingEntry := pokemonTimingCache.Get(new.Id)
					if pokemonTimingEntry != nil {
						pokemonTiming := pokemonTimingEntry.Value()
						if pokemonTiming.first_encounter == 0 {
							pokemonTiming.first_encounter = new.Updated.ValueOrZero()
							timeToEncounter = pokemonTiming.first_encounter - pokemonTiming.first_wild

							pokemonTimingCache.Set(new.Id, pokemonTiming, ttlcache.DefaultTTL)
						}
					}
				}
				if currentSeenType == SeenType_Wild && oldSeenType == SeenType_Encounter {
					// stats reset
					statsResetCountIncr++
				}
			}

			// Update record if we have a new stat
			if monsSeenIncr > 0 || monsIvIncr > 0 || verifiedEncIncr > 0 || unverifiedEncIncr > 0 ||
				bucket >= 0 || timeToEncounter > 0 || mons100Incr > 0 || statsResetCountIncr > 0 ||
				verifiedReEncounterIncr > 0 {
				areaStats := pokemonStats[area]
				if bucket >= 0 {
					areaStats.tthBucket[bucket]++
				}
				areaStats.monsIv += monsIvIncr
				areaStats.monsSeen += monsSeenIncr
				areaStats.verifiedEnc += verifiedEncIncr
				areaStats.unverifiedEnc += unverifiedEncIncr
				areaStats.verifiedEncSecTotal += verifiedEncSecTotalIncr
				areaStats.mons100Iv += mons100Incr
				areaStats.statsResetCount += statsResetCountIncr
				areaStats.verifiedReEncounter += verifiedReEncounterIncr
				areaStats.verifiedReEncSecTotal += verifiedReEncSecTotalIncr
				if timeToEncounter > 1 {
					areaStats.timeToEncounterCount++
					areaStats.timeToEncounterSum += timeToEncounter
				}
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

func logPokemonCount() {
	pokemonStatsLock.Lock()
	defer pokemonStatsLock.Unlock()
	log.Infof("---STATS COUNTS---")
	for area, stats := range pokemonCount {
		log.Infof("STATS COUNTS Pokemon stats for %+v %+v", area, stats)
	}
	pokemonCount = make(map[areaName]*areaPokemonCountDetail) // clear stats
}
