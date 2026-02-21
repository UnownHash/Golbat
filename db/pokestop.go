package db

import (
	"database/sql"

	"github.com/paulmach/orb/geojson"
)

type QuestLocation struct {
	Id        string  `db:"id" json:"id"`
	Latitude  float64 `db:"lat" json:"latitude"`
	Longitude float64 `db:"lon" json:"longitude"`
}

type FortId struct {
	Id string `db:"id"`
}

type QuestStatus struct {
	ArQuests   uint32 `db:"ar_quests" json:"ar_quests"`
	NoArQuests uint32 `db:"no_ar_quests" json:"no_ar_quests"`
	TotalStops uint32 `db:"total" json:"total"`
}

func GetPokestopPositions(db DbDetails, fence *geojson.Feature) ([]QuestLocation, error) {
	bbox := fence.Geometry.Bound()
	bytes, err := fence.MarshalJSON()
	if err != nil {
		return nil, err
	}
	areas := []QuestLocation{}
	err = db.GeneralDb.Select(&areas, "SELECT id, lat, lon FROM pokestop "+
		"WHERE lat > ? and lon > ? and lat < ? and lon < ? and enabled = 1 "+
		"and ST_CONTAINS(ST_GeomFromGeoJSON('"+string(bytes)+"', 2, 0), POINT(lon, lat))",
		bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon())

	statsCollector.IncDbQuery("select pokestop-positions", err)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return areas, nil
}

func GetQuestStatus(db DbDetails, fence *geojson.Feature) (QuestStatus, error) {
	bbox := fence.Geometry.Bound()
	status := QuestStatus{}

	bytes, err := fence.MarshalJSON()
	if err != nil {
		return status, err
	}

	err = db.GeneralDb.Get(&status,
		"SELECT COUNT(*) AS total, "+
			"COUNT(CASE WHEN quest_type IS NOT NULL THEN 1 END) AS ar_quests, "+
			"COUNT(CASE WHEN alternative_quest_type IS NOT NULL THEN 1 END) AS no_ar_quests FROM pokestop "+
			"WHERE lat > ? AND lon > ? AND lat < ? AND lon < ? AND enabled = 1 AND deleted = 0 "+
			"AND ST_CONTAINS(ST_GeomFromGeoJSON('"+string(bytes)+"', 2, 0), POINT(lon, lat)) ",
		bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon(),
	)

	statsCollector.IncDbQuery("select quest-status", err)
	if err == sql.ErrNoRows {
		return status, nil
	}

	if err != nil {
		return status, err
	}

	return status, nil
}
