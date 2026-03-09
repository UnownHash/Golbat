package main

import (
	"context"
	"database/sql"
	"fmt"
	"golbat/config"
	db2 "golbat/db"
	"golbat/decoder"
	"time"

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

func StartDbUsageStatsLogger(db *sqlx.DB) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			<-ticker.C

			stats := db.Stats()
			log.Infof("DB - InUse: %d Idle %d WaitDuration %s", stats.InUse, stats.Idle, stats.WaitDuration)
		}
	}()
}

type PokemonIdToDelete struct {
	Id string `db:"id"`
}

const databaseDeleteChunkSize = 500

func StartDatabaseArchiver(db *sqlx.DB) {
	ticker := time.NewTicker(time.Minute)

	go func() {
		for {
			<-ticker.C
			log.Infof("DB - Archive of pokemon table - starting")
			start := time.Now()

			var resultCounter int64
			var result sql.Result
			var err error

			start = time.Now()

			for {
				pokemonId := []PokemonIdToDelete{}
				err = db.Select(&pokemonId,
					fmt.Sprintf("SELECT id FROM pokemon WHERE expire_timestamp < UNIX_TIMESTAMP() AND expire_timestamp_verified = 1 LIMIT %d;", databaseDeleteChunkSize))
				if err != nil {
					log.Errorf("DB - Archive of pokemon table (expire time verified) select error [after %d rows] %s", resultCounter, err)
					break
				}

				if len(pokemonId) == 0 {
					break
				}

				var ids []string
				for i := 0; i < len(pokemonId); i++ {
					ids = append(ids, pokemonId[i].Id)
				}

				query, args, _ := sqlx.In("DELETE FROM pokemon WHERE id IN (?);", ids)
				query = db.Rebind(query)

				result, err = db.Exec(query, args...)

				if err != nil {
					log.Errorf("DB - Archive of pokemon table (expire time verified) error [after %d rows] %s", resultCounter, err)
					break
				} else {
					rows, _ := result.RowsAffected()
					resultCounter += rows
					if rows < databaseDeleteChunkSize {
						elapsed := time.Since(start)
						log.Infof("DB - Archive of pokemon table (verified timestamps) took %s (%d rows)", elapsed, resultCounter)
						break
					}
				}
			}

			log.Infof("DB - Archive of pokemon table - starting second phase (unverified timestamps)")

			resultCounter = 0
			start = time.Now()

			for {
				pokemonId := []PokemonIdToDelete{}
				err = db.Select(&pokemonId,
					fmt.Sprintf("SELECT id FROM pokemon WHERE expire_timestamp < (UNIX_TIMESTAMP() - 2400) AND expire_timestamp_verified = 0 LIMIT %d;", databaseDeleteChunkSize))
				if err != nil {
					log.Errorf("DB - Archive of pokemon table (unverified timestamps) select error [after %d rows] %s", resultCounter, err)
					break
				}

				if len(pokemonId) == 0 {
					break
				}

				var ids []string
				for i := 0; i < len(pokemonId); i++ {
					ids = append(ids, pokemonId[i].Id)
				}

				query, args, _ := sqlx.In("DELETE FROM pokemon WHERE id IN (?);", ids)
				query = db.Rebind(query)

				result, err = db.Exec(query, args...)

				if err != nil {
					log.Errorf("DB - Archive of pokemon table (unverified timestamps) error [after %d rows] %s", resultCounter, err)
					break
				} else {
					rows, _ := result.RowsAffected()
					resultCounter += rows
					if rows < databaseDeleteChunkSize {
						elapsed := time.Since(start)
						log.Infof("DB - Archive of pokemon table (unverified timestamps) took %s (%d rows)", elapsed, resultCounter)
						break
					}
				}
			}
		}
	}()

}

func StartStatsExpiry(db *sqlx.DB) {
	ticker := time.NewTicker(3*time.Hour + 7*time.Minute)
	go func() {
		for {
			<-ticker.C
			start := time.Now()

			var result sql.Result
			var err error

			result, err = db.Exec("DELETE FROM pokemon_area_stats WHERE `datetime` < UNIX_TIMESTAMP() - 10080;")

			elapsed := time.Since(start)

			if err != nil {
				log.Errorf("DB - Cleanup of pokemon_area_stats table error %s", err)
			} else {
				rows, _ := result.RowsAffected()
				log.Infof("DB - Cleanup of pokemon_area_stats table took %s (%d rows)", elapsed, rows)
			}

			tables := []string{"pokemon_stats", "pokemon_shiny_stats", "pokemon_iv_stats", "pokemon_hundo_stats", "pokemon_nundo_stats", "invasion_stats", "quest_stats", "raid_stats"}

			for _, table := range tables {
				start = time.Now()

				result, err = db.Exec(fmt.Sprintf("DELETE FROM %s WHERE `date` < DATE(NOW() - INTERVAL %d DAY);", table, config.Config.Cleanup.StatsDays))
				elapsed = time.Since(start)

				if err != nil {
					log.Errorf("DB - Cleanup of %s table error %s", table, err)
				} else {
					rows, _ := result.RowsAffected()
					log.Infof("DB - Cleanup of %s table took %s (%d rows)", table, elapsed, rows)
				}
			}
		}
	}()
}

func StartIncidentExpiry(db *sqlx.DB) {
	ticker := time.NewTicker(time.Hour + 11*time.Minute)
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
				log.Infof("DB - Cleanup of incident table took %s (%d rows)", elapsed, rows)
			}
		}
	}()
}

func StartTappableExpiry(db *sqlx.DB) {
	ticker := time.NewTicker(time.Hour + 16*time.Minute)
	go func() {
		for {
			<-ticker.C
			start := time.Now()

			var result sql.Result
			var err error

			result, err = db.Exec("DELETE FROM tappable WHERE expire_timestamp < UNIX_TIMESTAMP();")

			elapsed := time.Since(start)

			if err != nil {
				log.Errorf("DB - Cleanup of tappable table error %s", err)
			} else {
				rows, _ := result.RowsAffected()
				log.Infof("DB - Cleanup of tappable table took %s (%d rows)", elapsed, rows)
			}
		}
	}()
}

func StartQuestExpiry(dbDetails db2.DbDetails) {
	ticker := time.NewTicker(time.Hour + 1*time.Minute)
	go func() {
		for {
			<-ticker.C
			start := time.Now()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			count, err := decoder.ExpireQuests(ctx, dbDetails)
			cancel()

			if err != nil {
				log.Errorf("DB - Cleanup of quest table error %s", err)
			}

			elapsed := time.Since(start)
			log.Infof("DB - Cleanup of quest table took %s (%d pokestops)", elapsed, count)
		}
	}()
}
