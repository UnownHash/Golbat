package decoder

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// Hysteresis touch: entries keep residency like touch-on-hit, but the TTL
// refresh (a heap operation under the shard lock) happens only when the
// remaining lifetime falls below the threshold — not on every Get.
func TestShardedCacheHysteresisTouch(t *testing.T) {
	sc := NewShardedCache(ShardedCacheConfig[uint64, *int]{
		NumShards:         1,
		TTL:               200 * time.Millisecond,
		KeyToShard:        Uint64KeyToShard,
		TouchRefreshBelow: 80 * time.Millisecond,
	})
	v := 42

	sc.Set(1, &v, ttlcache.DefaultTTL)
	firstExpiry := sc.Get(1).ExpiresAt()

	// Well above the threshold: Get must NOT extend the TTL.
	if got := sc.Get(1).ExpiresAt(); got.After(firstExpiry) {
		t.Fatalf("Get above threshold extended TTL: %v -> %v", firstExpiry, got)
	}

	// Age into the refresh window; a Get must now extend the TTL.
	time.Sleep(140 * time.Millisecond)
	if item := sc.Get(1); item == nil {
		t.Fatal("entry expired before refresh window was exercised")
	}
	refreshed := sc.Get(1).ExpiresAt()
	if !refreshed.After(firstExpiry) {
		t.Fatalf("Get below threshold did not refresh TTL (%v vs %v)", refreshed, firstExpiry)
	}

	// The refresh keeps the entry resident past its original expiry —
	// touch-on-hit semantics preserved.
	time.Sleep(100 * time.Millisecond) // beyond firstExpiry
	if sc.Get(1) == nil {
		t.Fatal("actively-read entry was evicted despite hysteresis refresh")
	}

	// A refresh must not fire eviction callbacks (Set on an existing key
	// updates in place).
	var evictions atomic.Int32
	sc2 := NewShardedCache(ShardedCacheConfig[uint64, *int]{
		NumShards:         1,
		TTL:               200 * time.Millisecond,
		KeyToShard:        Uint64KeyToShard,
		TouchRefreshBelow: 199 * time.Millisecond, // refresh on nearly every Get
	})
	sc2.OnEviction(func(_ context.Context, _ ttlcache.EvictionReason, _ *ttlcache.Item[uint64, *int]) {
		evictions.Add(1)
	})
	sc2.Set(2, &v, ttlcache.DefaultTTL)
	for i := 0; i < 5; i++ {
		if sc2.Get(2) == nil {
			t.Fatal("entry vanished during refresh loop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := evictions.Load(); n != 0 {
		t.Fatalf("hysteresis refreshes fired %d eviction callbacks", n)
	}
}
