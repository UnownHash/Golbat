package decoder

import (
	"testing"

	"golbat/config"
	"golbat/stats_collector"
)

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
//
// statsCollector needs no seeding here: its own package-level initializer
// installs a noop (see its doc comment in main.go), and that runs before this
// init().
func init() {
	InitDataCache()

	// Apply derive_iv's, derive_strong's and derive_despawn's shipped defaults. The test binary
	// never reads a config file, so without this the package would exercise
	// both derivations switched off while production ships them on. Tests
	// that care about the off case set the flag themselves. ditto_derivation
	// is left at Go's zero value (off): tests that exercise the roll switch it
	// on with withDittoDerivation.
	config.Config.DeriveIv = true
	config.Config.DeriveStrong = true
	config.Config.DeriveDespawn = true
}

// setStatsCollectorForTest swaps the shared statsCollector for the duration
// of one test and restores the previous value on cleanup. Safe under -race
// against the background stats-aggregation worker and ticker (both read
// statsCollector via the same atomic.Pointer): the package-init stats
// aggregation worker (decoder/stats.go) reads the same statsCollector
// global concurrently with any test that swaps it, so statsCollector is an
// atomic.Pointer and a direct reassignment would be a data race.
func setStatsCollectorForTest(t *testing.T, collector stats_collector.StatsCollector) {
	t.Helper()
	previous := statsCollector.Load()
	statsCollector.Store(&collector)
	t.Cleanup(func() { statsCollector.Store(previous) })
}
