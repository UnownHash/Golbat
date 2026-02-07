package decoder

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jellydator/ttlcache/v3"
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
func PreservePokemonToDatabase(dbDetails db.DbDetails) {
	startTime := time.Now()
	now := time.Now().Unix()

	var saved, skipped, errored int
	batch := make([]PokemonData, 0, preserveBatchSize)
	ctx := context.Background()

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		_, err := dbDetails.PokemonDb.NamedExecContext(ctx, pokemonBatchUpsertQuery, batch)
		if err != nil {
			log.Errorf("PreservePokemon: batch write error - %s", err)
			errored += len(batch)
		} else {
			saved += len(batch)
		}
		// Reset batch by reslicing to zero length (reuses backing array)
		batch = batch[:0]
	}

	// Stream through cache, batching writes
	pokemonCache.Range(func(item *ttlcache.Item[uint64, *Pokemon]) bool {
		pokemon := item.Value()

		// Skip if expired or no valid expire timestamp (no lock needed at shutdown)
		if !pokemon.ExpireTimestamp.Valid || pokemon.ExpireTimestamp.Int64 <= now {
			skipped++
			return true
		}

		// Add to batch
		batch = append(batch, pokemon.PokemonData)

		// Flush when batch is full
		if len(batch) >= preserveBatchSize {
			flushBatch()
			if saved%10000 == 0 && saved > 0 {
				log.Infof("PreservePokemon: saved %d pokemon...", saved)
			}
		}

		return true
	})

	// Flush remaining
	flushBatch()

	log.Infof("PreservePokemon: saved %d pokemon, skipped %d expired, %d errors in %v",
		saved, skipped, errored, time.Since(startTime))
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
				// Calculate remaining TTL
				ttl := pokemon.remainingDuration(currentTime)
				if ttl <= 0 {
					continue
				}

				// Add to cache with appropriate TTL
				pokemonCache.Set(uint64(pokemon.Id), pokemon, ttl)

				// Update rtree
				pokemonRtreeUpdatePokemonOnGet(pokemon)

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
