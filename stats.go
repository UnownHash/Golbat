package main

import (
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/decoder"
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

	if config.Config.Stats {
		go func() {
			for {
				<-ticker.C
				start := time.Now()

				var result sql.Result
				var err error

				result, err = db.Exec("call createStatsAndArchive();")

				elapsed := time.Since(start)

				if err != nil {
					log.Errorf("DB - Archive of pokemon table error %s", err)
				} else {
					rows, _ := result.RowsAffected()
					log.Infof("DB - Archive of pokemon table took %s (%d rows)", elapsed, rows)
				}
			}
		}()
	} else {
		go func() {
			for {
				<-ticker.C
				start := time.Now()

				var result sql.Result
				var err error

				for {
					result, err = db.Exec("DELETE FROM pokemon WHERE expire_timestamp < UNIX_TIMESTAMP() AND expire_timestamp_verified = 1 LIMIT 1000;")

					elapsed := time.Since(start)

					if err != nil {
						log.Errorf("DB - Archive of pokemon table error %s", err)
						break
					} else {
						rows, _ := result.RowsAffected()
						log.Infof("DB - Archive of pokemon table (verified timestamps) took %s (%d rows)", elapsed, rows)
						if rows < 1000 {
							break
						}
					}
				}

				for {
					result, err = db.Exec("DELETE FROM pokemon WHERE expire_timestamp < (UNIX_TIMESTAMP() - 2400) LIMIT 1000;")

					elapsed := time.Since(start)

					if err != nil {
						log.Errorf("DB - Archive of pokemon table error %s", err)
						break
					} else {
						rows, _ := result.RowsAffected()
						log.Infof("DB - Archive of pokemon table took %s (%d rows)", elapsed, rows)
						if rows < 1000 {
							break
						}
					}
				}
			}
		}()
	}

}

func StartIncidentExpiry(db *sqlx.DB) {
	ticker := time.NewTicker(time.Hour)
	go func() {
		for {
			<-ticker.C
			start := time.Now()

			var result sql.Result
			var err error

			result, err = db.Exec("DELETE FROM incident WHERE expiration < UNIX_TIMESTAMP();")

			elapsed := time.Since(start)

			if err != nil {
				log.Errorf("DB - Cleanup of incident table error %s", err)
			} else {
				rows, _ := result.RowsAffected()
				log.Infof("Cleanup of incident table took %s (%d rows)", elapsed, rows)
			}
		}
	}()
}

func StartQuestExpiry(db *sqlx.DB) {
	ticker := time.NewTicker(time.Hour)
	go func() {
		for {
			<-ticker.C
			start := time.Now()
			var totalRows int64 = 0

			var result sql.Result
			var err error

			decoder.ClearPokestopCache()
			result, err = db.Exec("UPDATE pokestop " +
				"SET " +
				"quest_type = NULL," +
				"quest_timestamp = NULL," +
				"quest_target = NULL," +
				"quest_conditions = NULL," +
				"quest_rewards = NULL," +
				"quest_template = NULL," +
				"quest_title = NULL " +
				"WHERE quest_expiry < UNIX_TIMESTAMP();")

			if err != nil {
				log.Errorf("DB - Cleanup of quest table error %s", err)
				return
			}

			rows, _ := result.RowsAffected()

			totalRows += rows

			result, err = db.Exec("UPDATE pokestop " +
				"SET " +
				"alternative_quest_type = NULL," +
				"alternative_quest_timestamp = NULL," +
				"alternative_quest_target = NULL," +
				"alternative_quest_conditions = NULL," +
				"alternative_quest_rewards = NULL," +
				"alternative_quest_template = NULL," +
				"alternative_quest_title = NULL " +
				"WHERE alternative_quest_expiry < UNIX_TIMESTAMP();")

			if err != nil {
				log.Errorf("DB - Cleanup of quest table error %s", err)
			} else {
				rows, _ = result.RowsAffected()
				totalRows += rows

				elapsed := time.Since(start)

				decoder.ClearPokestopCache()

				log.Infof("Cleanup of quest table took %s (%d quests)", elapsed, totalRows)
			}
		}
	}()
}

func StartInMemoryCleardown(db *sqlx.DB) {
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
				return
			}

			rows, _ := result.RowsAffected()
			log.Infof("DB - Cleardown of in-memory pokemon table took %s (%d rows)", elapsed, rows)

		}
	}()
}
