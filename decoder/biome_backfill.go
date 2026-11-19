package decoder

import (
	"context"
	"encoding/json"
	"time"

	"github.com/golang/geo/s2"
	log "github.com/sirupsen/logrus"
)

// Upgrade profiles from the original local/geofence scopes without waiting
// for another encounter at every previously mapped cell.
func (worker *biomeWorker) backfillScopes(ctx context.Context) {
	var after uint64
	for ctx.Err() == nil {
		last, err := worker.backfillScopeBatch(ctx, after)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warnf("[BIOMES] could not update legacy cell scopes: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
			continue
		}
		if last == after {
			return
		}
		after = last
	}
}

func (worker *biomeWorker) backfillScopeBatch(ctx context.Context, after uint64) (uint64, error) {
	var cells []struct {
		ID        uint64 `db:"cell_id"`
		Geography []byte `db:"geography"`
		Observed  int64  `db:"observed_at_ms"`
		Scope     string `db:"spawn_scope"`
	}
	if err := worker.db.SelectContext(ctx, &cells, `
		SELECT cell_id, geography, observed_at_ms, spawn_scope FROM biome_cell
		WHERE cell_id > ? AND spawn_scope NOT LIKE 'tz:%' AND spawn_scope NOT LIKE 'cell:%'
		ORDER BY cell_id LIMIT 256`, after); err != nil || len(cells) == 0 {
		return after, err
	}
	tx, err := worker.db.BeginTxx(ctx, nil)
	if err != nil {
		return after, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, cell := range cells {
		var geography biomeGeography
		if err := json.Unmarshal(cell.Geography, &geography); err != nil {
			return after, err
		}
		// An encounter may have updated the geography since the read.
		_, err := tx.ExecContext(ctx, `UPDATE biome_cell SET biome_key = ?, spawn_scope = ?
			WHERE cell_id = ? AND observed_at_ms = ? AND spawn_scope = ?`,
			geography.signature(), biomeSpawnScope(s2.CellID(cell.ID)), cell.ID, cell.Observed, cell.Scope)
		if err != nil {
			return after, err
		}
	}
	return cells[len(cells)-1].ID, tx.Commit()
}
