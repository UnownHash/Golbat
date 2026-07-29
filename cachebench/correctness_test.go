package cachebench

// Cross-candidate correctness tests. These are the race-verified half of
// benchmark 4 plus the highest-risk items from the brief's conversion-sketch
// appendix (points 1 and 3). Run with -race:
//
//	go test -race -run 'TestSingleWinner|TestReplacement|TestEvictReason' ./...
import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func testConfig(onEvict func(key uint64, v *Entity, reason EvictReason)) Config {
	return Config{
		Shards:          8,
		DefaultTTL:      time.Hour,
		ExpectedEntries: 10_000,
		SweepInterval:   200 * time.Millisecond,
		OnEvict:         onEvict,
	}
}

// Appendix point 1: two racers MUST receive the same pointer, and the
// factory must run at most once per key (single-winner).
func TestSingleWinnerAllCandidates(t *testing.T) {
	const keys = 1000
	const goroutines = 96
	for _, name := range envCandidates() {
		t.Run(name, func(t *testing.T) {
			cache := Candidates[name](testConfig(nil))
			defer cache.Close()

			var factoryCalls [keys]atomic.Int64
			var winners [keys]atomic.Pointer[Entity]
			var wg sync.WaitGroup
			start := make(chan struct{})
			errCh := make(chan error, goroutines)
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for k := uint64(0); k < keys; k++ {
						v, _ := cache.GetOrSetFunc(k, func() *Entity {
							factoryCalls[k].Add(1)
							return newEntity(k)
						}, time.Hour)
						if v == nil {
							errCh <- fmt.Errorf("key %d: nil value", k)
							return
						}
						if !winners[k].CompareAndSwap(nil, v) && winners[k].Load() != v {
							errCh <- fmt.Errorf("key %d: racers got different pointers %p vs %p",
								k, unsafe.Pointer(v), unsafe.Pointer(winners[k].Load()))
							return
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errCh)
			for err := range errCh {
				t.Fatal(err)
			}
			for k := range factoryCalls {
				if n := factoryCalls[k].Load(); n != 1 {
					t.Fatalf("key %d: factory ran %d times, want 1", k, n)
				}
			}
		})
	}
}

// Appendix point 3 (highest conversion risk): re-Setting a LIVE entry must
// not surface any eviction event through the adapter. Caffeine-lineage
// caches fire a Replacement deletion cause on overwrite; if the adapter let
// it through, Golbat's handlePokemonEviction/deferFortEviction would
// enqueue bogus tree deletes for a live entity. ttlcache (the semantics
// Golbat is built around) fires nothing on Set-overwrite.
func TestReplacementFiresNoEviction(t *testing.T) {
	for _, name := range envCandidates() {
		t.Run(name, func(t *testing.T) {
			type event struct {
				key    uint64
				reason EvictReason
			}
			var mu sync.Mutex
			var events []event
			cache := Candidates[name](testConfig(func(key uint64, _ *Entity, reason EvictReason) {
				mu.Lock()
				events = append(events, event{key, reason})
				mu.Unlock()
			}))
			defer cache.Close()

			for i := 0; i < 100; i++ {
				cache.Set(42, newEntity(42), time.Hour) // 99 live overwrites
			}
			cache.Set(43, newEntity(43), time.Hour)
			// Let async policies (otter maintenance etc.) process the writes.
			time.Sleep(2 * time.Second)
			mu.Lock()
			preDelete := len(events)
			mu.Unlock()
			if preDelete != 0 {
				t.Fatalf("%d eviction events fired from overwrites of live entries", preDelete)
			}

			cache.Delete(42)
			deadline := time.Now().Add(5 * time.Second)
			for {
				mu.Lock()
				n := len(events)
				mu.Unlock()
				if n >= 1 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(events) != 1 || events[0].key != 42 || events[0].reason != EvictDeleted {
				t.Fatalf("after Delete want exactly [{42 Deleted}], got %v", events)
			}
		})
	}
}

// Expiry must surface as EvictExpired, explicit deletes as EvictDeleted.
func TestEvictReasonMapping(t *testing.T) {
	for _, name := range envCandidates() {
		t.Run(name, func(t *testing.T) {
			type event struct {
				key    uint64
				reason EvictReason
			}
			ch := make(chan event, 16)
			cache := Candidates[name](testConfig(func(key uint64, _ *Entity, reason EvictReason) {
				ch <- event{key, reason}
			}))
			defer cache.Close()

			cache.Set(1, newEntity(1), 1500*time.Millisecond)
			cache.Set(2, newEntity(2), time.Hour)
			cache.Delete(2)

			got := map[uint64]EvictReason{}
			timeout := time.After(15 * time.Second)
			for len(got) < 2 {
				select {
				case ev := <-ch:
					if prev, dup := got[ev.key]; dup {
						t.Fatalf("key %d fired twice (%v then %v)", ev.key, prev, ev.reason)
					}
					got[ev.key] = ev.reason
				case <-timeout:
					t.Fatalf("timed out; events so far: %v", got)
				}
			}
			if got[1] != EvictExpired {
				t.Errorf("key 1 reason = %v, want EvictExpired", got[1])
			}
			if got[2] != EvictDeleted {
				t.Errorf("key 2 reason = %v, want EvictDeleted", got[2])
			}
		})
	}
}

// The hysteresis baseline must actually keep hot entries alive (touch
// semantics preserved) — a Get inside the refresh window re-stamps the TTL.
func TestHysteresisKeepsHotEntriesResident(t *testing.T) {
	cfg := Config{
		Shards:          4,
		DefaultTTL:      2 * time.Second,
		ExpectedEntries: 100,
		SweepInterval:   50 * time.Millisecond,
	}
	// Mirror the candidate wiring: threshold = TTL/4, touch off underneath.
	cfg.TouchRefreshBelow = cfg.DefaultTTL / 4
	cache := NewTTLCacheAdapter(cfg)
	defer cache.Close()

	cache.Set(1, newEntity(1), 2*time.Second)
	deadline := time.Now().Add(6 * time.Second) // 3x the TTL
	for time.Now().Before(deadline) {
		if _, ok := cache.Get(1); !ok {
			t.Fatal("hot entry expired despite hysteresis touch")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
