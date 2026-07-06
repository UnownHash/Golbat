// Sustained decode-at-volume runner: N workers decode corpus payloads in a
// loop (decode -> read Golbat's field set -> drop), reporting throughput and
// GC metrics. Run once per configuration and compare:
//
//	open:         go run ./cmd/bench
//	opaque+lazy:  go run -tags protoopaque ./cmd/bench
//	opaque-lazy:  go run -tags protoopaque ./cmd/bench -nolazy
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"protobench/corpus"
	"protobench/readers"
)

type runConfig struct {
	corpusDir string
	workers   int
	duration  time.Duration
	nolazy    bool
	ballastMB int
	methods   string // CSV filter; empty = all with readers
}

type report struct {
	decodes    uint64
	bytes      uint64
	perMethod  map[string]uint64
	elapsed    time.Duration
	gcCPUShare float64
	allocBytes uint64
	allocObjs  uint64
	gcCycles   uint64
	pauseP50   time.Duration
	pauseP99   time.Duration
}

type ballastNode struct {
	next *ballastNode
	_    [48]byte
}

// buildBallast allocates a pointer-dense linked structure approximating
// Golbat's live caches (GC mark cost scales with live pointerful heap).
func buildBallast(mb int) *ballastNode {
	if mb <= 0 {
		return nil
	}
	n := mb * (1 << 20) / 64
	var head *ballastNode
	for i := 0; i < n; i++ {
		head = &ballastNode{next: head}
	}
	return head
}

var metricNames = []string{
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
	"/gc/heap/allocs:bytes",
	"/gc/heap/allocs:objects",
	"/gc/cycles/total:gc-cycles",
	"/sched/pauses/total/gc:seconds",
}

// histPercentile computes a percentile from the delta of two cumulative
// pause histograms (runtime/metrics Float64Histogram).
func histPercentile(before, after *metrics.Float64Histogram, q float64) time.Duration {
	if before == nil || after == nil {
		return 0
	}
	var total uint64
	deltas := make([]uint64, len(after.Counts))
	for i := range after.Counts {
		d := after.Counts[i]
		if i < len(before.Counts) {
			d -= before.Counts[i]
		}
		deltas[i] = d
		total += d
	}
	if total == 0 {
		return 0
	}
	// Ceil with a floor of 1: with few samples, truncating (e.g. total==1)
	// makes any q land on target 0, which is trivially <= the first
	// bucket's count and misreports the percentile as the first bucket.
	target := uint64(math.Ceil(q * float64(total)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, d := range deltas {
		cum += d
		if cum >= target {
			// Buckets has len(Counts)+1 boundaries; use the bucket's upper edge.
			return boundaryDuration(after.Buckets, i+1)
		}
	}
	return boundaryDuration(after.Buckets, len(after.Buckets)-1)
}

// boundaryDuration converts the bucket boundary at buckets[idx] to a
// Duration. Float64Histogram documents that the outermost boundaries may be
// +Inf/-Inf (open-ended buckets); converting that directly via
// time.Duration(math.Inf(1) * float64(time.Second)) is implementation-defined
// garbage. When the boundary is non-finite, fall back to the bucket's other
// (lower) edge; if that is non-finite too, report 0.
func boundaryDuration(buckets []float64, idx int) time.Duration {
	v := buckets[idx]
	if math.IsInf(v, 0) {
		if idx == 0 {
			return 0
		}
		v = buckets[idx-1]
		if math.IsInf(v, 0) {
			return 0
		}
	}
	return time.Duration(v * float64(time.Second))
}

func readMetrics() map[string]metrics.Value {
	samples := make([]metrics.Sample, len(metricNames))
	for i, n := range metricNames {
		samples[i].Name = n
	}
	metrics.Read(samples)
	out := make(map[string]metrics.Value, len(samples))
	for _, s := range samples {
		out[s.Name] = s.Value
	}
	return out
}

func run(cfg runConfig) (report, error) {
	byMethod, err := corpus.Load(cfg.corpusDir)
	if err != nil {
		return report{}, err
	}
	filter := map[string]bool{}
	for _, m := range strings.Split(cfg.methods, ",") {
		if m = strings.TrimSpace(m); m != "" {
			filter[m] = true
		}
	}
	type item struct {
		data   []byte
		reader readers.Reader
		method string
	}
	var items []item
	perMethodIdx := map[string]int{}
	var methodNames []string
	for method, payloads := range byMethod {
		reader, ok := readers.Registry[method]
		if !ok || (len(filter) > 0 && !filter[method]) {
			continue
		}
		if _, seen := perMethodIdx[method]; !seen {
			perMethodIdx[method] = len(methodNames)
			methodNames = append(methodNames, method)
		}
		for _, p := range payloads {
			items = append(items, item{data: p.Data, reader: reader, method: method})
		}
	}
	if len(items) == 0 {
		known := make([]string, 0, len(readers.Registry))
		for m := range readers.Registry {
			known = append(known, m)
		}
		sort.Strings(known)
		return report{}, fmt.Errorf("corpus at %s has no payloads with readers (have readers: %s)", cfg.corpusDir, strings.Join(known, ", "))
	}

	o := proto.UnmarshalOptions{NoLazyDecoding: cfg.nolazy}
	ballast := buildBallast(cfg.ballastMB)

	perMethod := make([]atomic.Uint64, len(methodNames))
	var decodes, bytes atomic.Uint64
	deadline := time.Now().Add(cfg.duration)
	before := readMetrics()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for time.Now().Before(deadline) {
				it := items[rng.Intn(len(items))]
				if err := it.reader(it.data, o); err != nil {
					panic(fmt.Sprintf("decode %s: %v", it.method, err))
				}
				decodes.Add(1)
				bytes.Add(uint64(len(it.data)))
				perMethod[perMethodIdx[it.method]].Add(1)
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	elapsed := time.Since(start)
	after := readMetrics()
	runtime.KeepAlive(ballast)

	f64 := func(m map[string]metrics.Value, k string) float64 { return m[k].Float64() }
	u64 := func(m map[string]metrics.Value, k string) uint64 { return m[k].Uint64() }
	gcCPU := f64(after, "/cpu/classes/gc/total:cpu-seconds") - f64(before, "/cpu/classes/gc/total:cpu-seconds")
	totCPU := f64(after, "/cpu/classes/total:cpu-seconds") - f64(before, "/cpu/classes/total:cpu-seconds")
	rep := report{
		decodes:    decodes.Load(),
		bytes:      bytes.Load(),
		perMethod:  map[string]uint64{},
		elapsed:    elapsed,
		allocBytes: u64(after, "/gc/heap/allocs:bytes") - u64(before, "/gc/heap/allocs:bytes"),
		allocObjs:  u64(after, "/gc/heap/allocs:objects") - u64(before, "/gc/heap/allocs:objects"),
		gcCycles:   u64(after, "/gc/cycles/total:gc-cycles") - u64(before, "/gc/cycles/total:gc-cycles"),
	}
	if totCPU > 0 {
		rep.gcCPUShare = gcCPU / totCPU
	}
	beforeH := before["/sched/pauses/total/gc:seconds"].Float64Histogram()
	afterH := after["/sched/pauses/total/gc:seconds"].Float64Histogram()
	rep.pauseP50 = histPercentile(beforeH, afterH, 0.50)
	rep.pauseP99 = histPercentile(beforeH, afterH, 0.99)
	for i, m := range methodNames {
		rep.perMethod[m] = perMethod[i].Load()
	}
	return rep, nil
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.corpusDir, "corpus", "../capture", "corpus directory")
	flag.IntVar(&cfg.workers, "workers", 96, "concurrent decode workers (matches raw_processing_concurrency)")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "run duration")
	flag.BoolVar(&cfg.nolazy, "nolazy", false, "NoLazyDecoding (only meaningful with -tags protoopaque)")
	flag.IntVar(&cfg.ballastMB, "ballast-mb", 0, "pointer-dense live-heap ballast")
	flag.StringVar(&cfg.methods, "methods", "", "CSV method filter")
	flag.Parse()

	rep, err := run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	lazyState := "n/a (open build)"
	if buildMode == "opaque" {
		if cfg.nolazy {
			lazyState = "disabled"
		} else {
			lazyState = "enabled"
		}
	}
	fmt.Printf("build=%s lazy=%s workers=%d duration=%s ballast=%dMB\n",
		buildMode, lazyState, cfg.workers, rep.elapsed.Round(time.Millisecond), cfg.ballastMB)
	fmt.Printf("decodes:      %d (%.0f/s)\n", rep.decodes, float64(rep.decodes)/rep.elapsed.Seconds())
	fmt.Printf("throughput:   %.1f MB/s\n", float64(rep.bytes)/1e6/rep.elapsed.Seconds())
	fmt.Printf("alloc/decode: %.0f B, %.1f objects\n",
		float64(rep.allocBytes)/float64(rep.decodes), float64(rep.allocObjs)/float64(rep.decodes))
	fmt.Printf("GC:           cpu-share=%.1f%% cycles=%d pause-p50=%s pause-p99=%s\n",
		rep.gcCPUShare*100, rep.gcCycles, rep.pauseP50, rep.pauseP99)
	methods := make([]string, 0, len(rep.perMethod))
	for m := range rep.perMethod {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	for _, m := range methods {
		fmt.Printf("  %-24s %d\n", m, rep.perMethod[m])
	}
}
