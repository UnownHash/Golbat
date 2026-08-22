package decoder

import (
	"sync/atomic"

	"golbat/stats_collector"
)

// countingStatsCollector is the noop collector plus atomic counters for the
// few metrics a test needs to prove actually fired. It is installed once for
// the whole binary rather than swapped in per test, for the same reason the
// noop collector was: the package-init stats aggregation worker reads the
// statsCollector global asynchronously, so reassigning it mid-test would be a
// data race. The counters are atomic for that same reason.
type countingStatsCollector struct {
	stats_collector.StatsCollector
	peerLookupDropped atomic.Int64
}

func (c *countingStatsCollector) IncPeerLookupDropped() { c.peerLookupDropped.Add(1) }

var testStatsCollector = &countingStatsCollector{
	StatsCollector: stats_collector.NewNoopStatsCollector(),
}

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
func init() {
	InitDataCache()
	// Set once here rather than per-test: the package-init stats aggregation
	// worker (decoder/stats.go) asynchronously drains events enqueued by
	// earlier tests' saves and reads the same statsCollector global, so
	// re-assigning it from individual tests races under -race.
	SetStatsCollector(testStatsCollector)
}
