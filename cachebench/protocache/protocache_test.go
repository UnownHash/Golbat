package protocache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock lets tests control time without sleeping.
type fakeClock struct{ now atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.now.Store(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) Now() int64              { return c.now.Load() }
func (c *fakeClock) Advance(d time.Duration) { c.now.Add(int64(d)) }

func newTestCache(t *testing.T, cfg Config[uint64, string], clock *fakeClock) *Cache[uint64, string] {
	t.Helper()
	cfg.Now = clock.Now
	if cfg.WheelTick <= 0 {
		// Short real tick so the evictor goroutine drains fake-clock-due
		// buckets quickly.
		cfg.WheelTick = 5 * time.Millisecond
	}
	c := New(cfg)
	t.Cleanup(c.Close)
	return c
}

func TestSetGetBasic(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{}, clock)

	c.Set(1, "a", time.Minute)
	if v, ok := c.Get(1); !ok || v != "a" {
		t.Fatalf("Get(1) = %q, %v; want a, true", v, ok)
	}
	if _, ok := c.Get(2); ok {
		t.Fatal("Get(2) should miss")
	}
	if !c.Has(1) || c.Has(2) {
		t.Fatal("Has mismatch")
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

// Get must never return an expired entry, even before any sweep has run.
func TestNeverReturnExpired(t *testing.T) {
	clock := newFakeClock()
	// Huge wheel tick: the evictor will effectively never sweep during the
	// test, so only lazy expiry protects the reader.
	c := newTestCache(t, Config[uint64, string]{WheelTick: time.Hour}, clock)

	c.Set(1, "a", time.Minute)
	clock.Advance(time.Minute - time.Nanosecond)
	if _, ok := c.Get(1); !ok {
		t.Fatal("entry expired too early")
	}
	clock.Advance(time.Nanosecond)
	if _, ok := c.Get(1); ok {
		t.Fatal("Get returned an expired entry")
	}
	if c.Has(1) {
		t.Fatal("Has returned true for an expired entry")
	}
}

func TestTouchOnHitExtends(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{TouchOnHit: true, WheelTick: time.Hour}, clock)

	c.Set(1, "a", time.Minute)
	for i := 0; i < 10; i++ {
		clock.Advance(45 * time.Second) // always within the (touched) TTL
		if _, ok := c.Get(1); !ok {
			t.Fatalf("touched entry expired on iteration %d", i)
		}
	}
	// Stop touching: entry dies one TTL after the last touch.
	clock.Advance(time.Minute)
	if _, ok := c.Get(1); ok {
		t.Fatal("entry survived a full TTL without touches")
	}
}

func TestNoTouchWhenDisabled(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{TouchOnHit: false, WheelTick: time.Hour}, clock)

	c.Set(1, "a", time.Minute)
	clock.Advance(45 * time.Second)
	if _, ok := c.Get(1); !ok {
		t.Fatal("entry expired too early")
	}
	clock.Advance(30 * time.Second) // 75s > 60s TTL despite the recent Get
	if _, ok := c.Get(1); ok {
		t.Fatal("read extended TTL despite TouchOnHit=false")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// The evictor must proactively remove expired entries and deliver exactly
// one ReasonExpired callback per entry, on one dispatcher goroutine.
func TestProactiveEvictionAndCallbacks(t *testing.T) {
	clock := newFakeClock()
	var mu sync.Mutex
	got := map[uint64]int{}
	var reasons []Reason
	c := newTestCache(t, Config[uint64, string]{
		OnEviction: func(key uint64, _ string, reason Reason) {
			mu.Lock()
			got[key]++
			reasons = append(reasons, reason)
			mu.Unlock()
		},
	}, clock)

	const n = 1000
	for i := uint64(0); i < n; i++ {
		c.Set(i, "v", time.Minute)
	}
	clock.Advance(2 * time.Minute)

	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == n
	})
	mu.Lock()
	defer mu.Unlock()
	for k, count := range got {
		if count != 1 {
			t.Fatalf("key %d got %d callbacks, want 1", k, count)
		}
	}
	for _, r := range reasons {
		if r != ReasonExpired {
			t.Fatalf("reason = %v, want ReasonExpired", r)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d after full expiry, want 0", c.Len())
	}
}

func TestDeleteFiresDeletedReason(t *testing.T) {
	clock := newFakeClock()
	ch := make(chan Reason, 1)
	c := newTestCache(t, Config[uint64, string]{
		OnEviction: func(_ uint64, _ string, reason Reason) { ch <- reason },
	}, clock)

	c.Set(1, "a", time.Minute)
	c.Delete(1)
	select {
	case r := <-ch:
		if r != ReasonDeleted {
			t.Fatalf("reason = %v, want ReasonDeleted", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no callback delivered")
	}
	if _, ok := c.Get(1); ok {
		t.Fatal("deleted entry still present")
	}
	// Deleting again must not fire another callback.
	c.Delete(1)
	select {
	case <-ch:
		t.Fatal("second Delete fired a callback")
	case <-time.After(50 * time.Millisecond):
	}
}

// A touched entry must survive wheel drains: the drain re-files it rather
// than evicting, and its callback fires only after touches stop.
func TestTouchedEntrySurvivesDrains(t *testing.T) {
	clock := newFakeClock()
	var evicted atomic.Int64
	c := newTestCache(t, Config[uint64, string]{
		TouchOnHit: true,
		OnEviction: func(_ uint64, _ string, _ Reason) { evicted.Add(1) },
	}, clock)

	c.Set(1, "a", time.Minute)
	for i := 0; i < 5; i++ {
		clock.Advance(30 * time.Second)
		if _, ok := c.Get(1); !ok { // touch: pushes deadline out again
			t.Fatalf("entry lost on iteration %d", i)
		}
		time.Sleep(15 * time.Millisecond) // let the evictor drain due buckets
	}
	if n := evicted.Load(); n != 0 {
		t.Fatalf("touched entry produced %d eviction callbacks", n)
	}
	clock.Advance(2 * time.Minute) // no more touches
	waitFor(t, 5*time.Second, func() bool { return evicted.Load() == 1 })
}

// Set over an existing key supersedes silently: no callback for the old
// generation, and the old wheel filing must not evict or duplicate the new
// entry.
func TestSupersedeSilently(t *testing.T) {
	clock := newFakeClock()
	var evictions atomic.Int64
	c := newTestCache(t, Config[uint64, string]{
		OnEviction: func(_ uint64, _ string, _ Reason) { evictions.Add(1) },
	}, clock)

	c.Set(1, "old", time.Minute)
	clock.Advance(30 * time.Second)
	c.Set(1, "new", time.Hour) // supersedes before old deadline
	clock.Advance(45 * time.Second)
	time.Sleep(30 * time.Millisecond) // old generation's bucket drains (ghost)
	if v, ok := c.Get(1); !ok || v != "new" {
		t.Fatalf("Get = %q, %v; want new, true", v, ok)
	}
	if n := evictions.Load(); n != 0 {
		t.Fatalf("supersede produced %d callbacks, want 0", n)
	}
	// The new generation still expires at ITS deadline.
	clock.Advance(time.Hour)
	waitFor(t, 5*time.Second, func() bool { return evictions.Load() == 1 })
}

// GetOrSetFunc: exactly one factory invocation per key under heavy races.
func TestGetOrSetFuncSingleWinner(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{}, clock)

	const keys = 100
	const goroutines = 96
	var factoryCalls [keys]atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := uint64(0); k < keys; k++ {
				v, _ := c.GetOrSetFunc(k, func() string {
					factoryCalls[k].Add(1)
					return "v"
				}, time.Minute)
				if v != "v" {
					t.Errorf("GetOrSetFunc returned %q", v)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	for k := range factoryCalls {
		if n := factoryCalls[k].Load(); n != 1 {
			t.Fatalf("key %d: factory ran %d times, want 1", k, n)
		}
	}
}

// GetOrSetFunc must treat an expired-but-unswept entry as absent.
func TestGetOrSetFuncReplacesExpired(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{WheelTick: time.Hour}, clock)

	c.Set(1, "old", time.Minute)
	clock.Advance(2 * time.Minute)
	v, found := c.GetOrSetFunc(1, func() string { return "new" }, time.Minute)
	if found || v != "new" {
		t.Fatalf("GetOrSetFunc = %q, found=%v; want new, false", v, found)
	}
}

func TestUpdateTTL(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{WheelTick: time.Hour}, clock)

	c.Set(1, "a", time.Minute)
	clock.Advance(30 * time.Second)
	c.UpdateTTL(1, time.Hour)
	clock.Advance(45 * time.Minute)
	if _, ok := c.Get(1); !ok {
		t.Fatal("UpdateTTL did not extend the entry")
	}
	clock.Advance(16 * time.Minute)
	if _, ok := c.Get(1); ok {
		t.Fatal("entry outlived its updated TTL")
	}
}

func TestRangeSkipsExpired(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(t, Config[uint64, string]{WheelTick: time.Hour}, clock)

	c.Set(1, "live", time.Hour)
	c.Set(2, "dead", time.Minute)
	clock.Advance(2 * time.Minute)
	seen := map[uint64]string{}
	c.Range(func(k uint64, v string) bool {
		seen[k] = v
		return true
	})
	if len(seen) != 1 || seen[1] != "live" {
		t.Fatalf("Range saw %v, want only key 1", seen)
	}
}

// Callbacks are delivered in eviction order on a single goroutine.
func TestCallbackOrderingSingleDispatcher(t *testing.T) {
	clock := newFakeClock()
	var mu sync.Mutex
	var order []uint64
	var concurrent, maxConcurrent atomic.Int64
	c := newTestCache(t, Config[uint64, string]{
		OnEviction: func(key uint64, _ string, _ Reason) {
			if n := concurrent.Add(1); n > maxConcurrent.Load() {
				maxConcurrent.Store(n)
			}
			mu.Lock()
			order = append(order, key)
			mu.Unlock()
			concurrent.Add(-1)
		},
	}, clock)

	// Deletes go straight to the dispatcher from this goroutine, so their
	// order is deterministic.
	for i := uint64(0); i < 500; i++ {
		c.Set(i, "v", time.Hour)
	}
	for i := uint64(0); i < 500; i++ {
		c.Delete(i)
	}
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 500
	})
	mu.Lock()
	defer mu.Unlock()
	for i, k := range order {
		if k != uint64(i) {
			t.Fatalf("order[%d] = %d, want %d", i, k, i)
		}
	}
	if maxConcurrent.Load() != 1 {
		t.Fatalf("callbacks ran on %d goroutines concurrently, want 1", maxConcurrent.Load())
	}
}

// Hammer the cache from many goroutines with mixed ops while time advances,
// as a -race exerciser. Correctness assertion: Get never returns a value
// whose entry was set with an already-elapsed deadline.
func TestConcurrentMixedOps(t *testing.T) {
	clock := newFakeClock()
	var evictions atomic.Int64
	c := newTestCache(t, Config[uint64, string]{
		TouchOnHit: true,
		OnEviction: func(_ uint64, _ string, _ Reason) { evictions.Add(1) },
	}, clock)

	const goroutines = 16
	const opsPerG = 5000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := seed*2654435761 + 1
			for i := 0; i < opsPerG; i++ {
				rng = rng*6364136223846793005 + 1442695040888963407
				key := (rng >> 33) % 512
				switch rng % 5 {
				case 0:
					c.Set(key, "v", time.Duration(1+rng%120)*time.Second)
				case 1:
					c.Delete(key)
				case 2:
					c.GetOrSetFunc(key, func() string { return "f" }, time.Minute)
				case 3:
					c.UpdateTTL(key, time.Minute)
				default:
					c.Get(key)
				}
				if i%1000 == 0 {
					clock.Advance(time.Second)
				}
			}
		}(uint64(g))
	}
	wg.Wait()
}
