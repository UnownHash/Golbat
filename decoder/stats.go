package decoder

import (
	"fmt"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"os"
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
	timeToEncounterCount  int
	timeToEncounterSum    int64
	statsResetCount       int
	verifiedReEncounter   int
	verifiedReEncSecTotal int64
}

type pokemonTimings struct {
	firstWild      int64
	firstEncounter int64
}

var pokemonCount = make(map[areaName]*areaPokemonCountDetail)

const maxPokemonNo = 1000

type areaPokemonCountDetail struct {
	hundos  [maxPokemonNo]int
	nundos  [maxPokemonNo]int
	shiny   [maxPokemonNo]int
	count   [maxPokemonNo]int
	ivCount [maxPokemonNo]int
}

var pokemonTimingCache *ttlcache.Cache[string, pokemonTimings]

var pokemonStats = make(map[areaName]areaStatsCount)
var pokemonStatsLock sync.Mutex

func initLiveStats() {
	pokemonTimingCache = ttlcache.New[string, pokemonTimings](
		ttlcache.WithTTL[string, pokemonTimings](60*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, pokemonTimings](),
	)
	go pokemonTimingCache.Start()

	if err := ReadGeofences(); err != nil {
		if os.IsNotExist(err) {
			log.Infof("No geofence file found, skipping")
			return
		}
		panic(fmt.Sprintf("Error reading geofences: %v", err))
	}
}

func initNests() {
	if err := ReadNestGeofences(); err != nil {
		if os.IsNotExist(err) {
			log.Infof("No nest geofence file found, skipping")
			return
		}
		panic(fmt.Sprintf("Error reading nest geofences: %v", err))
	}
}

func StartStatsWriter(statsDb *sqlx.DB) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			<-ticker.C
			logPokemonStats(statsDb)
		}
	}()

	t2 := time.NewTicker(10 * time.Minute)
	go func() {
		for {
			<-t2.C
			logPokemonCount(statsDb)
		}
	}()

	t3 := time.NewTicker(15 * time.Minute)
	go func() {
		for {
			<-t3.C
			logNestCount()
		}
	}()
}

func ReloadGeofenceAndClearStats() {
	log.Info("Reloading stats geofence")

	pokemonStatsLock.Lock()
	defer pokemonStatsLock.Unlock()

	if err := ReadGeofences(); err != nil {
		log.Errorf("Error reading geofences during hot-reload: %v", err)
		return
	}
	pokemonStats = make(map[areaName]areaStatsCount)          // clear stats
	pokemonCount = make(map[areaName]*areaPokemonCountDetail) // clear count
}

func updatePokemonStats(old *Pokemon, new *Pokemon) {
	var areas []areaName

	if statsFeatureCollection != nil {
		areas = matchGeofences(statsFeatureCollection, new.Lat, new.Lon)
	}

	if len(areas) == 0 {
		areas = []areaName{
			{
				parent: "world",
				name:   "world",
			},
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
	statsResetCountIncr := 0
	verifiedReEncounterIncr := 0
	verifiedReEncSecTotalIncr := int64(0)

	var pokemonTiming *pokemonTimings

	populatePokemonTiming := func() {
		if pokemonTiming == nil {
			pokemonTimingEntry := pokemonTimingCache.Get(new.Id)
			if pokemonTimingEntry != nil {
				p := pokemonTimingEntry.Value()
				pokemonTiming = &p
				return
			}
		}
		pokemonTiming = &pokemonTimings{}
	}

	updatePokemonTiming := func() {
		if pokemonTiming != nil {
			remaining := ttlcache.DefaultTTL
			if new.ExpireTimestampVerified {
				timeLeft := 60 + new.ExpireTimestamp.ValueOrZero() - time.Now().Unix()
				if timeLeft > 1 {
					remaining = time.Duration(timeLeft) * time.Second
				}
			}
			pokemonTimingCache.Set(new.Id, *pokemonTiming, remaining)
		}
	}

	currentSeenType := new.SeenType.ValueOrZero()
	oldSeenType := ""
	if old != nil {
		oldSeenType = old.SeenType.ValueOrZero()
	}

	if currentSeenType != oldSeenType {
		if oldSeenType == "" || oldSeenType == SeenType_NearbyStop || oldSeenType == SeenType_Cell {
			// New pokemon, or transition from cell or nearby stop

			if currentSeenType == SeenType_Wild {
				// transition to wild for the first time
				pokemonTiming = &pokemonTimings{firstWild: new.Updated.ValueOrZero()}
				updatePokemonTiming()
			}

			if currentSeenType == SeenType_Wild || currentSeenType == SeenType_Encounter {
				// transition to wild or encounter for the first time
				monsSeenIncr = 1
			}
		}

		if currentSeenType == SeenType_Encounter {
			populatePokemonTiming()

			if pokemonTiming.firstEncounter == 0 {
				// This is first encounter
				pokemonTiming.firstEncounter = new.Updated.ValueOrZero()
				updatePokemonTiming()

				if pokemonTiming.firstWild > 0 {
					timeToEncounter = pokemonTiming.firstEncounter - pokemonTiming.firstWild
				}

				monsIvIncr = 1

				if new.ExpireTimestampVerified {
					tth := new.ExpireTimestamp.ValueOrZero() - new.Updated.ValueOrZero() // relies on Updated being set
					bucket = tth / (5 * 60)
					if bucket > 11 {
						bucket = 11
					}
					verifiedEncIncr = 1
					verifiedEncSecTotalIncr = tth
				} else {
					unverifiedEncIncr = 1
				}
			} else {
				if new.ExpireTimestampVerified {
					tth := new.ExpireTimestamp.ValueOrZero() - new.Updated.ValueOrZero() // relies on Updated being set

					verifiedReEncounterIncr = 1
					verifiedReEncSecTotalIncr = tth
				}
			}
		}
	}

	if (currentSeenType == SeenType_Wild && oldSeenType == SeenType_Encounter) ||
		(currentSeenType == SeenType_Encounter && oldSeenType == SeenType_Encounter &&
			new.PokemonId != old.PokemonId) {
		// stats reset
		statsResetCountIncr = 1
	}

	locked := false

	for i := 0; i < len(areas); i++ {
		area := areas[i]

		// Count stats

		if old == nil || old.Cp != new.Cp { // pokemon is new or cp has changed (eg encountered, or re-encountered)
			if locked == false {
				pokemonStatsLock.Lock()
				locked = true
			}

			countStats := pokemonCount[area]

			if countStats == nil {
				countStats = &areaPokemonCountDetail{}
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
						countStats.nundos[new.PokemonId]++
					}
				}
			}
		}

		// Update record if we have a new stat
		if monsSeenIncr > 0 || monsIvIncr > 0 || verifiedEncIncr > 0 || unverifiedEncIncr > 0 ||
			bucket >= 0 || timeToEncounter > 0 || statsResetCountIncr > 0 ||
			verifiedReEncounterIncr > 0 {
			if locked == false {
				pokemonStatsLock.Lock()
				locked = true
			}

			areaStats := pokemonStats[area]
			if bucket >= 0 {
				areaStats.tthBucket[bucket]++
			}
			areaStats.monsIv += monsIvIncr
			areaStats.monsSeen += monsSeenIncr
			areaStats.verifiedEnc += verifiedEncIncr
			areaStats.unverifiedEnc += unverifiedEncIncr
			areaStats.verifiedEncSecTotal += verifiedEncSecTotalIncr
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

	if locked {
		pokemonStatsLock.Unlock()
	}
}

type pokemonStatsDbRow struct {
	DateTime              int64  `db:"datetime"`
	Area                  string `db:"area"`
	Fence                 string `db:"fence"`
	TotMon                int    `db:"totMon"`
	IvMon                 int    `db:"ivMon"`
	VerifiedEnc           int    `db:"verifiedEnc"`
	UnverifiedEnc         int    `db:"unverifiedEnc"`
	VerifiedReEnc         int    `db:"verifiedReEnc"`
	VerifiedWild          int    `db:"verifiedWild"`
	EncSecLeft            int64  `db:"encSecLeft"`
	EncTthMax5            int    `db:"encTthMax5"`
	EncTth5to10           int    `db:"encTth5to10"`
	EncTth10to15          int    `db:"encTth10to15"`
	EncTth15to20          int    `db:"encTth15to20"`
	EncTth20to25          int    `db:"encTth20to25"`
	EncTth25to30          int    `db:"encTth25to30"`
	EncTth30to35          int    `db:"encTth30to35"`
	EncTth35to40          int    `db:"encTth35to40"`
	EncTth40to45          int    `db:"encTth40to45"`
	EncTth45to50          int    `db:"encTth45to50"`
	EncTth50to55          int    `db:"encTth50to55"`
	EncTthMin55           int    `db:"encTthMin55"`
	ResetMon              int    `db:"resetMon"`
	ReencounterTthLeft    int64  `db:"re_encSecLeft"`
	NumWildEncounters     int    `db:"numWiEnc"`
	SumSecWildToEncounter int64  `db:"secWiEnc"`
}

func logPokemonStats(statsDb *sqlx.DB) {
	pokemonStatsLock.Lock()
	log.Infof("STATS: Write area stats")

	currentStats := pokemonStats
	pokemonStats = make(map[areaName]areaStatsCount) // clear stats
	pokemonStatsLock.Unlock()
	go func() {
		var rows []pokemonStatsDbRow
		t := time.Now().Truncate(time.Minute).Unix()
		for area, stats := range currentStats {
			rows = append(rows, pokemonStatsDbRow{
				DateTime:      t,
				Area:          area.parent,
				Fence:         area.name,
				TotMon:        stats.monsSeen,
				IvMon:         stats.monsIv,
				VerifiedEnc:   stats.verifiedEnc,
				VerifiedReEnc: stats.verifiedReEncounter,
				UnverifiedEnc: stats.unverifiedEnc,

				EncSecLeft:   stats.verifiedEncSecTotal,
				EncTthMax5:   stats.tthBucket[0],
				EncTth5to10:  stats.tthBucket[1],
				EncTth10to15: stats.tthBucket[2],
				EncTth15to20: stats.tthBucket[3],
				EncTth20to25: stats.tthBucket[4],
				EncTth25to30: stats.tthBucket[5],
				EncTth30to35: stats.tthBucket[6],
				EncTth35to40: stats.tthBucket[7],
				EncTth40to45: stats.tthBucket[8],
				EncTth45to50: stats.tthBucket[9],
				EncTth50to55: stats.tthBucket[10],
				EncTthMin55:  stats.tthBucket[11],

				ResetMon:              stats.statsResetCount,
				ReencounterTthLeft:    stats.verifiedReEncSecTotal,
				NumWildEncounters:     stats.timeToEncounterCount,
				SumSecWildToEncounter: stats.timeToEncounterSum,
			})
		}

		if len(rows) > 0 {
			_, err := statsDb.NamedExec(
				"INSERT INTO pokemon_area_stats "+
					"(datetime, area, fence, totMon, ivMon, verifiedEnc, unverifiedEnc, verifiedReEnc, encSecLeft, encTthMax5, encTth5to10, encTth10to15, encTth15to20, encTth20to25, encTth25to30, encTth30to35, encTth35to40, encTth40to45, encTth45to50, encTth50to55, encTthMin55, resetMon, re_encSecLeft, numWiEnc, secWiEnc) "+
					"VALUES (:datetime, :area, :fence, :totMon, :ivMon, :verifiedEnc, :unverifiedEnc, :verifiedReEnc, :encSecLeft, :encTthMax5, :encTth5to10, :encTth10to15, :encTth15to20, :encTth20to25, :encTth25to30, :encTth30to35, :encTth35to40, :encTth40to45, :encTth45to50, :encTth50to55, :encTthMin55, :resetMon, :re_encSecLeft, :numWiEnc, :secWiEnc)",
				rows)
			if err != nil {
				log.Errorf("Error inserting pokemon_area_stats: %v", err)
			}
		}
	}()

}

type pokemonCountDbRow struct {
	Date      time.Time `db:"date"`
	Area      string    `db:"area"`
	Fence     string    `db:"fence"`
	PokemonId int       `db:"pokemon_id"`
	Count     int       `db:"count"`
}

func logPokemonCount(statsDb *sqlx.DB) {
	pokemonStatsLock.Lock()

	log.Infof("STATS: Update pokemon count tables")

	currentStats := pokemonCount
	pokemonCount = make(map[areaName]*areaPokemonCountDetail) // clear stats
	pokemonStatsLock.Unlock()

	go func() {
		var hundoRows []pokemonCountDbRow
		var shinyRows []pokemonCountDbRow
		var nundoRows []pokemonCountDbRow
		var ivRows []pokemonCountDbRow
		var allRows []pokemonCountDbRow

		t := time.Now()
		midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)

		for area, stats := range currentStats {
			addRows := func(rows *[]pokemonCountDbRow, pokemonId int, count int) {
				*rows = append(*rows, pokemonCountDbRow{
					Date:      midnight,
					Area:      area.parent,
					Fence:     area.name,
					PokemonId: pokemonId,
					Count:     count,
				})
			}

			for pokemonId, count := range stats.count {
				if count > 0 {
					addRows(&allRows, pokemonId, count)
				}
			}
			for pokemonId, count := range stats.ivCount {
				if count > 0 {
					addRows(&ivRows, pokemonId, count)
				}
			}
			for pokemonId, count := range stats.hundos {
				if count > 0 {
					addRows(&hundoRows, pokemonId, count)
				}
			}
			for pokemonId, count := range stats.nundos {
				if count > 0 {
					addRows(&nundoRows, pokemonId, count)
				}
			}
			for pokemonId, count := range stats.shiny {
				if count > 0 {
					addRows(&shinyRows, pokemonId, count)
				}
			}
		}

		updateStatsCount := func(table string, rows []pokemonCountDbRow) {
			if len(rows) > 0 {
				_, err := statsDb.NamedExec(
					fmt.Sprintf("INSERT INTO %s (date, area, fence, pokemon_id, `count`)"+
						" VALUES (:date, :area, :fence, :pokemon_id, :count)"+
						" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`)", table),
					rows,
				)
				if err != nil {
					log.Errorf("Error inserting %s: %v", table, err)
				}
			}
		}
		updateStatsCount("pokemon_stats", allRows)
		updateStatsCount("pokemon_iv_stats", ivRows)
		updateStatsCount("pokemon_hundo_stats", hundoRows)
		updateStatsCount("pokemon_nundo_stats", nundoRows)
		updateStatsCount("pokemon_shiny_stats", shinyRows)
	}()

}
