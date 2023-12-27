package db

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
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

func RemoveQuests(ctx context.Context, db DbDetails, fence *geojson.Feature) (sql.Result, error) {
	bbox := fence.Geometry.Bound()
	bytes, err := fence.MarshalJSON()
	if err != nil {
		return nil, err
	}

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
		"and ST_CONTAINS(ST_GeomFromGeoJSON('" + string(bytes) + "', 2, 0), POINT(lon, lat))"
	res, err := db.GeneralDb.ExecContext(ctx, query,
		bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon())

	statsCollector.IncDbQuery("remove quests", err)
	return res, err
}

func FindOldPokestops(ctx context.Context, db DbDetails, cellId int64) ([]string, error) {
	fortIds := []FortId{}
	err := db.GeneralDb.SelectContext(ctx, &fortIds,
		"SELECT id FROM pokestop WHERE deleted = 0 AND cell_id = ? AND updated < UNIX_TIMESTAMP() - 3600;", cellId)
	statsCollector.IncDbQuery("select old-pokestops", err)
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
	statsCollector.IncDbQuery("clear old-pokestops", err)
	if err != nil {
		return err
	}
	return nil
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
