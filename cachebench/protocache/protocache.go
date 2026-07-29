// Package protocache is the purpose-built TTL cache prototype from the
// cache-investigation brief (candidate D). It is NOT wired into Golbat —
// integration is a separate decision.
//
// Design (sized against Golbat's workload, see docs/cache-investigation-brief.md):
//
//   - Storage: xsync.Map (lock-free reads, per-bucket locking on writes).
//   - Expiry: an atomic per-entry deadline. Get never returns an expired
//     entry (lazy expiry), so correctness never depends on sweep timing.
//   - Touch-on-hit is a single atomic store of a new deadline — no heap
//     fix-up, no shard lock (ttlcache pays an O(log n) heap update under the
//     shard RWMutex for every touching Get).
//   - Proactive eviction: a coarse timing wheel. Buckets are keyed by
//     absolute tick index (deadline / tick); a single evictor goroutine
//     drains all due buckets once per tick. Touched/superseded entries are
//     lazily re-filed when their old bucket drains, so touches stay O(1).
//   - Eviction callbacks: delivered by ONE dispatcher goroutine in eviction
//     order through a bounded queue — never a goroutine per event
//     (ttlcache's OnEviction spawns one goroutine per evicted item).
//
// Callback semantics (deliberately matching what Golbat actually relies on):
// a callback fires exactly once when a CURRENT entry leaves the cache — by
// expiry sweep (ReasonExpired) or Delete (ReasonDeleted). Overwrites (Set /
// GetOrSetFunc replacing an expired-but-unswept entry) supersede silently,
// which is the behavior Golbat's eviction guards already assume (a re-cached
// entity's callback must skip cleanup).
package protocache

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
)

// Reason says why an entry left the cache.
type Reason int

const (
	ReasonExpired Reason = iota + 1
	ReasonDeleted
)

const wheelStripes = 64 // must be a power of two

// Config configures a Cache.
type Config[K comparable, V any] struct {
	// TouchOnHit: a Get on a live entry extends its deadline by the entry's
	// own TTL (spawnpoint/fort pattern). When false, reads never move the
	// deadline (pokemon pattern).
	TouchOnHit bool
	// WheelTick is the timing-wheel bucket width and evictor cadence.
	// Defaults to 1 minute (per the brief); benchmarks use 1s to match
	// otter/theine maintenance cadence. Expired entries are invisible to
	// Get regardless of tick width — the wheel only bounds how long dead
	// entries hold memory and how late callbacks fire.
	WheelTick time.Duration
	// QueueSize bounds the eviction-dispatch queue (default 65536). The
	// evictor blocks when it is full, so the OnEviction consumer must be
	// fast and non-blocking — the same contract Golbat's tree-writer
	// callbacks already meet (non-blocking TryEnqueue).
	QueueSize int
	// OnEviction, when non-nil, receives every expiry/delete exactly once,
	// in order, on a single dispatcher goroutine.
	OnEviction func(key K, value V, reason Reason)
	// Now overrides the clock (tests). Returns unix nanos.
	Now func() int64
}

type entry[V any] struct {
	value    V
	deadline atomic.Int64 // unix nanos; authoritative for expiry
	ttl      int64        // nanos; used by touch to extend the deadline
	wheelIdx atomic.Int64 // bucket index this entry is currently filed under
}

func (e *entry[V]) expired(now int64) bool {
	return now >= e.deadline.Load()
}

type wheelStripe[K comparable] struct {
	mu      sync.Mutex
	buckets map[int64][]K
}

type evictEvent[K comparable, V any] struct {
	key    K
	value  V
	reason Reason
}

// Cache is a TTL cache with lock-free reads, O(1) touch, coarse proactive
// eviction, and single-dispatcher eviction callbacks.
type Cache[K comparable, V any] struct {
	cfg     Config[K, V]
	tick    int64 // nanos
	now     func() int64
	m       *xsync.Map[K, *entry[V]]
	seed    maphash.Seed
	stripes [wheelStripes]wheelStripe[K]

	evictCh chan evictEvent[K, V]
	stopCh  chan struct{}
	wg      sync.WaitGroup
	closed  atomic.Bool
}

// New builds and starts a Cache (evictor + dispatcher goroutines run until
// Close).
func New[K comparable, V any](cfg Config[K, V]) *Cache[K, V] {
	if cfg.WheelTick <= 0 {
		cfg.WheelTick = time.Minute
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 65536
	}
	now := cfg.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixNano() }
	}
	c := &Cache[K, V]{
		cfg:    cfg,
		tick:   int64(cfg.WheelTick),
		now:    now,
		m:      xsync.NewMap[K, *entry[V]](),
		seed:   maphash.MakeSeed(),
		stopCh: make(chan struct{}),
	}
	for i := range c.stripes {
		c.stripes[i].buckets = make(map[int64][]K)
	}
	if cfg.OnEviction != nil {
		c.evictCh = make(chan evictEvent[K, V], cfg.QueueSize)
		c.wg.Add(1)
		go c.dispatchLoop()
	}
	c.wg.Add(1)
	go c.evictLoop()
	return c
}

// Get returns the live value for key. Expired entries are never returned,
// regardless of whether the evictor has swept them yet. On a live hit with
// TouchOnHit, the deadline is extended by the entry's own TTL with a single
// atomic store; the wheel is fixed up lazily when the old bucket drains.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	e, ok := c.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	now := c.now()
	if e.expired(now) {
		var zero V
		return zero, false
	}
	if c.cfg.TouchOnHit {
		e.deadline.Store(now + e.ttl)
	}
	return e.value, true
}

// Has reports whether a live entry exists for key (no touch).
func (c *Cache[K, V]) Has(key K) bool {
	e, ok := c.m.Load(key)
	return ok && !e.expired(c.now())
}

// Set stores value with its own TTL, replacing any existing entry. Replaced
// entries are superseded silently (no eviction callback), matching ttlcache
// Set-overwrite behavior.
func (c *Cache[K, V]) Set(key K, value V, ttl time.Duration) {
	now := c.now()
	e := &entry[V]{value: value, ttl: int64(ttl)}
	e.deadline.Store(now + int64(ttl))
	c.m.Store(key, e)
	c.file(key, e)
}

// GetOrSetFunc returns the existing live value for key, or atomically
// creates one via factory. found=true means an existing live entry was
// returned. factory runs at most once per absent key even under races
// (single-winner, via xsync.Map.Compute's per-key serialization); it must be
// fast and must not access the cache. An expired-but-unswept entry is
// replaced as if absent.
func (c *Cache[K, V]) GetOrSetFunc(key K, factory func() V, ttl time.Duration) (V, bool) {
	now := c.now()
	var out *entry[V]
	found := true
	c.m.Compute(key, func(old *entry[V], loaded bool) (*entry[V], xsync.ComputeOp) {
		if loaded && !old.expired(now) {
			out = old
			return old, xsync.CancelOp
		}
		found = false
		e := &entry[V]{value: factory(), ttl: int64(ttl)}
		e.deadline.Store(now + int64(ttl))
		out = e
		return e, xsync.UpdateOp
	})
	if !found {
		c.file(key, out)
	} else if c.cfg.TouchOnHit {
		out.deadline.Store(now + out.ttl)
	}
	return out.value, found
}

// UpdateTTL gives an existing live entry a new TTL starting now. Missing or
// expired entries are left alone.
func (c *Cache[K, V]) UpdateTTL(key K, ttl time.Duration) {
	e, ok := c.m.Load(key)
	if !ok {
		return
	}
	now := c.now()
	if e.expired(now) {
		return
	}
	// ttl is fixed per entry generation; deadline is what moves. Entries
	// needing a different ttl base get re-filed by the wheel on drain.
	e.deadline.Store(now + int64(ttl))
	c.file(key, e)
}

// Delete removes the entry and dispatches ReasonDeleted (like ttlcache,
// even if the entry had already expired but not yet been swept).
func (c *Cache[K, V]) Delete(key K) {
	e, loaded := c.m.LoadAndDelete(key)
	if !loaded {
		return
	}
	c.dispatch(key, e.value, ReasonDeleted)
}

// Len returns the number of stored entries, including expired-but-unswept
// ones (bounded by one wheel tick of garbage).
func (c *Cache[K, V]) Len() int {
	return c.m.Size()
}

// Range iterates live entries. Expired-but-unswept entries are skipped.
func (c *Cache[K, V]) Range(fn func(key K, value V) bool) {
	now := c.now()
	c.m.Range(func(key K, e *entry[V]) bool {
		if e.expired(now) {
			return true
		}
		return fn(key, e.value)
	})
}

// Close stops the evictor and dispatcher. Pending queued callbacks are
// delivered before Close returns. The cache must not be used after Close.
func (c *Cache[K, V]) Close() {
	if c.closed.Swap(true) {
		return
	}
	close(c.stopCh)
	c.wg.Wait()
}

// --- timing wheel ---

func (c *Cache[K, V]) stripe(key K) *wheelStripe[K] {
	return &c.stripes[maphash.Comparable(c.seed, key)&(wheelStripes-1)]
}

// file records that e (currently stored under key) should be visited by the
// evictor when its deadline bucket drains.
func (c *Cache[K, V]) file(key K, e *entry[V]) {
	idx := e.deadline.Load() / c.tick
	e.wheelIdx.Store(idx)
	s := c.stripe(key)
	s.mu.Lock()
	s.buckets[idx] = append(s.buckets[idx], key)
	s.mu.Unlock()
}

func (c *Cache[K, V]) evictLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.cfg.WheelTick)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.drainDue()
		}
	}
}

// drainDue processes every bucket whose tick index is <= the current one
// (catching up if ticks were missed). For each filed key it either skips a
// ghost (entry re-filed elsewhere), re-files a touched entry under its new
// deadline, or removes a genuinely expired entry and dispatches its
// callback.
func (c *Cache[K, V]) drainDue() {
	nowIdx := c.now() / c.tick
	type dueBucket struct {
		idx  int64
		keys []K
	}
	for i := range c.stripes {
		s := &c.stripes[i]
		var due []dueBucket
		s.mu.Lock()
		for idx, keys := range s.buckets {
			if idx <= nowIdx {
				due = append(due, dueBucket{idx: idx, keys: keys})
				delete(s.buckets, idx)
			}
		}
		s.mu.Unlock()
		for _, b := range due {
			for _, key := range b.keys {
				c.sweepKey(key, b.idx)
			}
		}
	}
}

// sweepKey handles one key filed under bucket drainIdx. Exactly one bucket
// "owns" an entry at any time — the one its wheelIdx points to (file()
// maintains that invariant: it stamps wheelIdx, then appends to that same
// bucket). References from any other bucket are ghosts left behind by
// supersedes/re-files and are skipped, which is what keeps re-filing from
// accumulating duplicates.
func (c *Cache[K, V]) sweepKey(key K, drainIdx int64) {
	e, ok := c.m.Load(key)
	if !ok {
		return // deleted (or superseded then deleted): nothing to do
	}
	if e.wheelIdx.Load() != drainIdx {
		return // ghost: the current entry is owned by a different bucket
	}
	removed, refile := false, false
	c.m.Compute(key, func(cur *entry[V], loaded bool) (*entry[V], xsync.ComputeOp) {
		if !loaded || cur != e {
			return cur, xsync.CancelOp // replaced by a racing Set: its bucket owns it
		}
		// Deadline is re-checked inside Compute so a touch that lands
		// before this point wins: the entry stays and is re-filed below.
		if cur.deadline.Load() > c.now() {
			refile = true
			return cur, xsync.CancelOp
		}
		removed = true
		return nil, xsync.DeleteOp
	})
	if removed {
		c.dispatch(key, e.value, ReasonExpired)
	} else if refile {
		c.file(key, e)
	}
}

// --- eviction dispatch ---

func (c *Cache[K, V]) dispatch(key K, value V, reason Reason) {
	if c.evictCh == nil {
		return
	}
	select {
	case c.evictCh <- evictEvent[K, V]{key: key, value: value, reason: reason}:
	case <-c.stopCh:
	}
}

func (c *Cache[K, V]) dispatchLoop() {
	defer c.wg.Done()
	for {
		select {
		case ev := <-c.evictCh:
			c.cfg.OnEviction(ev.key, ev.value, ev.reason)
		case <-c.stopCh:
			// Drain what's already queued, then exit.
			for {
				select {
				case ev := <-c.evictCh:
					c.cfg.OnEviction(ev.key, ev.value, ev.reason)
				default:
					return
				}
			}
		}
	}
}
