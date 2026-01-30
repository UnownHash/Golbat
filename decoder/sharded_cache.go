package decoder

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// ShardedCache is a generic sharded cache for improved concurrency.
// It distributes entries across multiple ttlcache instances to reduce lock contention.
type ShardedCache[K comparable, V any] struct {
	shards     []*ttlcache.Cache[K, V]
	keyToShard func(K) uint64
}

// ShardedCacheConfig holds configuration for creating a ShardedCache
type ShardedCacheConfig[K comparable, V any] struct {
	NumShards         int
	TTL               time.Duration
	KeyToShard        func(K) uint64
	DisableTouchOnHit bool
}

// NewShardedCache creates a new sharded cache with the given configuration.
// The keyToShard function converts keys to uint64 for shard selection.
func NewShardedCache[K comparable, V any](config ShardedCacheConfig[K, V]) *ShardedCache[K, V] {
	sc := &ShardedCache[K, V]{
		shards:     make([]*ttlcache.Cache[K, V], config.NumShards),
		keyToShard: config.KeyToShard,
	}

	for i := 0; i < config.NumShards; i++ {
		opts := []ttlcache.Option[K, V]{
			ttlcache.WithTTL[K, V](config.TTL),
		}
		if config.DisableTouchOnHit {
			opts = append(opts, ttlcache.WithDisableTouchOnHit[K, V]())
		}
		sc.shards[i] = ttlcache.New[K, V](opts...)
		go sc.shards[i].Start()
	}

	return sc
}

// getShard returns the cache shard for the given key
func (sc *ShardedCache[K, V]) getShard(key K) *ttlcache.Cache[K, V] {
	return sc.shards[sc.keyToShard(key)%uint64(len(sc.shards))]
}

// Get retrieves an item from the appropriate shard
func (sc *ShardedCache[K, V]) Get(key K) *ttlcache.Item[K, V] {
	return sc.getShard(key).Get(key)
}

// Set stores an item in the appropriate shard
func (sc *ShardedCache[K, V]) Set(key K, value V, ttl time.Duration) {
	sc.getShard(key).Set(key, value, ttl)
}

// Delete removes an item from the appropriate shard
func (sc *ShardedCache[K, V]) Delete(key K) {
	sc.getShard(key).Delete(key)
}

// Range iterates over all items in all shards.
// The callback should return true to continue iteration or false to stop.
func (sc *ShardedCache[K, V]) Range(fn func(*ttlcache.Item[K, V]) bool) {
	for _, shard := range sc.shards {
		shard.Range(fn)
	}
}

// OnEviction sets an eviction callback on all shards
func (sc *ShardedCache[K, V]) OnEviction(fn func(context.Context, ttlcache.EvictionReason, *ttlcache.Item[K, V])) {
	for _, shard := range sc.shards {
		shard.OnEviction(fn)
	}
}

// Len returns the total number of items across all shards
func (sc *ShardedCache[K, V]) Len() int {
	total := 0
	for _, shard := range sc.shards {
		total += shard.Len()
	}
	return total
}

// DeleteAll removes all items from all shards
func (sc *ShardedCache[K, V]) DeleteAll() {
	for _, shard := range sc.shards {
		shard.DeleteAll()
	}
}

// GetOrSetFunc retrieves an item from the cache by the provided key.
// If the element is not found, it is created by executing the fn function
// with the provided options and then returned.
// The bool return value is true if the item was found, false if created
// during the execution of the method.
func (sc *ShardedCache[K, V]) GetOrSetFunc(key K, createFunc func() V, opts ...ttlcache.Option[K, V]) (*ttlcache.Item[K, V], bool) {
	return sc.getShard(key).GetOrSetFunc(key, createFunc, opts...)
}

// --- Key conversion helpers ---

// Uint64KeyToShard is the identity function for uint64 keys
func Uint64KeyToShard(key uint64) uint64 {
	return key
}

// Int64KeyToShard converts int64 keys to uint64 for sharding
func Int64KeyToShard(key int64) uint64 {
	return uint64(key)
}

// StringKeyToShard hashes string keys to uint64 for sharding using FNV-1a
func StringKeyToShard(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
