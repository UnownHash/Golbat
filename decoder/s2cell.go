package decoder

import (
	"context"
	"github.com/golang/geo/s2"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"gopkg.in/guregu/null.v4"
	"time"
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

func (s2Cell *S2Cell) updateS2CellFromClientMapProto(mapS2CellId uint64) *S2Cell {
	mapS2Cell := s2.CellFromCellID(s2.CellID(mapS2CellId))
	s2Cell.Id = mapS2CellId
	s2Cell.Latitude = mapS2Cell.CapBound().RectBound().Center().Lat.Degrees()
	s2Cell.Longitude = mapS2Cell.CapBound().RectBound().Center().Lng.Degrees()
	s2Cell.Level = null.IntFrom(int64(mapS2Cell.Level()))
	return s2Cell
}

func saveS2CellRecord(ctx context.Context, db db.DbDetails, s2Cell *S2Cell) {
	now := time.Now().Unix()

	if c := s2CellCache.Get(s2Cell.Id); c != nil {
		cachedCell := c.Value()
		if cachedCell.Updated > now-900 {
			return
		}
	}
	s2Cell.Updated = now

	res, err := db.GeneralDb.NamedExecContext(ctx,
		"INSERT INTO s2cell (id, center_lat, center_lon, level, updated) "+
			"VALUES (:id, :center_lat, :center_lon, :level, :updated) "+
			"ON DUPLICATE KEY UPDATE "+
			"center_lat=VALUES(center_lat), center_lon=VALUES(center_lon), level=VALUES(level), updated=VALUES(updated)",
		s2Cell)
	if err != nil {
		log.Errorf("insert s2Cell: %s", err)
		return
	}
	_, _ = res, err

	s2CellCache.Set(s2Cell.Id, *s2Cell, ttlcache.DefaultTTL)
}
