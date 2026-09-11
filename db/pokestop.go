package db

import (
	"context"
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

func GetPokestopPositions(ctx context.Context, db DbDetails, fence *geojson.Feature) ([]QuestLocation, error) {
	const label = "select pokestop-positions"

	matcher, err := newFenceMatcher(fence)
	if err != nil {
		return nil, err
	}

	// Only matches are retained; the candidate rows stream past.
	areas := []QuestLocation{}
	var area QuestLocation
	err = matcher.forEachCandidate(ctx, db.GeneralDb, label,
		"SELECT id, lat, lon FROM pokestop "+fenceBBoxWhere, FenceBoundArgs(fence),
		func(rows *sql.Rows) (float64, float64, error) {
			err := rows.Scan(&area.Id, &area.Latitude, &area.Longitude)
			return area.Latitude, area.Longitude, err
		},
		func() { areas = append(areas, area) })
	if err != nil {
		return nil, err
	}
	return areas, nil
}

func GetQuestStatus(ctx context.Context, db DbDetails, fence *geojson.Feature) (QuestStatus, error) {
	const label = "select quest-status"
	const bboxWhere = fenceBBoxWhere + "AND deleted = 0 "

	status := QuestStatus{}

	matcher, err := newFenceMatcher(fence)
	if err != nil {
		return QuestStatus{}, err
	}

	// Counting in Go keeps the aggregate identical while the per-row polygon
	// test moves out of the database.
	var lat, lon float64
	var hasQuest, hasAltQuest bool
	err = matcher.forEachCandidate(ctx, db.GeneralDb, label,
		"SELECT lat, lon, quest_type IS NOT NULL, alternative_quest_type IS NOT NULL FROM pokestop "+bboxWhere,
		FenceBoundArgs(fence),
		func(rows *sql.Rows) (float64, float64, error) {
			err := rows.Scan(&lat, &lon, &hasQuest, &hasAltQuest)
			return lat, lon, err
		},
		func() {
			status.TotalStops++
			if hasQuest {
				status.ArQuests++
			}
			if hasAltQuest {
				status.NoArQuests++
			}
		})
	if err != nil {
		return QuestStatus{}, err
	}
	return status, nil
}
