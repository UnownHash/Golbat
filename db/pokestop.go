package db

import (
	"context"
	"database/sql"
	"errors"

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

func RemoveQuests(ctx context.Context, db DbDetails, fence *geojson.Feature) (int64, error) {
	const updateChunkSize = 500

	//goland:noinspection GoPreferNilSlice
	allIdsToUpdate := []string{}
	var removedQuestsCount int64

	bbox := fence.Geometry.Bound()
	bytes, err := fence.MarshalJSON()
	if err != nil {
		statsCollector.IncDbQuery("remove quests", err)
		return removedQuestsCount, err
	}

	idQueryString := "SELECT `id` FROM `pokestop` " +
		"WHERE lat >= ? and lon >= ? and lat <= ? and lon <= ? and enabled = 1 " +
		"AND ST_CONTAINS(ST_GeomFromGeoJSON('" + string(bytes) + "', 2, 0), POINT(lon, lat))"

	//log.Debugf("Clear quests query: %s", idQueryString)

	// collect allIdsToUpdate
	err = db.GeneralDb.Select(&allIdsToUpdate, idQueryString,
		bbox.Min.Lat(), bbox.Min.Lon(), bbox.Max.Lat(), bbox.Max.Lon(),
	)

	if errors.Is(err, sql.ErrNoRows) {
		statsCollector.IncDbQuery("remove quests", err)
		return removedQuestsCount, nil
	}

	if err != nil {
		statsCollector.IncDbQuery("remove quests", err)
		return removedQuestsCount, err
	}

	for {
		// take at most updateChunkSize elements from allIdsToUpdate
		updateIdsCount := len(allIdsToUpdate)

		if updateIdsCount == 0 {
			break
		}

		if updateIdsCount > updateChunkSize {
			updateIdsCount = updateChunkSize
		}

		updateIds := allIdsToUpdate[:updateIdsCount]

		// remove processed elements from allIdsToUpdate
		allIdsToUpdate = allIdsToUpdate[updateIdsCount:]

		query, args, _ := sqlx.In("UPDATE pokestop "+
			"SET "+
			"quest_type = NULL,"+
			"quest_timestamp = NULL,"+
			"quest_target = NULL,"+
			"quest_conditions = NULL,"+
			"quest_rewards = NULL,"+
			"quest_template = NULL,"+
			"quest_title = NULL, "+
			"quest_expiry = NULL, "+
			"alternative_quest_type = NULL,"+
			"alternative_quest_timestamp = NULL,"+
			"alternative_quest_target = NULL,"+
			"alternative_quest_conditions = NULL,"+
			"alternative_quest_rewards = NULL,"+
			"alternative_quest_template = NULL,"+
			"alternative_quest_title = NULL, "+
			"alternative_quest_expiry = NULL "+
			"WHERE id IN (?)", updateIds)

		query = db.GeneralDb.Rebind(query)
		res, err := db.GeneralDb.ExecContext(ctx, query, args...)

		if err != nil {
			statsCollector.IncDbQuery("remove quests", err)
			return removedQuestsCount, err
		}

		rowsAffected, _ := res.RowsAffected()
		removedQuestsCount += rowsAffected
	}

	statsCollector.IncDbQuery("remove quests", err)
	return removedQuestsCount, err
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
