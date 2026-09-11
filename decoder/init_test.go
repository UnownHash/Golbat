package decoder

import (
	"testing"

	"golbat/stats_collector"
)

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
//
// This does NOT also seed statsCollector — that used to happen here via an
// explicit SetStatsCollector(NewNoopStatsCollector()) call, but statsCollector
// is seeded with a noop in its own package-level initializer now (see its
// doc comment in main.go), which runs before this init() does regardless of
// which file it lives in. Calling SetStatsCollector here again would only
// swap one noop for an equivalent one.
func init() {
	InitDataCache()
}

// setStatsCollectorForTest swaps the shared statsCollector for the duration
// of one test and restores the previous value on cleanup. Safe under -race
// against the background stats-aggregation worker and ticker (both read
// statsCollector via the same atomic.Pointer): the package-init stats
// aggregation worker (decoder/stats.go) reads the same statsCollector
// global concurrently with any test that swaps it, which used to make a
// direct `statsCollector = x` reassignment from a test a data race;
// statsCollector is an atomic.Pointer specifically so this swap is
// race-free under -race instead.
func setStatsCollectorForTest(t *testing.T, collector stats_collector.StatsCollector) {
	t.Helper()
	previous := statsCollector.Load()
	statsCollector.Store(&collector)
	t.Cleanup(func() { statsCollector.Store(previous) })
}
