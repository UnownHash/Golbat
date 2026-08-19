package decoder

import (
	"context"
	"fmt"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdateGymRecordWithFortDetailsOutProto(ctx context.Context, db db.DbDetails, fort *pogo.FortDetailsOutProto) string {
	fortId, ok := ParseFortId(fort.Id)
	if !ok {
		log.Errorf("UpdateGymRecordWithFortDetailsOutProto: unparseable fort id %q", fort.Id)
		return fmt.Sprintf("Error: unparseable fort id %q", fort.Id)
	}
	gym, unlock, err := getOrCreateGymRecord(ctx, db, fortId, "UpdateGymFromFortDetails")
	if err != nil {
		return err.Error()
	}
	defer unlock()

	gym.updateGymFromFortProto(fortId, fort)

	updateGymGetMapFortCache(gym, true)
	saveGymRecord(ctx, db, gym)

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithGymInfoProto(ctx context.Context, db db.DbDetails, gymInfo *pogo.GymGetInfoOutProto) string {
	rawFortId := gymInfo.GymStatusAndDefenders.PokemonFortProto.FortId
	fortId, ok := ParseFortId(rawFortId)
	if !ok {
		log.Errorf("UpdateGymRecordWithGymInfoProto: unparseable fort id %q", rawFortId)
		return fmt.Sprintf("Error: unparseable fort id %q", rawFortId)
	}
	gym, unlock, err := getOrCreateGymRecord(ctx, db, fortId, "UpdateGymFromGymInfo")
	if err != nil {
		return err.Error()
	}
	defer unlock()

	gym.updateGymFromGymInfoOutProto(fortId, gymInfo)

	updateGymGetMapFortCache(gym, true)
	saveGymRecord(ctx, db, gym)
	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithGetMapFortsOutProto(ctx context.Context, db db.DbDetails, fortId FortId, mapFort *pogo.GetMapFortsOutProto_FortProto) (bool, string) {
	gym, unlock, err := getGymRecordForUpdate(ctx, db, fortId, "UpdateGymFromGetMapForts")
	if err != nil {
		return false, err.Error()
	}

	// we missed it in Pokestop & Gym. Lets save it to cache
	if gym == nil {
		return false, ""
	}
	defer unlock()

	gym.updateGymFromGetMapFortsOutProto(fortId, mapFort, false)
	saveGymRecord(ctx, db, gym)
	return true, fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func UpdateGymRecordWithRsvpProto(ctx context.Context, db db.DbDetails, req *pogo.RaidDetails, resp *pogo.GetEventRsvpsOutProto) string {
	fortId, ok := ParseFortId(req.FortId)
	if !ok {
		log.Errorf("UpdateGymRecordWithRsvpProto: unparseable fort id %q", req.FortId)
		return fmt.Sprintf("%s unparseable fort id", req.FortId)
	}
	gym, unlock, err := getGymRecordForUpdate(ctx, db, fortId, "UpdateGymWithRsvp")
	if err != nil {
		return err.Error()
	}

	if gym == nil {
		// Do not add RSVP details to unknown gyms
		return fmt.Sprintf("%s Gym not present", req.FortId)
	}
	defer unlock()

	gym.updateGymFromRsvpProto(resp)

	saveGymRecord(ctx, db, gym)

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}

func ClearGymRsvp(ctx context.Context, db db.DbDetails, fortId string) string {
	parsedFortId, ok := ParseFortId(fortId)
	if !ok {
		log.Errorf("ClearGymRsvp: unparseable fort id %q", fortId)
		return fmt.Sprintf("%s unparseable fort id", fortId)
	}
	gym, unlock, err := getGymRecordForUpdate(ctx, db, parsedFortId, "ClearGymRsvp")
	if err != nil {
		return err.Error()
	}

	if gym == nil {
		// Do not add RSVP details to unknown gyms
		return fmt.Sprintf("%s Gym not present", fortId)
	}
	defer unlock()

	if gym.Rsvps.Valid {
		gym.SetRsvps(null.NewString("", false))

		saveGymRecord(ctx, db, gym)
	}

	return fmt.Sprintf("%s %s", gym.Id, gym.Name.ValueOrZero())
}
