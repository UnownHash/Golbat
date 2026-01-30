package decoder

import (
	"context"
	"sync"
	"time"

	"golbat/db"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
)

// PendingPokemon stores wild pokemon data awaiting a potential encounter
type PendingPokemon struct {
	EncounterId   uint64
	WildPokemon   *pogo.WildPokemonProto
	CellId        int64
	TimestampMs   int64
	UpdateTime    int64
	WeatherLookup map[int64]pogo.GameplayWeatherProto_WeatherCondition
	Username      string
	ReceivedAt    time.Time
}

// PokemonPendingQueue manages pokemon awaiting encounter data
type PokemonPendingQueue struct {
	mu      sync.RWMutex
	pending map[uint64]*PendingPokemon
	timeout time.Duration
}

// NewPokemonPendingQueue creates a new pending queue with the specified timeout
func NewPokemonPendingQueue(timeout time.Duration) *PokemonPendingQueue {
	return &PokemonPendingQueue{
		pending: make(map[uint64]*PendingPokemon),
		timeout: timeout,
	}
}

// AddPending stores a wild pokemon awaiting encounter data.
// Returns true if the pokemon was added, false if it already exists.
func (q *PokemonPendingQueue) AddPending(p *PendingPokemon) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Only add if not already present (first sighting wins)
	if _, exists := q.pending[p.EncounterId]; exists {
		return false
	}

	p.ReceivedAt = time.Now()
	q.pending[p.EncounterId] = p
	return true
}

// TryComplete attempts to retrieve and remove a pending pokemon for an encounter.
// Returns the pending pokemon and true if found, nil and false otherwise.
func (q *PokemonPendingQueue) TryComplete(encounterId uint64) (*PendingPokemon, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	p, exists := q.pending[encounterId]
	if exists {
		delete(q.pending, encounterId)
	}
	return p, exists
}

// Remove removes a pending pokemon without processing it.
func (q *PokemonPendingQueue) Remove(encounterId uint64) {
	q.mu.Lock()
	delete(q.pending, encounterId)
	q.mu.Unlock()
}

// Size returns the current number of pending pokemon
func (q *PokemonPendingQueue) Size() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.pending)
}

// collectExpired removes and returns all entries older than timeout
func (q *PokemonPendingQueue) collectExpired() []*PendingPokemon {
	cutoff := time.Now().Add(-q.timeout)

	q.mu.Lock()
	defer q.mu.Unlock()

	var expired []*PendingPokemon
	for id, p := range q.pending {
		if p.ReceivedAt.Before(cutoff) {
			expired = append(expired, p)
			delete(q.pending, id)
		}
	}

	return expired
}

// StartSweeper starts a background goroutine that processes expired entries
func (q *PokemonPendingQueue) StartSweeper(ctx context.Context, interval time.Duration, dbDetails db.DbDetails) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Info("Pokemon pending queue sweeper stopped")
				return
			case <-ticker.C:
				expired := q.collectExpired()
				if len(expired) > 0 {
					log.Debugf("Processing %d expired pending pokemon", len(expired))
					q.processExpired(ctx, dbDetails, expired)
				}
			}
		}
	}()
}

// processExpired handles pokemon that didn't receive an encounter within the timeout
func (q *PokemonPendingQueue) processExpired(ctx context.Context, dbDetails db.DbDetails, expired []*PendingPokemon) {
	for _, p := range expired {
		processCtx, cancel := context.WithTimeout(ctx, 3*time.Second)

		pokemon, unlock, err := getOrCreatePokemonRecord(processCtx, dbDetails, p.EncounterId)
		if err != nil {
			log.Errorf("getOrCreatePokemonRecord in sweeper: %s", err)
			cancel()
			continue
		}

		// Update if there is still a change required & this update is the most recent
		if pokemon.wildSignificantUpdate(p.WildPokemon, p.UpdateTime) && pokemon.Updated.ValueOrZero() < p.UpdateTime {
			log.Debugf("DELAYED UPDATE: Updating pokemon %d from wild (sweeper)", p.EncounterId)

			pokemon.updateFromWild(processCtx, dbDetails, p.WildPokemon, p.CellId, p.WeatherLookup, p.TimestampMs, p.Username)
			savePokemonRecordAsAtTime(processCtx, dbDetails, pokemon, false, true, true, p.UpdateTime)
		}

		unlock()
		cancel()
	}
}

// Global pending queue instance
var pokemonPendingQueue *PokemonPendingQueue

// InitPokemonPendingQueue initializes the global pending queue
func InitPokemonPendingQueue(ctx context.Context, dbDetails db.DbDetails, timeout time.Duration, sweepInterval time.Duration) {
	pokemonPendingQueue = NewPokemonPendingQueue(timeout)
	pokemonPendingQueue.StartSweeper(ctx, sweepInterval, dbDetails)
	log.Infof("Pokemon pending queue started with %v timeout and %v sweep interval", timeout, sweepInterval)
}

// GetPokemonPendingQueue returns the global pending queue instance
func GetPokemonPendingQueue() *PokemonPendingQueue {
	return pokemonPendingQueue
}
