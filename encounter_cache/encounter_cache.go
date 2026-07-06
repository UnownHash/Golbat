// package encounter_cache provides an auto-expiring
// cache of stats by encounterId.
package encounter_cache

import (
	"context"
	"time"

	"github.com/maypok86/otter/v2"
)

// Value is the object that is inserted into the cache.
type Value struct {
	FirstWild      int64
	FirstEncounter int64

	accountsSeen map[string]bool
}

// SetAccountSeen() marks an account having seen the
// encounterId for this cache value. Returns whether
// the account had already seen it.
func (v *Value) SetAccountSeen(username string) bool {
	if v.accountsSeen[username] {
		return true
	}
	v.accountsSeen[username] = true
	return false
}

// NumAccountsSeen() returns the number of accounts
// that have encountered the encounterId for this
// cache value.
func (v *Value) NumAccountsSeen() int {
	return len(v.accountsSeen)
}

// EncounterCache is an object that represents an auto-expiring
// cache, providing methods to manage cache entries.
//
// Backed by otter v2 (lock-free reads, timing-wheel expiry): the previous
// single ttlcache's per-Put heap ops and cohort sweeps ran on the stats
// aggregation worker's critical path. Writing-based expiry only (reads do
// not extend TTLs — same as the previous DisableTouchOnHit configuration).
type EncounterCache struct {
	// encounterCache is keyed by encounterId (Pokemon.Id).
	// Note that since these are value types in the cache,
	// there's built-in way to synchronize changes to the values.
	// The current use of this cache is guarded by the stats aggregation
	// worker being the only mutator.
	encounterCache *otter.Cache[uint64, encEntry]
	defaultTTL     time.Duration
}

// encEntry carries the per-entry TTL for otter's variable-expiry
// calculator (otter has no per-call TTL argument).
type encEntry struct {
	v   Value
	ttl time.Duration
}

// UpdateTTL updates the ttl for a cache entry, if an entry
// is cached for the encounterId. If no entry exists for the
// encounterId, nothing is done.
func (cache *EncounterCache) UpdateTTL(encounterId uint64, ttl time.Duration) {
	if ttl <= 0 {
		ttl = cache.defaultTTL
	}
	cache.encounterCache.SetExpiresAfter(encounterId, ttl)
}

// Put will add or replace a cache entry. A ttl of 0 means to use the default
// ttl.
func (cache *EncounterCache) Put(encounterId uint64, value *Value, ttl time.Duration) {
	if value != nil {
		if ttl <= 0 {
			ttl = cache.defaultTTL
		}
		cache.encounterCache.Set(encounterId, encEntry{v: *value, ttl: ttl})
	}
}

// Get will return the cache entry for the encounterId, if it exists.
// Returns nil if it does not exist.
func (cache *EncounterCache) Get(encounterId uint64) *Value {
	entry, ok := cache.encounterCache.GetIfPresent(encounterId)
	if !ok {
		return nil
	}
	value := entry.v
	return &value
}

// GetOrCreate will return the cache entry for the encounterId, if it
// exists. If it does not exist, a new entry will be allocated and
// returned, but not insertedt, yet.
func (cache *EncounterCache) GetOrCreate(encounterId uint64) *Value {
	value := cache.Get(encounterId)
	if value == nil {
		value = &Value{
			accountsSeen: make(map[string]bool),
		}
	}
	return value
}

// Run blocks until 'ctx' is cancelled, then stops the cache's background
// goroutines. (otter needs no explicit start; kept for API compatibility.)
func (cache *EncounterCache) Run(ctx context.Context) {
	<-ctx.Done()
	cache.encounterCache.StopAllGoroutines()
}

// NewEncounterCache creates and returns a new auto-expiring cache,
// using 'defaultTTL' as the default ttl. If defaultTTL <= 0, a
// default of 60 minutes will be used.
func NewEncounterCache(defaultTTL time.Duration) *EncounterCache {
	if defaultTTL <= 0 {
		defaultTTL = 60 * time.Minute
	}
	return &EncounterCache{
		defaultTTL: defaultTTL,
		encounterCache: otter.Must(&otter.Options[uint64, encEntry]{
			ExpiryCalculator: otter.ExpiryWritingFunc[uint64, encEntry](
				func(entry otter.Entry[uint64, encEntry]) time.Duration {
					return entry.Value.ttl
				}),
		}),
	}
}
