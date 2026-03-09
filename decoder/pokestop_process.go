package decoder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdatePokestopRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort *pogo.FortDetailsOutProto) string {
	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fort.Id)
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return fmt.Sprintf("Error %s", err)
	}
	defer unlock()

	pokestop.updatePokestopFromFortDetailsProto(fort)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)
	return fmt.Sprintf("%s %s", fort.Id, fort.Name)
}

func UpdatePokestopWithQuest(ctx context.Context, db db.DbDetails, quest *pogo.FortSearchOutProto, haveAr bool) string {
	haveArStr := "NoAR"
	if haveAr {
		haveArStr = "AR"
	}

	if quest.ChallengeQuest == nil {
		statsCollector.IncDecodeQuest("error", "no_quest")
		return fmt.Sprintf("%s %s Blank quest", quest.FortId, haveArStr)
	}

	statsCollector.IncDecodeQuest("ok", haveArStr)

	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, quest.FortId)
	if err != nil {
		log.Printf("Update quest %s", err)
		return fmt.Sprintf("error %s", err)
	}
	defer unlock()

	questTitle := pokestop.updatePokestopFromQuestProto(quest, haveAr)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)

	areas := MatchStatsGeofence(pokestop.Lat, pokestop.Lon)
	updateQuestStats(pokestop, haveAr, areas)

	return fmt.Sprintf("%s %s %s", quest.FortId, haveArStr, questTitle)
}

func ClearQuestsWithinGeofence(ctx context.Context, dbDetails db.DbDetails, geofence *geojson.Feature) {
	started := time.Now()
	count, err := RemoveQuestsWithinGeofence(ctx, dbDetails, geofence)
	if err != nil {
		log.Errorf("ClearQuest: Error removing quests: %s", err)
		return
	}
	log.Infof("ClearQuest: Removed quests from %d pokestops in %s", count, time.Since(started))
}

func GetQuestStatusWithGeofence(dbDetails db.DbDetails, geofence *geojson.Feature) db.QuestStatus {
	res, err := db.GetQuestStatus(dbDetails, geofence)
	if err != nil {
		log.Errorf("QuestStatus: Error retrieving quests: %s", err)
		return db.QuestStatus{}
	}
	return res
}

func UpdatePokestopRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, mapFort.Id)
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return false, fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		return false, ""
	}
	defer unlock()

	pokestop.updatePokestopFromGetMapFortsOutProto(mapFort)
	savePokestopRecord(ctx, db, pokestop)
	return true, fmt.Sprintf("%s %s", mapFort.Id, mapFort.Name)
}

func GetPokestopPositions(details db.DbDetails, geofence *geojson.Feature) ([]db.QuestLocation, error) {
	return db.GetPokestopPositions(details, geofence)
}

func UpdatePokestopWithContestData(ctx context.Context, db db.DbDetails, request *pogo.GetContestDataProto, contestData *pogo.GetContestDataOutProto) string {
	if contestData.ContestIncident == nil || len(contestData.ContestIncident.Contests) == 0 {
		return "No contests found"
	}

	var fortId string
	if request != nil {
		fortId = request.FortId
	} else {
		fortId = getFortIdFromContest(contestData.ContestIncident.Contests[0].ContestId)
	}

	if fortId == "" {
		return "No fortId found"
	}

	if len(contestData.ContestIncident.Contests) > 1 {
		log.Errorf("More than one contest found")
		return fmt.Sprintf("More than one contest found in %s", fortId)
	}

	contest := contestData.ContestIncident.Contests[0]

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}
	defer unlock()

	pokestop.updatePokestopFromGetContestDataOutProto(contest)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest %s", fortId)
}

func getFortIdFromContest(id string) string {
	return strings.Split(id, "-")[0]
}

func UpdatePokestopWithPokemonSizeContestEntry(ctx context.Context, db db.DbDetails, request *pogo.GetPokemonSizeLeaderboardEntryProto, contestData *pogo.GetPokemonSizeLeaderboardEntryOutProto) string {
	fortId := getFortIdFromContest(request.GetContestId())

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId)
	if err != nil {
		log.Printf("Get pokestop %s", err)
		return "Error getting pokestop"
	}

	if pokestop == nil {
		log.Infof("Contest data for pokestop %s not found", fortId)
		return fmt.Sprintf("Contest data for pokestop %s not found", fortId)
	}
	defer unlock()

	pokestop.updatePokestopFromGetPokemonSizeContestEntryOutProto(contestData)
	savePokestopRecord(ctx, db, pokestop)

	return fmt.Sprintf("Contest Detail %s", fortId)
}
