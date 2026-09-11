package main

import (
	db2 "golbat/db"
	"golbat/decoder"
	"golbat/stats_collector"
)

// The production binary calls decoder.InitDataCache and installs the stats
// collectors from main() after config load; the test binary has no main(), so
// construct the caches and install no-op collectors here. Without a collector
// any db or decoder path that records a query nil-panics.
func init() {
	decoder.InitDataCache()
	noop := stats_collector.NewNoopStatsCollector()
	db2.SetStatsCollector(noop)
	decoder.SetStatsCollector(noop)
}
