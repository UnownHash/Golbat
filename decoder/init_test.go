package decoder

import (
	"testing"

	"golbat/stats_collector"
)

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
func init() {
	InitDataCache()
	// The package-init stats aggregation worker (decoder/stats.go)
	// asynchronously drains events enqueued by earlier tests' saves and
	// reads the same statsCollector global concurrently with any test that
	// swaps it (see setStatsCollectorForTest below). That used to make a
	// direct `statsCollector = x` reassignment from a test a data race;
	// statsCollector is now an atomic.Pointer specifically so this and
	// setStatsCollectorForTest are race-free under -race.
	SetStatsCollector(stats_collector.NewNoopStatsCollector())
}

// setStatsCollectorForTest swaps the shared statsCollector for the duration
// of one test and restores the previous value on cleanup. Safe under -race
// against the background stats-aggregation worker and ticker (both read
// statsCollector via the same atomic.Pointer) — see the init() comment
// above for why that matters.
func setStatsCollectorForTest(t *testing.T, collector stats_collector.StatsCollector) {
	t.Helper()
	previous := statsCollector.Load()
	statsCollector.Store(&collector)
	t.Cleanup(func() { statsCollector.Store(previous) })
}
