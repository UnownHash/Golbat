package db

import (
	"database/sql"
	"golbat/geo"
)

type QuestLocation struct {
	Id        string  `db:"id"`
	Latitude  float64 `db:"lat"`
	Longitude float64 `db:"lon"`
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

func RemoveQuests(db DbDetails, fence geo.Geofence) (sql.Result, error) {
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
	return db.GeneralDb.Exec(query,
		bbox.MinimumLatitude, bbox.MinimumLongitude, bbox.MaximumLatitude, bbox.MaximumLongitude)
}
