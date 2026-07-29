package cachebench

import (
	"context"
	"math/rand/v2"
	"runtime"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// ttlcacheAdapter replicates decoder/sharded_cache.go: N ttlcache instances,
// key -> shard by identity for uint64 keys (decoder.Uint64KeyToShard), each
// shard started with its own cleanup goroutine, per-entry TTLs on Set, and
// OnEviction registered on every shard. This is the baseline "as configured
// in this repo".
//
// When touchRefreshBelow > 0 the adapter also replicates the hysteresis
// touch from decoder commit 75b3df0: ttlcache-level touch is off, and a Get
// (or found GetOrSetFunc) re-Sets the entry with a fresh jittered TTL only
// when its remaining lifetime is below the threshold — one heap op per
// entry per TTL period instead of per read.
type ttlcacheAdapter struct {
	shards            []*ttlcache.Cache[uint64, *Entity]
	touchRefreshBelow time.Duration
	defaultTTL        time.Duration
}

func NewTTLCacheAdapter(cfg Config) BenchCache {
	n := cfg.Shards
	if n <= 0 {
		n = runtime.NumCPU()
	}
	a := &ttlcacheAdapter{
		shards:            make([]*ttlcache.Cache[uint64, *Entity], n),
		touchRefreshBelow: cfg.TouchRefreshBelow,
		defaultTTL:        cfg.DefaultTTL,
	}
	for i := range a.shards {
		opts := []ttlcache.Option[uint64, *Entity]{
			ttlcache.WithTTL[uint64, *Entity](cfg.DefaultTTL),
		}
		if !cfg.TouchOnHit || cfg.TouchRefreshBelow > 0 {
			opts = append(opts, ttlcache.WithDisableTouchOnHit[uint64, *Entity]())
		}
		a.shards[i] = ttlcache.New[uint64, *Entity](opts...)
		if cfg.OnEvict != nil {
			onEvict := cfg.OnEvict
			a.shards[i].OnEviction(func(_ context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[uint64, *Entity]) {
				r := EvictExpired
				if reason == ttlcache.EvictionReasonDeleted {
					r = EvictDeleted
				}
				onEvict(item.Key(), item.Value(), r)
			})
		}
		go a.shards[i].Start()
	}
	return a
}

func (a *ttlcacheAdapter) shard(key uint64) *ttlcache.Cache[uint64, *Entity] {
	return a.shards[key%uint64(len(a.shards))]
}

func (a *ttlcacheAdapter) Get(key uint64) (*Entity, bool) {
	shard := a.shard(key)
	item := shard.Get(key)
	if item == nil {
		return nil, false
	}
	a.maybeRefresh(shard, key, item)
	return item.Value(), true
}

// maybeRefresh is the hysteresis touch (75b3df0): re-Set with a fresh
// jittered TTL only when the remaining lifetime is below the threshold.
// Set on an existing key updates in place and fires no eviction callbacks.
func (a *ttlcacheAdapter) maybeRefresh(shard *ttlcache.Cache[uint64, *Entity], key uint64, item *ttlcache.Item[uint64, *Entity]) {
	if a.touchRefreshBelow <= 0 {
		return
	}
	if time.Until(item.ExpiresAt()) >= a.touchRefreshBelow {
		return
	}
	// Re-jittered refresh TTL, like fortCacheEntryTTL(): production is
	// 60m + [0,10m), i.e. TTL + up to TTL/6 of jitter.
	shard.Set(key, item.Value(), a.defaultTTL+rand.N(a.defaultTTL/6+1))
}

func (a *ttlcacheAdapter) Set(key uint64, v *Entity, ttl time.Duration) {
	a.shard(key).Set(key, v, ttl)
}

func (a *ttlcacheAdapter) GetOrSetFunc(key uint64, factory func() *Entity, ttl time.Duration) (*Entity, bool) {
	shard := a.shard(key)
	item, found := shard.GetOrSetFunc(key, factory, ttlcache.WithTTL[uint64, *Entity](ttl))
	if found {
		a.maybeRefresh(shard, key, item)
	}
	return item.Value(), found
}

func (a *ttlcacheAdapter) Delete(key uint64) {
	a.shard(key).Delete(key)
}

func (a *ttlcacheAdapter) Len() int {
	total := 0
	for _, s := range a.shards {
		total += s.Len()
	}
	return total
}

func (a *ttlcacheAdapter) Range(fn func(key uint64, v *Entity) bool) {
	for _, s := range a.shards {
		stop := false
		s.Range(func(item *ttlcache.Item[uint64, *Entity]) bool {
			if !fn(item.Key(), item.Value()) {
				stop = true
				return false
			}
			return true
		})
		if stop {
			return
		}
	}
}

func (a *ttlcacheAdapter) Close() {
	for _, s := range a.shards {
		s.Stop()
	}
}
