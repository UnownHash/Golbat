package cachebench

import (
	"time"

	"github.com/maypok86/otter/v2"
)

// otterAdapter wraps otter v2 (Caffeine-style W-TinyLFU cache).
//
// API-fit notes discovered while building this adapter (see RECOMMENDATION.md):
//   - otter has no per-entry TTL argument on Set. Variable per-entry TTLs
//     require a custom ExpiryCalculator that derives the duration from the
//     entry, so the adapter stores the desired TTL alongside the value
//     (otterVal wrapper). This is Caffeine's "variable expiry" pattern.
//   - Touch-on-hit maps to ExpireAfterRead returning the entry's own TTL;
//     touch-off maps to returning ExpiresAfter() (unchanged deadline).
//   - OnDeletion is delivered via Options.Executor whose DEFAULT is
//     `go fn()` — one goroutine per deletion event, the same shape that
//     produced Golbat's goroutine bomb. The adapter instead uses
//     OnAtomicDeletion, which runs inline on the goroutine performing the
//     deletion (maintenance goroutine for expiry): bounded, ordered-ish, no
//     goroutine spawn — but the handler must be fast and non-blocking.
//   - Unbounded (TTL-only) operation: leave MaximumSize unset.
//   - Single-winner creation: ComputeIfAbsent.
type otterVal struct {
	v   *Entity
	ttl time.Duration
}

type otterExpiry struct{ touchOnHit bool }

func (e otterExpiry) ExpireAfterCreate(entry otter.Entry[uint64, otterVal]) time.Duration {
	return entry.Value.ttl
}

func (e otterExpiry) ExpireAfterUpdate(entry otter.Entry[uint64, otterVal], _ otterVal) time.Duration {
	return entry.Value.ttl
}

func (e otterExpiry) ExpireAfterRead(entry otter.Entry[uint64, otterVal]) time.Duration {
	if e.touchOnHit {
		return entry.Value.ttl
	}
	return entry.ExpiresAfter()
}

type otterAdapter struct {
	c *otter.Cache[uint64, otterVal]
}

func NewOtterAdapter(cfg Config) BenchCache {
	opts := &otter.Options[uint64, otterVal]{
		ExpiryCalculator: otterExpiry{touchOnHit: cfg.TouchOnHit},
	}
	if cfg.ExpectedEntries > 0 {
		opts.InitialCapacity = cfg.ExpectedEntries
	}
	if cfg.OnEvict != nil {
		onEvict := cfg.OnEvict
		opts.OnAtomicDeletion = func(ev otter.DeletionEvent[uint64, otterVal]) {
			switch ev.Cause {
			case otter.CauseExpiration:
				onEvict(ev.Key, ev.Value.v, EvictExpired)
			case otter.CauseInvalidation:
				onEvict(ev.Key, ev.Value.v, EvictDeleted)
			}
			// CauseReplacement/CauseOverflow are not eviction events in
			// Golbat's model (Set-overwrite fires nothing in ttlcache).
		}
	}
	return &otterAdapter{c: otter.Must(opts)}
}

func (a *otterAdapter) Get(key uint64) (*Entity, bool) {
	// GetIfPresent triggers ExpireAfterRead via the expiry calculator, which
	// implements the touch/no-touch policy.
	w, ok := a.c.GetIfPresent(key)
	if !ok {
		return nil, false
	}
	return w.v, true
}

func (a *otterAdapter) Set(key uint64, v *Entity, ttl time.Duration) {
	a.c.Set(key, otterVal{v: v, ttl: ttl})
}

func (a *otterAdapter) GetOrSetFunc(key uint64, factory func() *Entity, ttl time.Duration) (*Entity, bool) {
	created := false
	w, _ := a.c.ComputeIfAbsent(key, func() (otterVal, bool) {
		created = true
		return otterVal{v: factory(), ttl: ttl}, false
	})
	return w.v, !created
}

func (a *otterAdapter) Delete(key uint64) {
	a.c.Invalidate(key)
}

func (a *otterAdapter) Len() int {
	return a.c.EstimatedSize()
}

func (a *otterAdapter) Range(fn func(key uint64, v *Entity) bool) {
	for k, w := range a.c.All() {
		if !fn(k, w.v) {
			return
		}
	}
}

func (a *otterAdapter) Close() {
	a.c.StopAllGoroutines()
}
