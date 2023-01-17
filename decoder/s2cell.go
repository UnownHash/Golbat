package decoder

import (
	"database/sql"
	"github.com/golang/geo/s2"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
)

type S2cell struct {
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

func getS2cellRecord(db db.DbDetails, s2cellId int64) (*S2cell, error) {
	inMemoryS2cell := s2cellCache.Get(s2cellId)
	if inMemoryS2cell != nil {
		s2cell := inMemoryS2cell.Value()
		return &s2cell, nil
	}
	s2cell := S2cell{}

	err := db.GeneralDb.Get(&s2cell, "SELECT id, center_lat, center_lon, level, updated FROM s2cell WHERE id = ?", s2cellId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	s2cellCache.Set(s2cellId, s2cell, ttlcache.DefaultTTL)
	return &s2cell, nil
}

func (s2_cell *S2cell) updateS2cellFromClientMapCellProto(clientS2Cell *pogo.ClientMapCellProto) *S2cell {
	s2_cell.Id = clientS2Cell.S2CellId
	s2cell := s2.CellFromCellID(s2.CellID(clientS2Cell.S2CellId))
	s2_cell.Latitude = s2cell.CapBound().RectBound().Center().Lat.Degrees()
	s2_cell.Longitude = s2cell.CapBound().RectBound().Center().Lng.Degrees()
	s2_cell.Level = null.IntFrom(int64(s2cell.Level()))
	return s2_cell
}

func hasChangesS2cell(old *S2cell, new *S2cell) bool {
	return !cmp.Equal(old, new, ignoreNearFloats)
}

func saveS2CellRecord(db db.DbDetails, s2cell *S2cell) {
	oldS2Cell, _ := getS2cellRecord(db, int64(s2cell.Id))
	if oldS2Cell != nil && !hasChangesS2cell(oldS2Cell, s2cell) {
		return
	}
	if oldS2Cell == nil {
		res, err := db.GeneralDb.NamedExec(
			"INSERT INTO s2cell ("+
				"id, center_lat, center_lon, level, updated)"+
				"VALUES ("+
				":id, :center_lat, :center_lon, :level,"+
				"UNIX_TIMESTAMP())",
			s2cell)
		if err != nil {
			log.Errorf("insert s2cell: %s", err)
			return
		}
		_ = res
	} else {
		res, err := db.GeneralDb.NamedExec("UPDATE s2cell SET "+
			"center_lat = :latitude, "+
			"center_lon = :longitude, "+
			"level = :level, "+
			"updated = UNIX_TIMESTAMP() "+
			"WHERE id = :id",
			s2cell)
		if err != nil {
			log.Errorf("update s2cell: %s", err)
			return
		}
		_ = res
	}
	s2cellCache.Set(int64(s2cell.Id), *s2cell, ttlcache.DefaultTTL)
}
