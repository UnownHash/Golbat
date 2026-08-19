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
	fortId, ok := ParseFortId(fort.Id)
	if !ok {
		log.Errorf("UpdatePokestopRecordWithFortDetailsOutProto: unparseable fort id %q", fort.Id)
		return fmt.Sprintf("Error: unparseable fort id %q", fort.Id)
	}
	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fortId, "UpdatePokestopFromFortDetails")
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return fmt.Sprintf("Error %s", err)
	}
	defer unlock()

	pokestop.updatePokestopFromFortDetailsProto(fortId, fort)

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
		getStatsCollector().IncDecodeQuest("error", "no_quest")
		return fmt.Sprintf("%s %s Blank quest", quest.FortId, haveArStr)
	}

	getStatsCollector().IncDecodeQuest("ok", haveArStr)

	fortId, ok := ParseFortId(quest.FortId)
	if !ok {
		log.Errorf("UpdatePokestopWithQuest: unparseable fort id %q", quest.FortId)
		return fmt.Sprintf("%s %s unparseable fort id", quest.FortId, haveArStr)
	}

	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fortId, "UpdatePokestopWithQuest")
	if err != nil {
		log.Printf("Update quest %s", err)
		return fmt.Sprintf("error %s", err)
	}
	defer unlock()

	questTitle := pokestop.updatePokestopFromQuestProto(quest, haveAr)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)

	areas := MatchStatsGeofenceWithCell(pokestop.Lat, pokestop.Lon, uint64(pokestop.CellId.ValueOrZero()))
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

func UpdatePokestopRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, fortId FortId, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId, "UpdatePokestopFromGetMapForts")
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return false, fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		return false, ""
	}
	defer unlock()

	pokestop.updatePokestopFromGetMapFortsOutProto(fortId, mapFort)
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

	var fortIdStr string
	if request != nil {
		fortIdStr = request.FortId
	} else {
		fortIdStr = getFortIdFromContest(contestData.ContestIncident.Contests[0].ContestId)
	}

	fortId, ok := ParseFortId(fortIdStr)
	if !ok {
		log.Errorf("UpdatePokestopWithContestData: unparseable fort id %q", fortIdStr)
		return "No fortId found"
	}

	if len(contestData.ContestIncident.Contests) > 1 {
		log.Errorf("More than one contest found")
		return fmt.Sprintf("More than one contest found in %s", fortId)
	}

	contest := contestData.ContestIncident.Contests[0]

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId, "UpdatePokestopWithContestData")
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
	fortIdStr := getFortIdFromContest(request.GetContestId())

	fortId, ok := ParseFortId(fortIdStr)
	if !ok {
		log.Errorf("UpdatePokestopWithPokemonSizeContestEntry: unparseable fort id %q", fortIdStr)
		return "Error: unparseable fort id"
	}

	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId, "UpdatePokestopWithContestEntry")
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
