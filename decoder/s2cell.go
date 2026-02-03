package decoder

import (
	"context"
	"strconv"
	"strings"
	"time"

	"golbat/db"
	"golbat/decoder/writebehind"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
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
	var cellsToWrite []*writebehind.S2CellData

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

		cellsToWrite = append(cellsToWrite, &writebehind.S2CellData{
			Id:        s2Cell.Id,
			Latitude:  s2Cell.Latitude,
			Longitude: s2Cell.Longitude,
			Level:     s2Cell.Level.ValueOrZero(),
			Updated:   s2Cell.Updated,
		})
	}

	if len(cellsToWrite) == 0 {
		return
	}

	if dbDebugEnabled {
		var updatedCells []string
		for _, cell := range cellsToWrite {
			updatedCells = append(updatedCells, strconv.FormatUint(cell.Id, 10))
		}
		log.Debugf("[DB_UPDATE] S2Cell Updated cells: %s", strings.Join(updatedCells, ","))
	}

	// Queue through the accumulator if available
	if s2CellAccumulator != nil {
		s2CellAccumulator.Add(cellsToWrite)
	} else {
		// Fallback to direct write if accumulator not initialized
		_ = s2CellBatchWrite(db, cellsToWrite)
	}
}

// s2CellBatchWrite performs the actual batch database write for S2Cells
// This is called by both direct writes and the accumulator
func s2CellBatchWrite(db db.DbDetails, cells []*writebehind.S2CellData) error {
	ctx := context.Background()

	// Convert to slice of S2Cell for the query
	s2Cells := make([]*S2Cell, len(cells))
	for i, cell := range cells {
		s2Cells[i] = &S2Cell{
			Id:        cell.Id,
			Latitude:  cell.Latitude,
			Longitude: cell.Longitude,
			Level:     null.IntFrom(cell.Level),
			Updated:   cell.Updated,
		}
	}

	_, err := db.GeneralDb.NamedExecContext(ctx, `
		INSERT INTO s2cell (id, center_lat, center_lon, level, updated)
		VALUES (:id, :center_lat, :center_lon, :level, :updated)
		ON DUPLICATE KEY UPDATE updated=VALUES(updated)
	`, s2Cells)

	statsCollector.IncDbQuery("insert s2cell", err)
	if err != nil {
		log.Errorf("s2CellBatchWrite: %s", err)
		return err
	}
	return nil
}
