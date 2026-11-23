package decoder

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/golang/geo/s2"
	"github.com/jmoiron/sqlx"
)

// One collector owns this cache. Keep only recent profiles on large deployments.
const maxBiomeCellProfiles = 65536

type biomeCellProfile struct {
	geography biomeGeography
	seen      int64
}

func (g biomeGeography) equal(other biomeGeography) bool {
	return g.Landform == other.Landform && g.Landcover == other.Landcover &&
		g.Temperature == other.Temperature && g.Moisture == other.Moisture &&
		g.IsInPark == other.IsInPark && g.ParkStatus == other.ParkStatus &&
		g.Settlement == other.Settlement && slices.Equal(g.Features, other.Features)
}

func (w *biomeWorker) persistCells(ctx context.Context, events []biomeEvent) error {
	if len(events) == 0 {
		return nil
	}
	if w.cells == nil {
		w.cells = make(map[uint64]biomeCellProfile)
	}
	// Stage changes until the database write succeeds. Repeated encounters
	// with a cached profile take neither a SQL query nor JSON encoding.
	profiles := make(map[uint64]biomeCellProfile, len(events))
	var missing []uint64
	for _, event := range events {
		if _, ok := profiles[event.cellID]; ok {
			continue
		}
		profile, ok := w.cells[event.cellID]
		profiles[event.cellID] = profile
		if !ok {
			missing = append(missing, event.cellID)
		}
	}
	if len(missing) > 0 {
		query, args, err := sqlx.In("SELECT cell_id, geography, observed_at_ms FROM biome_cell WHERE cell_id IN (?)", missing)
		if err != nil {
			return err
		}
		var stored []struct {
			ID        uint64 `db:"cell_id"`
			Geography []byte `db:"geography"`
			At        int64  `db:"observed_at_ms"`
		}
		if err := w.db.SelectContext(ctx, &stored, query, args...); err != nil {
			return err
		}
		for _, row := range stored {
			var fields biomeGeography
			if err := json.Unmarshal(row.Geography, &fields); err != nil {
				return err
			}
			profiles[row.ID] = biomeCellProfile{fields, max(row.At, w.cellFloor)}
		}
	}

	var changed map[uint64]biomeEvent
	for _, event := range events {
		previous := profiles[event.cellID]
		if event.atMS <= previous.seen {
			continue
		}
		if previous.seen == 0 || !previous.geography.equal(event.geography) {
			if changed == nil {
				changed = make(map[uint64]biomeEvent)
			}
			changed[event.cellID] = event
		}
		profiles[event.cellID] = biomeCellProfile{event.geography, event.atMS}
	}
	if len(changed) > 0 {
		rows := make([]map[string]any, 0, len(changed))
		for id, event := range changed {
			fields, err := json.Marshal(event.geography)
			if err != nil {
				return err
			}
			rows = append(rows, map[string]any{
				"cell": id, "geography": string(fields), "biome": event.geography.signature(),
				"scope": biomeSpawnScope(s2.CellID(id)), "at": event.atMS,
			})
		}
		_, err := w.db.NamedExecContext(ctx, `
			INSERT INTO biome_cell (cell_id, geography, biome_key, spawn_scope, observed_at_ms, geography_changed_at_ms)
			VALUES (:cell, :geography, :biome, :scope, :at, :at)
			ON DUPLICATE KEY UPDATE
			 geography_changed_at_ms = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(observed_at_ms), geography_changed_at_ms),
			 geography = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(geography), geography),
			 biome_key = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(biome_key), biome_key),
			 spawn_scope = IF(VALUES(observed_at_ms) > observed_at_ms, VALUES(spawn_scope), spawn_scope),
			 observed_at_ms = GREATEST(observed_at_ms, VALUES(observed_at_ms))`, rows)
		if err != nil {
			return err
		}
	}
	if len(w.cells)+len(missing) > maxBiomeCellProfiles {
		// A late correction must not roll back a more recent confirmation
		// once its per-cell timestamp is forgotten.
		for _, profile := range w.cells {
			w.cellFloor = max(w.cellFloor, profile.seen)
		}
		clear(w.cells)
	}
	for id, profile := range profiles {
		w.cells[id] = profile
	}
	return nil
}
