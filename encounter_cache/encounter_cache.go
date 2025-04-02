// package encounter_cache provides an auto-expiring
// cache of stats by encounterId.
package encounter_cache

import (
	"context"
	"time"

	"github.com/jellydator/ttlcache/v3"
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
type EncounterCache struct {
	// encounterCache is keyed by encounterId (Pokemon.Id).
	// Note that since these are value types in the cache,
	// there's built-in way to synchronize changes to the values.
	// The current use of this cache is guarded by pokemonStripedMutex
	// to synchronize access.
	encounterCache *ttlcache.Cache[uint64, Value]
}

// UpdateTTL updates the ttl for a cache entry, if an entry
// is cached for the encounterId. If no entry exists for the
// encounterId, nothing is done.
func (cache *EncounterCache) UpdateTTL(encounterId uint64, ttl time.Duration) {
	cache.Put(encounterId, cache.Get(encounterId), ttl)
}

// Put will add or replace a cache entry. A ttl of 0 means to use the default
// ttl.
func (cache *EncounterCache) Put(encounterId uint64, value *Value, ttl time.Duration) {
	if value != nil {
		cache.encounterCache.Set(encounterId, *value, ttl)
	}
}

// Get will return the cache entry for the encounterId, if it exists.
// Returns nil if it does not exist.
func (cache *EncounterCache) Get(encounterId uint64) *Value {
	entry := cache.encounterCache.Get(encounterId)
	if entry == nil {
		return nil
	}
	value := entry.Value()
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

// Run will run the auto-expiring goroutine until 'ctx' is
// cancelled.
func (cache *EncounterCache) Run(ctx context.Context) {
	doneCh := make(chan bool)

	go func() {
		defer close(doneCh)
		cache.encounterCache.Start()
	}()

	<-ctx.Done()
	cache.encounterCache.Stop()
	<-doneCh
}

// NewEncounterCache creates and returns a new auto-expiring cache,
// using 'defaultTTL' as the default ttl. If defaultTTL <= 0, a
// default of 60 minutes will be used.
func NewEncounterCache(defaultTTL time.Duration) *EncounterCache {
	if defaultTTL <= 0 {
		defaultTTL = 60 * time.Minute
	}
	return &EncounterCache{
		encounterCache: ttlcache.New[uint64, Value](
			ttlcache.WithTTL[uint64, Value](defaultTTL),
			ttlcache.WithDisableTouchOnHit[uint64, Value](),
		),
	}
}
