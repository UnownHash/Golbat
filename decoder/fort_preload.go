package decoder

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
)

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
		log.Errorf("PreloadForts: failed to query pokestops - %s", err)
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
					log.Infof("PreloadForts: loaded %d pokestops...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var pokestop Pokestop
		err := rows.StructScan(&pokestop)
		if err != nil {
			log.Errorf("PreloadForts: pokestop scan error - %s", err)
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
		log.Errorf("PreloadForts: failed to query gyms - %s", err)
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
					log.Infof("PreloadForts: loaded %d gyms...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var gym Gym
		err := rows.StructScan(&gym)
		if err != nil {
			log.Errorf("PreloadForts: gym scan error - %s", err)
			continue
		}
		jobs <- &gym
	}
	close(jobs)
	wg.Wait()

	return count
}
