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
	const bboxWhere = "WHERE lat > ? and lon > ? and lat < ? and lon < ? and enabled = 1 "

	matcher, ok := newFenceMatcher(fence)
	if !ok {
		// Not a polygon: let the database do containment, as before.
		args, err := FenceQueryArgs(fence)
		if err != nil {
			return nil, err
		}
		areas := []QuestLocation{}
		err = db.GeneralDb.Select(&areas, "SELECT id, lat, lon FROM pokestop "+
			bboxWhere+"and "+FenceContainsPredicate,
			args...)
		statsCollector.IncDbQuery("select pokestop-positions", err)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return areas, nil
	}

	rows, err := db.GeneralDb.Queryx("SELECT id, lat, lon FROM pokestop "+bboxWhere,
		FenceBoundArgs(fence)...)
	statsCollector.IncDbQuery("select pokestop-positions", err)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Only matches are retained; the candidate rows stream past.
	areas := []QuestLocation{}
	for rows.Next() {
		var area QuestLocation
		if err := rows.StructScan(&area); err != nil {
			return nil, err
		}
		if matcher.contains(area.Latitude, area.Longitude) {
			areas = append(areas, area)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return areas, nil
}

func GetQuestStatus(db DbDetails, fence *geojson.Feature) (QuestStatus, error) {
	status := QuestStatus{}

	const bboxWhere = "WHERE lat > ? AND lon > ? AND lat < ? AND lon < ? AND enabled = 1 AND deleted = 0 "

	matcher, ok := newFenceMatcher(fence)
	if !ok {
		// Not a polygon: let the database do containment, as before.
		args, err := FenceQueryArgs(fence)
		if err != nil {
			return status, err
		}
		err = db.GeneralDb.Get(&status,
			"SELECT COUNT(*) AS total, "+
				"COUNT(CASE WHEN quest_type IS NOT NULL THEN 1 END) AS ar_quests, "+
				"COUNT(CASE WHEN alternative_quest_type IS NOT NULL THEN 1 END) AS no_ar_quests FROM pokestop "+
				bboxWhere+"AND "+FenceContainsPredicate+" ",
			args...,
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

	// Counting in Go keeps the aggregate identical while the per-row polygon
	// test moves out of the database.
	rows, err := db.GeneralDb.Query(
		"SELECT lat, lon, quest_type IS NOT NULL, alternative_quest_type IS NOT NULL FROM pokestop "+bboxWhere,
		FenceBoundArgs(fence)...,
	)
	statsCollector.IncDbQuery("select quest-status", err)
	if err == sql.ErrNoRows {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	defer rows.Close()

	for rows.Next() {
		var lat, lon float64
		var hasQuest, hasAltQuest bool
		if err := rows.Scan(&lat, &lon, &hasQuest, &hasAltQuest); err != nil {
			return QuestStatus{}, err
		}
		if !matcher.contains(lat, lon) {
			continue
		}
		status.TotalStops++
		if hasQuest {
			status.ArQuests++
		}
		if hasAltQuest {
			status.NoArQuests++
		}
	}
	if err := rows.Err(); err != nil {
		return QuestStatus{}, err
	}

	return status, nil
}
