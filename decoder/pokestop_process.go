package decoder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogoshim"
)

func UpdatePokestopRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort pogoshim.FortDetailsOutProto) string {
	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fort.GetId(), "UpdatePokestopFromFortDetails")
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return fmt.Sprintf("Error %s", err)
	}
	defer unlock()

	pokestop.updatePokestopFromFortDetailsProto(fort)

	updatePokestopGetMapFortCache(pokestop)
	savePokestopRecord(ctx, db, pokestop)
	return fmt.Sprintf("%s %s", fort.GetId(), fort.GetName())
}

func UpdatePokestopWithQuest(ctx context.Context, db db.DbDetails, quest pogoshim.FortSearchOutProto, haveAr bool) string {
	haveArStr := "NoAR"
	if haveAr {
		haveArStr = "AR"
	}

	if !quest.HasChallengeQuest() {
		statsCollector.IncDecodeQuest("error", "no_quest")
		return fmt.Sprintf("%s %s Blank quest", quest.GetFortId(), haveArStr)
	}

	statsCollector.IncDecodeQuest("ok", haveArStr)

	pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, quest.GetFortId(), "UpdatePokestopWithQuest")
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

	return fmt.Sprintf("%s %s %s", quest.GetFortId(), haveArStr, questTitle)
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

func UpdatePokestopRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, mapFort pogoshim.GetMapFortsOutProto_FortProto) (bool, string) {
	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, mapFort.GetId(), "UpdatePokestopFromGetMapForts")
	if err != nil {
		log.Printf("Update pokestop %s", err)
		return false, fmt.Sprintf("Error %s", err)
	}

	if pokestop == nil {
		return false, ""
	}
	defer unlock()

	pokestop.updatePokestopFromMapFortSummary(mapFortSummaryFromShim(mapFort))
	savePokestopRecord(ctx, db, pokestop)
	return true, fmt.Sprintf("%s %s", mapFort.GetId(), mapFort.GetName())
}

func GetPokestopPositions(details db.DbDetails, geofence *geojson.Feature) ([]db.QuestLocation, error) {
	return db.GetPokestopPositions(details, geofence)
}

// UpdatePokestopWithContestData's request parameter is a value type, so
// there's no way to distinguish "no request bytes on the wire" from "a
// present-but-empty request" the way the pre-shim code's always-non-nil
// *pogo.GetContestDataProto pointer (decode.go's decodeGetContestData
// always passed &decodedContestDataRequest, whether or not it had actually
// unmarshaled anything into it) implicitly could. That pointer's `request !=
// nil` check was consequently ALWAYS true at the one real call site, making
// the getFortIdFromContest(...) fallback below permanently unreachable
// (verified: decodeGetContestData is UpdatePokestopWithContestData's only
// caller) -- request.GetFortId() alone reproduces the exact observed
// behavior (empty string when the request was absent, real value otherwise).
func UpdatePokestopWithContestData(ctx context.Context, db db.DbDetails, request pogoshim.GetContestDataProto, contestData pogoshim.GetContestDataOutProto) string {
	if !contestData.HasContestIncident() || contestData.GetContestIncident().GetContests().Len() == 0 {
		return "No contests found"
	}

	fortId := request.GetFortId()
	if fortId == "" {
		return "No fortId found"
	}

	contests := contestData.GetContestIncident().GetContests()
	if contests.Len() > 1 {
		log.Errorf("More than one contest found")
		return fmt.Sprintf("More than one contest found in %s", fortId)
	}

	contest := contests.At(0)

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

func UpdatePokestopWithPokemonSizeContestEntry(ctx context.Context, db db.DbDetails, request pogoshim.GetPokemonSizeLeaderboardEntryProto, contestData pogoshim.GetPokemonSizeLeaderboardEntryOutProto) string {
	fortId := getFortIdFromContest(request.GetContestId())

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
