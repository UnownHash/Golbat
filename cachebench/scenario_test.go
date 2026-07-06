package cachebench

// Scenario tests: benchmark-matrix items 3 (mass-expiry wave), 5 (eviction
// callback throughput) and 7 (memory + GC churn + full-cache Range). These
// measure wall-clock phenomena — goroutine counts, callback lag, GC pauses —
// so they are tests, not ns/op benchmarks. They are heavy (10M entries by
// default) and only run when CACHEBENCH_SCENARIO=1:
//
//	CACHEBENCH_SCENARIO=1 CACHEBENCH_CANDIDATE=ttlcache \
//	  go test -run TestScenarioMassExpiryWave -timeout 30m -v
//
// run.sh runs each candidate in its own process (a 10M x ~1KB population is
// ~11GB; the 36GB test host can't hold two candidates at once).
//
// All RESULT lines are t.Logf'd with a stable "RESULT <scenario>/<candidate>"
// prefix for collection into RESULTS.md.

import (
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func scenarioGate(t *testing.T) {
	if os.Getenv("CACHEBENCH_SCENARIO") != "1" {
		t.Skip("set CACHEBENCH_SCENARIO=1 to run scenario tests (heavy)")
	}
}

// goroutineSampler samples runtime.NumGoroutine every 50ms, tracking max.
type goroutineSampler struct {
	max  atomic.Int64
	stop chan struct{}
	done chan struct{}
}

func startGoroutineSampler() *goroutineSampler {
	s := &goroutineSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				if n := int64(runtime.NumGoroutine()); n > s.max.Load() {
					s.max.Store(n)
				}
			}
		}
	}()
	return s
}

func (s *goroutineSampler) Stop() int64 {
	close(s.stop)
	<-s.done
	return s.max.Load()
}

// lagRecorder accumulates callback delivery lag (delivery time minus entry
// deadline) in fixed buckets, lock-free (callbacks may be hot paths).
type lagRecorder struct {
	// bucket i: lag < 2^i milliseconds (last bucket = overflow)
	buckets [22]atomic.Int64
	maxNs   atomic.Int64
	count   atomic.Int64
}

func (r *lagRecorder) record(lagNs int64) {
	if lagNs < 0 {
		lagNs = 0
	}
	r.count.Add(1)
	for {
		old := r.maxNs.Load()
		if lagNs <= old || r.maxNs.CompareAndSwap(old, lagNs) {
			break
		}
	}
	ms := lagNs / 1e6
	for i := range r.buckets {
		if ms < 1<<i || i == len(r.buckets)-1 {
			r.buckets[i].Add(1)
			return
		}
	}
}

func (r *lagRecorder) percentile(p float64) time.Duration {
	total := r.count.Load()
	if total == 0 {
		return 0
	}
	target := int64(float64(total)*p/100) + 1
	var cum int64
	for i := range r.buckets {
		cum += r.buckets[i].Load()
		if cum >= target {
			return time.Duration(1<<i) * time.Millisecond // bucket upper bound
		}
	}
	return time.Duration(r.maxNs.Load())
}

// latSampler records every 64th Get latency into a preallocated slice.
type latSampler struct {
	mu      sync.Mutex
	samples []time.Duration
}

func (l *latSampler) add(d time.Duration) {
	l.mu.Lock()
	if len(l.samples) < cap(l.samples) {
		l.samples = append(l.samples, d)
	}
	l.mu.Unlock()
}

// --- Benchmark 3: mass-expiry wave.
//
// Preload N entries with TTLs of 60s + uniform[0,60s) from each entry's Set
// time, then run a rate-limited mixed load (95% zipf Gets / 5% Sets of NEW
// keys at ~200k ops/s total across 32 workers — Set rate ~10k/s matches the
// production wave scale). Phases anchor to t0 = preload start: nothing can
// expire before t0+60s, so [preload end, t0+60s) is the steady-state
// baseline and [t0+60s, t0+60s+preloadDur+60s) is the wave, during which
// the entire preloaded population expires under load.
func TestScenarioMassExpiryWave(t *testing.T) {
	scenarioGate(t)
	n := envEntries()
	for _, name := range envCandidates() {
		if name == "ttlcache-hyst" {
			continue // expiry machinery identical to ttlcache
		}
		t.Run(name, func(t *testing.T) {
			lags := &lagRecorder{}
			var delivered atomic.Int64
			cache := Candidates[name](Config{
				Shards:          runtime.NumCPU(),
				TouchOnHit:      false,
				DefaultTTL:      time.Hour,
				ExpectedEntries: n,
				SweepInterval:   time.Second,
				OnEvict: func(_ uint64, v *Entity, reason EvictReason) {
					if reason == EvictExpired && v != nil {
						delivered.Add(1)
						lags.record(time.Now().UnixNano() - v.ExpireTimestamp)
					}
				},
			})
			defer cache.Close()

			t.Log("preloading...")
			t0 := time.Now()
			preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
				return time.Minute + time.Duration(k%60)*time.Second
			})
			preloadDur := time.Since(t0)
			t.Logf("preload of %d entries took %s", n, preloadDur.Round(time.Millisecond))
			// Digest the preload allocation burst now so it doesn't
			// contaminate the steady-state latency baseline.
			runtime.GC()
			waveBoundary := t0.Add(time.Minute) // earliest possible deadline
			// Steady-state samples come only from the last 30s before the
			// wave; the stretch right after preload still carries GC and
			// cache-internal settling.
			steadyStart := waveBoundary.Add(-30 * time.Second)
			if steadyWindow := time.Until(waveBoundary); steadyWindow < 10*time.Second {
				t.Logf("WARNING: only %v of steady-state window left after preload", steadyWindow.Round(time.Second))
			}
			// Last preload deadline is ~t0+preloadDur+120s; run the load a
			// bit past it so the whole wave happens under load.
			loadEnd := t0.Add(preloadDur + 130*time.Second)

			steady := &latSampler{samples: make([]time.Duration, 0, 1<<20)}
			wave := &latSampler{samples: make([]time.Duration, 0, 1<<21)}
			sampler := startGoroutineSampler()
			gcBefore := readGCPauses()

			const workers = 32
			const opsPerWorkerPerSec = 6250 // ~200k ops/s total
			var setsExpiringInWindow atomic.Int64
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					rng, zipf := newZipf(uint64(w)*104729+7, n)
					newKey := uint64(n + w*10_000_000)
					const batch = 64
					for time.Now().Before(loadEnd) {
						batchStart := time.Now()
						for i := 0; i < batch; i++ {
							if rng.IntN(20) == 0 { // 5% Sets, unique new keys
								newKey++
								// TTL floor of 60s keeps load-set expiries
								// out of the steady-state window.
								ttl := time.Minute + time.Duration(rng.Int64N(int64(60*time.Second)))
								e := newEntity(newKey)
								e.ExpireTimestamp = time.Now().Add(ttl).UnixNano()
								if time.Now().Add(ttl).Before(loadEnd.Add(25 * time.Second)) {
									setsExpiringInWindow.Add(1)
								}
								cache.Set(newKey, e, ttl)
							} else {
								k := zipf.Uint64()
								if i%8 == 0 { // sample latency of every 8th Get
									gs := time.Now()
									cache.Get(k)
									d := time.Since(gs)
									if gs.Before(waveBoundary) {
										if gs.After(steadyStart) {
											steady.add(d)
										}
									} else {
										wave.add(d)
									}
								} else {
									cache.Get(k)
								}
							}
						}
						// Pace to opsPerWorkerPerSec.
						elapsed := time.Since(batchStart)
						want := time.Duration(batch) * time.Second / opsPerWorkerPerSec
						if elapsed < want {
							time.Sleep(want - elapsed)
						}
					}
				}(w)
			}
			wg.Wait()
			// Allow stragglers: sweeps + callback queues drain. Everything
			// counted in expected has a deadline within ~25s of loadEnd.
			expected := int64(n) + setsExpiringInWindow.Load()
			settleDeadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(settleDeadline) {
				if delivered.Load() >= expected {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			maxGoroutines := sampler.Stop()
			gcAfter := readGCPauses()

			steadyP50 := percentile(steady.samples, 50)
			steadyP99 := percentile(steady.samples, 99)
			waveP50 := percentile(wave.samples, 50)
			waveP99 := percentile(wave.samples, 99)
			ratio := float64(waveP99) / float64(max(int64(steadyP99), 1))
			pauses, pP50, pP99, pMax := pauseDelta(gcBefore, gcAfter)

			t.Logf("RESULT wave/%s steady_get_p50=%v steady_get_p99=%v wave_get_p50=%v wave_get_p99=%v wave_vs_steady_p99=%.1fx",
				name, steadyP50, steadyP99, waveP50, waveP99, ratio)
			t.Logf("RESULT wave/%s max_goroutines=%d callbacks_delivered=%d expected~%d lag_p50=%v lag_p99=%v lag_max=%v",
				name, maxGoroutines, delivered.Load(), expected, lags.percentile(50), lags.percentile(99), time.Duration(lags.maxNs.Load()))
			t.Logf("RESULT wave/%s gc_pauses=%d gc_pause_p50=%v gc_pause_p99=%v gc_pause_max=%v gc_cpu_delta=%.1fs",
				name, pauses, pP50, pP99, pMax, gcAfter.gcCPU-gcBefore.gcCPU)
		})
	}
}

// --- Benchmark 5: eviction callback throughput — callbacks enqueue to a
// channel exactly like Golbat's tree writer (non-blocking TryEnqueue with a
// drop counter; consumer drains continuously). 1M entries expire within
// ~10s; measure end-to-end eviction->delivered rate and goroutines used.
func TestScenarioCallbackThroughput(t *testing.T) {
	scenarioGate(t)
	const n = 1_000_000
	for _, name := range envCandidates() {
		if name == "ttlcache-hyst" {
			continue // callback machinery identical to ttlcache
		}
		t.Run(name, func(t *testing.T) {
			type treeOp struct {
				key uint64
			}
			ch := make(chan treeOp, 262144) // tree-writer queue size
			var delivered, dropped atomic.Int64
			var firstNs, lastNs atomic.Int64
			consumerStop := make(chan struct{})
			consumerDone := make(chan struct{})
			go func() { // the "tree writer": drains and counts
				defer close(consumerDone)
				for {
					select {
					case <-ch:
						delivered.Add(1)
					case <-consumerStop:
						for { // drain what's left
							select {
							case <-ch:
								delivered.Add(1)
							default:
								return
							}
						}
					}
				}
			}()
			cache := Candidates[name](Config{
				Shards:          runtime.NumCPU(),
				DefaultTTL:      time.Hour,
				ExpectedEntries: n,
				SweepInterval:   time.Second,
				OnEvict: func(key uint64, _ *Entity, reason EvictReason) {
					if reason != EvictExpired {
						return
					}
					now := time.Now().UnixNano()
					firstNs.CompareAndSwap(0, now)
					lastNs.Store(now)
					select { // TryEnqueue (commit ed50bf8): never block
					case ch <- treeOp{key: key}:
					default:
						dropped.Add(1)
					}
				},
			})

			sampler := startGoroutineSampler()
			preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
				return time.Second + time.Duration(k%10)*time.Second
			})
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				if delivered.Load()+dropped.Load() >= n {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			maxGoroutines := sampler.Stop()
			cache.Close()
			// Callbacks may still be in flight (ttlcache spawns them on
			// their own goroutines); ch stays open — stragglers hit the
			// buffered/non-blocking send harmlessly.
			time.Sleep(time.Second)
			close(consumerStop)
			<-consumerDone

			total := delivered.Load() + dropped.Load()
			window := time.Duration(lastNs.Load() - firstNs.Load())
			rate := float64(0)
			if window > 0 {
				rate = float64(total) / window.Seconds()
			}
			t.Logf("RESULT cbthroughput/%s delivered=%d dropped=%d of=%d window=%v rate=%.0f/s max_goroutines=%d",
				name, delivered.Load(), dropped.Load(), n, window.Round(time.Millisecond), rate, maxGoroutines)
			if total < n {
				t.Errorf("only %d of %d evictions surfaced within 90s", total, n)
			}
		})
	}
}

// --- Benchmark 5b: the goroutine bomb, reproduced. The production incident
// (commit ed50bf8) was an eviction callback that BLOCKED (full tree-writer
// queue) during mass expiry: ttlcache spawns one goroutine per callback, so
// every eviction during the stall parked another goroutine — millions in
// production, each holding an entity lock. Here callbacks block on a gate
// for a 10s stall window while a 200k-entry population expires, then the
// gate opens (consumer recovers) and everything drains. Measured: goroutine
// count at the end of the stall (ttlcache: one per eviction; bounded-
// pipeline candidates: constant), worst writer (Set) latency (does the
// stalled pipeline backpressure onto cache writers? theine's listener
// shares the maintenance goroutine that Set feeds with BLOCKING sends),
// and time to full delivery after recovery.
func TestScenarioCallbackBomb(t *testing.T) {
	scenarioGate(t)
	for _, name := range envCandidates() {
		if name == "ttlcache-hyst" {
			continue // callback machinery identical to ttlcache
		}
		// Two consumer-failure shapes: "stall" = consumer fully wedged for
		// 10s then recovers (the acute ed50bf8 incident); "slow" = consumer
		// sustainably too slow (100µs per event vs the expiry rate), the
		// chronic version. theine passes the acute test (its maintenance
		// goroutine parks, so expiry stops and writeChan barely fills) but
		// fails the chronic one (writers block behind the busy maintenance
		// goroutine).
		t.Run(name+"/stall", func(t *testing.T) { runBombArm(t, name, true) })
		t.Run(name+"/slow", func(t *testing.T) { runBombArm(t, name, false) })
	}
}

func runBombArm(t *testing.T, name string, hardStall bool) {
	const n = 200_000
	gate := make(chan struct{})
	var delivered atomic.Int64
	cache := Candidates[name](Config{
		Shards:          runtime.NumCPU(),
		DefaultTTL:      time.Hour,
		ExpectedEntries: n,
		SweepInterval:   time.Second,
		OnEvict: func(_ uint64, _ *Entity, reason EvictReason) {
			if reason != EvictExpired {
				return
			}
			if hardStall {
				<-gate // the incident: callback blocked on a stalled consumer
			} else {
				time.Sleep(100 * time.Microsecond) // chronic: consumer slower than expiry
			}
			delivered.Add(1)
		},
	})
	defer cache.Close()

	sampler := startGoroutineSampler()
	preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
		return time.Second + time.Duration(k%3)*time.Second
	})

	// Writer probe: one Set every 10ms; a healthy cache keeps these
	// fast no matter what the eviction pipeline is doing.
	var probeMax atomic.Int64
	probeStop := make(chan struct{})
	var probeWg sync.WaitGroup
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		k := uint64(1 << 62)
		for {
			select {
			case <-probeStop:
				return
			case <-time.After(10 * time.Millisecond):
				k++
				s := time.Now()
				cache.Set(k, newEntity(k), time.Hour)
				if d := int64(time.Since(s)); d > probeMax.Load() {
					probeMax.Store(d)
				}
			}
		}
	}()

	// Stall window: the whole population's deadlines pass while the
	// consumer is down (or crawling).
	time.Sleep(10 * time.Second)
	stallGoroutines := sampler.max.Load()
	if hardStall {
		close(gate) // consumer recovers
	}
	recoverStart := time.Now()
	deadline := recoverStart.Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if delivered.Load() >= n {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	drainTime := time.Since(recoverStart)
	close(probeStop)
	probeWg.Wait()
	maxGoroutines := sampler.Stop()

	arm := "slow"
	if hardStall {
		arm = "stall"
	}
	t.Logf("RESULT cbbomb/%s/%s delivered=%d of=%d stall_goroutines=%d max_goroutines=%d drain_after_recovery=%v probe_set_max=%v",
		name, arm, delivered.Load(), n, stallGoroutines, maxGoroutines,
		drainTime.Round(time.Millisecond), time.Duration(probeMax.Load()).Round(time.Microsecond))
}

// --- Benchmark 7: memory per entry at N entries, full-cache Range cost
// (appendix point 5: the PreservePokemonToDatabase shutdown path Ranges 10M
// entries), and GC behavior under steady churn.
//
// CACHEBENCH_CANDIDATE=mapref measures a plain map[uint64]*Entity as the
// floor reference.
func TestScenarioMemory(t *testing.T) {
	scenarioGate(t)
	n := envEntries()
	var names []string
	if os.Getenv("CACHEBENCH_CANDIDATE") == "mapref" {
		names = []string{"mapref"} // plain-map floor reference
	} else {
		names = envCandidates()
	}
	for _, name := range names {
		if name == "ttlcache-hyst" {
			continue // storage identical to ttlcache
		}
		t.Run(name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			var cache BenchCache
			var plain map[uint64]*Entity
			if name == "mapref" {
				plain = make(map[uint64]*Entity, n)
				for i := 0; i < n; i++ {
					plain[uint64(i)] = newEntity(uint64(i))
				}
			} else {
				cache = Candidates[name](Config{
					Shards:          runtime.NumCPU(),
					DefaultTTL:      3 * time.Hour,
					ExpectedEntries: n,
					SweepInterval:   time.Second,
				})
				defer cache.Close()
				preload(cache, n, runtime.NumCPU(), func(k uint64) time.Duration {
					return 2*time.Hour + time.Duration(k%3600)*time.Second
				})
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			delta := int64(after.HeapAlloc) - int64(before.HeapAlloc) // signed: prior subtest's freed heap can underflow
			perEntry := float64(delta) / float64(n)
			t.Logf("RESULT memory/%s entries=%d heap_delta=%.2fGiB bytes_per_entry=%.0f",
				name, n, float64(delta)/(1<<30), perEntry)

			// Appendix point 5: full-cache iteration (shutdown preserve path).
			count := 0
			rangeStart := time.Now()
			if name == "mapref" {
				for range plain {
					count++
				}
			} else {
				cache.Range(func(_ uint64, _ *Entity) bool {
					count++
					return true
				})
			}
			rangeDur := time.Since(rangeStart)
			t.Logf("RESULT range/%s visited=%d in=%v rate=%.0f entries/s",
				name, count, rangeDur.Round(time.Millisecond), float64(count)/rangeDur.Seconds())
			if count < n*99/100 {
				t.Errorf("Range visited %d of %d entries", count, n)
			}
			if name == "mapref" {
				return
			}

			// GC behavior at steady churn: ~50k Sets/s of new keys for 20s.
			// At GOGC=100 a ~1GB churn against an ~11GB live heap never
			// triggers a cycle, so force cycles with SetGCPercent(5) — the
			// point is the pause/CPU cost of GC cycles that scan the
			// candidate's 10M-entry structures, not GC frequency.
			oldGC := debug.SetGCPercent(5)
			gcBefore := readGCPauses()
			var wg sync.WaitGroup
			churnEnd := time.Now().Add(20 * time.Second)
			for w := 0; w < 8; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					k := uint64(n + w*50_000_000)
					for time.Now().Before(churnEnd) {
						for i := 0; i < 64; i++ {
							k++
							cache.Set(k, newEntity(k), time.Hour)
						}
						time.Sleep(10 * time.Millisecond) // ~6.4k/s per worker
					}
				}(w)
			}
			wg.Wait()
			gcAfter := readGCPauses()
			debug.SetGCPercent(oldGC)
			pauses, pP50, pP99, pMax := pauseDelta(gcBefore, gcAfter)
			t.Logf("RESULT churn/%s gc_cycles=%d gc_pauses=%d gc_pause_p50=%v gc_pause_p99=%v gc_pause_max=%v gc_cpu_delta=%.1fs (GOGC=5 forced)",
				name, gcAfter.totalGC-gcBefore.totalGC, pauses, pP50, pP99, pMax, gcAfter.gcCPU-gcBefore.gcCPU)
		})
	}
}
