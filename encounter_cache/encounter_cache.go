// package encounter_cache provides an auto-expiring
// cache of stats by encounterId.
package encounter_cache

import (
	"context"
	"time"

	"golbat/ottercache"
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
// A thin Value-semantics layer over the shared ottercache.OtterCache adapter
// (writing-based expiry: reads do not extend TTLs — same as the previous
// DisableTouchOnHit configuration).
type EncounterCache struct {
	// encounterCache is keyed by encounterId (Pokemon.Id).
	// Note that since these are value types in the cache,
	// there's built-in way to synchronize changes to the values.
	// The current use of this cache is guarded by the stats aggregation
	// worker being the only mutator.
	encounterCache *ottercache.OtterCache[uint64, Value]
}

// UpdateTTL updates the ttl for a cache entry, if an entry
// is cached for the encounterId. If no entry exists for the
// encounterId, nothing is done.
func (ec *EncounterCache) UpdateTTL(encounterId uint64, ttl time.Duration) {
	ec.encounterCache.UpdateTTL(encounterId, ttl)
}

// Put will add or replace a cache entry. A ttl of 0 means to use the default
// ttl.
func (ec *EncounterCache) Put(encounterId uint64, value *Value, ttl time.Duration) {
	if value != nil {
		ec.encounterCache.Set(encounterId, *value, ttl)
	}
}

// Get will return the cache entry for the encounterId, if it exists.
// Returns nil if it does not exist.
func (ec *EncounterCache) Get(encounterId uint64) *Value {
	value, ok := ec.encounterCache.Get(encounterId)
	if !ok {
		return nil
	}
	return &value
}

// GetOrCreate will return the cache entry for the encounterId, if it
// exists. If it does not exist, a new entry will be allocated and
// returned, but not insertedt, yet.
func (ec *EncounterCache) GetOrCreate(encounterId uint64) *Value {
	value := ec.Get(encounterId)
	if value == nil {
		value = &Value{
			accountsSeen: make(map[string]bool),
		}
	}
	return value
}

// Run blocks until 'ctx' is cancelled. (The underlying cache needs no
// explicit start; kept for API compatibility.)
func (ec *EncounterCache) Run(ctx context.Context) {
	<-ctx.Done()
}

// NewEncounterCache creates and returns a new auto-expiring cache,
// using 'defaultTTL' as the default ttl. If defaultTTL <= 0, a
// default of 60 minutes will be used.
func NewEncounterCache(defaultTTL time.Duration) *EncounterCache {
	if defaultTTL <= 0 {
		defaultTTL = 60 * time.Minute
	}
	return &EncounterCache{
		encounterCache: ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, Value]{
			Name:       "encounter_stats",
			DefaultTTL: defaultTTL,
			TouchOnHit: false,
		}),
	}
}
