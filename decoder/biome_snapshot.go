package decoder

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

func (worker *biomeWorker) refresh(ctx context.Context, now time.Time) error {
	// Keep deduplication history across the window and restarts. Bounded
	// deletes avoid holding a large range of locks in a single statement.
	expiry := now.Add(-biomeRetention).UnixMilli()
	for {
		const batchSize = 1000
		var keys [][]byte
		if err := worker.db.SelectContext(ctx, &keys, `
			SELECT encounter_key FROM biome_spawn_observation
			WHERE observed_at_ms <= ? ORDER BY observed_at_ms LIMIT ?`, expiry, batchSize); err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		query, args, err := sqlx.In(`DELETE FROM biome_spawn_observation
			WHERE encounter_key IN (?) AND observed_at_ms <= ?`, keys, expiry)
		if err != nil {
			return err
		}
		if _, err := worker.db.ExecContext(ctx, query, args...); err != nil {
			return err
		}
		if len(keys) < batchSize {
			break
		}
	}
	start := now.Add(-biomeWindow).UnixMilli()
	end := now.UnixMilli()
	if worker.lastLoss.Load() >= start {
		return nil
	}

	// Isolate the session used by temporary tables. Clean up after the
	// transaction, also when its context expires before or during a write.
	connection, err := worker.db.Connx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()

	const dropStaging = "DROP TEMPORARY TABLE IF EXISTS biome_next_group, biome_next_rate, biome_window"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = connection.ExecContext(cleanupCtx, dropStaging)
	}()
	if _, err = connection.ExecContext(ctx, dropStaging); err != nil {
		return err
	}

	// A locking source read would stall concurrent inserts while the staging tables are built.
	tx, err := connection.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Materialize the window once so concurrent ingest cannot change the
	// denominator between the group and rate calculations.
	if _, err = tx.ExecContext(ctx, `
		CREATE TEMPORARY TABLE biome_window ENGINE=InnoDB AS
		SELECT spawn_scope, biome_key, weather, cell_id, pokemon_id, form
		FROM biome_spawn_observation WHERE observed_at_ms > ? AND observed_at_ms <= ?`, start, end); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		CREATE TEMPORARY TABLE biome_next_group (PRIMARY KEY (spawn_scope, biome_key, weather))
		ENGINE=InnoDB AS
		SELECT spawn_scope, biome_key, weather, COUNT(*) AS sample_count, COUNT(DISTINCT cell_id) AS cell_count
		FROM biome_window
		GROUP BY spawn_scope, biome_key, weather
		HAVING COUNT(*) >= 100 AND COUNT(DISTINCT cell_id) >= 3`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		CREATE TEMPORARY TABLE biome_next_rate
		    (PRIMARY KEY (spawn_scope, biome_key, weather, pokemon_id, form))
		ENGINE=InnoDB AS
		SELECT o.spawn_scope, o.biome_key, o.weather, o.pokemon_id, o.form,
		       CAST(CAST(COUNT(*) AS DECIMAL(32,15)) * 100 / g.sample_count AS DECIMAL(18,12)) AS percent
		FROM biome_window o
		JOIN biome_next_group g USING (spawn_scope, biome_key, weather)
		GROUP BY o.spawn_scope, o.biome_key, o.weather, o.pokemon_id, o.form, g.sample_count`); err != nil {
		return err
	}
	if worker.lastLoss.Load() >= start {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE g FROM biome_spawn_group g
		LEFT JOIN biome_next_group n USING (spawn_scope, biome_key, weather)
		WHERE n.biome_key IS NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO biome_spawn_group
		    (spawn_scope, biome_key, weather, sample_count, cell_count,
		     window_start_ms, window_end_ms, valid_until_ms)
		SELECT spawn_scope, biome_key, weather, sample_count, cell_count, ?, ?, ?
		FROM biome_next_group
		ON DUPLICATE KEY UPDATE
		    sample_count = VALUES(sample_count), cell_count = VALUES(cell_count),
		    window_start_ms = VALUES(window_start_ms), window_end_ms = VALUES(window_end_ms),
		    valid_until_ms = VALUES(valid_until_ms)`, start, end, now.Add(2*biomeRefresh).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE r FROM biome_spawn_rate r
		LEFT JOIN biome_next_rate n USING (spawn_scope, biome_key, weather, pokemon_id, form)
		WHERE n.pokemon_id IS NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO biome_spawn_rate (spawn_scope, biome_key, weather, pokemon_id, form, percent)
		SELECT n.spawn_scope, n.biome_key, n.weather, n.pokemon_id, n.form, n.percent
		FROM biome_next_rate n
		LEFT JOIN biome_spawn_rate r USING (spawn_scope, biome_key, weather, pokemon_id, form)
		WHERE r.pokemon_id IS NULL OR r.percent <> n.percent
		ON DUPLICATE KEY UPDATE percent = VALUES(percent)`); err != nil {
		return err
	}
	return tx.Commit()
}
