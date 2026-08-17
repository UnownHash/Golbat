package db

import (
	"golbat/stats_collector"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

type DbDetails struct {
	PokemonDb       *sqlx.DB
	UsePokemonCache bool
	GeneralDb       *sqlx.DB
}

// statsCollector is seeded with a noop in its own initializer so it is never
// nil, matching decoder's. Before that, it stayed nil until main() called
// SetStatsCollector, and only timing.go nil-checked it — db/pokestop.go and
// db/stats.go call straight through. Nothing reaches those today before the
// setter runs, but the guarantee is cheaper to hold than to keep re-deriving
// per call site, and the nil checks in timing.go went away with it.
//
// Unlike decoder's this is a plain variable, not an atomic.Pointer: it is
// written exactly once at boot and no test swaps it, so there is no
// concurrent Store to make race-free. Keep it that way — if a caller ever
// needs to swap it at runtime, it needs decoder's treatment first.
var statsCollector = stats_collector.NewNoopStatsCollector()

// SetStatsCollector swaps in the real collector once main() has read config.
// A nil collector is refused rather than stored, for the same reason as
// decoder.SetStatsCollector: it would turn every call site here into a
// nil-interface panic arbitrarily far from the mistake.
func SetStatsCollector(collector stats_collector.StatsCollector) {
	if collector == nil {
		log.Panic("db.SetStatsCollector: nil collector. Pass stats_collector.NewNoopStatsCollector() to disable stats")
	}
	statsCollector = collector
}
