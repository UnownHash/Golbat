package decoder

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
)

// preserveBatchSize is the number of pokemon to write per batch
const preserveBatchSize = 1000

// skipPreservePokemon is set via API to prevent PreservePokemonToDatabase on shutdown
var skipPreservePokemon atomic.Bool

// SetSkipPreservePokemon sets the flag to skip pokemon preservation on shutdown
func SetSkipPreservePokemon(skip bool) {
	skipPreservePokemon.Store(skip)
}

// ShouldPreservePokemon returns true if preservation should be performed
func ShouldPreservePokemon() bool {
	return !skipPreservePokemon.Load()
}

// PreservePokemonToDatabase writes all non-expired pokemon from cache to database.
// Called during shutdown when preserve_pokemon is enabled.
// Does not take locks since cache is no longer being modified at shutdown.
//
// Parallelized (mirrors PreloadPreservedPokemon): a single-writer pass
// measured ~13k rows/s in production — over ten minutes for a full
// evening cache, far beyond any process manager's kill window. Workers
// bring it to a couple of minutes; the process manager's kill timeout
// (e.g. pm2 kill_timeout) must still exceed the preserve duration or the
// process is SIGKILLed mid-save.
func PreservePokemonToDatabase(dbDetails db.DbDetails) {
	startTime := time.Now()
	now := time.Now().Unix()

	// Upper bound: expired-but-unswept entries are skipped during the walk.
	// Logged so operators can size their process manager's kill window —
	// preservation is useless if the process is SIGKILLed mid-save.
	total := int64(pokemonCache.Len())
	log.Infof("PreservePokemon: preserving up to %d cached pokemon (expired entries will be skipped)", total)

	var saved, skipped, errored atomic.Int64
	ctx := context.Background()

	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8 // bounded: the DB is the constraint, not CPU
	}
	batches := make(chan []PokemonData, numWorkers*2)
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batches {
				_, err := dbDetails.PokemonDb.NamedExecContext(ctx, pokemonBatchUpsertQuery, batch)
				if err != nil {
					log.Errorf("PreservePokemon: batch write error - %s", err)
					errored.Add(int64(len(batch)))
				} else {
					if s := saved.Add(int64(len(batch))); s%10000 == 0 {
						elapsed := time.Since(startTime)
						rate := float64(s) / elapsed.Seconds()
						remaining := total - s - skipped.Load()
						if remaining < 0 {
							remaining = 0
						}
						eta := time.Duration(float64(remaining)/rate) * time.Second
						log.Infof("PreservePokemon: saved %d/~%d pokemon (%.0f rows/s, ~%s remaining)",
							s, total, rate, eta.Round(time.Second))
					}
				}
			}
		}()
	}

	// Stream through cache, handing full batches to the writers
	batch := make([]PokemonData, 0, preserveBatchSize)
	pokemonCache.Range(func(_ uint64, pokemon *Pokemon) bool {
		// Skip if expired or no valid expire timestamp (no lock needed at shutdown)
		if !pokemon.ExpireTimestamp.Valid || pokemon.ExpireTimestamp.Int64 <= now {
			skipped.Add(1)
			return true
		}

		batch = append(batch, pokemon.PokemonData)
		if len(batch) >= preserveBatchSize {
			batches <- batch
			batch = make([]PokemonData, 0, preserveBatchSize)
		}
		return true
	})
	if len(batch) > 0 {
		batches <- batch
	}
	close(batches)
	wg.Wait()

	log.Infof("PreservePokemon: saved %d pokemon, skipped %d expired, %d errors in %v",
		saved.Load(), skipped.Load(), errored.Load(), time.Since(startTime))
}

// PreloadPreservedPokemon loads non-expired pokemon from database into cache.
// Called during startup when preserve_pokemon is enabled.
func PreloadPreservedPokemon(dbDetails db.DbDetails) int32 {
	startTime := time.Now()
	now := time.Now().Unix()

	// Load pokemon that haven't expired yet
	query := "SELECT " + pokemonSelectColumns + " FROM pokemon WHERE expire_timestamp > ?"
	rows, err := dbDetails.PokemonDb.Queryx(query, now)
	if err != nil {
		log.Errorf("PreloadPokemon: failed to query pokemon - %s", err)
		return 0
	}
	defer rows.Close()

	numWorkers := runtime.NumCPU()
	jobs := make(chan *Pokemon, 100)
	var wg sync.WaitGroup
	var count int32

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			currentTime := time.Now().Unix()
			for pokemon := range jobs {
				// Only restore pokemon with verified despawns. Unverified
				// expire timestamps are guesses, and remainingDuration now
				// always returns a positive (jittered) duration for them, so
				// the ttl<=0 check below no longer filters them out the way
				// the old DefaultTTL(=0) sentinel did.
				if !pokemon.ExpireTimestampVerified {
					continue
				}

				// Calculate remaining TTL
				ttl := pokemon.remainingDuration(currentTime)
				if ttl <= 0 {
					continue
				}

				// Add to cache with appropriate TTL
				pokemonCache.Set(uint64(pokemon.Id), pokemon, ttl)

				// Update rtree (direct: pre-traffic, avoids flooding the tree worker)
				pokemonRtreePreloadInsert(pokemon)

				c := atomic.AddInt32(&count, 1)
				if c%10000 == 0 {
					log.Infof("PreloadPokemon: loaded %d pokemon...", c)
				}
			}
		}()
	}

	for rows.Next() {
		var pokemon Pokemon
		err := rows.StructScan(&pokemon)
		if err != nil {
			log.Errorf("PreloadPokemon: pokemon scan error - %s", err)
			continue
		}
		jobs <- &pokemon
	}
	close(jobs)
	wg.Wait()

	log.Infof("PreloadPokemon: loaded %d pokemon in %v", count, time.Since(startTime))
	return count
}
