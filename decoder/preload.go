package decoder

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
)

// Preload loads forts, stations, and recent spawnpoints from DB into cache.
// If populateRtree is true, also builds the rtree index for forts.
func Preload(dbDetails db.DbDetails, populateRtree bool) {
	startTime := time.Now()

	var wg sync.WaitGroup
	var pokestopCount, gymCount, stationCount, spawnpointCount int32

	wg.Add(4)
	go func() {
		defer wg.Done()
		pokestopCount = preloadPokestops(dbDetails, populateRtree)
	}()
	go func() {
		defer wg.Done()
		gymCount = preloadGyms(dbDetails, populateRtree)
	}()
	go func() {
		defer wg.Done()
		stationCount = preloadStations(dbDetails)
	}()
	go func() {
		defer wg.Done()
		spawnpointCount = preloadSpawnpoints(dbDetails)
	}()
	wg.Wait()

	log.Infof("Preload: loaded %d pokestops, %d gyms, %d stations, %d spawnpoints in %v (rtree=%v)",
		pokestopCount, gymCount, stationCount, spawnpointCount, time.Since(startTime), populateRtree)
}

// PreloadForts loads all forts from DB into cache.
// If populateRtree is true, also builds the rtree index.
// Fort tracker is always populated during preload.
func PreloadForts(dbDetails db.DbDetails, populateRtree bool) error {
	startTime := time.Now()

	var wg sync.WaitGroup
	var pokestopCount, gymCount int32

	wg.Add(2)
	go func() {
		defer wg.Done()
		pokestopCount = preloadPokestops(dbDetails, populateRtree)
	}()
	go func() {
		defer wg.Done()
		gymCount = preloadGyms(dbDetails, populateRtree)
	}()
	wg.Wait()

	log.Infof("PreloadForts: loaded %d pokestops and %d gyms in %v (rtree=%v)",
		pokestopCount, gymCount, time.Since(startTime), populateRtree)

	return nil
}

func preloadPokestops(dbDetails db.DbDetails, populateRtree bool) int32 {
	query := "SELECT " + pokestopSelectColumns + " FROM pokestop WHERE deleted = 0"
	rows, err := dbDetails.GeneralDb.Queryx(query)
	if err != nil {
		log.Errorf("Preload: failed to query pokestops - %s", err)
		return 0
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan *Pokestop, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pokestop := range jobs {
				// Add to cache
				pokestopCache.Set(pokestop.Id, pokestop, 0) // 0 = use default TTL

				// Update rtree if enabled
				if populateRtree {
					fortRtreeUpdatePokestopOnSave(pokestop)
				}

				// Register with fort tracker
				if fortTracker != nil && pokestop.CellId.Valid {
					fortTracker.RegisterFort(
						pokestop.Id,
						uint64(pokestop.CellId.Int64),
						false,
						pokestop.Updated*1000, // convert to milliseconds
					)
				}

				c := atomic.AddInt32(&count, 1)
				if c%10000 == 0 {
					log.Infof("Preload: loaded %d pokestops...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var pokestop Pokestop
		err := rows.StructScan(&pokestop)
		if err != nil {
			log.Errorf("Preload: pokestop scan error - %s", err)
			continue
		}
		jobs <- &pokestop
	}
	close(jobs)
	wg.Wait()

	return count
}

func preloadGyms(dbDetails db.DbDetails, populateRtree bool) int32 {
	query := "SELECT " + gymSelectColumns + " FROM gym WHERE deleted = 0"
	rows, err := dbDetails.GeneralDb.Queryx(query)
	if err != nil {
		log.Errorf("Preload: failed to query gyms - %s", err)
		return 0
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan *Gym, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for gym := range jobs {
				// Add to cache
				gymCache.Set(gym.Id, gym, 0) // 0 = use default TTL

				// Update rtree if enabled
				if populateRtree {
					fortRtreeUpdateGymOnSave(gym)
				}

				// Register with fort tracker
				if fortTracker != nil && gym.CellId.Valid {
					fortTracker.RegisterFort(
						gym.Id,
						uint64(gym.CellId.Int64),
						true,
						gym.Updated*1000, // convert to milliseconds
					)
				}

				c := atomic.AddInt32(&count, 1)
				if c%10000 == 0 {
					log.Infof("Preload: loaded %d gyms...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var gym Gym
		err := rows.StructScan(&gym)
		if err != nil {
			log.Errorf("Preload: gym scan error - %s", err)
			continue
		}
		jobs <- &gym
	}
	close(jobs)
	wg.Wait()

	return count
}

func preloadStations(dbDetails db.DbDetails) int32 {
	query := "SELECT " + stationSelectColumns + " FROM station"
	rows, err := dbDetails.GeneralDb.Queryx(query)
	if err != nil {
		log.Errorf("Preload: failed to query stations - %s", err)
		return 0
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan *Station, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for station := range jobs {
				// Add to cache
				stationCache.Set(station.Id, station, 0) // 0 = use default TTL

				c := atomic.AddInt32(&count, 1)
				if c%10000 == 0 {
					log.Infof("Preload: loaded %d stations...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var station Station
		err := rows.StructScan(&station)
		if err != nil {
			log.Errorf("Preload: station scan error - %s", err)
			continue
		}
		jobs <- &station
	}
	close(jobs)
	wg.Wait()

	return count
}

func preloadSpawnpoints(dbDetails db.DbDetails) int32 {
	// Load spawnpoints seen in the last 48 hours
	cutoff := time.Now().Unix() - 48*60*60
	query := "SELECT " + spawnpointSelectColumns + " FROM spawnpoint WHERE last_seen > ?"
	rows, err := dbDetails.GeneralDb.Queryx(query, cutoff)
	if err != nil {
		log.Errorf("Preload: failed to query spawnpoints - %s", err)
		return 0
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan *Spawnpoint, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spawnpoint := range jobs {
				// Add to cache
				spawnpointCache.Set(spawnpoint.Id, spawnpoint, 0) // 0 = use default TTL

				c := atomic.AddInt32(&count, 1)
				if c%10000 == 0 {
					log.Infof("Preload: loaded %d spawnpoints...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var spawnpoint Spawnpoint
		err := rows.StructScan(&spawnpoint)
		if err != nil {
			log.Errorf("Preload: spawnpoint scan error - %s", err)
			continue
		}
		jobs <- &spawnpoint
	}
	close(jobs)
	wg.Wait()

	return count
}
