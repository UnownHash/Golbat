package decoder

import (
	"context"
	"time"

	"golbat/db"

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

		if dbDebugEnabled {
			log.Debugf("[DB_UPDATE] S2Cell Updated cell: %d", s2Cell.Id)
		}

		// Queue through the typed queue
		if s2cellQueue != nil {
			s2cellQueue.Enqueue(S2CellData{
				Id:        s2Cell.Id,
				Latitude:  s2Cell.Latitude,
				Longitude: s2Cell.Longitude,
				Level:     s2Cell.Level.ValueOrZero(),
				Updated:   s2Cell.Updated,
			}, false, 0)
		}
	}
}
