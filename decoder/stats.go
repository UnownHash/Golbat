package decoder

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"

	"golbat/encounter_cache"
	"golbat/geo"
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

var pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail)

// max dex id
const maxPokemonNo = 1050

type shinyChecks struct {
	shiny int
	total int
}

type areaPokemonCountDetail struct {
	hundos      [maxPokemonNo + 1]int
	nundos      [maxPokemonNo + 1]int
	shinyChecks [maxPokemonNo + 1]shinyChecks
	count       [maxPokemonNo + 1]int
	ivCount     [maxPokemonNo + 1]int
}

// a cache indexed by encounterId (Pokemon.Id)
var encounterCache *encounter_cache.EncounterCache

var pokemonStats = make(map[geo.AreaName]areaStatsCount)
var pokemonStatsLock sync.Mutex

func initLiveStats() {
	encounterCache = encounter_cache.NewEncounterCache(60 * time.Minute)
	// TODO: fix later to shutdown cleanly, if we care.
	go encounterCache.Run(context.Background())
}

func LoadStatsGeofences() {
	if err := ReadGeofences(); err != nil {
		if os.IsNotExist(err) {
			log.Infof("No geofence file found, skipping")
			return
		}
		panic(fmt.Sprintf("Error reading geofences: %v", err))
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
	pokemonStats = make(map[geo.AreaName]areaStatsCount)          // clear stats
	pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail) // clear count
}

// update stats for an encounterId
func updateEncounterStats(pokemon *Pokemon) {
	// We should only be called from encounters. It's important to do so,
	// so that the 'DuplicateEncounters' stats below are correct.
	// And double check that we have IVs, anyway.
	if !(pokemon.AtkIv.Valid && pokemon.DefIv.Valid && pokemon.StaIv.Valid) {
		return
	}

	// Keep track of encounter Id -> account username. Count shinies
	// for the same encounter Ids, but only if an account has not seen
	// it before. We'll ignore things like re-rolls.

	username := pokemon.Username.ValueOrZero()
	if username == "" {
		username = "<NoUsername>"
	}

	encounterCacheVal := encounterCache.GetOrCreate(pokemon.Id)
	isNewEncounter := encounterCacheVal.NumAccountsSeen() == 0

	if encounterCacheVal.SetAccountSeen(pokemon.Username.ValueOrZero()) {
		// account has already seen this encounter Id
		statsCollector.IncDuplicateEncounters(true)
		return
	}

	if !isNewEncounter {
		// at least one other account has already seen this
		// encounter. This is the first time for this account.
		statsCollector.IncDuplicateEncounters(false)
	}

	encounterCache.Put(pokemon.Id, encounterCacheVal, pokemon.remainingDuration())

	pokemonIdStr := strconv.Itoa(int(pokemon.PokemonId))
	var formIdStr string
	if pokemon.Form.Valid {
		formIdStr = strconv.Itoa(int(pokemon.Form.ValueOrZero()))
	}

	// For the DB
	func() {
		areaName := geo.AreaName{Parent: "world", Name: "world"}

		pokemonStatsLock.Lock()
		defer pokemonStatsLock.Unlock()

		countStats := pokemonCount[areaName]
		if countStats == nil {
			countStats = &areaPokemonCountDetail{}
			pokemonCount[areaName] = countStats
		}
		countStats.shinyChecks[pokemon.PokemonId].total++
		if pokemon.Shiny.ValueOrZero() {
			countStats.shinyChecks[pokemon.PokemonId].shiny++
		}
	}()

	// Prometheus
	if pokemon.Shiny.ValueOrZero() {
		statsCollector.IncPokemonCountShiny(pokemonIdStr, formIdStr)
		if pokemon.AtkIv.Int64 == 15 && pokemon.DefIv.Int64 == 15 && pokemon.StaIv.Int64 == 15 {
			statsCollector.IncPokemonCountShundo()
		} else if pokemon.AtkIv.Int64 == 0 && pokemon.DefIv.Int64 == 0 && pokemon.StaIv.Int64 == 0 {
			statsCollector.IncPokemonCountSnundo()
		}
	} else {
		// send non-shinies also, so that we can compute odds.
		statsCollector.IncPokemonCountNonShiny(pokemonIdStr, formIdStr)
	}
}

func updatePokemonStats(old *Pokemon, new *Pokemon, areas []geo.AreaName) {
	if len(areas) == 0 {
		areas = []geo.AreaName{
			{
				Parent: "world",
				Name:   "world",
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

	var encounterCacheVal *encounter_cache.Value

	populateEncounterCacheVal := func() {
		if encounterCacheVal == nil {
			encounterCacheVal = encounterCache.GetOrCreate(new.Id)
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
				// transition to wild for the first time..
				populateEncounterCacheVal()
				encounterCacheVal.FirstEncounter = 0
				encounterCacheVal.FirstWild = new.Updated.ValueOrZero()
				// This will be put into the cache later.
			}

			if currentSeenType == SeenType_Wild || currentSeenType == SeenType_Encounter {
				// transition to wild or encounter for the first time
				monsSeenIncr = 1
			}
		}

		if currentSeenType == SeenType_Encounter {
			populateEncounterCacheVal()
			if encounterCacheVal.FirstEncounter == 0 {
				// This is first encounter
				encounterCacheVal.FirstEncounter = new.Updated.ValueOrZero()

				if encounterCacheVal.FirstWild > 0 {
					timeToEncounter = encounterCacheVal.FirstEncounter - encounterCacheVal.FirstWild
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

	// If we have a cache entry, it means we updated it. So now let's store it.
	if encounterCacheVal != nil {
		encounterCache.Put(new.Id, encounterCacheVal, new.remainingDuration())
	}

	if (currentSeenType == SeenType_Wild && oldSeenType == SeenType_Encounter) ||
		(currentSeenType == SeenType_Encounter && oldSeenType == SeenType_Encounter &&
			new.PokemonId != old.PokemonId) {
		// stats reset
		statsResetCountIncr = 1
	}

	locked := false

	var isHundo bool
	var isNundo bool

	if new.Cp.Valid && new.AtkIv.Valid && new.DefIv.Valid && new.StaIv.Valid {
		atk := new.AtkIv.ValueOrZero()
		def := new.DefIv.ValueOrZero()
		sta := new.StaIv.ValueOrZero()
		if atk == 15 && def == 15 && sta == 15 {
			isHundo = true
		} else if atk == 0 && def == 0 && sta == 0 {
			isNundo = true
		}
	}

	for i := 0; i < len(areas); i++ {
		area := areas[i]
		fullAreaName := area.String()

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
				statsCollector.IncPokemonCountNew(fullAreaName)
				if new.ExpireTimestampVerified {
					statsCollector.UpdateVerifiedTtl(area, new.SeenType, new.ExpireTimestamp)
				}
			}
			if new.Cp.Valid {
				countStats.ivCount[new.PokemonId]++
				statsCollector.IncPokemonCountIv(fullAreaName)
				if isHundo {
					statsCollector.IncPokemonCountHundo(fullAreaName)
					countStats.hundos[new.PokemonId]++
				} else if isNundo {
					statsCollector.IncPokemonCountNundo(fullAreaName)
					countStats.nundos[new.PokemonId]++
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

			statsCollector.AddPokemonStatsResetCount(fullAreaName, float64(statsResetCountIncr))

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
	pokemonStats = make(map[geo.AreaName]areaStatsCount) // clear stats
	pokemonStatsLock.Unlock()
	go func() {
		var rows []pokemonStatsDbRow
		t := time.Now().Truncate(time.Minute).Unix()
		for area, stats := range currentStats {
			rows = append(rows, pokemonStatsDbRow{
				DateTime:      t,
				Area:          area.Parent,
				Fence:         area.Name,
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
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	Count     int    `db:"count"`
}

type pokemonShinyCountDbRow struct {
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	Count     int    `db:"count"`
	Total     int    `db:"total"`
}

func logPokemonCount(statsDb *sqlx.DB) {

	log.Infof("STATS: Update pokemon count tables")

	pokemonStatsLock.Lock()
	currentStats := pokemonCount
	pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail) // clear stats
	pokemonStatsLock.Unlock()

	go func() {
		var hundoRows []pokemonCountDbRow
		var shinyRows []pokemonShinyCountDbRow
		var nundoRows []pokemonCountDbRow
		var ivRows []pokemonCountDbRow
		var allRows []pokemonCountDbRow

		t := time.Now().In(time.Local)
		midnightString := t.Format("2006-01-02")

		for area, stats := range currentStats {
			addRows := func(rows *[]pokemonCountDbRow, pokemonId int, count int) {
				*rows = append(*rows, pokemonCountDbRow{
					Date:      midnightString,
					Area:      area.Parent,
					Fence:     area.Name,
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
			for pokemonId, checks := range stats.shinyChecks {
				if checks.total > 0 {
					shinyRows = append(shinyRows, pokemonShinyCountDbRow{
						Date:      midnightString,
						Area:      area.Parent,
						Fence:     area.Name,
						PokemonId: pokemonId,
						Count:     checks.shiny,
						Total:     checks.total,
					})
				}
			}
		}

		updateStatsCount := func(table string, rows []pokemonCountDbRow) {
			if len(rows) > 0 {
				chunkSize := 100

				for i := 0; i < len(rows); i += chunkSize {
					end := i + chunkSize

					// necessary check to avoid slicing beyond
					// slice capacity
					if end > len(rows) {
						end = len(rows)
					}

					rowsToWrite := rows[i:end]

					_, err := statsDb.NamedExec(
						fmt.Sprintf("INSERT INTO %s (date, area, fence, pokemon_id, `count`)"+
							" VALUES (:date, :area, :fence, :pokemon_id, :count)"+
							" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", table),
						rowsToWrite,
					)
					if err != nil {
						log.Errorf("Error inserting %s: %v", table, err)
					}
				}
			}
		}

		updateStatsCount("pokemon_stats", allRows)
		updateStatsCount("pokemon_iv_stats", ivRows)
		updateStatsCount("pokemon_hundo_stats", hundoRows)
		updateStatsCount("pokemon_nundo_stats", nundoRows)

		if rows := shinyRows; len(rows) > 0 {
			chunkSize := 100

			for i := 0; i < len(rows); i += chunkSize {
				end := i + chunkSize

				// necessary check to avoid slicing beyond
				// slice capacity
				if end > len(rows) {
					end = len(rows)
				}

				rowsToWrite := rows[i:end]

				_, err := statsDb.NamedExec(
					"INSERT INTO pokemon_shiny_stats (date, area, fence, pokemon_id, `count`, total)"+
						" VALUES (:date, :area, :fence, :pokemon_id, :count, :total)"+
						" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`), total = total + VALUES(total);",
					rowsToWrite,
				)
				if err != nil {
					log.Errorf("Error inserting pokemon_shiny_stats: %v", err)
				}
			}
		}
	}()
}
