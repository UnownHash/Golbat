package decoder

import (
	"context"
	"strconv"
	"strings"
	"time"

	"golbat/db"

	"github.com/golang/geo/s2"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

type S2Cell struct {
	Id        uint64   `db:"id"`
	Latitude  float64  `db:"center_lat"`
	Longitude float64  `db:"center_lon"`
	Level     null.Int `db:"level"`
	Updated   int64    `db:"updated"`
}

// CREATE TABLE `weather` (
//  `id` bigint NOT NULL,
//  `level` tinyint unsigned DEFAULT NULL,
//  `center_lat` double(18,14) NOT NULL DEFAULT '0.00000000000000',
//  `center_lon` double(18,14) NOT NULL DEFAULT '0.00000000000000',
//  `updated` int unsigned NOT NULL,
//  PRIMARY KEY (`id`)
//)

func saveS2CellRecords(ctx context.Context, db db.DbDetails, cellIds []uint64) {
	now := time.Now().Unix()
	var outputCellIds []*S2Cell

	// prepare list of cells to update
	for _, cellId := range cellIds {
		var s2Cell *S2Cell

		if c := s2CellCache.Get(cellId); c != nil {
			cachedCell := c.Value()
			if cachedCell.Updated > now-GetUpdateThreshold(900) {
				continue
			}
			s2Cell = cachedCell
		} else {
			mapS2Cell := s2.CellFromCellID(s2.CellID(cellId))
			s2Cell = &S2Cell{}
			s2Cell.Id = cellId
			s2Cell.Latitude = mapS2Cell.CapBound().RectBound().Center().Lat.Degrees()
			s2Cell.Longitude = mapS2Cell.CapBound().RectBound().Center().Lng.Degrees()
			s2Cell.Level = null.IntFrom(int64(mapS2Cell.Level()))

			s2CellCache.Set(s2Cell.Id, s2Cell, ttlcache.DefaultTTL)
		}
		s2Cell.Updated = now

		outputCellIds = append(outputCellIds, s2Cell)
	}

	if len(outputCellIds) == 0 {
		return
	}

	if dbDebugEnabled {
		var updatedCells []string
		for _, s2cell := range outputCellIds {
			updatedCells = append(updatedCells, strconv.FormatUint(s2cell.Id, 10))
		}
		log.Debugf("[DB_S2CELL] Updated cells: %s", strings.Join(updatedCells, ","))
	}

	// run bulk query
	_, err := db.GeneralDb.NamedExecContext(ctx, `
		INSERT INTO s2cell (id, center_lat, center_lon, level, updated)
		VALUES (:id, :center_lat, :center_lon, :level, :updated)
		ON DUPLICATE KEY UPDATE updated=VALUES(updated)
	`, outputCellIds)

	statsCollector.IncDbQuery("insert s2cell", err)
	if err != nil {
		log.Errorf("saveS2CellRecords: %s", err)
		return
	}

	// since cache is now a pointer, ttl will already have been updated
	//for _, cellId := range outputCellIds {
	//	s2CellCache.Set(cellId.Id, cellId, ttlcache.DefaultTTL)
	//}
}
