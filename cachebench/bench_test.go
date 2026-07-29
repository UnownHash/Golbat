package cachebench

// Benchmark-matrix items 1, 2, 4 and 6 from the brief. Items 3, 5 and 7
// (mass-expiry wave, callback throughput, memory) are scenario tests in
// scenario_test.go because they measure wall-clock phenomena, goroutine
// counts and GC pauses rather than ns/op.
//
// Population size comes from CACHEBENCH_N (default 10M — the brief's
// production sizing); candidate filtering from CACHEBENCH_CANDIDATE
// (run.sh isolates memory-heavy candidates per process).

import (
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func benchConfig(n int, touch bool) Config {
	shards := runtime.NumCPU() // decoder cacheShardCount() default
	if s := os.Getenv("CACHEBENCH_SHARDS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			panic(err)
		}
		shards = v // sensitivity check: production runs up to ~100 shards
	}
	return Config{
		Shards:          shards,
		TouchOnHit:      touch,
		DefaultTTL:      time.Hour,
		ExpectedEntries: n,
		SweepInterval:   time.Second, // match otter/theine 1s maintenance
	}
}

// runConcurrentOps distributes b.N ops over exactly `goroutines` workers
// (b.RunParallel can't go below GOMAXPROCS workers; production runs 96
// decode goroutines). Each worker gets its own rng + zipf over [0, n).
func runConcurrentOps(b *testing.B, goroutines, n int, op func(rng *rand.Rand, zipf *rand.Zipf, i int)) {
	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	per := b.N / goroutines
	for g := 0; g < goroutines; g++ {
		ops := per
		if g == 0 {
			ops += b.N % goroutines
		}
		wg.Add(1)
		go func(g, ops int) {
			defer wg.Done()
			rng, zipf := newZipf(uint64(g)*7919+1, n)
			for i := 0; i < ops; i++ {
				op(rng, zipf, i)
			}
		}(g, ops)
	}
	wg.Wait()
}

// --- Benchmark 1: read-hot 90% Get / 10% Set, zipf keys, 8/32/96 goroutines.
// Models pokemon+spawnpoint steady state (touch off = pokemon; the
// ttlcache-hyst arm carries the production spawnpoint/fort read path).
func BenchmarkReadHot(b *testing.B) {
	n := envEntries()
	for _, name := range envCandidates() {
		b.Run(name, func(b *testing.B) {
			cache := Candidates[name](benchConfig(n, false))
			defer cache.Close()
			preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
				return 55*time.Minute + time.Duration(k%600)*time.Second
			})
			for _, g := range []int{8, 32, 96} {
				b.Run(fmt.Sprintf("g%d", g), func(b *testing.B) {
					runConcurrentOps(b, g, n, func(rng *rand.Rand, zipf *rand.Zipf, _ int) {
						k := zipf.Uint64()
						if rng.IntN(10) == 0 {
							cache.Set(k, newEntity(k), jitteredHourTTL(rng))
						} else {
							cache.Get(k)
						}
					})
				})
			}
		})
	}
}

// --- Benchmark 2: touch cost isolation — 100% Get at 96 goroutines.
// Arms: touch ON, touch OFF, and (for ttlcache-hyst) hysteresis touch.
// Models the spawnpoint per-wild-sighting Get. Touch is load-bearing:
// candidates must make it cheap, not merely support disabling it.
func BenchmarkTouch(b *testing.B) {
	n := envEntries()
	const goroutines = 96
	for _, name := range envCandidates() {
		arms := []bool{true, false}
		if name == "ttlcache-hyst" {
			arms = []bool{true} // hysteresis IS its touch arm; off ≡ ttlcache
		}
		for _, touch := range arms {
			arm := "touch"
			if !touch {
				arm = "notouch"
			}
			if name == "ttlcache-hyst" {
				arm = "hysteresis"
			}
			b.Run(name+"/"+arm, func(b *testing.B) {
				// This body contains a b.Run, so it executes once: the
				// preload is not repeated as the inner benchmark's b.N ramps.
				cache := Candidates[name](benchConfig(n, touch))
				defer cache.Close()
				// Remaining lifetimes uniform in [5m, 65m): a realistic
				// steady-state age mix; ~15% of entries sit below the
				// hysteresis 15m threshold so its refresh path is exercised.
				preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
					return 5*time.Minute + time.Duration(k%3600)*time.Second
				})
				b.Run("get", func(b *testing.B) {
					runConcurrentOps(b, goroutines, n, func(_ *rand.Rand, zipf *rand.Zipf, _ int) {
						cache.Get(zipf.Uint64())
					})
				})
			})
		}
	}
}

// --- Benchmark 4: GetOrSetFunc storm — 96 goroutines racing over the same
// 1k keys (cold-start convoy). Throughput here; single-winner correctness
// is asserted by TestSingleWinnerAllCandidates under -race.
func BenchmarkGetOrSetStorm(b *testing.B) {
	const keys = 1000
	const goroutines = 96
	for _, name := range envCandidates() {
		b.Run(name, func(b *testing.B) {
			cache := Candidates[name](benchConfig(keys, false))
			defer cache.Close()
			var creates atomic.Int64
			runConcurrentOps(b, goroutines, keys, func(_ *rand.Rand, _ *rand.Zipf, i int) {
				k := uint64(i) % keys
				cache.GetOrSetFunc(k, func() *Entity {
					creates.Add(1)
					return newEntity(k)
				}, time.Hour)
			})
			b.StopTimer()
			if c := creates.Load(); c > keys {
				b.Errorf("factory ran %d times for %d keys: not single-winner", c, keys)
			}
		})
	}
}

// --- Benchmark 6: single-goroutine consumer tax — the encounterCache
// pattern (stats worker saturated at ~25k events/sec; each event does a
// Get + Set on one goroutine). ~70% of events are new encounter ids, ~30%
// re-hits of recent ones.
func BenchmarkSingleGoroutineConsumer(b *testing.B) {
	for _, name := range envCandidates() {
		b.Run(name, func(b *testing.B) {
			cache := Candidates[name](benchConfig(1_000_000, false))
			defer cache.Close()
			rng := rand.New(rand.NewPCG(1, 2))
			next := uint64(0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var k uint64
				if next == 0 || rng.IntN(10) < 7 {
					next++
					k = next
				} else {
					k = next - uint64(rng.IntN(minInt(int(next), 10_000)))
				}
				cache.Get(k)
				cache.Set(k, newEntity(k), time.Hour)
			}
		})
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
