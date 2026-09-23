package decoder

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/geo"
)

// RepairZeroLocationSpawnpoints rewrites every spawnpoint row stored at 0,0
// with the location derived from its id (geo.SpawnpointLocation). Such
// rows were written while the game sent WildPokemonProtos at 0,0, before
// the decoder derived the location instead. Run before Preload so the cache
// is warmed with repaired rows. The select is served by ix_coords and the
// row count is bounded by the damage, so this is one UPDATE per row.
// repairProgressBatch is how many rows RepairZeroLocationSpawnpoints
// processes between progress summaries.
const repairProgressBatch = 500

func RepairZeroLocationSpawnpoints(dbDetails db.DbDetails) {
	start := time.Now()
	ctx := context.Background()

	var ids []int64
	err := dbDetails.GeneralDb.SelectContext(ctx, &ids, "SELECT id FROM spawnpoint WHERE lat = 0 AND lon = 0")
	getStatsCollector().IncDbQuery("select spawnpoint zero location", err)
	if err != nil {
		log.Errorf("RepairZeroLocationSpawnpoints: select failed - %s", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	repaired := 0
	for i, id := range ids {
		location, ok := geo.SpawnpointLocation(id)
		if !ok {
			log.Warnf("RepairZeroLocationSpawnpoints: spawnpoint %d/%x does not decode to a level-%d cell; left at 0,0", id, id, geo.SpawnpointCellLevel)
			continue
		}
		_, err := dbDetails.GeneralDb.ExecContext(ctx,
			"UPDATE spawnpoint SET lat = ?, lon = ? WHERE id = ? AND lat = 0 AND lon = 0", location.Latitude, location.Longitude, id)
		getStatsCollector().IncDbQuery("update spawnpoint zero location", err)
		if err != nil {
			log.Errorf("RepairZeroLocationSpawnpoints: spawnpoint %d/%x update failed - %s", id, id, err)
			continue
		}
		log.Debugf("RepairZeroLocationSpawnpoints: spawnpoint %d/%x repaired to %f,%f", id, id, location.Latitude, location.Longitude)
		repaired++
		// Progress is summarised per batch; per-row detail is debug-only.
		if done := i + 1; done%repairProgressBatch == 0 && done < len(ids) {
			log.Infof("RepairZeroLocationSpawnpoints: %d/%d rows processed, %d repaired so far", done, len(ids), repaired)
		}
	}
	log.Infof("RepairZeroLocationSpawnpoints: %d row(s) at 0,0, %d repaired in %v", len(ids), repaired, time.Since(start))
}
