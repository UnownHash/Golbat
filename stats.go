package main

import (
	"database/sql"
	"fmt"
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

func StartDatabaseArchiver(db *sqlx.DB) {
	ticker := time.NewTicker(time.Minute)
	go func() {
		for {
			<-ticker.C
			start := time.Now()

			var result sql.Result
			var err error

			unix := time.Now().Unix()

			result, err = db.Exec(
				fmt.Sprintf("DELETE FROM pokemon WHERE expire_timestamp < %d;",
					unix-5*60))

			elapsed := time.Since(start)

			if err != nil {
				log.Errorf("DB - Archive of pokemon table error %s", err)
			}
			_ = result
			//rows, _ := result.RowsAffected()
			log.Infof("DB - Archive of pokemon table took %s", elapsed)

		}
	}()
}
