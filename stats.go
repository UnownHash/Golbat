package main

import (
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"time"
)

func StartStatsLogger(db *sqlx.DB) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			<-ticker.C

			stats := db.Stats()
			log.Infof("DB - InUse: %d Idle %d WaitDuration %s", stats.InUse, stats.Idle, stats.WaitDuration)
		}
	}()
}
