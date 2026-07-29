package cachebench

import (
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/metrics"
	"sort"
	"strconv"
	"sync"
	"time"
)

// envEntries returns the benchmark population size (CACHEBENCH_N, default
// 10M per the brief's production sizing).
func envEntries() int {
	if s := os.Getenv("CACHEBENCH_N"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			panic(fmt.Sprintf("bad CACHEBENCH_N %q: %v", s, err))
		}
		return n
	}
	return 10_000_000
}

// envCandidates returns the candidate names to run: CACHEBENCH_CANDIDATE
// (single name) or all four. run.sh uses this for process-level isolation of
// the memory-heavy cases.
func envCandidates() []string {
	if s := os.Getenv("CACHEBENCH_CANDIDATE"); s != "" {
		if _, ok := Candidates[s]; !ok {
			panic("unknown CACHEBENCH_CANDIDATE " + s)
		}
		return []string{s}
	}
	return CandidateNames
}

func newEntity(id uint64) *Entity {
	e := &Entity{Id: id, Lat: 51.5, Lon: -0.1, Updated: int64(id)}
	return e
}

// preload fills cache with keys [0, n) using parallel workers. TTLs come
// from ttlFn(key) so scenarios can shape expiry cohorts. Each entity's
// ExpireTimestamp is stamped with its deadline (unix nanos) so eviction
// callbacks can measure delivery lag.
func preload(cache BenchCache, n int, workers int, ttlFn func(key uint64) time.Duration) {
	var wg sync.WaitGroup
	chunk := (n + workers - 1) / workers
	for w := 0; w < workers; w++ {
		lo := w * chunk
		hi := min(lo+chunk, n)
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				k := uint64(i)
				e := newEntity(k)
				ttl := ttlFn(k)
				e.ExpireTimestamp = time.Now().Add(ttl).UnixNano()
				cache.Set(k, e, ttl)
			}
		}(lo, hi)
	}
	wg.Wait()
}

// jitteredHourTTL mimics the production pokemon/spawnpoint TTLs: 55-65 min.
func jitteredHourTTL(rng *rand.Rand) time.Duration {
	return 55*time.Minute + time.Duration(rng.Int64N(int64(10*time.Minute)))
}

func newZipf(seed uint64, n int) (*rand.Rand, *rand.Zipf) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	return rng, rand.NewZipf(rng, 1.01, 1, uint64(n-1))
}

// percentile returns the p-th percentile (0..100) of samples; samples is
// sorted in place.
func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	idx := int(float64(len(samples)-1) * p / 100)
	return samples[idx]
}

// gcPauseSnapshot reads the cumulative GC stop-the-world pause histogram.
type gcPauseSnapshot struct {
	hist    *metrics.Float64Histogram
	gcCPU   float64
	totalGC uint64
}

func readGCPauses() gcPauseSnapshot {
	samples := []metrics.Sample{
		{Name: "/sched/pauses/total/gc:seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples)
	snap := gcPauseSnapshot{}
	if samples[0].Value.Kind() == metrics.KindFloat64Histogram {
		snap.hist = samples[0].Value.Float64Histogram()
	}
	if samples[1].Value.Kind() == metrics.KindFloat64 {
		snap.gcCPU = samples[1].Value.Float64()
	}
	if samples[2].Value.Kind() == metrics.KindUint64 {
		snap.totalGC = samples[2].Value.Uint64()
	}
	return snap
}

// pauseDelta summarizes the pauses that occurred between two snapshots as
// (count, approx p50, approx p99, max) using the histogram buckets.
func pauseDelta(before, after gcPauseSnapshot) (count uint64, p50, p99, maxPause time.Duration) {
	if before.hist == nil || after.hist == nil {
		return 0, 0, 0, 0
	}
	counts := make([]uint64, len(after.hist.Counts))
	var total uint64
	for i := range counts {
		c := after.hist.Counts[i]
		if i < len(before.hist.Counts) {
			c -= before.hist.Counts[i]
		}
		counts[i] = c
		total += c
	}
	if total == 0 {
		return 0, 0, 0, 0
	}
	bucketAt := func(target uint64) time.Duration {
		var cum uint64
		for i, c := range counts {
			cum += c
			if cum >= target {
				// Buckets[i+1] is the bucket's upper bound in seconds.
				ub := after.hist.Buckets[min(i+1, len(after.hist.Buckets)-1)]
				return time.Duration(ub * float64(time.Second))
			}
		}
		return 0
	}
	p50 = bucketAt((total + 1) / 2)
	p99 = bucketAt(uint64(float64(total)*0.99) + 1)
	for i := len(counts) - 1; i >= 0; i-- {
		if counts[i] > 0 {
			ub := after.hist.Buckets[min(i+1, len(after.hist.Buckets)-1)]
			maxPause = time.Duration(ub * float64(time.Second))
			break
		}
	}
	return total, p50, p99, maxPause
}
