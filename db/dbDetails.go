package db

import (
	"golbat/stats_collector"

	"github.com/jmoiron/sqlx"
)

type DbDetails struct {
	PokemonDb       *sqlx.DB
	UsePokemonCache bool
	GeneralDb       *sqlx.DB
}

var statsCollector stats_collector.StatsCollector

func SetStatsCollector(collector stats_collector.StatsCollector) {
	statsCollector = collector
}
