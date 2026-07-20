package cachebench

import (
	"sync"
	"sync/atomic"
	"time"

	theine "github.com/Yiling-J/theine-go"
)

// theineAdapter wraps theine-go (W-TinyLFU with timer-wheel expiry).
//
// API-fit notes discovered while building this adapter (see RECOMMENDATION.md):
//   - A maximum size is MANDATORY (NewBuilder(maxsize)). The adapter sizes it
//     at 2x expected entries so capacity eviction stays out of the way, but a
//     bounded cache with admission control is a structural mismatch for
//     Golbat, where silently dropping a live entity breaks correctness.
//   - Set/SetWithTTL return a bool: a write can be REJECTED. The adapter
//     counts rejections (SetRejects) so benchmarks can report them.
//   - No touch-on-hit: expiry is fixed at write time. The adapter emulates
//     touch by re-issuing SetWithTTL on every Get when TouchOnHit is set —
//     the cheapest mechanism theine offers, and it turns every read into a
//     write. This is measured deliberately (benchmark 2).
//   - No per-call GetOrSetFunc: loaders are fixed at Build time. The adapter
//     emulates single-winner with a striped-mutex side path; the extra
//     machinery is itself part of the API-fit verdict.
//   - RemovalListener runs synchronously on theine's maintenance goroutine
//     and distinguishes REMOVED/EVICTED/EXPIRED.
type theineAdapter struct {
	c          *theine.Cache[uint64, *Entity]
	touchOnHit bool
	setRejects atomic.Int64
	// getOrSetMu stripes emulate single-winner creation (theine has no
	// ComputeIfAbsent equivalent).
	getOrSetMu [256]sync.Mutex
}

func NewTheineAdapter(cfg Config) BenchCache {
	maxSize := int64(cfg.ExpectedEntries) * 2
	if maxSize <= 0 {
		maxSize = 1 << 24 // ~16.7M default headroom
	}
	a := &theineAdapter{touchOnHit: cfg.TouchOnHit}
	b := theine.NewBuilder[uint64, *Entity](maxSize)
	if cfg.OnEvict != nil {
		onEvict := cfg.OnEvict
		b.RemovalListener(func(key uint64, value *Entity, reason theine.RemoveReason) {
			switch reason {
			case theine.EXPIRED:
				onEvict(key, value, EvictExpired)
			case theine.REMOVED:
				onEvict(key, value, EvictDeleted)
			}
			// EVICTED (capacity) has no Golbat equivalent; if it fires the
			// candidate is already breaking the unbounded-cache contract.
		})
	}
	c, err := b.Build()
	if err != nil {
		panic(err)
	}
	a.c = c
	return a
}

func (a *theineAdapter) Get(key uint64) (*Entity, bool) {
	v, ok := a.c.Get(key)
	if !ok {
		return nil, false
	}
	if a.touchOnHit {
		// theine cannot extend an entry's life on read; the only mechanism
		// is a full re-Set with a fresh TTL. Golbat's touch semantics extend
		// by the entry's original TTL, which theine doesn't retain, so the
		// adapter cannot even faithfully reproduce the semantics — it uses
		// a fixed 60m here purely so benchmark 2 can price the mechanism.
		if !a.c.SetWithTTL(key, v, 1, 60*time.Minute) {
			a.setRejects.Add(1)
		}
	}
	return v, true
}

func (a *theineAdapter) Set(key uint64, v *Entity, ttl time.Duration) {
	if !a.c.SetWithTTL(key, v, 1, ttl) {
		a.setRejects.Add(1)
	}
}

func (a *theineAdapter) GetOrSetFunc(key uint64, factory func() *Entity, ttl time.Duration) (*Entity, bool) {
	if v, ok := a.c.Get(key); ok {
		return v, true
	}
	mu := &a.getOrSetMu[key%256]
	mu.Lock()
	defer mu.Unlock()
	if v, ok := a.c.Get(key); ok {
		return v, true
	}
	v := factory()
	if !a.c.SetWithTTL(key, v, 1, ttl) {
		a.setRejects.Add(1)
	}
	return v, false
}

func (a *theineAdapter) Delete(key uint64) {
	a.c.Delete(key)
}

func (a *theineAdapter) Len() int {
	return a.c.Len()
}

func (a *theineAdapter) Range(fn func(key uint64, v *Entity) bool) {
	a.c.Range(fn)
}

func (a *theineAdapter) Close() {
	a.c.Close()
}

// SetRejects reports how many writes theine's admission policy rejected.
func (a *theineAdapter) SetRejects() int64 {
	return a.setRejects.Load()
}
