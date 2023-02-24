package db

import (
	"context"
	"database/sql"
	"github.com/jmoiron/sqlx"
	"golbat/geo"
)

type QuestLocation struct {
	Id        string  `db:"id"`
	Latitude  float64 `db:"lat"`
	Longitude float64 `db:"lon"`
}

type FortId struct {
	Id string `db:"id"`
}

func GetPokestopPositions(db DbDetails, fence geo.Geofence) ([]QuestLocation, error) {
	bbox := fence.GetBoundingBox()

	areas := []QuestLocation{}
	err := db.GeneralDb.Select(&areas, "SELECT id, lat, lon FROM pokestop "+
		"WHERE lat > ? and lon > ? and lat < ? and lon < ? and enabled = 1 "+
		"and ST_CONTAINS(ST_GEOMFROMTEXT('POLYGON(("+fence.ToPolygonString()+"))'), point(lat,lon))",
		bbox.MinimumLatitude, bbox.MinimumLongitude, bbox.MaximumLatitude, bbox.MaximumLongitude)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return areas, nil
}

func RemoveQuests(ctx context.Context, db DbDetails, fence geo.Geofence) (sql.Result, error) {
	bbox := fence.GetBoundingBox()

	query := "UPDATE pokestop " +
		"SET " +
		"quest_type = NULL," +
		"quest_timestamp = NULL," +
		"quest_target = NULL," +
		"quest_conditions = NULL," +
		"quest_rewards = NULL," +
		"quest_template = NULL," +
		"quest_title = NULL, " +
		"quest_expiry = NULL, " +
		"alternative_quest_type = NULL," +
		"alternative_quest_timestamp = NULL," +
		"alternative_quest_target = NULL," +
		"alternative_quest_conditions = NULL," +
		"alternative_quest_rewards = NULL," +
		"alternative_quest_template = NULL," +
		"alternative_quest_title = NULL, " +
		"alternative_quest_expiry = NULL " +
		"WHERE lat > ? and lon > ? and lat < ? and lon < ? and enabled = 1 " +
		"and ST_CONTAINS(ST_GEOMFROMTEXT('POLYGON((" + fence.ToPolygonString() + "))'), point(lat,lon))"
	return db.GeneralDb.ExecContext(ctx, query,
		bbox.MinimumLatitude, bbox.MinimumLongitude, bbox.MaximumLatitude, bbox.MaximumLongitude)
}

func FindOldPokestops(ctx context.Context, db DbDetails, cellId int64) ([]string, error) {
	fortIds := []FortId{}
	err := db.GeneralDb.SelectContext(ctx, &fortIds,
		"SELECT id FROM pokestop WHERE deleted = 0 AND cell_id = ? AND updated < UNIX_TIMESTAMP() - 3600;", cellId)
	if err != nil {
		return nil, err
	}
	if len(fortIds) == 0 {
		return nil, nil
	}

	// convert slices of struct to slices of string
	var list []string
	for _, element := range fortIds {
		list = append(list, element.Id)
	}
	return list, nil
}

func ClearOldPokestops(ctx context.Context, db DbDetails, stopIds []string) error {
	query, args, _ := sqlx.In("UPDATE pokestop SET deleted = 1 WHERE id IN (?);", stopIds)
	query = db.GeneralDb.Rebind(query)

	_, err := db.GeneralDb.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return nil
}
