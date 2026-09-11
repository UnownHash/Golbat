package db

import "golbat/stats_collector"

// The production binary installs the stats collector from main(); the test
// binary has no main(), so install a no-op one here. Individual tests may swap
// in a capturing collector and restore this one.
func init() {
	SetStatsCollector(stats_collector.NewNoopStatsCollector())
}
