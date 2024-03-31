package db

import (
	"database/sql"
	"errors"
	log "github.com/sirupsen/logrus"
	"time"
)

type GymStats struct {
	TeamId   int8    `db:"team_id"`
	InBattle bool    `db:"in_battle"`
	Count    float64 `db:"count"`
}

type RaidStats struct {
	RaidLevel int64   `db:"raid_level"`
	Count     float64 `db:"count"`
}

type IncidentsStats struct {
	DisplayType int8    `db:"display_type"`
	Confirmed   bool    `db:"confirmed"`
	Count       float64 `db:"count"`
}

type LureStats struct {
	LureId int32   `db:"lure_id"`
	Count  float64 `db:"count"`
}

type QuestStats struct {
	NoAr float64 `db:"no_ar"`
	Ar   float64 `db:"ar"`
}

func GetGymStats(db DbDetails) ([]GymStats, error) {
	stats := []GymStats{}

	// fetch counts for gyms updated within last hour
	err := db.GeneralDb.Select(&stats,
		"SELECT count(*) as count, team_id, in_battle "+
			"FROM `gym` WHERE updated > UNIX_TIMESTAMP() - 3600 GROUP BY team_id, in_battle;",
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return stats, nil
}

func GetRaidStats(db DbDetails) ([]RaidStats, error) {
	stats := []RaidStats{}

	err := db.GeneralDb.Select(&stats,
		"SELECT count(*) AS count, COALESCE(raid_level, 0) AS raid_level "+
			"FROM `gym` WHERE raid_end_timestamp > UNIX_TIMESTAMP() GROUP BY raid_level;",
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return stats, nil
}

func GetIncidentsStats(db DbDetails) ([]IncidentsStats, error) {
	stats := []IncidentsStats{}

	err := db.GeneralDb.Select(&stats,
		"SELECT count(*) as count, display_type, confirmed "+
			"FROM `incident` WHERE expiration > UNIX_TIMESTAMP() AND display_type != 0 "+
			"GROUP BY display_type, confirmed;",
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return stats, nil
}

func GetLureStats(db DbDetails) ([]LureStats, error) {
	stats := []LureStats{}

	err := db.GeneralDb.Select(&stats,
		"SELECT count(*) as count, lure_id "+
			"FROM `pokestop` WHERE lure_expire_timestamp > UNIX_TIMESTAMP() GROUP BY lure_id;",
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return stats, nil
}

func GetQuestStats(db DbDetails) (QuestStats, error) {
	stats := QuestStats{}
	err := db.GeneralDb.Get(&stats,
		"SELECT COUNT(quest_type) AS no_ar, COUNT(alternative_quest_type) AS ar FROM pokestop;",
	)

	if errors.Is(err, sql.ErrNoRows) {
		return QuestStats{}, nil
	}

	if err != nil {
		return QuestStats{}, err
	}

	return stats, nil
}

func PromLiveStatsUpdater(dbDetails DbDetails, sleepTime int) {
	log.Infof("[Prometheus] LiveStats loop started with %d seconds of sleep", sleepTime)
	for {
		start := time.Now()

		gymStats, err := GetGymStats(dbDetails)
		if err == nil {
			for _, stat := range gymStats {
				statsCollector.SetGyms(stat.TeamId, stat.InBattle, stat.Count)
			}
		}

		raidStats, err := GetRaidStats(dbDetails)
		if err == nil {
			for _, stat := range raidStats {
				statsCollector.SetRaids(stat.RaidLevel, stat.Count)
			}
		}

		incidentsStats, err := GetIncidentsStats(dbDetails)
		if err == nil {
			for _, stat := range incidentsStats {
				statsCollector.SetIncidents(stat.DisplayType, stat.Confirmed, stat.Count)
			}
		}

		luresStats, err := GetLureStats(dbDetails)
		if err == nil {
			for _, stat := range luresStats {
				statsCollector.SetLures(stat.LureId, stat.Count)
			}
		}

		questStats, err := GetQuestStats(dbDetails)
		if err == nil {
			statsCollector.SetQuests(questStats.Ar, questStats.NoAr)
		}

		elapsed := time.Since(start)
		log.Infof("[Prometheus] LiveStats fetched in: %s", elapsed)

		time.Sleep(time.Duration(sleepTime) * time.Second)
	}
}
