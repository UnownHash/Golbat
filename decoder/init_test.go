package decoder

import "golbat/stats_collector"

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
func init() {
	InitDataCache()
	// Set once here rather than per-test: the package-init stats aggregation
	// worker (decoder/stats.go) asynchronously drains events enqueued by
	// earlier tests' saves and reads the same statsCollector global, so
	// re-assigning it from individual tests races under -race.
	SetStatsCollector(stats_collector.NewNoopStatsCollector())
}
