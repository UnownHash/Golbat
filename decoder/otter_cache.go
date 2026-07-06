package decoder

import (
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/maypok86/otter/v2"

	"golbat/util"
)

// OtterCache adapts otter v2 (Caffeine-style: lock-free reads, hierarchical
// timing-wheel expiry, bounded maintenance) to the cache API Golbat's entity
// layer was built against. It replaces ShardedCache for the hot entity
// caches: otter's table is internally concurrent, so sharding, TTL-jitter-
// as-cohort-defense, and hysteresis touch all become unnecessary.
//
// The adapter is HARDENED by construction — two otter behaviors are unsafe
// for Golbat and are not configurable here:
//
//  1. Deletion events are re-dispatched from OnAtomicDeletion to a single
//     dispatcher goroutine via a bounded queue. otter's OnDeletion DEFAULT
//     executor is `go fn()` per event (the goroutine-per-eviction shape
//     that produced the ed50bf8 incident), and raw OnAtomicDeletion runs
//     inline under otter's internal synchronization — a handler taking an
//     entity lock there deadlocks against a saver holding that entity lock
//     while writing to the same cache. The dispatcher holds no cache
//     internals, so handlers keep the exact ttlcache-era semantics: async,
//     may take entity locks, guarded against races. Queue overflow drops
//     the event with once-per-second accounting (evictions are
//     drop-tolerant: ghost tree points are already handled).
//  2. Only CauseExpiration and CauseInvalidation reach the handler.
//     otter fires CauseReplacement on overwriting a live entry, and
//     Golbat's save paths Set live entries on every save — always the
//     SAME pointer (values are never replaced): getOrCreate inserts the
//     entity with the cache default TTL, and the save's Set stamps the
//     real per-entry TTL over it (despawn-derived pokemon TTLs, jittered
//     fort TTLs). A Replacement event reaching the eviction guards would
//     enqueue a bogus tree delete for a live entity on every save.
//     ttlcache fired nothing on overwrite; parity is kept. (Set rather
//     than UpdateTTL on the save paths is deliberate: its upsert arm
//     re-caches an entity evicted mid-save, without which the save's
//     lookup/tree self-heal would leak entries the already-fired eviction
//     callback can no longer clean.)
type OtterCache[K comparable, V any] struct {
	c          *otter.Cache[K, otterVal[V]]
	defaultTTL time.Duration
	onEvict    atomic.Pointer[func(K, V, EvictionReason)]
	name       string
	evictCh    chan otterEvictEvent[K, V]
	drops      util.DropReporter
}

type otterEvictEvent[K comparable, V any] struct {
	key    K
	value  V
	reason EvictionReason
}

// EvictionReason mirrors the two ttlcache reasons Golbat's handlers use.
type EvictionReason int

const (
	EvictionExpired EvictionReason = iota
	EvictionDeleted
)

// CacheItem keeps call sites written against ttlcache's `item.Value()`
// shape working unchanged (nil item = absent).
type CacheItem[V any] struct{ v V }

func (i *CacheItem[V]) Value() V { return i.v }

// otterVal carries the entry's own TTL: otter has no per-call TTL argument;
// per-entry TTLs are implemented via an ExpiryCalculator that reads the
// duration back off the entry (Caffeine's "variable expiry" pattern).
type otterVal[V any] struct {
	v   V
	ttl time.Duration
}

type OtterCacheConfig[K comparable, V any] struct {
	// Name appears in eviction-drop log lines.
	Name       string
	DefaultTTL time.Duration
	// TouchOnHit selects the expiry calculator: true = ExpiryAccessingFunc
	// (reads reset the timer to the entry's own TTL — forts, spawnpoints),
	// false = ExpiryWritingFunc (only create/update reset it — pokemon,
	// whose TTL encodes the despawn time and must never extend on read).
	// otter touches via the timing wheel (~ns), so plain touch-on-hit is
	// affordable again; the hysteresis workaround is not carried over.
	TouchOnHit      bool
	InitialCapacity int
}

func NewOtterCache[K comparable, V any](cfg OtterCacheConfig[K, V]) *OtterCache[K, V] {
	oc := &OtterCache[K, V]{
		defaultTTL: cfg.DefaultTTL,
		name:       cfg.Name,
		evictCh:    make(chan otterEvictEvent[K, V], 65536),
	}
	go oc.evictDispatchLoop()

	ttlOf := func(entry otter.Entry[K, otterVal[V]]) time.Duration {
		return entry.Value.ttl
	}
	var expiry otter.ExpiryCalculator[K, otterVal[V]]
	if cfg.TouchOnHit {
		expiry = otter.ExpiryAccessingFunc[K, otterVal[V]](ttlOf)
	} else {
		expiry = otter.ExpiryWritingFunc[K, otterVal[V]](ttlOf)
	}

	opts := &otter.Options[K, otterVal[V]]{
		ExpiryCalculator: expiry,
		InitialCapacity:  cfg.InitialCapacity,
		OnAtomicDeletion: func(ev otter.DeletionEvent[K, otterVal[V]]) {
			if oc.onEvict.Load() == nil {
				return
			}
			var reason EvictionReason
			switch ev.Cause {
			case otter.CauseExpiration:
				reason = EvictionExpired
			case otter.CauseInvalidation:
				reason = EvictionDeleted
			default:
				return // Replacement/Overflow: not eviction events in Golbat's model
			}
			// Non-blocking: this runs inline under otter's internal
			// synchronization; blocking here would stall cache maintenance.
			select {
			case oc.evictCh <- otterEvictEvent[K, V]{key: ev.Key, value: ev.Value.v, reason: reason}:
			default:
				oc.drops.Report(func(dropped int64) {
					log.Warnf("[CACHE_EVICT] %s dropped %d eviction events in the last second (dispatcher backlogged %d/%d)",
						oc.name, dropped, len(oc.evictCh), cap(oc.evictCh))
				})
			}
		},
	}
	oc.c = otter.Must(opts)
	return oc
}

// Get returns the item, touching it per the cache's expiry policy.
func (oc *OtterCache[K, V]) Get(key K) *CacheItem[V] {
	w, ok := oc.c.GetIfPresent(key)
	if !ok {
		return nil
	}
	return &CacheItem[V]{v: w.v}
}

func (oc *OtterCache[K, V]) Has(key K) bool {
	_, ok := oc.c.GetIfPresent(key)
	return ok
}

// Set stores value with the given TTL; ttl <= 0 uses the cache default
// (matches ttlcache.DefaultTTL semantics at existing call sites).
func (oc *OtterCache[K, V]) Set(key K, value V, ttl time.Duration) {
	if ttl <= 0 {
		ttl = oc.defaultTTL
	}
	oc.c.Set(key, otterVal[V]{v: value, ttl: ttl})
}

// GetOrSetFunc returns the existing item or atomically creates one via
// factory (single winner: racing callers receive the same value). The bool
// is true if the item already existed, mirroring ttlcache.
func (oc *OtterCache[K, V]) GetOrSetFunc(key K, factory func() V) (*CacheItem[V], bool) {
	created := false
	w, _ := oc.c.ComputeIfAbsent(key, func() (otterVal[V], bool) {
		created = true
		return otterVal[V]{v: factory(), ttl: oc.defaultTTL}, false
	})
	return &CacheItem[V]{v: w.v}, !created
}

// GetOrSetFuncTTL is GetOrSetFunc with an explicit TTL for the created
// entry (existing entries keep their own TTL). NOTE: as with ttlcache's
// GetOrSetFunc, the factory runs under internal cache synchronization for
// the key's bucket — it must not acquire locks that can be held while
// calling into this cache.
func (oc *OtterCache[K, V]) GetOrSetFuncTTL(key K, factory func() V, ttl time.Duration) (*CacheItem[V], bool) {
	if ttl <= 0 {
		ttl = oc.defaultTTL
	}
	created := false
	w, _ := oc.c.ComputeIfAbsent(key, func() (otterVal[V], bool) {
		created = true
		return otterVal[V]{v: factory(), ttl: ttl}, false
	})
	return &CacheItem[V]{v: w.v}, !created
}

// UpdateTTL re-arms the entry's expiry without rewriting the value (and
// without firing any deletion event).
func (oc *OtterCache[K, V]) UpdateTTL(key K, ttl time.Duration) {
	if ttl <= 0 {
		ttl = oc.defaultTTL
	}
	oc.c.SetExpiresAfter(key, ttl)
}

func (oc *OtterCache[K, V]) Delete(key K) {
	oc.c.Invalidate(key)
}

func (oc *OtterCache[K, V]) DeleteAll() {
	oc.c.InvalidateAll()
}

// Len is otter's EstimatedSize: exact enough for metrics/logging, which are
// its only Golbat uses.
func (oc *OtterCache[K, V]) Len() int {
	return oc.c.EstimatedSize()
}

// Range iterates entries (weakly consistent snapshot; expired entries are
// skipped). Return false from fn to stop.
func (oc *OtterCache[K, V]) Range(fn func(key K, value V) bool) {
	for k, w := range oc.c.All() {
		if !fn(k, w.v) {
			return
		}
	}
}

// OnEviction registers the eviction handler (at most one; last wins). It is
// invoked on the cache's single dispatcher goroutine for Expired and
// Deleted causes only — async relative to cache operations and entity-lock
// holders (same delivery contract Golbat's guards were written against).
func (oc *OtterCache[K, V]) OnEviction(fn func(key K, value V, reason EvictionReason)) {
	oc.onEvict.Store(&fn)
}

func (oc *OtterCache[K, V]) evictDispatchLoop() {
	for ev := range oc.evictCh {
		if fn := oc.onEvict.Load(); fn != nil {
			(*fn)(ev.key, ev.value, ev.reason)
		}
	}
}
